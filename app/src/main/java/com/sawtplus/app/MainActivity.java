package com.sawtplus.app;

import android.Manifest;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.view.LayoutInflater;
import android.view.View;
import android.view.ViewGroup;
import android.widget.EditText;
import android.widget.TextView;

import androidx.appcompat.app.ActionBarDrawerToggle;
import androidx.appcompat.app.AppCompatActivity;
import androidx.appcompat.app.AppCompatDelegate;
import androidx.core.graphics.Insets;
import androidx.core.view.GravityCompat;
import androidx.core.view.ViewCompat;
import androidx.core.view.WindowInsetsCompat;
import androidx.drawerlayout.widget.DrawerLayout;

import com.google.android.material.bottomsheet.BottomSheetDialog;
import com.google.android.material.dialog.MaterialAlertDialogBuilder;
import com.google.android.material.button.MaterialButton;
import com.google.android.material.button.MaterialButtonToggleGroup;
import com.google.android.material.color.DynamicColors;
import com.google.android.material.color.MaterialColors;
import com.google.android.material.materialswitch.MaterialSwitch;
import com.google.android.material.navigation.NavigationView;
import com.google.android.material.slider.Slider;
import com.google.android.material.snackbar.Snackbar;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

public class MainActivity extends AppCompatActivity {
    private static final int REQUEST_PERMISSIONS = 100;

    private final Handler handler = new Handler(Looper.getMainLooper());

    private TextView statusView;
    private TextView addressView;
    private TextView codeView;
    private TextView clientsView;
    private TextView sourceView;
    private TextView errorView;
    private MaterialButton startButton;
    private MaterialButton batteryButton;
    private MaterialButtonToggleGroup transportGroup;
    private MaterialSwitch muteSwitch;
    private Slider gainSlider;
    private TextView gainValueView;
    private View batteryCard;
    private DrawerLayout drawerLayout;
    private NavigationView navigationView;
    private ActionBarDrawerToggle drawerToggle;
    private TokenStore tokens;

    private boolean syncingUi;
    private String uiError = "";

    private final Runnable poller = new Runnable() {
        @Override
        public void run() {
            updateUi();
            handler.postDelayed(this, 500);
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        tokens = new TokenStore(this);
        super.onCreate(savedInstanceState);
        if (TokenStore.THEME_SYSTEM.equals(tokens.theme())) {
            DynamicColors.applyToActivityIfAvailable(this);
        }
        tokens.deviceId();
        setContentView(R.layout.activity_main);
        bindViews();
        applyInsets();
        setupDrawer();
        setupListeners();
        ServiceState.muted = tokens.muted();

        if (missingPermissions().isEmpty()) {
            if (!ServiceState.userStopped) {
                RecordService.start(this);
            }
        } else {
            requestPermissions(missingPermissions().toArray(new String[0]), REQUEST_PERMISSIONS);
        }
        updateUi();
    }

    @Override
    protected void onResume() {
        super.onResume();
        handler.post(poller);
    }

    @Override
    protected void onPause() {
        super.onPause();
        handler.removeCallbacks(poller);
    }

    // ------------------------------------------------------------------
    // Permissions
    // ------------------------------------------------------------------

    private List<String> missingPermissions() {
        List<String> missing = new ArrayList<>();
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
            missing.add(Manifest.permission.RECORD_AUDIO);
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU
                && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            missing.add(Manifest.permission.POST_NOTIFICATIONS);
        }
        return missing;
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode != REQUEST_PERMISSIONS) {
            return;
        }
        if (missingPermissions().isEmpty()) {
            uiError = "";
            ServiceState.userStopped = false;
            RecordService.start(this);
        } else {
            showError(getString(R.string.permission_microphone_required));
        }
        updateUi();
    }

    // ------------------------------------------------------------------
    // UI setup
    // ------------------------------------------------------------------

    private void bindViews() {
        statusView = findViewById(R.id.statusView);
        addressView = findViewById(R.id.addressView);
        codeView = findViewById(R.id.codeView);
        clientsView = findViewById(R.id.clientsView);
        sourceView = findViewById(R.id.sourceView);
        errorView = findViewById(R.id.errorView);
        startButton = findViewById(R.id.startButton);
        batteryButton = findViewById(R.id.batteryButton);
        batteryCard = findViewById(R.id.batteryCard);
        transportGroup = findViewById(R.id.transportGroup);
        muteSwitch = findViewById(R.id.muteSwitch);
        gainSlider = findViewById(R.id.gainSlider);
        gainValueView = findViewById(R.id.gainValueView);
        drawerLayout = findViewById(R.id.root);
        navigationView = findViewById(R.id.navigationView);
        // Streaming always uses ASCII digits, even in Arabic locales.
        codeView.setTextLocale(Locale.US);
    }

    private void applyInsets() {
        View root = findViewById(R.id.root);
        View content = findViewById(R.id.content);
        int margin = Math.round(16 * getResources().getDisplayMetrics().density);
        ViewCompat.setOnApplyWindowInsetsListener(root, (view, insets) -> {
            Insets bars = insets.getInsets(WindowInsetsCompat.Type.systemBars());
            if (content.getPaddingTop() != bars.top
                    || content.getPaddingBottom() != bars.bottom
                    || content.getPaddingLeft() != bars.left
                    || content.getPaddingRight() != bars.right) {
                content.setPadding(bars.left, bars.top, bars.right, bars.bottom);
            }
            ViewGroup.MarginLayoutParams lp =
                    (ViewGroup.MarginLayoutParams) navigationView.getLayoutParams();
            int top = bars.top + margin;
            int bottom = bars.bottom + margin;
            if (lp.topMargin != top || lp.bottomMargin != bottom
                    || lp.leftMargin != 0 || lp.rightMargin != 0) {
                lp.setMargins(0, top, 0, bottom);
                navigationView.setLayoutParams(lp);
            }
            return insets;
        });
    }

    private static void setTextIfChanged(TextView view, CharSequence text) {
        if (!view.getText().toString().contentEquals(text)) {
            view.setText(text);
        }
    }

    private static void setColorIfChanged(TextView view, int color) {
        if (view.getCurrentTextColor() != color) {
            view.setTextColor(color);
        }
    }

    private void setupListeners() {
        startButton.setOnClickListener(v -> {
            if (ServiceState.running) {
                ServiceState.userStopped = true;
                RecordService.stop(this);
            } else if (missingPermissions().isEmpty()) {
                ServiceState.userStopped = false;
                RecordService.start(this);
            } else {
                requestPermissions(missingPermissions().toArray(new String[0]), REQUEST_PERMISSIONS);
            }
        });

        transportGroup.addOnButtonCheckedListener((group, checkedId, isChecked) -> {
            if (!isChecked || syncingUi) {
                return;
            }
            boolean wifi = checkedId == R.id.transportWifi;
            tokens.setTransportWifi(wifi);
            // Switching transport never starts the service on its own; only
            // reload it if the service is already running.
            if (ServiceState.running) {
                RecordService.reload(this);
            }
            updateUi();
        });

        muteSwitch.setOnCheckedChangeListener((buttonView, isChecked) -> {
            if (syncingUi) {
                return;
            }
            tokens.setMuted(isChecked);
            ServiceState.muted = isChecked;
        });

        batteryButton.setOnClickListener(v -> {
            tokens.setBatteryAcknowledged(true);
            if (batteryCard != null) {
                batteryCard.setVisibility(View.GONE);
            }
            openBatterySettings();
        });

        codeView.setOnLongClickListener(v -> copyPairingCode());
        addressView.setOnClickListener(v -> copyAddress());

        gainSlider.setValue(clampGain(tokens.gain()));
        updateGainLabel(gainSlider.getValue());
        gainSlider.addOnChangeListener((slider, value, fromUser) -> {
            updateGainLabel(value);
            if (!fromUser) {
                return;
            }
            tokens.setGain(value);
            ServiceState.gain = value;
        });
    }

    private static float clampGain(float gain) {
        return Math.max(0f, Math.min(8f, gain));
    }

    private void updateGainLabel(float gain) {
        setTextIfChanged(gainValueView, String.format(Locale.US, "%.2f×", gain));
    }

    private void setupDrawer() {
        com.google.android.material.appbar.MaterialToolbar toolbar = findViewById(R.id.toolbar);
        drawerToggle = new ActionBarDrawerToggle(this, drawerLayout, toolbar,
                R.string.open_menu, R.string.close_menu);
        drawerLayout.addDrawerListener(drawerToggle);
        drawerToggle.syncState();
        navigationView.setNavigationItemSelectedListener(item -> {
            int id = item.getItemId();
            drawerLayout.closeDrawer(GravityCompat.START);
            if (id == R.id.nav_settings) {
                showSettingsSheet();
            } else if (id == R.id.nav_about) {
                showAboutSheet();
            }
            return true;
        });
    }

    // ------------------------------------------------------------------
    // Drawer sheets
    // ------------------------------------------------------------------

    private void showSettingsSheet() {
        View content = LayoutInflater.from(this).inflate(R.layout.sheet_settings, null);
        TextView languageValue = content.findViewById(R.id.languageValueView);
        TextView themeValue = content.findViewById(R.id.themeValueView);
        TextView deviceNameValue = content.findViewById(R.id.deviceNameValueView);
        languageValue.setText(languageLabel(tokens.language()));
        themeValue.setText(themeLabel(tokens.theme()));
        deviceNameValue.setText(tokens.deviceName());

        BottomSheetDialog dialog = new BottomSheetDialog(this);
        dialog.setContentView(content);
        content.findViewById(R.id.rowLanguage).setOnClickListener(v -> {
            dialog.dismiss();
            showLanguageDialog();
        });
        content.findViewById(R.id.rowTheme).setOnClickListener(v -> {
            dialog.dismiss();
            showThemeDialog();
        });
        content.findViewById(R.id.rowDeviceName).setOnClickListener(v -> {
            dialog.dismiss();
            showDeviceNameDialog();
        });
        content.findViewById(R.id.rowBattery).setOnClickListener(v -> {
            dialog.dismiss();
            openBatterySettings();
        });
        dialog.show();
    }

    private void showLanguageDialog() {
        final String[] values = {"system", "en", "ar"};
        int checked = 0;
        for (int i = 0; i < values.length; i++) {
            if (values[i].equals(tokens.language())) {
                checked = i;
                break;
            }
        }
        String[] labels = {
                getString(R.string.language_system),
                getString(R.string.language_english),
                getString(R.string.language_arabic)
        };
        new MaterialAlertDialogBuilder(this)
                .setTitle(R.string.settings_language)
                .setSingleChoiceItems(labels, checked, (d, which) -> {
                    tokens.setLanguage(values[which]);
                    AppCompatDelegate.setApplicationLocales(AudioApp.locales(values[which]));
                    d.dismiss();
                })
                .setNegativeButton(android.R.string.cancel, null)
                .show();
    }

    private void showThemeDialog() {
        final String[] values = {
                TokenStore.THEME_SYSTEM,
                TokenStore.THEME_LIGHT,
                TokenStore.THEME_DARK
        };
        int checked = 0;
        for (int i = 0; i < values.length; i++) {
            if (values[i].equals(tokens.theme())) {
                checked = i;
                break;
            }
        }
        String[] labels = {
                getString(R.string.theme_system),
                getString(R.string.theme_light),
                getString(R.string.theme_dark)
        };
        new MaterialAlertDialogBuilder(this)
                .setTitle(R.string.settings_theme)
                .setSingleChoiceItems(labels, checked, (d, which) -> {
                    tokens.setTheme(values[which]);
                    int mode = AudioApp.nightMode(values[which]);
                    if (AppCompatDelegate.getDefaultNightMode() != mode) {
                        AppCompatDelegate.setDefaultNightMode(mode);
                    } else {
                        recreate();
                    }
                    d.dismiss();
                })
                .setNegativeButton(android.R.string.cancel, null)
                .show();
    }

    private void showDeviceNameDialog() {
        EditText input = new EditText(this);
        input.setText(tokens.deviceName());
        input.setSingleLine(true);
        input.setSelectAllOnFocus(true);
        input.setHint(R.string.device_name_hint);
        int pad = Math.round(24 * getResources().getDisplayMetrics().density);
        input.setPadding(pad, pad / 2, pad, pad / 2);

        new MaterialAlertDialogBuilder(this)
                .setTitle(R.string.settings_device_name)
                .setView(input)
                .setPositiveButton(android.R.string.ok, (d, which) -> {
                    tokens.setDeviceName(input.getText().toString());
                    if (ServiceState.running) {
                        RecordService.reload(this);
                    }
                    Snackbar.make(findViewById(R.id.root), R.string.device_name_saved, Snackbar.LENGTH_SHORT).show();
                })
                .setNegativeButton(android.R.string.cancel, null)
                .show();
    }

    private String languageLabel(String language) {
        if ("ar".equals(language)) {
            return getString(R.string.language_arabic);
        }
        if ("en".equals(language)) {
            return getString(R.string.language_english);
        }
        return getString(R.string.language_system);
    }

    private String themeLabel(String theme) {
        if (TokenStore.THEME_LIGHT.equals(theme)) {
            return getString(R.string.theme_light);
        }
        if (TokenStore.THEME_DARK.equals(theme)) {
            return getString(R.string.theme_dark);
        }
        return getString(R.string.theme_system);
    }

    private void showAboutSheet() {
        View content = LayoutInflater.from(this).inflate(R.layout.sheet_about, null);
        TextView versionView = content.findViewById(R.id.versionView);
        String version;
        try {
            version = getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
        } catch (PackageManager.NameNotFoundException e) {
            version = "";
        }
        versionView.setText(getString(R.string.about_version, version));

        BottomSheetDialog dialog = new BottomSheetDialog(this);
        dialog.setContentView(content);
        dialog.show();
    }

    // ------------------------------------------------------------------
    // State rendering
    // ------------------------------------------------------------------

    private void updateUi() {
        if (statusView == null) {
            return;
        }
        boolean running = ServiceState.running;
        boolean wifi = ServiceState.wifiEnabled;
        String address = ServiceState.address;
        int port = ServiceState.port;
        boolean wifiPref = tokens.transportWifi();

        int onlineColor = MaterialColors.getColor(statusView,
                com.google.android.material.R.attr.colorPrimary);
        int idleColor = MaterialColors.getColor(statusView,
                com.google.android.material.R.attr.colorOnSurfaceVariant);

        if (running) {
            if (wifi && !address.isEmpty()) {
                setTextIfChanged(statusView, getText(R.string.status_ready_wifi));
            } else if (ServiceState.transport != null && ServiceState.transport.contains("ADB")) {
                setTextIfChanged(statusView, getText(R.string.status_adb));
            } else {
                setTextIfChanged(statusView, getText(R.string.status_running));
            }
            setColorIfChanged(statusView, onlineColor);
        } else {
            setTextIfChanged(statusView, getText(R.string.status_stopped));
            setColorIfChanged(statusView, idleColor);
        }

        if (wifi && !address.isEmpty()) {
            setTextIfChanged(addressView, address + ":" + port);
        } else if (!wifiPref) {
            setTextIfChanged(addressView, getText(R.string.address_adb));
        } else {
            setTextIfChanged(addressView, getText(R.string.address_unavailable));
        }

        String code = ServiceState.pairingCode;
        if (wifi && code != null && !code.isEmpty()) {
            String pretty = code.length() == 6
                    ? code.substring(0, 3) + " " + code.substring(3)
                    : code;
            setTextIfChanged(codeView, pretty);
            long left = ServiceState.pairingExpiresAt - System.currentTimeMillis();
            long minutes = Math.max(0, left / 60000);
            setTextIfChanged(clientsView, getString(R.string.pairing_valid, minutes, ServiceState.clients));
        } else {
            setTextIfChanged(codeView, getText(R.string.pairing_empty));
            setTextIfChanged(clientsView, getString(R.string.clients_connected, ServiceState.clients));
        }

        String source = ServiceState.source.isEmpty()
                ? getString(R.string.source_default)
                : ServiceState.source;
        setTextIfChanged(sourceView, getString(R.string.source_info, source, ServiceState.frames, ServiceState.dropped));

        String error = ServiceState.error == null ? "" : ServiceState.error;
        if (error.isEmpty()) {
            error = uiError;
        }
        if (!error.isEmpty()) {
            setTextIfChanged(errorView, error);
            if (errorView.getVisibility() != View.VISIBLE) {
                errorView.setVisibility(View.VISIBLE);
            }
        } else if (errorView.getVisibility() != View.GONE) {
            errorView.setVisibility(View.GONE);
        }

        setTextIfChanged(startButton, getText(running
                ? R.string.action_stop_service
                : R.string.action_start));

        syncingUi = true;
        int transportId = wifiPref ? R.id.transportWifi : R.id.transportAdb;
        if (transportGroup.getCheckedButtonId() != transportId) {
            transportGroup.check(transportId);
        }
        if (muteSwitch.isChecked() != tokens.muted()) {
            muteSwitch.setChecked(tokens.muted());
        }
        if (Math.abs(gainSlider.getValue() - tokens.gain()) > 0.001f) {
            gainSlider.setValue(clampGain(tokens.gain()));
        }
        updateGainLabel(gainSlider.getValue());
        int batteryVisibility = tokens.batteryAcknowledged() ? View.GONE : View.VISIBLE;
        if (batteryCard.getVisibility() != batteryVisibility) {
            batteryCard.setVisibility(batteryVisibility);
        }
        syncingUi = false;
    }

    // ------------------------------------------------------------------
    // Actions
    // ------------------------------------------------------------------

    private boolean copyAddress() {
        if (!ServiceState.wifiEnabled || ServiceState.address.isEmpty()) {
            return false;
        }
        String address = ServiceState.address + ":" + ServiceState.port;
        ClipboardManager clipboard = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
        if (clipboard == null) {
            return false;
        }
        clipboard.setPrimaryClip(ClipData.newPlainText(getString(R.string.address_label), address));
        Snackbar.make(findViewById(R.id.root), R.string.address_copied, Snackbar.LENGTH_SHORT).show();
        return true;
    }

    private boolean copyPairingCode() {
        CharSequence current = codeView.getText();
        if (current == null || current.length() == 0 || "—".contentEquals(current)) {
            return false;
        }
        String code = ServiceState.pairingCode;
        if (code == null || code.isEmpty()) {
            return false;
        }
        ClipboardManager clipboard = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
        if (clipboard == null) {
            return false;
        }
        clipboard.setPrimaryClip(ClipData.newPlainText(getString(R.string.pairing_label), code));
        Snackbar.make(findViewById(R.id.root), R.string.code_copied, Snackbar.LENGTH_SHORT).show();
        return true;
    }

    private void showError(String message) {
        uiError = message;
        if (errorView != null) {
            errorView.setText(message);
            errorView.setVisibility(View.VISIBLE);
        }
    }

    private void openBatterySettings() {
        try {
            Intent intent = new Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS);
            startActivity(intent);
        } catch (Exception e) {
            Intent intent = new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                    Uri.parse("package:" + getPackageName()));
            startActivity(intent);
        }
    }
}
