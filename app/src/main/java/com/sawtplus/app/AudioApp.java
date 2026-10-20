package com.sawtplus.app;

import android.app.Application;

import androidx.appcompat.app.AppCompatDelegate;
import androidx.core.os.LocaleListCompat;

/**
 * Applies the user's in-app language and theme (system/light/dark)
 * before any activity is created. The selection is stored in SharedPreferences
 * so it survives process restarts and reinstalls that keep app data.
 */
public class AudioApp extends Application {
    @Override
    public void onCreate() {
        super.onCreate();
        TokenStore store = new TokenStore(this);
        AppCompatDelegate.setDefaultNightMode(nightMode(store.theme()));
        AppCompatDelegate.setApplicationLocales(locales(store.language()));
    }

    static int nightMode(String theme) {
        if (TokenStore.THEME_LIGHT.equals(theme)) {
            return AppCompatDelegate.MODE_NIGHT_NO;
        }
        if (TokenStore.THEME_DARK.equals(theme)) {
            return AppCompatDelegate.MODE_NIGHT_YES;
        }
        return AppCompatDelegate.MODE_NIGHT_FOLLOW_SYSTEM;
    }

    static LocaleListCompat locales(String language) {
        if ("ar".equals(language)) {
            return LocaleListCompat.forLanguageTags("ar");
        }
        if ("en".equals(language)) {
            return LocaleListCompat.forLanguageTags("en");
        }
        return LocaleListCompat.getEmptyLocaleList();
    }
}
