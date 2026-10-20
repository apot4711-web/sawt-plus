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

public class RecordService extends Service {
    private static final String TAG = App.TAG;

    static final String ACTION_START = App.PKG + ".START";
    static final String ACTION_STOP = App.PKG + ".STOP";
    static final String ACTION_RELOAD = App.PKG + ".RELOAD";

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

        ensureStarted();

        if (ACTION_RELOAD.equals(action)) {
            stopWifi();
            applyTransportPreference();
        }

        updateNotification();
        return START_STICKY;
    }

    private void ensureStarted() {
        if (hub != null) {
            return;
        }
        Log.i(TAG, "starting service");
        hub = new StreamHub(this);
        adbServer = new AdbServer(hub);
        adbServer.start();

        if (tokens == null) {
            tokens = new TokenStore(this);
        }
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
            wifiServer.startServer();
            ServiceState.error = "";
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
        handler.removeCallbacks(notificationTicker);
        stopWifi();
        if (adbServer != null) {
            adbServer.shutdown();
            adbServer = null;
        }
        if (hub != null) {
            hub.shutdown();
            hub = null;
        }
        releaseWakeLock();
        ServiceState.reset();
        stopForeground(true);
        super.onDestroy();
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
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
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
        Intent openIntent = new Intent(this, MainActivity.class);
        PendingIntent contentIntent = PendingIntent.getActivity(this, 0, openIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);

        Intent stopIntent = new Intent(this, RecordService.class).setAction(ACTION_STOP);
        PendingIntent stopPending = PendingIntent.getService(this, 1, stopIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);

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
