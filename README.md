# Sawt+ (صوت+)

**English** · [العربية](README.ar.md)

Sawt+ turns an Android phone into a wireless microphone for a Linux computer.
The phone captures its microphone and streams 48 kHz mono PCM over the local
Wi-Fi network to a small headless daemon on the PC, which exposes it as a
PipeWire or PulseAudio source. You control it from the terminal or from an
optional local web page.

Sawt+ is based on the open-source project
[`gdzx/audiosource`](https://github.com/gdzx/audiosource), but uses Wi-Fi as the
primary transport and adds pairing, discovery, input-level control, and a
Material 3 Android app. It is a separate project and does not replace the
original app.

## Download and install from scratch

### 1. Install the Android app

Download the ready APK from the Releases page:

- [Latest release](https://github.com/apot4711-web/sawt-plus/releases/latest)
- [Download `sawt-plus.apk`](https://github.com/apot4711-web/sawt-plus/releases/latest/download/sawt-plus.apk)

Open the file on the phone and allow the browser or file manager to install
apps from this source. If Play Protect shows a warning, choose **Install
anyway**.

> The APK is debug-signed and intended for manual installation and testing. The
> app needs no account or internet access, only a local network shared with the
> computer.

If you have ADB and the APK saved on the computer:

```bash
adb install -r sawt-plus.apk
```

### 2. Install the computer program

Requirements:

- Linux on **x86_64**.
- PipeWire or PulseAudio, with the `pactl` command in `PATH`.
- Optional: `avahi-browse` for better discovery.

On Fedora:

```bash
sudo dnf install pulseaudio-utils avahi-tools
```

On Debian/Ubuntu:

```bash
sudo apt install pulseaudio-utils avahi-utils
```

Download the ready executable to your local directory:

```bash
mkdir -p ~/.local/bin
curl -fL -o ~/.local/bin/sawt \
  https://github.com/apot4711-web/sawt-plus/releases/latest/download/sawt-linux-amd64
chmod +x ~/.local/bin/sawt
sawt version
```

If you get `sawt: command not found`, add the directory to `PATH`:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

You do not need Go or the Android SDK to run the program.

Alternative: clone the repository and run the executable directly:

```bash
git clone https://github.com/apot4711-web/sawt-plus.git
cd sawt-plus
./bin/sawt-linux-amd64 version
```

### 3. Run the phone and the computer

1. Open the **Sawt+** app on the phone.
2. Tap **Start service** and grant microphone permission.
3. Note the 6-digit pairing code and keep the app running in the background.
4. On the computer, run:

```bash
sawt web
```

The browser opens at `http://127.0.0.1:8787`. Select the phone, enter the
pairing code, and start streaming. After the first pairing, the connection is
automatic.

Alternative from the terminal:

```bash
sawt discover
sawt pair 192.168.1.42:8765 123456
```

Replace the address and code with the ones shown on your devices.

### 4. Select the microphone

The computer creates an audio source named `sawt-plus`. Select it as the
microphone in your meeting or recording application. To adjust the level, use
**Input level** in the app, or run:

```bash
sawt gain 1.5x
sawt mute on
```

If the source does not appear, install `pavucontrol` and open it to make sure
**Sawt+** is selected as the default source.

### Updating and removal

- **Update the app**: download the latest APK and install it over the current
  version.
- **Remove the app**: `adb uninstall com.sawtplus.app`, or uninstall it from
  Android settings.
- **Remove the computer program**: delete `~/.local/bin/sawt` and, optionally,
  `~/.config/sawt-plus`.

### Verifying file integrity

Download all release assets into the same directory, then run:

```bash
sha256sum -c SHA256SUMS
```

If you cloned the full repository, the same command works from the repo root.

## Features

- **Wi-Fi first** on the local network, with USB/ADB as a fallback.
- **Multi-layer discovery**: mDNS, `avahi-browse`, and an active network scan,
  merged into one device list.
- **Pair once**: enter the 6-digit code and the token is saved for automatic
  connections later.
- **Control from phone and computer**: mute and input level in the app, the
  terminal, and the web page.
- **Optional local web UI** served by the daemon; no Node.js required.
- **Material 3 app** with English and Arabic support, RTL, and themes.

Audio is signed 16-bit PCM, 48 kHz, mono, in 20 ms frames over WebSocket: text
control messages and binary audio frames.

## Requirements

- Linux x86_64 with PipeWire or PulseAudio and `pactl` in `PATH`.
- Android 5.0 (API 21) or newer.
- Both devices on the same local network for Wi-Fi mode, or USB debugging for
  the ADB fallback.
- Optional: `avahi-utils`/`avahi-tools` for more reliable discovery.

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
"Input level" slider that scales the captured samples before sending them. The
two controls are independent: leave one at 1.00× and adjust the other. Both are
named "مستوى الصوت" / "Input level" in the UI to avoid the technical term
"gain" in user-facing text.

## USB/ADB fallback

Wi-Fi is the intended transport, but ADB still works when both devices are on
different networks or when Wi-Fi is unreliable.

1. Enable USB debugging on the phone and connect the cable.
2. Check the device: `adb devices` (or `sawt adb devices`).
3. Connect: `sawt connect adb:SERIAL`, run the ADB loop directly with
   `sawt adb run`, or pick the device labeled `USB/ADB` in the browser device
   list.

The app exposes a separate ADB socket (`sawt-plus`), so it does not collide
with the original `audiosource` app.

## Android app

- The home screen shows the connection status, address, pairing code, transport
  (Wi-Fi / USB-ADB), input level, and mute.
- Tap the address to copy it; long-press the pairing code to copy that.
- The menu (top-left) opens Settings and About. Settings uses rows that open a
  choice dialog for Language (system/English/Arabic), Theme
  (system/light/dark), and the device name shown to the computer, plus the
  battery-optimization shortcut (Android 6.0 and newer).
- Set Battery usage to **Unrestricted** on aggressive ROMs (Realme/Oppo and
  similar) so the system does not kill the foreground microphone service.

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
- **`sawt: command not found`**: add `~/.local/bin` to `PATH`.
- **The web page says it needs the local access token**: open it with `sawt web`
  (or use the URL printed by `sawt web --no-open`). The token lives in
  `~/.config/sawt-plus/api.token` with mode `0600`; the browser receives it
  once and keeps it in an HttpOnly cookie.

## Project layout

- `apk/`: ready-to-install Android APK.
- `bin/`: prebuilt Linux daemon binary.
- `cmd/sawt`: CLI entry point.
- `internal/`: config, protocol, discovery, audio/PipeWire, ADB, daemon, HTTP
  API, embedded web UI, client.
- `app/`: Android app source (Java, Material 3).
- `internal/web/dist/index.html`: the dependency-free browser UI.

## Credits

- Original project: [`gdzx/audiosource`](https://github.com/gdzx/audiosource).
- [sndcpy](https://github.com/rom1v/sndcpy) for the initial approach to audio
  forwarding.

## License

MIT. See [LICENSE](LICENSE).
