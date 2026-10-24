package com.sawtplus.app;

import android.content.Context;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import android.net.wifi.WifiManager;
import android.os.Looper;
import android.util.Log;

import org.java_websocket.WebSocket;
import org.java_websocket.drafts.Draft;
import org.java_websocket.exceptions.InvalidDataException;
import org.java_websocket.handshake.ClientHandshake;
import org.java_websocket.handshake.ServerHandshakeBuilder;
import org.java_websocket.server.WebSocketServer;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.IOException;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.NetworkInterface;
import java.net.SocketException;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Collections;
import java.util.Enumeration;
import java.util.Iterator;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

import javax.net.ssl.SSLContext;

/**
 * Wi-Fi PCM streaming over TLS (wss://) with mDNS discovery and token pairing.
 *
 * <p><b>Wire contract for the PC client (TLS):</b>
 * <ul>
 *   <li>Transport is <b>wss://HOST:PORT only</b> — the server speaks no
 *       cleartext ws://. TLS 1.2+ is enforced by the synchronous
 *       {@link TlsFrontend}; the WebSocket server itself listens on loopback
 *       and is reachable only through that front end.</li>
 *   <li>Server certificate: self-signed X.509, EC P-256 on API 23+,
 *       RSA-2048 on API 21-22 (AndroidKeyStore has no EC before API 23),
 *       CN "SawtPlus &lt;device-id&gt;" (no '+': it is special in RFC 2253
 *       DNs), 25-year validity. It is NOT anchored to
 *       any CA — the client must NOT use system trust.</li>
 *   <li>Instead the client pins the SHA-256 fingerprint (TOFU). It learns the
 *       expected value (lowercase hex, 64 chars) from:
 *       <ul>
 *         <li>the mDNS TXT record attribute {@code sha256}, or</li>
 *         <li>the JSON {@code cert_sha256} field of the {@code paired},
 *             {@code pair_required} and {@code ready} control messages.</li>
 *       </ul></li>
 *   <li>At pairing time the client stores {@code cert_sha256} alongside the
 *       pairing token. On every later connect, after the TLS handshake, it
 *       computes SHA-256 over the presented leaf certificate
 *       ({@code cert.getEncoded()}) and aborts the connection on mismatch.
 *       A changed fingerprint means re-pairing (new 6-digit code).</li>
 *   <li>The 6-digit pairing code remains the authentication mechanism;
 *       pairing messages keep their shape — only the transport scheme changed
 *       from ws:// to wss://. The pairing token therefore never travels in
 *       cleartext anymore.</li>
 * </ul>
 */
final class WifiServer extends WebSocketServer {
    private static final String TAG = App.TAG;
    private static final String SERVICE_TYPE = "_sawtplus._tcp.";
    private static final long PAIRING_TTL_MS = 10 * 60 * 1000L;
    private static final long AUTH_TIMEOUT_MS = 15 * 1000L;
    private static final int MAX_PENDING_CONNECTIONS = 16;
    private static final int MAX_CLIENTS = 8;
    private static final int FREE_PAIR_ATTEMPTS = 5;
    private static final int MAX_PAIR_DELAY_SECONDS = 30;
    private static final long PAIR_ATTEMPT_WINDOW_MS = 10 * 60 * 1000L;

    private final Context context;
    private final StreamHub hub;
    private final TokenStore tokens;
    private final int preferredPort;
    private final String deviceId;
    private final String deviceName;
    private final String certFingerprint;
    private final SSLContext sslContext;
    private final String internalSecret;
    private final SecureRandom random = new SecureRandom();
    private volatile int publicPort;
    private TlsFrontend frontend;

    private final Map<WebSocket, Session> sessions = new ConcurrentHashMap<>();
    private final Map<WebSocket, WifiClient> clients = new ConcurrentHashMap<>();
    private final Map<String, PairAttempts> pairAttempts = new ConcurrentHashMap<>();
    private final Object pairingLock = new Object();

    private NsdManager nsdManager;
    private NsdManager.RegistrationListener registrationListener;
    private WifiManager.MulticastLock multicastLock;
    private WifiManager.WifiLock wifiLock;
    private ScheduledExecutorService scheduler;
    private final AtomicBoolean shuttingDown = new AtomicBoolean(false);

    private volatile String pairingCode = "";
    private volatile long pairingExpiresAt;

    WifiServer(Context context, StreamHub hub, TokenStore tokens, int preferredPort) throws IOException {
        // The WebSocket server itself is cleartext and bound to loopback; the
        // public TLS listener (TlsFrontend) forwards decrypted bytes to it.
        // Binding on port 0 lets the kernel pick the port atomically; the
        // actual port is read after startServer() sees the server is bound.
        super(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0));
        this.context = context.getApplicationContext();
        this.hub = hub;
        this.tokens = tokens;
        this.preferredPort = preferredPort;
        this.deviceId = tokens.deviceId();
        this.deviceName = sanitizeDeviceName(tokens.deviceName());
        this.internalSecret = newInternalSecret();
        // TLS-only transport. Fail closed: if the certificate or SSLContext
        // cannot be created, the constructor throws and the server never
        // starts in cleartext.
        try {
            this.sslContext = TlsCertificate.serverContext(this.context, this.deviceId);
            this.certFingerprint = TlsCertificate.fingerprint(this.context, this.deviceId);
        } catch (Exception e) {
            throw new IOException("TLS setup failed", e);
        }
        ServiceState.certFingerprint = certFingerprint;
        setReuseAddr(true);
        setConnectionLostTimeout(30);
        ServiceState.deviceName = deviceName;
        ServiceState.deviceId = deviceId;
        ServiceState.source = (App.RATE / 1000) + " kHz · mono · " + App.FRAME_MS + " ms";
    }

    int port() {
        int port = publicPort;
        return port != 0 ? port : preferredPort;
    }

    /**
     * SHA-256 fingerprint (lowercase hex) of the server's self-signed TLS
     * certificate. Clients pin this value (TOFU); see the class Javadoc.
     */
    String certFingerprint() {
        return certFingerprint;
    }

    boolean startServer() {
        acquireLocks();
        refreshPairingCode();
        scheduler = Executors.newSingleThreadScheduledExecutor(r -> {
            Thread t = new Thread(r, "sawt-wifi");
            t.setDaemon(true);
            return t;
        });
        scheduler.scheduleAtFixedRate(this::refreshPairingCode, 30, 30, TimeUnit.SECONDS);
        scheduler.scheduleAtFixedRate(this::sendStats, 2, 2, TimeUnit.SECONDS);
        scheduler.scheduleAtFixedRate(this::closeStaleSessions, 5, 5, TimeUnit.SECONDS);

        start(); // bind the internal loopback WebSocket server first
        int internalPort = awaitInternalPort();
        if (internalPort == 0) {
            Log.e(TAG, "Wi-Fi server failed to bind on loopback");
            ServiceState.error = "Wi-Fi server failed to start";
            abortStart();
            return false;
        }
        try {
            frontend = new TlsFrontend(sslContext, preferredPort, internalPort, internalSecret);
            publicPort = frontend.start();
        } catch (IOException e) {
            Log.e(TAG, "TLS server failed to start", e);
            ServiceState.error = "TLS server failed: " + e.getMessage();
            frontend = null;
            abortStart();
            return false;
        }
        registerMdns();

        ServiceState.wifiEnabled = true;
        ServiceState.address = findLocalAddress();
        ServiceState.port = publicPort;
        ServiceState.transport = "Wi-Fi";
        Log.i(TAG, "Wi-Fi server listening on " + ServiceState.address + ":" + publicPort);
        return true;
    }

    private int awaitInternalPort() {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(3);
        while (!shuttingDown.get() && System.nanoTime() < deadline) {
            int port = getPort();
            if (port != 0) {
                return port;
            }
            try {
                Thread.sleep(10);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return 0;
            }
        }
        return getPort();
    }

    private void abortStart() {
        if (scheduler != null) {
            scheduler.shutdownNow();
            scheduler = null;
        }
        releaseLocks();
        try {
            stop(1000);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    void stopServer() {
        if (!shuttingDown.compareAndSet(false, true)) {
            return;
        }
        // Closing TLS/WebSocket sockets performs network I/O (TLS close_notify
        // writes). Android throws NetworkOnMainThreadException for that, and a
        // slow close can also ANR. RecordService already calls teardown on a
        // worker; this guard keeps the invariant even if a future caller
        // forgets.
        if (Looper.myLooper() == Looper.getMainLooper()) {
            Thread worker = new Thread(this::stopServerBlocking, "sawt-wifi-stop");
            worker.setDaemon(true);
            worker.start();
            return;
        }
        stopServerBlocking();
    }

    private void stopServerBlocking() {
        if (scheduler != null) {
            scheduler.shutdownNow();
            scheduler = null;
        }
        TlsFrontend tlsFrontend = frontend;
        frontend = null;
        if (tlsFrontend != null) {
            tlsFrontend.stop();
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
    public ServerHandshakeBuilder onWebsocketHandshakeReceivedAsServer(
            WebSocket conn, Draft draft, ClientHandshake request) throws InvalidDataException {
        // The internal WebSocket server is loopback-only; a per-process secret
        // injected by TlsFrontend keeps other apps on the phone out.
        String secret = request.getFieldValue("x-sawt-internal");
        if (secret == null || !MessageDigest.isEqual(
                secret.getBytes(StandardCharsets.US_ASCII),
                internalSecret.getBytes(StandardCharsets.US_ASCII))) {
            Log.w(TAG, "rejected direct connection to the internal WebSocket server");
            throw new InvalidDataException(1008, "forbidden");
        }
        return super.onWebsocketHandshakeReceivedAsServer(conn, draft, request);
    }

    @Override
    public void onOpen(WebSocket conn, ClientHandshake handshake) {
        String forwardedIp = sanitizeIp(handshake.getFieldValue("x-forwarded-for"));
        synchronized (sessions) {
            int pending = 0;
            for (Session session : sessions.values()) {
                if (!session.authenticated) {
                    pending++;
                }
            }
            if (pending >= MAX_PENDING_CONNECTIONS) {
                Log.w(TAG, "rejecting pending client: too many unauthenticated connections");
                conn.close(1013, "too many pending connections");
                return;
            }
            sessions.put(conn, new Session(System.currentTimeMillis(), forwardedIp));
        }
        Log.i(TAG, "Wi-Fi client connected: " + conn.getRemoteSocketAddress()
                + (forwardedIp == null ? "" : " (from " + forwardedIp + ")"));
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
                    if (session.authenticated) {
                        sendError(conn, "already paired");
                        break;
                    }
                    String ip = remoteIp(conn, session);
                    long cooldown = pairCooldownSeconds(ip);
                    if (cooldown > 0) {
                        sendError(conn, "too many attempts; try again in " + cooldown + "s", cooldown);
                        break;
                    }
                    String code = obj.optString("code", "");
                    if (validPairingCode(code)) {
                        String token = tokens.newToken();
                        tokens.add(token);
                        // The code is single-use: rotate it before the next
                        // client can try. Rotating also clears throttles.
                        synchronized (pairingLock) {
                            rotatePairingCodeLocked();
                            pairAttempts.clear();
                        }
                        sendPaired(conn, token);
                        authenticate(conn, session);
                    } else {
                        long retry = recordPairFailure(ip);
                        String errorText = retry > 0
                                ? "invalid or expired pairing code; try again in " + retry + "s"
                                : "invalid or expired pairing code";
                        sendError(conn, errorText, retry);
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
        Log.i(TAG, "internal WebSocket server listening on loopback port " + getPort());
    }

    // ------------------------------------------------------------------
    // Authentication and streaming
    // ------------------------------------------------------------------

    private void authenticate(WebSocket conn, Session session) {
        synchronized (session) {
            if (session.authenticated) {
                return;
            }
            if (clients.size() >= MAX_CLIENTS) {
                sendError(conn, "too many connected clients");
                conn.close(1013, "too many connected clients");
                return;
            }
            session.authenticated = true;
        }
        WifiClient client = new WifiClient(conn);
        // "ready" must be the first control message this client receives.
        // sendStats runs every 2 s over the clients map, so adding the client
        // before-ready lets stats (and audio) win the race and the PC aborts
        // the handshake with "unexpected handshake message stats".
        sendReady(conn, clients.size() + 1);
        clients.put(conn, client);
        hub.addSink(client);
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

    /**
     * Closes every authenticated client connection, e.g. after the user
     * revokes all pairing tokens. Per-connection cleanup runs through
     * onClose, so hub sinks are removed and the client count is updated
     * there. Affected PCs get "pair_required" on their next hello and must
     * pair again.
     */
    void dropAllClients() {
        for (WebSocket conn : clients.keySet()) {
            try {
                conn.close(4000, "pairing revoked");
            } catch (Exception ignored) {
            }
        }
        Log.i(TAG, "dropped all Wi-Fi clients after token revocation");
    }

    private void sendReady(WebSocket conn, int clientCount) {
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
            obj.put("clients", clientCount);
            obj.put("cert_sha256", certFingerprint);
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
            obj.put("cert_sha256", certFingerprint);
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
            obj.put("cert_sha256", certFingerprint);
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
        sendError(conn, message, 0);
    }

    private void sendError(WebSocket conn, String message, long retryAfter) {
        JSONObject obj = new JSONObject();
        try {
            obj.put("type", "error");
            obj.put("error", message);
            if (retryAfter > 0) {
                obj.put("retry_after", retryAfter);
            }
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
        synchronized (pairingLock) {
            long now = System.currentTimeMillis();
            if (pairingCode.isEmpty() || now > pairingExpiresAt) {
                generatePairingCodeLocked(now);
            }
            Iterator<Map.Entry<String, PairAttempts>> it =
                    pairAttempts.entrySet().iterator();
            while (it.hasNext()) {
                PairAttempts state = it.next().getValue();
                if (now - state.lastFailure > PAIR_ATTEMPT_WINDOW_MS) {
                    it.remove();
                }
            }
        }
    }

    /** Invalidates the current code and gives the user a fresh one. */
    void rotatePairingCode() {
        synchronized (pairingLock) {
            rotatePairingCodeLocked();
            pairAttempts.clear();
        }
    }

    private void rotatePairingCodeLocked() {
        generatePairingCodeLocked(System.currentTimeMillis());
    }

    private void generatePairingCodeLocked(long now) {
        pairingCode = String.format(Locale.US, "%06d", 100000 + random.nextInt(900000));
        pairingExpiresAt = now + PAIRING_TTL_MS;
        ServiceState.pairingCode = pairingCode;
        ServiceState.pairingExpiresAt = pairingExpiresAt;
    }

    private boolean validPairingCode(String code) {
        synchronized (pairingLock) {
            long now = System.currentTimeMillis();
            if (pairingCode.isEmpty() || now > pairingExpiresAt) {
                generatePairingCodeLocked(now);
            }
            if (code == null || code.isEmpty() || now > pairingExpiresAt) {
                return false;
            }
            return MessageDigest.isEqual(
                    code.getBytes(StandardCharsets.US_ASCII),
                    pairingCode.getBytes(StandardCharsets.US_ASCII));
        }
    }

    // A wrong code from the same address slows down further attempts instead
    // of locking the user out. The delay is per IP and resets on success or
    // after a quiet period.
    private long pairCooldownSeconds(String ip) {
        PairAttempts state = pairAttempts.get(ip);
        if (state == null) {
            return 0;
        }
        long now = System.currentTimeMillis();
        if (now - state.lastFailure > PAIR_ATTEMPT_WINDOW_MS) {
            pairAttempts.remove(ip);
            return 0;
        }
        long left = state.nextAllowedAt - now;
        return left > 0 ? (left + 999) / 1000 : 0;
    }

    private long recordPairFailure(String ip) {
        synchronized (pairingLock) {
            long now = System.currentTimeMillis();
            PairAttempts previous = pairAttempts.get(ip);
            if (previous != null && now - previous.lastFailure > PAIR_ATTEMPT_WINDOW_MS) {
                previous = null;
            }
            int failures = previous == null ? 1 : previous.failures + 1;
            long delay = pairDelaySeconds(failures);
            pairAttempts.put(ip, new PairAttempts(failures, now, now + delay * 1000L));
            return delay;
        }
    }

    private static long pairDelaySeconds(int failures) {
        if (failures <= FREE_PAIR_ATTEMPTS) {
            return 0;
        }
        int shift = Math.min(failures - FREE_PAIR_ATTEMPTS - 1, 10);
        return Math.min(MAX_PAIR_DELAY_SECONDS, 1L << shift);
    }

    private static String remoteIp(WebSocket conn, Session session) {
        if (session != null && session.remoteIp != null) {
            return session.remoteIp;
        }
        InetSocketAddress address = conn.getRemoteSocketAddress();
        if (address == null || address.getAddress() == null) {
            return "unknown";
        }
        return address.getAddress().getHostAddress();
    }

    /** Accepts only an IP literal injected by TlsFrontend; never resolves DNS. */
    private static String sanitizeIp(String value) {
        if (value == null) {
            return null;
        }
        String ip = value.trim();
        int zone = ip.indexOf('%');
        if (zone >= 0) {
            ip = ip.substring(0, zone);
        }
        if (ip.isEmpty() || ip.length() > 45 || !ip.matches("[0-9a-fA-F:.]+")) {
            return null;
        }
        return ip;
    }

    private static String newInternalSecret() {
        byte[] bytes = new byte[32];
        new SecureRandom().nextBytes(bytes);
        StringBuilder sb = new StringBuilder(bytes.length * 2);
        for (byte b : bytes) {
            sb.append(String.format(Locale.US, "%02x", b));
        }
        return sb.toString();
    }

    private void closeStaleSessions() {
        long now = System.currentTimeMillis();
        for (Map.Entry<WebSocket, Session> entry : sessions.entrySet()) {
            Session session = entry.getValue();
            synchronized (session) {
                if (!session.authenticated && now - session.openedAt > AUTH_TIMEOUT_MS) {
                    entry.getKey().close(1008, "authentication timeout");
                }
            }
        }
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
        info.setPort(port());
        try {
            info.setAttribute("id", deviceId);
            info.setAttribute("name", deviceName);
            info.setAttribute("v", "1");
            info.setAttribute("pair", "1");
            // TLS certificate fingerprint for client pinning (TOFU).
            info.setAttribute("sha256", certFingerprint);
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

    // ------------------------------------------------------------------
    // Client sink
    // ------------------------------------------------------------------

    private static final class Session {
        final long openedAt;
        final String remoteIp;
        volatile boolean authenticated;

        Session(long openedAt, String remoteIp) {
            this.openedAt = openedAt;
            this.remoteIp = remoteIp;
        }
    }

    private static final class PairAttempts {
        final int failures;
        final long lastFailure;
        final long nextAllowedAt;

        PairAttempts(int failures, long lastFailure, long nextAllowedAt) {
            this.failures = failures;
            this.lastFailure = lastFailure;
            this.nextAllowedAt = nextAllowedAt;
        }
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
