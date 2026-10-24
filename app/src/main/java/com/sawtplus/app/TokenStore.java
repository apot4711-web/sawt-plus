package com.sawtplus.app;

import android.content.Context;
import android.content.SharedPreferences;
import android.util.Base64;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.security.SecureRandom;
import java.util.HashSet;
import java.util.Set;
import java.util.UUID;

final class TokenStore {
    private static final String PREFS = "sawtplus";
    private static final String KEY_TOKENS = "paired_tokens";
    private static final String KEY_DEVICE_ID = "device_id";
    private static final String KEY_DEVICE_NAME = "device_name";
    private static final String DEFAULT_DEVICE_NAME = "Sawt+";
    private static final String KEY_WIFI = "wifi_enabled";
    private static final String KEY_TRANSPORT = "transport";
    private static final String KEY_MUTED = "muted";
    private static final String KEY_GAIN = "gain";
    private static final String KEY_THEME = "theme";
    private static final String KEY_LANGUAGE = "language";
    private static final String KEY_BATTERY_ACK = "battery_ack";

    private static final String TRANSPORT_WIFI = "wifi";
    private static final String TRANSPORT_ADB = "adb";

    static final String THEME_SYSTEM = "system";
    static final String THEME_LIGHT = "light";
    static final String THEME_DARK = "dark";

    private final SharedPreferences prefs;
    private final SecureRandom random = new SecureRandom();

    TokenStore(Context context) {
        prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    boolean transportWifi() {
        String transport = prefs.getString(KEY_TRANSPORT, null);
        if (TRANSPORT_ADB.equals(transport)) {
            return false;
        }
        if (TRANSPORT_WIFI.equals(transport)) {
            return true;
        }
        return prefs.getBoolean(KEY_WIFI, true);
    }

    void setTransportWifi(boolean wifi) {
        prefs.edit().putString(KEY_TRANSPORT, wifi ? TRANSPORT_WIFI : TRANSPORT_ADB).apply();
    }

    boolean muted() {
        return prefs.getBoolean(KEY_MUTED, false);
    }

    void setMuted(boolean muted) {
        prefs.edit().putBoolean(KEY_MUTED, muted).apply();
    }

    float gain() {
        return prefs.getFloat(KEY_GAIN, 1f);
    }

    void setGain(float gain) {
        prefs.edit().putFloat(KEY_GAIN, Math.max(0f, Math.min(8f, gain))).apply();
    }

    String theme() {
        String stored = prefs.getString(KEY_THEME, THEME_SYSTEM);
        // The monochrome theme was removed; fall back for old installs.
        if ("mono".equals(stored)) {
            return THEME_SYSTEM;
        }
        return stored;
    }

    void setTheme(String theme) {
        prefs.edit().putString(KEY_THEME, theme).apply();
    }

    String language() {
        return prefs.getString(KEY_LANGUAGE, "system");
    }

    void setLanguage(String language) {
        prefs.edit().putString(KEY_LANGUAGE, language).apply();
    }

    boolean batteryAcknowledged() {
        return prefs.getBoolean(KEY_BATTERY_ACK, false);
    }

    void setBatteryAcknowledged(boolean acknowledged) {
        prefs.edit().putBoolean(KEY_BATTERY_ACK, acknowledged).apply();
    }

    String deviceName() {
        String name = prefs.getString(KEY_DEVICE_NAME, DEFAULT_DEVICE_NAME);
        if (name == null || name.trim().isEmpty()) {
            return DEFAULT_DEVICE_NAME;
        }
        return name.trim();
    }

    void setDeviceName(String name) {
        String value = name == null ? "" : name.trim();
        if (value.isEmpty()) {
            value = DEFAULT_DEVICE_NAME;
        }
        prefs.edit().putString(KEY_DEVICE_NAME, value).apply();
    }

    String deviceId() {
        String id = prefs.getString(KEY_DEVICE_ID, null);
        if (id == null || id.isEmpty()) {
            id = UUID.randomUUID().toString().replace("-", "").substring(0, 16);
            prefs.edit().putString(KEY_DEVICE_ID, id).apply();
        }
        ServiceState.deviceId = id;
        return id;
    }

    boolean isValid(String token) {
        if (token == null || token.isEmpty()) {
            return false;
        }
        return tokens().contains(hash(token));
    }

    /**
     * Same as {@link #isValid(String)} but compares with
     * {@link MessageDigest#isEqual} so a local attacker cannot use timing
     * differences to test token guesses (used by the ADB socket handshake).
     */
    boolean isValidConstantTime(String token) {
        if (token == null || token.isEmpty()) {
            return false;
        }
        byte[] candidate = hash(token).getBytes(StandardCharsets.UTF_8);
        boolean ok = false;
        for (String stored : tokens()) {
            if (MessageDigest.isEqual(stored.getBytes(StandardCharsets.UTF_8), candidate)) {
                ok = true;
            }
        }
        return ok;
    }

    void add(String token) {
        Set<String> tokens = new HashSet<>(tokens());
        tokens.add(hash(token));
        prefs.edit().putStringSet(KEY_TOKENS, tokens).apply();
    }

    void remove(String token) {
        if (token == null || token.isEmpty()) {
            return;
        }
        Set<String> tokens = new HashSet<>(tokens());
        tokens.remove(hash(token));
        prefs.edit().putStringSet(KEY_TOKENS, tokens).apply();
    }

    /**
     * Revokes every pairing token. Live sessions must be dropped separately
     * (see WifiServer.dropAllClients and AdbServer.dropClient); any future
     * "hello" handshake presenting a revoked token is rejected and the PC
     * is told to pair again.
     */
    void removeAll() {
        prefs.edit().remove(KEY_TOKENS).apply();
    }

    String newToken() {
        byte[] bytes = new byte[32];
        random.nextBytes(bytes);
        return Base64.encodeToString(bytes,
                Base64.URL_SAFE | Base64.NO_WRAP | Base64.NO_PADDING);
    }

    Set<String> tokens() {
        Set<String> stored = prefs.getStringSet(KEY_TOKENS, null);
        return stored == null ? new HashSet<>() : new HashSet<>(stored);
    }

    private String hash(String token) {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            byte[] out = digest.digest(token.getBytes(StandardCharsets.UTF_8));
            StringBuilder sb = new StringBuilder(out.length * 2);
            for (byte b : out) {
                sb.append(String.format("%02x", b));
            }
            return sb.toString();
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
