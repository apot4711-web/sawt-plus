# Sawt+ (صوت+)

**English** · [العربية](README.ar.md)

> **Notice**
>
> This is a personal project built with the help of artificial intelligence.
> I am a programming beginner, and I made this project for my own personal use.
> I published it in the hope that it will be useful to others. It is not a
> commercial product and comes with no warranty or support.

## Quick Start

**What you need**

- A Linux computer with PipeWire or PulseAudio (most systems support this).
- An Android phone running Android 5.0 or newer.
- Both devices connected to the same Wi-Fi network.

**Steps**

1. Download and install the [Android app](https://github.com/apot4711-web/sawt-plus/releases/latest/download/sawt-plus.apk). If a security warning appears during installation, choose **Install anyway** or **Allow**.
2. Download the [computer program](https://github.com/apot4711-web/sawt-plus/releases/latest/download/sawt-linux-amd64) and save it on the computer. Then make it executable and start it:

   ```bash
   chmod +x ~/Downloads/sawt-linux-amd64
   ~/Downloads/sawt-linux-amd64 web
   ```

   If your file is in a different folder, change the path to match where you saved it. The [advanced guide](docs/advanced.md#linux-computer) has more details.

3. Open the app on the phone and tap **Start service**. Then start the Sawt+ program on the computer and enter the 6-digit code shown in the app.
4. Choose **Sawt+** (`sawt-plus`) as the microphone in your audio settings or meeting apps.

That is all you need to get started. The [advanced guide](docs/advanced.md) has
every technical detail and troubleshooting step.

## What it does

Sawt+ turns an Android phone into a wireless microphone for a Linux computer.
The phone captures its microphone and streams it over the local Wi-Fi network to
a small program on the computer, which exposes it as a microphone for your
meeting or recording app.

Sawt+ is based on [`gdzx/audiosource`](https://github.com/gdzx/audiosource), but
uses Wi-Fi as the primary connection and adds pairing, discovery, input-level
control, and a Material 3 Android app. It is a separate project and does not
replace the original app.

## Features

- Phone microphone over local Wi-Fi, with USB/ADB as a fallback.
- Pair once with a 6-digit code; connections are automatic afterwards.
- Control mute and input level from the phone, the terminal, or a local web page.
- Optional local web page; no Node.js required.
- English and Arabic interface with RTL support.

## Advanced use

Everything technical is documented in **[docs/advanced.md](docs/advanced.md)**:
manual install commands, the full CLI, firewall and discovery notes, USB/ADB,
checksums, and troubleshooting.

## Credits

- Original project: [`gdzx/audiosource`](https://github.com/gdzx/audiosource).
- [sndcpy](https://github.com/rom1v/sndcpy) for the initial approach to audio
  forwarding.

## License

MIT. See [LICENSE](LICENSE).

---

For advanced users or network problems: see [docs/advanced.md](docs/advanced.md).
