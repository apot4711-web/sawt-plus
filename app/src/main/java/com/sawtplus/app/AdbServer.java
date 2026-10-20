package com.sawtplus.app;

import android.net.LocalServerSocket;
import android.net.LocalSocket;
import android.util.Log;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * The original USB/ADB transport: the PC forwards a TCP port to this abstract
 * Unix socket and receives raw PCM.
 */
final class AdbServer extends Thread {
    private static final String TAG = App.TAG;
    private static final String SOCKET_NAME = "sawt-plus";

    private final StreamHub hub;
    private final AtomicBoolean running = new AtomicBoolean(true);
    private LocalServerSocket serverSocket;

    AdbServer(StreamHub hub) {
        super("sawt-adb");
        this.hub = hub;
    }

    @Override
    public void run() {
        try {
            serverSocket = new LocalServerSocket(SOCKET_NAME);
            Log.i(TAG, "ADB server listening on @sawt-plus");
        } catch (IOException e) {
            Log.e(TAG, "ADB LocalServerSocket", e);
            ServiceState.error = "ADB socket: " + e.getMessage();
            return;
        }

        while (running.get()) {
            LocalSocket socket = null;
            StreamHub.Sink sink = null;
            try {
                socket = serverSocket.accept();
                OutputStream out = socket.getOutputStream();
                InputStream in = socket.getInputStream();

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
                Log.i(TAG, "ADB client disconnected");
            }
        }

        closeServerSocket();
    }

    void shutdown() {
        running.set(false);
        closeServerSocket();
        interrupt();
    }

    private void closeServerSocket() {
        LocalServerSocket socket = serverSocket;
        serverSocket = null;
        if (socket != null) {
            try {
                socket.close();
            } catch (IOException ignored) {
            }
        }
    }
}
