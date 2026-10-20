package com.sawtplus.app;

import android.content.Context;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import android.net.wifi.WifiManager;
import android.util.Log;

import org.java_websocket.WebSocket;
import org.java_websocket.handshake.ClientHandshake;
import org.java_websocket.server.WebSocketServer;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.IOException;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.NetworkInterface;
import java.net.ServerSocket;
import java.net.SocketException;
import java.security.SecureRandom;
import java.util.Arrays;
import java.util.Collections;
import java.util.Enumeration;
import java.util.Map;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Wi-Fi PCM streaming with mDNS discovery and token pairing.
 */
final class WifiServer extends WebSocketServer {
    private static final String TAG = App.TAG;
    private static final String SERVICE_TYPE = "_sawtplus._tcp.";
    private static final long PAIRING_TTL_MS = 10 * 60 * 1000L;

    private final Context context;
    private final StreamHub hub;
    private final TokenStore tokens;
    private final int port;
    private final String deviceId;
    private final String deviceName;
    private final SecureRandom random = new SecureRandom();

    private final Map<WebSocket, Session> sessions = new ConcurrentHashMap<>();
    private final Map<WebSocket, WifiClient> clients = new ConcurrentHashMap<>();

    private NsdManager nsdManager;
    private NsdManager.RegistrationListener registrationListener;
    private WifiManager.MulticastLock multicastLock;
    private WifiManager.WifiLock wifiLock;
    private ScheduledExecutorService scheduler;
    private final AtomicBoolean shuttingDown = new AtomicBoolean(false);

    private volatile String pairingCode = "";
    private volatile long pairingExpiresAt;

    WifiServer(Context context, StreamHub hub, TokenStore tokens, int preferredPort) throws IOException {
        super(pickAddress(preferredPort));
        this.context = context.getApplicationContext();
        this.hub = hub;
        this.tokens = tokens;
        this.port = getAddress().getPort();
        this.deviceId = tokens.deviceId();
        this.deviceName = sanitizeDeviceName(tokens.deviceName());
        setReuseAddr(true);
        setConnectionLostTimeout(30);
        ServiceState.deviceName = deviceName;
        ServiceState.deviceId = deviceId;
        ServiceState.source = (App.RATE / 1000) + " kHz · mono · " + App.FRAME_MS + " ms";
    }

    int port() {
        return port;
    }

    void startServer() {
        acquireLocks();
        refreshPairingCode();
        scheduler = Executors.newSingleThreadScheduledExecutor(r -> {
            Thread t = new Thread(r, "sawt-wifi");
            t.setDaemon(true);
            return t;
        });
        scheduler.scheduleAtFixedRate(this::refreshPairingCode, 30, 30, TimeUnit.SECONDS);
        scheduler.scheduleAtFixedRate(this::sendStats, 2, 2, TimeUnit.SECONDS);

        start();
        registerMdns();

        ServiceState.wifiEnabled = true;
        ServiceState.address = findLocalAddress();
        ServiceState.port = port;
        ServiceState.transport = "Wi-Fi";
        Log.i(TAG, "Wi-Fi server listening on " + ServiceState.address + ":" + port);
    }

    void stopServer() {
        if (!shuttingDown.compareAndSet(false, true)) {
            return;
        }
        if (scheduler != null) {
            scheduler.shutdownNow();
            scheduler = null;
        }
        unregisterMdns();
        releaseLocks();
        for (WifiClient client : clients.values()) {
            client.close();
        }
        clients.clear();
        sessions.clear();
        try {
            stop(1000);
        } catch (Exception e) {
            Log.w(TAG, "stop Wi-Fi server", e);
        }
        ServiceState.wifiEnabled = false;
        ServiceState.address = "";
        ServiceState.port = 0;
        ServiceState.pairingCode = "";
    }

    // ------------------------------------------------------------------
    // WebSocket callbacks
    // ------------------------------------------------------------------

    @Override
    public void onOpen(WebSocket conn, ClientHandshake handshake) {
        sessions.put(conn, new Session());
        Log.i(TAG, "Wi-Fi client connected: " + conn.getRemoteSocketAddress());
    }

    @Override
    public void onClose(WebSocket conn, int code, String reason, boolean remote) {
        cleanup(conn);
        Log.i(TAG, "Wi-Fi client disconnected: " + code + " " + reason);
    }

    @Override
    public void onMessage(WebSocket conn, String message) {
        Session session = sessions.get(conn);
        if (session == null) {
            return;
        }
        try {
            JSONObject obj = new JSONObject(message);
            String type = obj.optString("type", "");
            switch (type) {
                case "hello":
                    if (tokens.isValid(obj.optString("token", ""))) {
                        authenticate(conn, session);
                    } else {
                        sendPairRequired(conn);
                    }
                    break;
                case "pair":
                    String code = obj.optString("code", "");
                    if (validPairingCode(code)) {
                        String token = tokens.newToken();
                        tokens.add(token);
                        sendPaired(conn, token);
                        authenticate(conn, session);
                    } else {
                        sendError(conn, "invalid or expired pairing code");
                    }
                    break;
                case "ping":
                    sendPong(conn, obj.optLong("t", 0));
                    break;
                case "set_gain":
                case "set_mute":
                    // Gain is applied on the PC side; mute can also be toggled
                    // in the app itself (silence frames).
                    break;
                default:
                    break;
            }
        } catch (JSONException e) {
            Log.w(TAG, "invalid control message", e);
        }
    }

    @Override
    public void onMessage(WebSocket conn, java.nio.ByteBuffer message) {
        // The client never sends audio.
    }

    @Override
    public void onError(WebSocket conn, Exception ex) {
        if (!shuttingDown.get()) {
            Log.e(TAG, "WebSocket error", ex);
            ServiceState.error = ex.getMessage() == null ? ex.toString() : ex.getMessage();
        }
        if (conn != null) {
            cleanup(conn);
        }
    }

    @Override
    public void onStart() {
        setConnectionLostTimeout(30);
        Log.i(TAG, "Wi-Fi WebSocket server started on port " + port);
    }

    // ------------------------------------------------------------------
    // Authentication and streaming
    // ------------------------------------------------------------------

    private void authenticate(WebSocket conn, Session session) {
        synchronized (session) {
            if (session.authenticated) {
                return;
            }
            session.authenticated = true;
        }
        WifiClient client = new WifiClient(conn);
        clients.put(conn, client);
        hub.addSink(client);
        sendReady(conn);
        ServiceState.clients = clients.size();
        ServiceState.transport = "Wi-Fi";
        ServiceState.error = "";
        Log.i(TAG, "Wi-Fi client authenticated");
    }

    private void cleanup(WebSocket conn) {
        sessions.remove(conn);
        WifiClient client = clients.remove(conn);
        if (client != null) {
            hub.removeSink(client);
        }
        ServiceState.clients = clients.size();
        if (clients.isEmpty()) {
            ServiceState.transport = ServiceState.wifiEnabled ? "Wi-Fi" : "idle";
        }
    }

    private void sendReady(WebSocket conn) {
        JSONObject obj = new JSONObject();
        try {
            obj.put("type", "ready");
            obj.put("version", 1);
            obj.put("id", deviceId);
            obj.put("name", deviceName);
            obj.put("rate", App.RATE);
            obj.put("channels", App.CHANNELS);
            obj.put("format", "s16le");
            obj.put("frame_ms", App.FRAME_MS);
            obj.put("clients", clients.size());
        } catch (JSONException ignored) {
        }
        conn.send(obj.toString());
    }

    private void sendPairRequired(WebSocket conn) {
        JSONObject obj = new JSONObject();
        try {
            obj.put("type", "pair_required");
            obj.put("version", 1);
            obj.put("id", deviceId);
            obj.put("name", deviceName);
            obj.put("pairing", true);
        } catch (JSONException ignored) {
        }
        conn.send(obj.toString());
    }

    private void sendPaired(WebSocket conn, String token) {
        JSONObject obj = new JSONObject();
        try {
            obj.put("type", "paired");
            obj.put("version", 1);
            obj.put("id", deviceId);
            obj.put("name", deviceName);
            obj.put("token", token);
        } catch (JSONException ignored) {
        }
        conn.send(obj.toString());
    }

    private void sendPong(WebSocket conn, long t) {
        JSONObject obj = new JSONObject();
        try {
            obj.put("type", "pong");
            obj.put("t", t);
        } catch (JSONException ignored) {
        }
        conn.send(obj.toString());
    }

    private void sendError(WebSocket conn, String message) {
        JSONObject obj = new JSONObject();
        try {
            obj.put("type", "error");
            obj.put("error", message);
        } catch (JSONException ignored) {
        }
        conn.send(obj.toString());
    }

    private void sendStats() {
        if (clients.isEmpty()) {
            return;
        }
        JSONObject obj = new JSONObject();
        try {
            obj.put("type", "stats");
            obj.put("clients", clients.size());
            obj.put("frames", ServiceState.frames);
            obj.put("dropped", ServiceState.dropped);
        } catch (JSONException ignored) {
        }
        String payload = obj.toString();
        for (WebSocket conn : clients.keySet()) {
            try {
                conn.send(payload);
            } catch (Exception ignored) {
            }
        }
    }

    // ------------------------------------------------------------------
    // Pairing
    // ------------------------------------------------------------------

    private void refreshPairingCode() {
        long now = System.currentTimeMillis();
        if (pairingCode.isEmpty() || now > pairingExpiresAt) {
            pairingCode = String.format("%06d", 100000 + random.nextInt(900000));
            pairingExpiresAt = now + PAIRING_TTL_MS;
            ServiceState.pairingCode = pairingCode;
            ServiceState.pairingExpiresAt = pairingExpiresAt;
        }
    }

    private boolean validPairingCode(String code) {
        refreshPairingCode();
        return code != null && !code.isEmpty() && code.equals(pairingCode)
                && System.currentTimeMillis() <= pairingExpiresAt;
    }

    // ------------------------------------------------------------------
    // Discovery
    // ------------------------------------------------------------------

    private void registerMdns() {
        nsdManager = (NsdManager) context.getSystemService(Context.NSD_SERVICE);
        if (nsdManager == null) {
            ServiceState.error = "NSD is not available on this device";
            return;
        }

        NsdServiceInfo info = new NsdServiceInfo();
        info.setServiceName("Sawt-" + deviceId.substring(0, Math.min(6, deviceId.length())));
        info.setServiceType(SERVICE_TYPE);
        info.setPort(port);
        try {
            info.setAttribute("id", deviceId);
            info.setAttribute("name", deviceName);
            info.setAttribute("v", "1");
            info.setAttribute("pair", "1");
        } catch (IllegalArgumentException e) {
            Log.w(TAG, "mDNS attributes", e);
        }

        registrationListener = new NsdManager.RegistrationListener() {
            @Override
            public void onServiceRegistered(NsdServiceInfo serviceInfo) {
                Log.i(TAG, "mDNS registered as " + serviceInfo.getServiceName());
            }

            @Override
            public void onRegistrationFailed(NsdServiceInfo serviceInfo, int errorCode) {
                Log.e(TAG, "mDNS registration failed: " + errorCode);
                ServiceState.error = "mDNS registration failed (" + errorCode + ")";
            }

            @Override
            public void onServiceUnregistered(NsdServiceInfo serviceInfo) {
            }

            @Override
            public void onUnregistrationFailed(NsdServiceInfo serviceInfo, int errorCode) {
            }
        };
        nsdManager.registerService(info, NsdManager.PROTOCOL_DNS_SD, registrationListener);
    }

    private void unregisterMdns() {
        if (nsdManager != null && registrationListener != null) {
            try {
                nsdManager.unregisterService(registrationListener);
            } catch (IllegalArgumentException ignored) {
            }
        }
        registrationListener = null;
    }

    // ------------------------------------------------------------------
    // Locks and network helpers
    // ------------------------------------------------------------------

    @SuppressWarnings("deprecation")
    private void acquireLocks() {
        try {
            WifiManager wm = (WifiManager) context.getSystemService(Context.WIFI_SERVICE);
            if (wm == null) {
                return;
            }
            multicastLock = wm.createMulticastLock("sawtplus");
            multicastLock.setReferenceCounted(false);
            multicastLock.acquire();
            wifiLock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "sawtplus");
            wifiLock.setReferenceCounted(false);
            wifiLock.acquire();
        } catch (Exception e) {
            Log.w(TAG, "Wi-Fi locks", e);
        }
    }

    private void releaseLocks() {
        try {
            if (multicastLock != null && multicastLock.isHeld()) {
                multicastLock.release();
            }
        } catch (Exception ignored) {
        }
        try {
            if (wifiLock != null && wifiLock.isHeld()) {
                wifiLock.release();
            }
        } catch (Exception ignored) {
        }
        multicastLock = null;
        wifiLock = null;
    }

    private static String findLocalAddress() {
        try {
            Enumeration<NetworkInterface> interfaces = NetworkInterface.getNetworkInterfaces();
            String fallback = "";
            for (NetworkInterface nif : Collections.list(interfaces)) {
                if (!nif.isUp() || nif.isLoopback()) {
                    continue;
                }
                for (InetAddress addr : Collections.list(nif.getInetAddresses())) {
                    if (addr instanceof Inet4Address && !addr.isLoopbackAddress() && !addr.isLinkLocalAddress()) {
                        String ip = addr.getHostAddress();
                        if (nif.getName().startsWith("wlan") || nif.getName().startsWith("ap")) {
                            return ip;
                        }
                        if (fallback.isEmpty()) {
                            fallback = ip;
                        }
                    }
                }
            }
            return fallback;
        } catch (SocketException e) {
            return "";
        }
    }

    private static String sanitizeDeviceName(String name) {
        if (name == null) {
            return "Sawt+";
        }
        StringBuilder sb = new StringBuilder();
        for (char c : name.toCharArray()) {
            if (!Character.isISOControl(c)) {
                sb.append(c);
            }
        }
        String out = sb.toString().trim();
        return out.isEmpty() ? "Sawt+" : out;
    }

    private static InetSocketAddress pickAddress(int preferredPort) throws IOException {
        IOException last = null;
        for (int port = preferredPort; port < preferredPort + 10; port++) {
            try (ServerSocket socket = new ServerSocket()) {
                socket.setReuseAddress(true);
                socket.bind(new InetSocketAddress(port));
                return new InetSocketAddress(port);
            } catch (IOException e) {
                last = e;
            }
        }
        throw last != null ? last : new IOException("no free port in range");
    }

    // ------------------------------------------------------------------
    // Client sink
    // ------------------------------------------------------------------

    private static final class Session {
        boolean authenticated;
    }

    private final class WifiClient implements StreamHub.Sink {
        private final WebSocket conn;
        private final ArrayBlockingQueue<byte[]> queue = new ArrayBlockingQueue<>(48);
        private final AtomicBoolean running = new AtomicBoolean(true);
        private final Thread writer;

        WifiClient(WebSocket conn) {
            this.conn = conn;
            this.writer = new Thread(this::writeLoop, "sawt-wifi-client");
            this.writer.setDaemon(true);
            this.writer.start();
        }

        @Override
        public boolean send(byte[] data, int length) {
            if (!running.get()) {
                return false;
            }
            byte[] frame = Arrays.copyOf(data, length);
            if (!queue.offer(frame)) {
                queue.poll();
                queue.offer(frame);
                return false;
            }
            return true;
        }

        private void writeLoop() {
            while (running.get()) {
                try {
                    byte[] frame = queue.take();
                    conn.send(frame);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    return;
                } catch (Exception e) {
                    close();
                    return;
                }
            }
        }

        @Override
        public void close() {
            if (!running.compareAndSet(true, false)) {
                return;
            }
            queue.clear();
            writer.interrupt();
            try {
                conn.close();
            } catch (Exception ignored) {
            }
        }
    }
}
