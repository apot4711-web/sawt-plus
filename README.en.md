<a id="top"></a>

<div align="center">
  <img src="assets/banner.svg" alt="Sawt+ — wireless microphone for Linux" width="100%">
  <h3>Your phone, as your computer's microphone.</h3>
  <p>
    Sawt+ turns an Android phone into a wireless microphone for a Linux computer
    over your local Wi-Fi network. No account, no cloud, no extra setup.
  </p>
  <p dir="ltr"><b>English</b> · <a href="README.md">العربية</a></p>
  <p dir="ltr">
    <a href="https://github.com/apot4711-web/sawt-plus/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/apot4711-web/sawt-plus?style=for-the-badge&color=3daee9&label=release"></a>
    <img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-8a5cd6?style=for-the-badge">
    <img alt="Android 5.0+" src="https://img.shields.io/badge/Android-5.0%2B-3ddc84?style=for-the-badge&logo=android&logoColor=white">
    <img alt="Linux" src="https://img.shields.io/badge/Linux-PipeWire-ffbc00?style=for-the-badge&logo=linux&logoColor=black">
    <img alt="Wi-Fi" src="https://img.shields.io/badge/transport-Wi--Fi-1d99f3?style=for-the-badge&logo=wifi&logoColor=white">
  </p>
  <p>
    <a href="#start"><b>Get started</b></a> ·
    <a href="#what"><b>What is Sawt+?</b></a> ·
    <a href="#features"><b>Features</b></a> ·
    <a href="#how"><b>How it works</b></a> ·
    <a href="#controls"><b>Control</b></a> ·
    <a href="#faq"><b>FAQ</b></a> ·
    <a href="#advanced"><b>Advanced</b></a>
  </p>
</div>

> [!NOTE]
> This is a personal project built with the help of artificial intelligence. I am
> a programming beginner and built it for my own use before publishing it in the
> hope that it helps others. It is not a commercial product, and no official
> support is provided.

---

<a id="start"></a>

## Get started in a minute

**What you need**

| Requirement | Details |
| --- | --- |
| Computer | Linux with PipeWire or PulseAudio (most modern distributions) |
| Phone | Android 5.0 or newer |
| Network | Both devices on the same Wi-Fi network |

**Steps**

1. Download and install the [Android app](https://github.com/apot4711-web/sawt-plus/releases/latest/download/sawt-plus.apk). If a security warning appears during installation, choose **Install anyway** or **Allow**.

2. Download the [computer program](https://github.com/apot4711-web/sawt-plus/releases/latest/download/sawt-linux-amd64), save it, then make it executable and start it:

   ```bash
   chmod +x ~/Downloads/sawt-linux-amd64
   ~/Downloads/sawt-linux-amd64 web
   ```

   If you saved the file somewhere else, change the path to match. The
   [advanced guide](docs/advanced.md#linux-computer) has more details.

3. Open the app on the phone and tap **Start service**, then start the Sawt+ program on the computer and enter the 6-digit code shown in the app.

4. Choose **Sawt+** (`sawt-plus`) as the microphone in your audio settings or meeting app.

> [!TIP]
> After the first pairing, connections happen automatically. The single
> executable is all you need; no extra packages are required on the computer.

<!-- IMAGE SLOT: hero
     When a Sawt+ interface screenshot is available, replace this comment with:
     <p align="center"><img src="assets/media/hero.png" alt="Sawt+ interface" width="92%"></p>
-->

---

<a id="what"></a>

## What is Sawt+?

Sawt+ turns an Android phone into a wireless microphone for a Linux computer. The
phone captures audio from its microphone and streams it over the local Wi-Fi
network to a small program on the computer, where it appears as an audio source
named `sawt-plus` that you can pick in any meeting or recording app.

The project is based on [`gdzx/audiosource`](https://github.com/gdzx/audiosource),
but uses Wi-Fi as the primary connection and adds pairing, discovery, input-level
control, and a Material 3 Android app. It is a separate project and does not
replace the original app.

---

<a id="features"></a>

## Features

| Feature | What it means for you |
| --- | --- |
| Local Wi-Fi connection | Steady quality and low latency without cables |
| USB/ADB fallback | A backup option when no shared network is available |
| Pair once | A 6-digit code, then automatic connections |
| Full control | From the phone, the terminal, or a local web page |
| Arabic and English UI | With full right-to-left support |
| No cloud | Audio stays inside your local network |
| Open source | MIT licensed, with the code available to everyone |

---

<a id="how"></a>

## How it works

| Step | What happens |
| --- | --- |
| 1 | The phone captures audio from its microphone |
| 2 | It streams the audio over Wi-Fi to the Sawt+ program on the computer |
| 3 | The program receives it and exposes it as a PipeWire or PulseAudio source |
| 4 | The source appears as `sawt-plus` in meeting and recording apps |
| 5 | You control the input level from the phone, the terminal, or the web page |

```text
Phone microphone
      |
      |  Wi-Fi (WebSocket) - PCM 48 kHz
      v
Sawt+ daemon on Linux
      |
      |  PipeWire / PulseAudio
      v
Source: sawt-plus
      |
      v
Meeting and recording apps
```

<!-- IMAGE SLOT: how-it-works
     When an illustration is available, replace this comment with:
     <p align="center"><img src="assets/media/how-it-works.png" alt="Sawt+ audio path" width="88%"></p>
-->

---

<a id="controls"></a>

## Control from anywhere

| Surface | What you can do |
| --- | --- |
| Phone | Start and stop the service, adjust the input level, generate a new pairing code |
| Terminal | `sawt status`, `sawt pair`, `sawt connect`, `sawt web` |
| Web page | View status and devices, adjust the input level, via `sawt web` |

The web page is optional, runs locally on your machine, and needs no Node.js.

<!-- IMAGE SLOT: web-ui
     When a web page screenshot is available, replace this comment with:
     <p align="center"><img src="assets/media/web-ui.png" alt="Sawt+ local web page" width="88%"></p>
-->

---

<a id="troubleshooting"></a>

## Troubleshooting

| Problem | What to try |
| --- | --- |
| The phone does not show up | Make sure both devices are on the same network and allow port `8765` through the firewall |
| `sawt-plus` does not appear as a microphone | Make sure PipeWire or PulseAudio is running, then restart the program |
| The pairing code is rejected | Tap **New code** in the app, then enter the new code |
| Audio cuts out | Move the phone closer to the router, or use a 5GHz network if possible |

Full details, including manual install commands, firewall settings, and USB/ADB,
are in the [advanced guide](docs/advanced.md).

---

<a id="faq"></a>

## FAQ

<details>
<summary><b>Does Sawt+ need internet access?</b></summary>
<br>
No. It works entirely on your local network and never sends audio to an external
service.
</details>

<details>
<summary><b>Does it work on Windows or macOS?</b></summary>
<br>
The computer program is Linux-only. The phone app runs on Android 5.0 or newer.
</details>

<details>
<summary><b>Does the phone need root?</b></summary>
<br>
No. You only need to grant the microphone permission on first launch.
</details>

<details>
<summary><b>Does audio pass through a server or the cloud?</b></summary>
<br>
No. Audio travels directly between the phone and the computer over the local
Wi-Fi network.
</details>

<details>
<summary><b>Does it work with any app?</b></summary>
<br>
Yes. Any app that can pick a PipeWire or PulseAudio source will see `sawt-plus`.
</details>

<details>
<summary><b>What if the phone's address changes?</b></summary>
<br>
The computer program tries to rediscover the device automatically when a
connection fails. If the problem persists, pair again from the app.
</details>

<details>
<summary><b>Is the project free?</b></summary>
<br>
Yes, it is open source under the MIT license.
</details>

---

<a id="advanced"></a>

## Advanced use

Everything technical is documented in the **[advanced guide](docs/advanced.md)**:

- Manual installation and the full command line.
- Firewall and discovery settings.
- Connecting over USB/ADB.
- Verifying file checksums (SHA-256).
- Extra troubleshooting steps.

---

<a id="credits"></a>

## Credits

- Original project: [`gdzx/audiosource`](https://github.com/gdzx/audiosource).
- [sndcpy](https://github.com/rom1v/sndcpy) for the initial approach to audio
  forwarding.

## License

MIT. See [LICENSE](LICENSE).

---

<div align="center">
  <p>Sawt+ — an open-source wireless microphone for Linux.</p>
  <p><a href="#top">Back to top</a></p>
</div>
