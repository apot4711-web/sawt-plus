package com.sawtplus.app;

import android.util.Log;

import java.io.ByteArrayOutputStream;
import java.io.Closeable;
import java.io.EOFException;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;

import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLServerSocket;
import javax.net.ssl.SSLSocket;

/**
 * Synchronous TLS front end for the Wi-Fi transport.
 *
 * <p>The public port is served by a plain blocking {@link SSLServerSocket}
 * ({@code javax.net.ssl} performs the handshake on the platform TLS stack),
 * not by Java-WebSocket's {@code SSLSocketChannel2}. Decrypted bytes are
 * forwarded to an internal cleartext WebSocket server bound to loopback,
 * which keeps the existing WebSocket/pairing code unchanged.
 *
 * <p>The internal server is reachable by any app on the phone, so the front
 * end injects a per-process random {@code X-Sawt-Internal} header into the
 * HTTP upgrade request; the loopback server rejects requests without it. The
 * real client address is passed in {@code X-Forwarded-For} so pairing
 * throttling still works per address.
 */
final class TlsFrontend {
    private static final String TAG = App.TAG;
    private static final int MAX_HEADER_BYTES = 16 * 1024;
    private static final int HANDSHAKE_TIMEOUT_MS = 15_000;
    private static final int HEADER_TIMEOUT_MS = 10_000;
    private static final int MAX_PENDING_HANDSHAKES = 24;
    private static final int PORT_ATTEMPTS = 10;
    private static final String HEADER_SECRET = "X-Sawt-Internal";
    private static final String HEADER_FORWARDED_FOR = "X-Forwarded-For";

    private final SSLContext sslContext;
    private final int preferredPort;
    private final int targetPort;
    private final String internalSecret;

    private final AtomicBoolean running = new AtomicBoolean(false);
    private final AtomicInteger pendingHandshakes = new AtomicInteger();
    private final Set<Socket> sockets =
            Collections.newSetFromMap(new ConcurrentHashMap<Socket, Boolean>());

    private volatile SSLServerSocket serverSocket;
    private Thread acceptThread;

    TlsFrontend(SSLContext sslContext, int preferredPort, int targetPort, String internalSecret) {
        this.sslContext = sslContext;
        this.preferredPort = preferredPort;
        this.targetPort = targetPort;
        this.internalSecret = internalSecret;
    }

    /**
     * Binds the first free port in [preferredPort, preferredPort+10) and
     * starts accepting. Returns the bound port.
     */
    int start() throws IOException {
        IOException last = null;
        for (int port = preferredPort; port < preferredPort + PORT_ATTEMPTS; port++) {
            SSLServerSocket candidate = null;
            try {
                candidate = (SSLServerSocket) sslContext.getServerSocketFactory()
                        .createServerSocket();
                candidate.setReuseAddress(true);
                List<String> protocols = modernProtocols(candidate);
                if (!protocols.isEmpty()) {
                    candidate.setEnabledProtocols(protocols.toArray(new String[0]));
                }
                candidate.setNeedClientAuth(false);
                candidate.bind(new InetSocketAddress(port), 50);
                serverSocket = candidate;
                last = null;
                break;
            } catch (IOException e) {
                last = e;
                closeQuietly(candidate);
            }
        }
        if (serverSocket == null) {
            throw last != null ? last : new IOException("no free TLS port");
        }
        running.set(true);
        acceptThread = new Thread(this::acceptLoop, "sawt-tls-accept");
        acceptThread.setDaemon(true);
        acceptThread.start();
        Log.i(TAG, "TLS server listening on port " + port()
                + " (internal loopback port " + targetPort + ")");
        return port();
    }

    int port() {
        SSLServerSocket socket = serverSocket;
        return socket == null ? 0 : socket.getLocalPort();
    }

    void stop() {
        running.set(false);
        closeQuietly(serverSocket);
        serverSocket = null;
        for (Socket socket : sockets) {
            closeQuietly(socket);
        }
        sockets.clear();
    }

    private static List<String> modernProtocols(SSLServerSocket socket) {
        List<String> protocols = new ArrayList<>();
        for (String protocol : socket.getSupportedProtocols()) {
            if ("TLSv1.2".equals(protocol) || "TLSv1.3".equals(protocol)) {
                protocols.add(protocol);
            }
        }
        return protocols;
    }

    private void acceptLoop() {
        while (running.get()) {
            SSLServerSocket server = serverSocket;
            if (server == null) {
                return;
            }
            Socket raw;
            try {
                raw = server.accept();
            } catch (IOException e) {
                if (running.get()) {
                    Log.w(TAG, "TLS accept failed", e);
                }
                return;
            }
            if (pendingHandshakes.incrementAndGet() > MAX_PENDING_HANDSHAKES) {
                pendingHandshakes.decrementAndGet();
                closeQuietly(raw);
                Log.w(TAG, "rejecting TLS client: too many pending handshakes");
                continue;
            }
            Thread worker = new Thread(() -> handle(raw), "sawt-tls-client");
            worker.setDaemon(true);
            worker.start();
        }
    }

    private void handle(Socket raw) {
        Socket plain = null;
        try {
            SSLSocket tls = (SSLSocket) raw;
            tls.setUseClientMode(false);
            tls.setSoTimeout(HANDSHAKE_TIMEOUT_MS);
            tls.startHandshake();
            sockets.add(tls);

            InputStream tlsIn = tls.getInputStream();
            OutputStream tlsOut = tls.getOutputStream();
            tls.setSoTimeout(HEADER_TIMEOUT_MS);
            byte[] requestBytes = readHeaders(tlsIn,
                    System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(HEADER_TIMEOUT_MS));
            tls.setSoTimeout(0);

            String request = new String(requestBytes, StandardCharsets.ISO_8859_1);
            if (!request.startsWith("GET ")) {
                throw new IOException("not a WebSocket upgrade request");
            }
            String clientIp = raw.getInetAddress() == null
                    ? "" : raw.getInetAddress().getHostAddress();
            byte[] forwarded = injectHeaders(request, clientIp)
                    .getBytes(StandardCharsets.ISO_8859_1);

            plain = new Socket();
            plain.connect(new InetSocketAddress(InetAddress.getLoopbackAddress(), targetPort), 5000);
            plain.setTcpNoDelay(true);
            sockets.add(plain);
            InputStream plainIn = plain.getInputStream();
            OutputStream plainOut = plain.getOutputStream();
            plainOut.write(forwarded);
            plainOut.flush();

            final Socket upstream = plain;
            Thread down = new Thread(() -> pump(tlsIn, plainOut, tls, upstream),
                    "sawt-tls-down");
            Thread up = new Thread(() -> pump(plainIn, tlsOut, upstream, tls),
                    "sawt-tls-up");
            down.setDaemon(true);
            up.setDaemon(true);
            down.start();
            up.start();
        } catch (IOException e) {
            Log.w(TAG, "TLS client failed: " + e.getMessage());
            closeQuietly(raw);
            closeQuietly(plain);
        } finally {
            pendingHandshakes.decrementAndGet();
        }
    }

    private void pump(InputStream in, OutputStream out, Socket a, Socket b) {
        byte[] buffer = new byte[8192];
        try {
            int n;
            while ((n = in.read(buffer)) != -1) {
                out.write(buffer, 0, n);
                out.flush();
            }
        } catch (IOException ignored) {
        } finally {
            sockets.remove(a);
            sockets.remove(b);
            closeQuietly(a);
            closeQuietly(b);
        }
    }

    private String injectHeaders(String request, String clientIp) {
        String head = request.endsWith("\r\n\r\n")
                ? request.substring(0, request.length() - 4) : request;
        StringBuilder sb = new StringBuilder(head.length() + 128);
        for (String line : head.split("\r\n", -1)) {
            String lower = line.toLowerCase(Locale.US);
            if (lower.startsWith("x-sawt-internal:") || lower.startsWith("x-forwarded-for:")) {
                continue; // never let a client spoof the internal headers
            }
            sb.append(line).append("\r\n");
        }
        sb.append(HEADER_SECRET).append(": ").append(internalSecret).append("\r\n");
        sb.append(HEADER_FORWARDED_FOR).append(": ").append(clientIp).append("\r\n\r\n");
        return sb.toString();
    }

    /** Reads the HTTP request head, including the terminating CRLFCRLF. */
    private static byte[] readHeaders(InputStream in, long deadlineNanos) throws IOException {
        ByteArrayOutputStream buffer = new ByteArrayOutputStream(1024);
        int match = 0;
        while (buffer.size() < MAX_HEADER_BYTES) {
            if (System.nanoTime() > deadlineNanos) {
                throw new IOException("request headers timed out");
            }
            int b = in.read();
            if (b == -1) {
                throw new EOFException("connection closed during request headers");
            }
            buffer.write(b);
            if (b == '\r' && (match == 0 || match == 2)) {
                match++;
            } else if (b == '\n' && (match == 1 || match == 3)) {
                match++;
            } else {
                match = 0;
            }
            if (match == 4) {
                return buffer.toByteArray();
            }
        }
        throw new IOException("request headers too large");
    }

    private static void closeQuietly(Closeable closeable) {
        if (closeable != null) {
            try {
                closeable.close();
            } catch (IOException ignored) {
            }
        }
    }
}
