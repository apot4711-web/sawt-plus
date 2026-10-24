package com.sawtplus.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.PowerManager;
import android.util.Log;

import androidx.core.app.NotificationCompat;
import androidx.core.content.ContextCompat;

import java.io.IOException;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;

public class RecordService extends Service {
    private static final String TAG = App.TAG;

    static final String ACTION_START = App.PKG + ".START";
    static final String ACTION_STOP = App.PKG + ".STOP";
    static final String ACTION_RELOAD = App.PKG + ".RELOAD";
    static final String ACTION_NEW_CODE = App.PKG + ".NEW_CODE";
    static final String ACTION_UNPAIR_ALL = App.PKG + ".UNPAIR_ALL";

    private static final String CHANNEL_ID = "sawt";
    private static final int NOTIFICATION_ID = 1;

    private final Handler handler = new Handler(Looper.getMainLooper());
    private final Runnable notificationTicker = new Runnable() {
        @Override
        public void run() {
            updateNotification();
            handler.postDelayed(this, 2000);
        }
    };

    // Transport setup/teardown touches sockets (TLS close, WebSocket close,
    // mDNS, ADB) and must never run on the main thread: closing a Conscrypt
    // TLS socket performs a close_notify write and throws
    // NetworkOnMainThreadException inside Service.onDestroy. All transport
    // mutations run on this single worker so start/reload/stop stay ordered.
    private final ExecutorService transportExecutor =
            Executors.newSingleThreadExecutor(r -> {
                Thread t = new Thread(r, "sawt-transport");
                t.setDaemon(true);
                return t;
            });
    // Static so a restarted service instance waits for the previous instance's
    // teardown (TLS/WebSocket close, ADB abstract socket, mDNS) before binding
    // the same ports and socket names again.
    private static final Object transportLock = new Object();
    /** Completed when the previous instance's teardown has fully run. */
    private static volatile CountDownLatch pendingTeardown;
    private volatile boolean destroyed;

    private StreamHub hub;
    private AdbServer adbServer;
    private WifiServer wifiServer;
    private TokenStore tokens;
    private PowerManager.WakeLock wakeLock;

    static void start(Context context) {
        Intent intent = new Intent(context, RecordService.class).setAction(ACTION_START);
        ContextCompat.startForegroundService(context, intent);
    }

    static void stop(Context context) {
        Intent intent = new Intent(context, RecordService.class).setAction(ACTION_STOP);
        context.startService(intent);
    }

    static void reload(Context context) {
        Intent intent = new Intent(context, RecordService.class).setAction(ACTION_RELOAD);
        ContextCompat.startForegroundService(context, intent);
    }

    static void newPairingCode(Context context) {
        Intent intent = new Intent(context, RecordService.class).setAction(ACTION_NEW_CODE);
        context.startService(intent);
    }

    /**
     * Revokes all pairing tokens and drops every live client session
     * (Wi-Fi and ADB). Affected PCs must pair again with a new code.
     */
    static void unpairAll(Context context) {
        Intent intent = new Intent(context, RecordService.class).setAction(ACTION_UNPAIR_ALL);
        context.startService(intent);
    }

    @Override
    public void onCreate() {
        super.onCreate();
        createChannel();
        startForegroundWithNotification(buildNotification(getString(R.string.notification_starting)));
        acquireWakeLock();
        tokens = new TokenStore(this);
        tokens.deviceId();
        ServiceState.muted = tokens.muted();
        ServiceState.gain = tokens.gain();
        ServiceState.running = true;
        handler.post(notificationTicker);
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        String action = intent == null ? ACTION_START : intent.getAction();
        if (ACTION_STOP.equals(action)) {
            stopSelf();
            return START_NOT_STICKY;
        }
        if (destroyed) {
            return START_NOT_STICKY;
        }

        transportExecutor.execute(() -> runTransportAction(action));
        return START_STICKY;
    }

    private void runTransportAction(String action) {
        if (destroyed) {
            return;
        }
        CountDownLatch pending = pendingTeardown;
        if (pending != null) {
            try {
                // Stop->Start in quick succession: let the old instance release
                // port 8765 and @sawt-plus before binding them here.
                pending.await(5, TimeUnit.SECONDS);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return;
            }
        }
        try {
            synchronized (transportLock) {
                if (destroyed) {
                    return;
                }
                ensureStarted();

                if (ACTION_RELOAD.equals(action)) {
                    stopWifi();
                    applyTransportPreference();
                } else if (ACTION_NEW_CODE.equals(action) && wifiServer != null) {
                    wifiServer.rotatePairingCode();
                } else if (ACTION_UNPAIR_ALL.equals(action)) {
                    tokens.removeAll();
                    if (wifiServer != null) {
                        wifiServer.dropAllClients();
                    }
                    if (adbServer != null) {
                        adbServer.dropClient();
                    }
                    Log.i(TAG, "all pairing tokens revoked by user");
                }
            }
        } catch (RuntimeException e) {
            Log.e(TAG, "transport action failed", e);
            ServiceState.error = e.getMessage() == null ? e.toString() : e.getMessage();
        }
        handler.post(this::updateNotification);
    }

    private void ensureStarted() {
        if (hub != null) {
            return;
        }
        Log.i(TAG, "starting service");
        hub = new StreamHub(this);
        if (tokens == null) {
            tokens = new TokenStore(this);
        }
        adbServer = new AdbServer(hub, tokens);
        adbServer.start();
        if (tokens.transportWifi()) {
            startWifi();
        }
        ServiceState.running = true;
    }

    private void applyTransportPreference() {
        if (tokens == null) {
            tokens = new TokenStore(this);
        }
        if (tokens.transportWifi()) {
            startWifi();
        } else {
            stopWifi();
        }
    }

    private void startWifi() {
        if (wifiServer != null) {
            return;
        }
        try {
            wifiServer = new WifiServer(this, hub, tokens, App.PORT);
            if (wifiServer.startServer()) {
                ServiceState.error = "";
            } else {
                wifiServer = null;
            }
        } catch (IOException e) {
            Log.e(TAG, "start Wi-Fi", e);
            ServiceState.error = "Wi-Fi server: " + e.getMessage();
            wifiServer = null;
        }
    }

    private void stopWifi() {
        if (wifiServer != null) {
            wifiServer.stopServer();
            wifiServer = null;
        }
        ServiceState.wifiEnabled = false;
        ServiceState.address = "";
        ServiceState.port = 0;
        ServiceState.pairingCode = "";
    }

    @Override
    public void onDestroy() {
        Log.i(TAG, "stopping service");
        destroyed = true;
        handler.removeCallbacks(notificationTicker);
        ServiceState.running = false;
        ServiceState.reset();
        stopForeground(true);
        // Release the ADB abstract socket synchronously: it is a local socket
        // (no network I/O) and holding it would make an immediate service
        // restart fail with "Address already in use".
        if (adbServer != null) {
            adbServer.shutdown();
            adbServer = null;
        }
        // The rest can block on network close (TLS close_notify, WebSocket
        // close, mDNS), so it runs on the transport worker. A restarted
        // instance waits on this latch (and transportLock) before binding the
        // same ports and socket names again.
        CountDownLatch latch = new CountDownLatch(1);
        pendingTeardown = latch;
        transportExecutor.execute(() -> teardown(latch));
        transportExecutor.shutdown();
        super.onDestroy();
    }

    private void teardown(CountDownLatch latch) {
        try {
            synchronized (transportLock) {
                stopWifi();
                if (adbServer != null) {
                    adbServer.shutdown();
                    adbServer = null;
                }
                if (hub != null) {
                    hub.shutdown();
                    hub = null;
                }
            }
            releaseWakeLock();
        } finally {
            latch.countDown();
            if (pendingTeardown == latch) {
                pendingTeardown = null;
            }
        }
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    // ------------------------------------------------------------------
    // Notification
    // ------------------------------------------------------------------

    private void createChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel channel = new NotificationChannel(CHANNEL_ID,
                    getString(R.string.channel_name), NotificationManager.IMPORTANCE_LOW);
            channel.setDescription(getString(R.string.channel_description));
            NotificationManager manager = getSystemService(NotificationManager.class);
            if (manager != null) {
                manager.createNotificationChannel(channel);
            }
        }
    }

    private void startForegroundWithNotification(Notification notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE);
        } else {
            startForeground(NOTIFICATION_ID, notification);
        }
    }

    private void updateNotification() {
        NotificationManager manager = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (manager != null) {
            manager.notify(NOTIFICATION_ID, buildNotification(notificationText()));
        }
    }

    private String notificationText() {
        if (!ServiceState.wifiEnabled && ServiceState.clients == 0) {
            return getString(R.string.notification_waiting);
        }
        StringBuilder sb = new StringBuilder();
        if (ServiceState.wifiEnabled) {
            String count = getResources().getQuantityString(
                    R.plurals.notification_clients, ServiceState.clients, ServiceState.clients);
            sb.append(getString(R.string.notification_wifi_status,
                    ServiceState.address, ServiceState.port, count));
        }
        if (ServiceState.transport != null && ServiceState.transport.contains("ADB")) {
            if (sb.length() > 0) {
                sb.append(" · ");
            }
            sb.append("ADB");
        }
        return sb.length() == 0 ? getString(R.string.notification_waiting) : sb.toString();
    }

    private Notification buildNotification(String text) {
        int pendingFlags = PendingIntent.FLAG_UPDATE_CURRENT;
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            pendingFlags |= PendingIntent.FLAG_IMMUTABLE;
        }

        Intent openIntent = new Intent(this, MainActivity.class);
        PendingIntent contentIntent = PendingIntent.getActivity(this, 0, openIntent,
                pendingFlags);

        Intent stopIntent = new Intent(this, RecordService.class).setAction(ACTION_STOP);
        PendingIntent stopPending = PendingIntent.getService(this, 1, stopIntent,
                pendingFlags);

        return new NotificationCompat.Builder(this, CHANNEL_ID)
                .setContentTitle(getString(R.string.app_name))
                .setContentText(text)
                .setSmallIcon(R.drawable.ic_microphone)
                .setContentIntent(contentIntent)
                .setOngoing(true)
                .setOnlyAlertOnce(true)
                .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
                .addAction(0, getString(R.string.action_stop), stopPending)
                .build();
    }

    // ------------------------------------------------------------------
    // Wake lock
    // ------------------------------------------------------------------

    private void acquireWakeLock() {
        try {
            PowerManager pm = (PowerManager) getSystemService(POWER_SERVICE);
            if (pm == null) {
                return;
            }
            wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "sawt:stream");
            wakeLock.setReferenceCounted(false);
            wakeLock.acquire();
        } catch (Exception e) {
            Log.w(TAG, "wake lock", e);
        }
    }

    private void releaseWakeLock() {
        try {
            if (wakeLock != null && wakeLock.isHeld()) {
                wakeLock.release();
            }
        } catch (Exception ignored) {
        }
        wakeLock = null;
    }
}
