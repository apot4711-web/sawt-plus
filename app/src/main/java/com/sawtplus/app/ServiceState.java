package com.sawtplus.app;

/**
 * Small process-wide status object polled by MainActivity. All fields are
 * written from service threads and read from the UI thread.
 */
public final class ServiceState {
    public static volatile boolean running;
    public static volatile boolean wifiEnabled;
    public static volatile String address = "";
    public static volatile int port;
    public static volatile String pairingCode = "";
    public static volatile long pairingExpiresAt;
    public static volatile int clients;
    public static volatile String error = "";
    public static volatile String deviceName = "";
    public static volatile String deviceId = "";
    public static volatile String certFingerprint = "";
    public static volatile String source = "";
    public static volatile long frames;
    public static volatile long dropped;
    public static volatile String transport = "idle";
    public static volatile boolean muted;
    public static volatile float gain = 1f;
    public static volatile boolean userStopped;

    private ServiceState() {
    }

    public static void reset() {
        running = false;
        wifiEnabled = false;
        address = "";
        port = 0;
        pairingCode = "";
        pairingExpiresAt = 0;
        clients = 0;
        error = "";
        source = "";
        frames = 0;
        dropped = 0;
        certFingerprint = "";
        transport = "idle";
        muted = false;
        gain = 1f;
    }
}
