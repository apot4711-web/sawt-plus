#!/usr/bin/env bash
# Installs a private JDK 17 and Android SDK inside .toolchain/.
# Nothing is installed system-wide and nothing outside this project is touched.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TOOLCHAIN="$ROOT/.toolchain"
JDK_DIR="$TOOLCHAIN/jdk17"
SDK_DIR="$TOOLCHAIN/android-sdk"

mkdir -p "$TOOLCHAIN"

need_download=0
[ -x "$JDK_DIR/bin/java" ] || need_download=1
[ -x "$SDK_DIR/cmdline-tools/latest/bin/sdkmanager" ] || need_download=1
[ -d "$SDK_DIR/platforms/android-35" ] || need_download=1
[ -d "$SDK_DIR/build-tools/35.0.0" ] || need_download=1
[ -x "$SDK_DIR/platform-tools/adb" ] || need_download=1

if [ "$need_download" = 1 ] && [ "${ASSUME_YES:-0}" != 1 ]; then
    echo "The Android toolchain is missing or incomplete."
    echo "This will download ~800 MB into: $TOOLCHAIN"
    read -r -p "Continue? [y/N] " answer
    case "$answer" in
        y|Y|yes|YES) ;;
        *) echo "Aborted. Nothing was downloaded."; exit 1 ;;
    esac
fi

if [ ! -x "$JDK_DIR/bin/java" ]; then
    echo "[+] Downloading Temurin JDK 17 (user-local)"
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    curl -fL --retry 3 -o "$tmp/jdk.tar.gz" \
        "https://api.adoptium.net/v3/binary/latest/17/ga/linux/x64/jdk/hotspot/normal/eclipse"
    mkdir -p "$JDK_DIR"
    tar -xzf "$tmp/jdk.tar.gz" -C "$JDK_DIR" --strip-components=1
    rm -rf "$tmp"
    trap - EXIT
else
    echo "[=] JDK 17 already present"
fi

if [ ! -x "$SDK_DIR/cmdline-tools/latest/bin/sdkmanager" ]; then
    echo "[+] Downloading Android command-line tools"
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    curl -fL --retry 3 -o "$tmp/tools.zip" \
        "https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip"
    unzip -q "$tmp/tools.zip" -d "$tmp"
    mkdir -p "$SDK_DIR/cmdline-tools"
    rm -rf "$SDK_DIR/cmdline-tools/latest"
    mv "$tmp/cmdline-tools" "$SDK_DIR/cmdline-tools/latest"
    rm -rf "$tmp"
    trap - EXIT
else
    echo "[=] Android command-line tools already present"
fi

export JAVA_HOME="$JDK_DIR"
export ANDROID_HOME="$SDK_DIR"
export ANDROID_SDK_ROOT="$SDK_DIR"
export PATH="$JAVA_HOME/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH"

echo "[+] Accepting SDK licenses"
yes | sdkmanager --licenses >/dev/null 2>&1 || true

echo "[+] Installing platform-tools, Android 35 platform, build-tools 35"
sdkmanager --install "platform-tools" "platforms;android-35" "build-tools;35.0.0"

cat > "$ROOT/.toolchain/env.sh" <<EOF
export JAVA_HOME="$JDK_DIR"
export ANDROID_HOME="$SDK_DIR"
export ANDROID_SDK_ROOT="$SDK_DIR"
export PATH="\$JAVA_HOME/bin:\$ANDROID_HOME/cmdline-tools/latest/bin:\$ANDROID_HOME/platform-tools:\$PATH"
EOF

cat > "$ROOT/local.properties" <<EOF
sdk.dir=$SDK_DIR
EOF

echo
echo "[+] Toolchain ready."
echo "    source .toolchain/env.sh"
echo "    ./gradlew :app:assembleDebug"
