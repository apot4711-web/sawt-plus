# صوت+ (Sawt+)

Turn an Android phone into a wireless microphone for a Linux computer. The
phone captures its microphone and streams 48 kHz mono PCM to a small headless
daemon on the PC, which exposes it as a PipeWire/PulseAudio source. Control it
from the terminal or from an optional local web page.

Sawt+ is based on the original open-source
[`gdzx/audiosource`](https://github.com/gdzx/audiosource) project, but uses
Wi-Fi as the primary transport, adds pairing, discovery, input-level control,
and a Material 3 Android app. It is a separate project; do not install it over
the original app.

## Features

- **Wi-Fi first** on the local network; USB/ADB remains as a fallback.
- **Multi-layer discovery**: mDNS, `avahi-browse`, and an active local-network
  scan, merged into one device list.
- **Pair once**: enter the 6-digit code shown on the phone; the token is stored
  so later connections are automatic.
- **One command to stream**: most CLI commands start the background daemon
  automatically.
- **Phone and PC controls**: mute and the input level ("مستوى الصوت") are
  available in the Android app, in the CLI, and in the browser UI.
- **Optional browser UI** served on localhost by the daemon; no Node.js or
  frontend build step.
- **Material 3 Android app** with Arabic and English, RTL support, and
  system/light/dark themes.

The stream is 16-bit signed little-endian PCM, 48 kHz, mono, in 20 ms frames
over a WebSocket: text control messages plus binary audio frames.

## Requirements

- Linux with PipeWire or PulseAudio and `pactl` in `PATH`.
- Android 7.0 (API 24) or newer on the phone.
- Both devices on the same local network (or USB debugging for the ADB
  fallback).
- For discovery: nothing extra is required for mDNS, but installing
  `avahi-utils` (for `avahi-browse`) makes discovery more reliable on systems
  where the Go mDNS resolver is blocked by the firewall.

To build from source you also need Go 1.26+ and a JDK/Android SDK for the app.
The repository keeps a private toolchain in `.toolchain`; see
[Building](#building).

## Quick start

### 1. Build and install the Android app

```console
$ ./gradlew :app:assembleDebug
$ adb install -r app/build/outputs/apk/debug/app-debug.apk
```

Or, with the Go client available:

```console
$ ./sawt build
$ ./sawt install
```

The first build needs the Android SDK and JDK; run
`scripts/setup-android.sh` once. It asks before any large download.

### 2. Build the Go client

```console
$ go build -o sawt ./cmd/sawt
```

On the phone, open the app, keep the microphone service running, and note the
6-digit pairing code.

### 3. Pair and connect

```console
$ ./sawt discover
NAME              ADDRESS             VERSION   PAIRING
Pixel 8           192.168.1.42:8765    1         required

$ ./sawt pair 192.168.1.42:8765 123456
```

`pair` connects and starts streaming unless you pass `--no-connect`. After the
first pairing you can simply run:

```console
$ ./sawt connect
```

The PC creates a source named `sawt-plus`. Select it as the microphone
in your meeting or recording application.

### 4. Optional browser UI

```console
$ ./sawt web
```

This starts the daemon if needed and opens `http://127.0.0.1:8787`. The page is
served by the embedded daemon; there is no separate web server.

## CLI

```console
$ sawt help
```

| Command | Description |
| --- | --- |
| `daemon` | Run the headless daemon and local web API in the foreground. |
| `web [--no-open]` | Open the browser UI (starts the daemon if needed). |
| `status [--json]` | Show stream, device, gain, and mute state. |
| `logs [-n N]` | Show recent daemon log lines. |
| `discover [--timeout 3s]` | Scan for Wi-Fi and USB/ADB devices. |
| `pair TARGET CODE` | Pair by `host:port` and the 6-digit code. |
| `pair --no-connect TARGET CODE` | Pair without starting the stream. |
| `unpair [TARGET]` | Forget a saved pairing (default: current device). |
| `connect [TARGET]` | Connect and stream (address, saved name, or `adb:SERIAL`). |
| `disconnect`, `stop` | Stop streaming. |
| `gain 1.5x` | Set the PC-side input level (`0`..`8`, `0%`..`800%`). |
| `mute [on\|off]` | Mute the microphone, or toggle with no argument. |
| `shutdown`, `quit` | Stop a daemon running in the background. |
| `adb <cmd>` | ADB fallback: `devices`, `pair`, `connect`, `disconnect`, `run`, `install`. |
| `build`, `install` | Build/install the Android APK. |
| `version` | Print the version. |

Most commands start `sawt daemon` in the background (detached, log at
`~/.config/sawt-plus/daemon.log`) if the API is not reachable. Set
`SAWT_NO_AUTOSTART=1` to require an explicit daemon.

## Discovery and the firewall

Discovery tries three layers and merges the results:

1. Go mDNS (`_sawtplus._tcp`).
2. `avahi-browse` on systems where it is installed.
3. An active scan of the local `/24` on TCP port 8765 that confirms a
   WebSocket server before reporting a device.

Devices connected over USB/ADB are merged into the same list and labeled
`USB/ADB`; Wi-Fi devices are labeled `Wi-Fi`.

If `discover` finds nothing while `avahi-browse -rt _sawtplus._tcp` sees the
phone, the desktop firewall is probably dropping mDNS for the Go process.
Allow UDP 5353, for example:

```console
# firewalld
$ sudo firewall-cmd --add-service=mdns
$ sudo firewall-cmd --runtime-to-permanent

# ufw
$ sudo ufw allow 5353/udp
```

The active scan works without the mDNS exception, as long as the phone accepts
connections on port 8765 in the local network.

## Input level

The PC applies a digital gain in the daemon, and the Android app has its own
"مستوى الصوت" slider that scales the captured samples before sending them. The
two controls are independent: leave one at 1.00× and adjust the other. Both are
named "مستوى الصوت" / "Input level" in the UI to avoid the technical term
"gain" in user-facing text.

There is no separate PipeWire source-volume control: one level control per
surface is enough.

## USB/ADB fallback

Wi-Fi is the intended transport, but ADB still works when both devices are on
different networks or when Wi-Fi is unreliable.

1. Enable USB debugging on the phone and connect the cable.
2. Check the device: `adb devices` (or `sawt adb devices`).
3. Connect: `sawt connect adb:SERIAL`, run the ADB loop directly with
   `sawt adb run`, or pick the device labeled `USB/ADB` in the browser
   device list.

The app exposes a separate ADB socket (`sawt-plus`), so it does not
collide with the original `audiosource` app.

## Android app

- The home screen shows the connection status, address, pairing code, transport
  (Wi‑Fi / USB-ADB), input level, and mute.
- Tap the address to copy it; long-press the pairing code to copy that.
- The menu (top-left) opens Settings and About. Settings uses rows that open a
  choice dialog for Language (system/English/Arabic), Theme
  (system/light/dark), and the device name shown to the computer, plus the
  battery-optimization shortcut.
- Set Battery usage to **Unrestricted** on aggressive ROMs (Realme/Oppo and
  similar) so the system does not kill the foreground microphone service.

## Building

Go:

```console
$ go build ./...
$ go test ./...
```

Android (private toolchain kept in the repository):

```console
$ source .toolchain/env.sh
$ ./gradlew :app:assembleDebug
```

The APK is written to `app/build/outputs/apk/debug/app-debug.apk`. Recreate the
toolchain with `scripts/setup-android.sh`; it is idempotent and asks before
downloading.

Release signing is optional; the debug build is enough for local use. A
release build expects the usual `keystore.properties` configuration.

## Troubleshooting

- **No devices found**: make sure the app is running on the phone and both
  devices are on the same network. Check the firewall note above. Try
  `avahi-browse -rt _sawtplus._tcp`.
- **Device requires pairing**: enter the 6-digit code shown in the app. The
  code expires after 10 minutes and can change if the service restarts.
- **No sound in the browser monitor**: start the stream first, check the
  browser's audio permissions, and use headphones to avoid feedback.
- **Level too low**: raise the input level on the phone or the PC, not both;
  both are multipliers.
- **Stream stops in the background**: disable battery optimization for the app.
- **ADB not found**: install Android platform tools and re-check `adb devices`.

## Project layout

- `cmd/sawt`: CLI entry point.
- `internal/`: config, protocol, discovery, audio/PipeWire, ADB, daemon, HTTP
  API, embedded web UI, client.
- `app/`: Android app (Java, Material 3).
- `internal/web/dist/index.html`: the dependency-free browser UI.

## Credits

- Original project: [`gdzx/audiosource`](https://github.com/gdzx/audiosource).
- [sndcpy](https://github.com/rom1v/sndcpy) for the initial approach to audio
  forwarding.

## License

MIT. See [LICENSE](LICENSE).
