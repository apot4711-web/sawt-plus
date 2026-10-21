# صوت+ (Sawt+)

حوّل هاتف أندرويد إلى ميكروفون لاسلكي لحاسوب لينكس. يلتقط الهاتف الصوت من
الميكروفون ويبثّه بصيغة PCM أحادية بتردد 48 كيلوهرتز عبر شبكة Wi-Fi المحلية إلى
خادم صغير بلا واجهة على الحاسوب، ثم يوفّره كمصدر صوتي في PipeWire أو PulseAudio.
يمكنك التحكم به من الطرفية أو من صفحة ويب محلية اختيارية.

Sawt+ مبني على مشروع [`gdzx/audiosource`](https://github.com/gdzx/audiosource)
مفتوح المصدر، لكنه يستخدم Wi-Fi كنقل أساسي، ويضيف الاقتران والاكتشاف والتحكم في
مستوى الصوت وتطبيق أندرويد بتصميم Material 3. هو مشروع منفصل ولا يستبدل التطبيق
الأصلي.

## التنزيل والتثبيت من الصفر

### ١. نزّل تطبيق الأندرويد

نزّل ملف APK الجاهز من المستودع:

- من صفحة المشروع: [`apk/sawt-plus-2.0.0.apk`](apk/sawt-plus-2.0.0.apk)
- أو [تحميل APK مباشرة](https://raw.githubusercontent.com/apot4711-web/sawt-plus/main/apk/sawt-plus-2.0.0.apk)

افتح الملف على الهاتف، واسمح للمتصفح أو مدير الملفات بتثبيت التطبيقات من هذا
المصدر. إذا ظهر تحذير Play Protect اختر **تثبيت على أي حال**.

> ملف APK موقّع بمفتاح التطوير (debug) وهو مناسب للتثبيت اليدوي والتجربة. لا
> يحتاج التطبيق إلى حساب أو إنترنت، فقط إلى شبكة محلية مشتركة مع الحاسوب.

إذا كان لديك ADB وملف APK محفوظ على الحاسوب:

```bash
adb install -r sawt-plus-2.0.0.apk
```

### ٢. نزّل برنامج الحاسوب

المتطلبات:

- لينكس بنظام **x86_64**.
- PipeWire أو PulseAudio، مع الأمر `pactl` في `PATH`.
- (اختياري) `avahi-browse` لتحسين الاكتشاف.

على فيدورا:

```bash
sudo dnf install pulseaudio-utils avahi-tools
```

على دبيان/أوبونتو:

```bash
sudo apt install pulseaudio-utils avahi-utils
```

نزّل الملف التنفيذي الجاهز إلى مجلدك المحلي:

```bash
mkdir -p ~/.local/bin
curl -fL -o ~/.local/bin/sawt \
  https://raw.githubusercontent.com/apot4711-web/sawt-plus/main/bin/sawt-linux-amd64
chmod +x ~/.local/bin/sawt
sawt version
```

إذا ظهر `sawt: command not found` فأضف المجلد إلى `PATH`:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

لا تحتاج إلى Go أو Android SDK لتشغيل البرنامج.

بديل: استنسخ المستودع وشغّل الملف التنفيذي مباشرة:

```bash
git clone https://github.com/apot4711-web/sawt-plus.git
cd sawt-plus
./bin/sawt-linux-amd64 version
```

### ٣. شغّل الهاتف والحاسوب

1. افتح تطبيق **صوت+** على الهاتف.
2. اضغط **تشغيل الخدمة** واسمح بإذن الميكروفون.
3. لاحظ رمز الاقتران المكوّن من ٦ أرقام، وابقِ التطبيق يعمل في الخلفية.
4. على الحاسوب شغّل:

```bash
sawt web
```

سيفتح المتصفح على `http://127.0.0.1:8787`. اختر الهاتف من القائمة، أدخل رمز
الاقتران، ثم ابدأ البث. بعد الاقتران الأول يصبح الاتصال تلقائياً.

بديل من الطرفية:

```bash
sawt discover
sawt pair 192.168.1.42:8765 123456
```

استبدل العنوان والرمز بما يظهر عندك.

### ٤. اختر الميكروفون

سيُنشئ الحاسوب مصدراً صوتياً باسم `sawt-plus`. اختره كميكروفون في تطبيق
الاجتماعات أو التسجيل. للتحكم في مستوى الصوت اختر **مستوى الصوت** داخل
التطبيق، أو استخدم:

```bash
sawt gain 1.5x
sawt mute on
```

إذا لم يظهر المصدر، ثبّت `pavucontrol` وافتحه للتأكد من اختيار
**Sawt+** كمصدر افتراضي.

### التحديث والإزالة

- **تحديث التطبيق**: نزّل أحدث APK ثم ثبّته فوق النسخة الحالية.
- **إزالة التطبيق**: `adb uninstall com.sawtplus.app` أو من إعدادات أندرويد.
- **إزالة برنامج الحاسوب**: احذف `~/.local/bin/sawt`، واختيارياً
  `~/.config/sawt-plus`.

### التحقق من سلامة الملفات

إذا استنسخت المستودع كاملاً:

```bash
sha256sum -c SHA256SUMS
```

## المزايا

- **Wi-Fi أولاً** على الشبكة المحلية، مع USB/ADB كبديل.
- **اكتشاف متعدد الطبقات**: mDNS و`avahi-browse` ومسح نشط للشبكة، مدموجة في
  قائمة أجهزة واحدة.
- **اقتران مرة واحدة**: أدخل الرمز المكوّن من ٦ أرقام، ويُحفظ الرمز للاتصال
  التلقائي لاحقاً.
- **تحكم من الهاتف والحاسوب**: كتم الصوت ومستوى الصوت في التطبيق والطرفية
  وصفحة الويب.
- **واجهة ويب محلية اختيارية** يقدّمها الخادم؛ لا تحتاج Node.js.
- **تطبيق Material 3** بالعربية والإنجليزية مع دعم RTL والثيمات.

البث هو PCM موقّع 16 بت، 48 كيلوهرتز، أحادي، بإطارات كل 20 مللي ثانية عبر
WebSocket: رسائل تحكم نصية وإطارات صوت ثنائية.

---

## English

Sawt+ turns an Android phone into a wireless microphone for a Linux computer.
The phone captures its microphone and streams 48 kHz mono PCM to a small
headless daemon on the PC, which exposes it as a PipeWire/PulseAudio source.

### Install

1. Download [`apk/sawt-plus-2.0.0.apk`](apk/sawt-plus-2.0.0.apk) and install
   it on the phone. Allow installs from your browser or file manager if asked.
2. Download the prebuilt Linux binary:

```bash
mkdir -p ~/.local/bin
curl -fL -o ~/.local/bin/sawt \
  https://raw.githubusercontent.com/apot4711-web/sawt-plus/main/bin/sawt-linux-amd64
chmod +x ~/.local/bin/sawt
sawt version
```

3. On the phone, open Sawt+, start the service, grant microphone permission,
   and note the 6-digit pairing code.
4. On the PC:

```bash
sawt web
```

Select the phone, enter the code, and start streaming. The PC creates a source
named `sawt-plus`; select it as the microphone in your meeting or recording
application.

The APK is debug-signed and intended for direct sideloading. The Linux binary is
static and needs no Go toolchain. It requires `pactl` (PipeWire or PulseAudio).

### Requirements

- Linux x86_64 with PipeWire or PulseAudio and `pactl` in `PATH`.
- Android 7.0 (API 24) or newer.
- Both devices on the same local network for Wi-Fi mode, or USB debugging for
  the ADB fallback.
- Optional: `avahi-utils`/`avahi-tools` for more reliable discovery.

### CLI

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

### Discovery and the firewall

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

### Input level

The PC applies a digital gain in the daemon, and the Android app has its own
"مستوى الصوت" slider that scales the captured samples before sending them. The
two controls are independent: leave one at 1.00× and adjust the other. Both are
named "مستوى الصوت" / "Input level" in the UI to avoid the technical term
"gain" in user-facing text.

### USB/ADB fallback

Wi-Fi is the intended transport, but ADB still works when both devices are on
different networks or when Wi-Fi is unreliable.

1. Enable USB debugging on the phone and connect the cable.
2. Check the device: `adb devices` (or `sawt adb devices`).
3. Connect: `sawt connect adb:SERIAL`, run the ADB loop directly with
   `sawt adb run`, or pick the device labeled `USB/ADB` in the browser device
   list.

The app exposes a separate ADB socket (`sawt-plus`), so it does not collide
with the original `audiosource` app.

### Android app

- The home screen shows the connection status, address, pairing code, transport
  (Wi-Fi / USB-ADB), input level, and mute.
- Tap the address to copy it; long-press the pairing code to copy that.
- The menu (top-left) opens Settings and About. Settings uses rows that open a
  choice dialog for Language (system/English/Arabic), Theme
  (system/light/dark), and the device name shown to the computer, plus the
  battery-optimization shortcut.
- Set Battery usage to **Unrestricted** on aggressive ROMs (Realme/Oppo and
  similar) so the system does not kill the foreground microphone service.

### Troubleshooting

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

### Project layout

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
