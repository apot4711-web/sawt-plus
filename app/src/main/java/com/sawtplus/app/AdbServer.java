package com.sawtplus.app;

import android.net.LocalServerSocket;
import android.net.LocalSocket;
import android.net.LocalSocketAddress;
import android.util.Log;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * The original USB/ADB transport: the PC forwards a TCP port to this abstract
 * Unix socket and receives raw PCM.
 *
 * <p>Protocol v2 (security): the client must first send one line
 * {@code TOKEN <pairing-token>\n}. The token is verified before any audio
 * flows; anything else and the socket is closed without starting capture.
 * This closes the hole where any app on the phone could tap the microphone
 * through the unauthenticated abstract socket.
 */
final class AdbServer extends Thread {
    private static final String TAG = App.TAG;
    private static final String SOCKET_NAME = "sawt-plus";

    /** First line the client must send: "TOKEN <pairing-token>\n". */
    private static final String HANDSHAKE_PREFIX = "TOKEN ";
    /** Give up on a client that never completes the handshake. */
    private static final int HANDSHAKE_TIMEOUT_MS = 10_000;
    /** Reject absurdly long handshake lines (a token is ~50 bytes). */
    private static final int MAX_HANDSHAKE_BYTES = 256;

    private final StreamHub hub;
    private final TokenStore tokens;
    private final AtomicBoolean running = new AtomicBoolean(true);
    private LocalServerSocket serverSocket;
    /** Currently connected client, if any; closed by dropClient(). */
    private volatile LocalSocket clientSocket;

    AdbServer(StreamHub hub, TokenStore tokens) {
        super("sawt-adb");
        this.hub = hub;
        this.tokens = tokens;
    }

    @Override
    public void run() {
        // Closing a LocalServerSocket does not wake a thread blocked in
        // accept() on Linux, and the abstract name stays bound until that
        // accept() returns. A restart can therefore briefly race the previous
        // instance's teardown; retry for up to ~2 s instead of giving up.
        LocalServerSocket server = null;
        for (int attempt = 0; running.get() && attempt < 20; attempt++) {
            try {
                server = new LocalServerSocket(SOCKET_NAME);
                break;
            } catch (IOException e) {
                if (!running.get()) {
                    return;
                }
                if (attempt == 19) {
                    Log.e(TAG, "ADB LocalServerSocket", e);
                    ServiceState.error = "ADB socket: " + e.getMessage();
                    return;
                }
                try {
                    Thread.sleep(100);
                } catch (InterruptedException interrupted) {
                    return;
                }
            }
        }
        if (server == null) {
            return;
        }
        serverSocket = server;
        Log.i(TAG, "ADB server listening on @sawt-plus");

        while (running.get()) {
            LocalSocket socket = null;
            StreamHub.Sink sink = null;
            try {
                socket = server.accept();
                if (!running.get()) {
                    break;
                }
                clientSocket = socket;

                // Authenticate before any audio flows: the abstract socket is
                // reachable by any app on the phone, so the client must prove
                // it knows the pairing token first.
                socket.setSoTimeout(HANDSHAKE_TIMEOUT_MS);
                InputStream in = socket.getInputStream();
                String presented = readHandshakeLine(in);
                if (presented == null || !tokens.isValidConstantTime(presented)) {
                    Log.w(TAG, "ADB client rejected: authentication failed");
                    continue; // finally closes the socket; capture never starts
                }
                socket.setSoTimeout(0);

                OutputStream out = socket.getOutputStream();

                final LocalSocket connected = socket;
                sink = new StreamHub.Sink() {
                    @Override
                    public boolean send(byte[] data, int length) {
                        try {
                            out.write(data, 0, length);
                            return true;
                        } catch (IOException e) {
                            return false;
                        }
                    }

                    @Override
                    public void close() {
                        try {
                            connected.close();
                        } catch (IOException ignored) {
                        }
                    }
                };

                hub.addSink(sink);
                ServiceState.transport = "ADB";
                Log.i(TAG, "ADB client connected");

                byte[] ignored = new byte[256];
                while (running.get() && in.read(ignored) != -1) {
                    // The PC never sends data; this only detects disconnects.
                }
            } catch (IOException e) {
                if (running.get()) {
                    Log.w(TAG, "ADB client", e);
                }
            } finally {
                if (sink != null) {
                    hub.removeSink(sink);
                }
                if (socket != null) {
                    try {
                        socket.close();
                    } catch (IOException ignored) {
                    }
                }
                if (clientSocket == socket) {
                    clientSocket = null;
                }
                Log.i(TAG, "ADB client disconnected");
            }
        }

        closeServerSocket();
    }

    /**
     * Reads the client's {@code TOKEN <token>\n} handshake line.
     *
     * @return the presented token, or null on timeout, EOF, overlong input,
     * or a malformed line. Never throws.
     */
    private static String readHandshakeLine(InputStream in) {
        ByteArrayOutputStream buf = new ByteArrayOutputStream(64);
        try {
            int b = in.read();
            while (b != -1 && b != '\n') {
                if (buf.size() >= MAX_HANDSHAKE_BYTES) {
                    return null;
                }
                buf.write(b);
                b = in.read();
            }
            if (b == -1 && buf.size() == 0) {
                return null;
            }
        } catch (IOException e) {
            return null; // includes the SoTimeout expiry
        }
        String line = new String(buf.toByteArray(), StandardCharsets.UTF_8);
        if (line.endsWith("\r")) {
            line = line.substring(0, line.length() - 1);
        }
        if (!line.startsWith(HANDSHAKE_PREFIX)) {
            return null;
        }
        String token = line.substring(HANDSHAKE_PREFIX.length()).trim();
        return token.isEmpty() ? null : token;
    }

    void shutdown() {
        Log.i(TAG, "ADB server shutdown requested");
        running.set(false);
        closeServerSocket();
        dropClient();
        // Wake a pending accept() with a throwaway connection so the abstract
        // name is released before a restarted service tries to bind it.
        wakeAccept();
        interrupt();
        try {
            join(500);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    /** Connects once to @sawt-plus and closes; the server side discards it. */
    private void wakeAccept() {
        LocalSocket wake = new LocalSocket();
        try {
            wake.connect(new LocalSocketAddress(
                    SOCKET_NAME, LocalSocketAddress.Namespace.ABSTRACT));
        } catch (IOException ignored) {
            // The name was already released; nothing to wake.
        } finally {
            try {
                wake.close();
            } catch (IOException ignored) {
            }
        }
    }

    /**
     * Closes the currently connected ADB client, if any (e.g. after the
     * user revokes pairing tokens). The run loop unblocks from its read,
     * cleans up, and goes back to accept(); a reconnect must present a
     * valid token again.
     */
    void dropClient() {
        LocalSocket socket = clientSocket;
        if (socket != null) {
            // close() alone does not always wake a thread blocked in read() on
            // Linux; shutdown() makes the pending read return immediately.
            try {
                socket.shutdownInput();
            } catch (IOException ignored) {
            }
            try {
                socket.shutdownOutput();
            } catch (IOException ignored) {
            }
            try {
                socket.close();
            } catch (IOException ignored) {
            }
        }
    }

    private void closeServerSocket() {
        LocalServerSocket socket = serverSocket;
        serverSocket = null;
        if (socket != null) {
            try {
                socket.close();
                Log.i(TAG, "ADB server socket closed");
            } catch (IOException ignored) {
            }
        }
    }
}
