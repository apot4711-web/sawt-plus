# Project State

> Restructured 2026-10-26 (user-approved): this file opens with a
> current-state snapshot, then recent session notes (sessions 33–21).
> Older history — sessions 20 and earlier, plus the early
> decision/done/verified lists — moved verbatim to STATE.archive.md.
> Nothing was deleted; the archive is the same history, only relocated.

## Current state (Sawt+ 2.1.2, 2026-10-26)

- Released: **2.1.2** (versionCode 6, versionName 2.1.2), 2026-10-26:
  <https://github.com/apot4711-web/sawt-plus/releases/tag/v2.1.2>.
  Same signing cert as 2.1.1, so it installs over 2.1.1 with no re-pair.
  APK SHA-256 `54bbd582…597bac5c6`, Linux binary `d56ba19d…c29125084`.
- What it is: an Android phone becomes a microphone for a Linux PC —
  Wi-Fi (TLS, cert pinning) first, USB/ADB second. Phone server port 8765;
  PC web UI on 127.0.0.1:8787. Protocol: WebSocket text control + binary
  `s16le` 48 kHz mono 20 ms frames; virtual mic via a PipeWire source.
- 2.1.2 fixed, over 2.1.1: Stop Service no longer crashes (TLS close off
  the main thread), quick Stop→Start works (ADB socket release + teardown
  wait), the ~20 s app-relaunch loop is gone (singleTop + AppRunning
  guard), ready is sent before stats for new Wi-Fi clients, stale-adb
  auto-restart and discovery warnings (USB debugging, PC Wi-Fi power save).
- Note 2026-10-26: the older "STATE.md stays local and is git-ignored"
  line under User preferences below is superseded — STATE.md is now
  maintained on GitHub at the user's direction. AGENTS.md is likewise
  present on GitHub since the 2026-10-26 phone-UI rule update.

## Standing rules

- No commit, push, or release without the user's explicit approval.
- No release until a full end-to-end test passes (fresh pairing + audio +
  reconnect).
- The user drives all phone UI interaction (taps, Stop/Start, pairing,
  settings). The agent never taps or automates the phone UI and verifies
  from the host only: `adb logcat`, `dumpsys`, daemon API, CLI.
- The daemon runs only when the user starts it manually — no autostart.
- USB hotplug auto-connect works only for previously-paired phones.
- License GPLv3. Project status: experimental beta; the user is currently
  its only user.
- Read this file first at session start; update it after every milestone,
  failure, or blocker. Ask before downloads, installs, or changes outside
  the repository.

## Open items

- Fix campaign in progress (Muse in the sandbox does the code fixes;
  the Fedora agent builds the APK and runs device end-to-end tests
  afterwards): security, audio, reliability, battery/UX, and minor fixes
  from the classified flaw review. No release until its full e2e passes.
- Wi-Fi audio gaps: separate issue (PC power save + packet loss). A
  10-minute baseline test was proposed; awaiting the user's call.
- Logo: the user's own design (square = bowl of ص with the plus inside,
  U-arc attached to its left, mic capsule with the grille inside).
  Draft `assets/logo-concepts/11-sad-mic-v2.svg` delivered; verdict
  pending. The logo does not block releases.
- FreeBSD port: plausible but untested; the user has not yet said whether
  he means FreeBSD or why.
- Windows: the user wants the exact same experience as Linux (browser web
  UI, same flow). He is researching Virtual Cables himself.
- README screenshots: still missing; they must be real captures from the
  user's devices, never fabricated.

## Recent sessions

## Session 33 (2026-10-24) — implement fixes; ADB relaunch/restart race

Approved fixes implemented:

- `RecordService`: transport actions and teardown moved off the main thread
  (`sawt-transport`); static `transportLock` + `pendingTeardown` latch makes a
  restarted instance wait for the previous teardown; ADB socket closed
  synchronously in `onDestroy`; `ServiceState.reset()` only in `onDestroy`.
- `WifiServer`: main-thread `stopServer()` hands off to `sawt-wifi-stop`;
  `sendReady` runs before `clients.put`/`hub.addSink`.
- Manifest/`MainActivity`: `launchMode=singleTop`, `onNewIntent -> updateUi`.
- `AdbServer`: bind retry (~2 s), `wakeAccept()` to unblock `accept()`, and
  `shutdownInput/Output` in `dropClient()` so the reader exits immediately.
- Go: `AppRunning` gate before `StartApp`, stale-adb recovery
  (`DevicesFresh`, `RestartServer`, `USBADBInterfacePresent`), power-save
  warning (`WifiPowerSaveWarning`, `iw ... get power_save`), discover/connect
  hints, hermetic adb hotplug test (`PATH=""`).

Verified on RMX3710 (user drives the UI; agent only reads
logcat/dumpsys/daemon API):

- 5 rapid Stop->Start cycles: `ADB server listening`, no
  `Address already in use`, no FATAL, UI `Ready over Wi-Fi`.
- ADB streaming: `sawt adb run` -> frames advance.
- User pressed Stop: `service=0`, no FATAL/NetworkOnMainThread, ADB socket
  closed and client disconnected within ~1 ms.
- 45 s stopped while the daemon retried: `MainActivity` 50 -> 50, 0
  `START u0 ... com.sawtplus.app` events, daemon state `error` (no frames).
  The ~20 s relaunch loop is fixed.
- User pressed Start (17:04:45): `service=1`, `ADB server listening`; daemon
  `adb run` streaming, frames advance.
- `/api/stream` WebSocket (Cookie auth): 5 frames / 9600 bytes, 4774 of 4800
  samples nonzero, RMS 63, peak 209. Path works; the room was near-silent.
- `sawt disconnect` -> `idle adb`; `sawt adb run` -> frames 9857 -> 10009 and
  the service stayed `1` (reconnect verified).
- `gofmt` clean; `go build ./...`, `go vet ./...`, `go test ./...` pass;
  `./gradlew :app:assembleDebug` pass.

Rule: do not run UI interaction tests; the user taps the phone. Verify with
`adb logcat`, `dumpsys`, and the daemon API only.

Release 2.1.2 (user-requested): `versionCode 6`, `versionName 2.1.2`,
`internal/version.Version = "2.1.2"`.

- `apk/sawt-plus-2.1.2.apk`: zipaligned release build (non-debuggable),
  signed with the same debug certificate as 2.1.1 (SHA-256
  `b3504a96...aa9e78`), so it installs over 2.1.1.
- `bin/sawt-linux-amd64`: rebuilt with `CGO_ENABLED=0`, `-trimpath`,
  `-ldflags="-s -w"`; reports `sawt 2.1.2`.
- `SHA256SUMS` updated; `docs/release-notes/v2.1.2.md` added.
- `assembleRelease` ran with `-x lintVital*` because the offline cache lacks
  `lint-gradle:31.7.3`; debug build and Go tests pass.

Added the phone-UI testing rule to `AGENTS.md` (user-approved: the user drives
all taps; the agent verifies via logcat/dumpsys/API) and updated the
ready-artifact name to `apk/sawt-plus-2.1.2.apk`.

Pushed `main` (`cab3b40`) and annotated tag `v2.1.2`. GitHub Actions run
`40102859724` completed successfully and created
<https://github.com/apot4711-web/sawt-plus/releases/tag/v2.1.2> with
`sawt-plus.apk`, `sawt-linux-amd64`, `sawt-plus-source.zip`, and
`SHA256SUMS`. The uploaded APK/binary hashes match the local artifacts
(`54bbd582...` / `d56ba19d...`).

## Session 32 (2026-10-24) — diagnosis only, no code changes

User reported three issues after 2.1.1 (USB not listed, Wi-Fi drops/reconnect
flicker, Stop Service exits the app) and asked for log-based diagnosis first.

Findings:

- USB: the phone *was* enumerated (`lsusb` 22d9:2769, interface 255/66/1 "ADB
  Interface", `/dev/bus/usb/002/009` rw for the user) but `adb devices` was
  empty because the adb server had been running since 2026-10-23 and was stale.
  `adb kill-server` + `adb start-server` immediately listed
  `WKTK6XLZTCMZAI6P device`, and `sawt discover` then showed the USB row.
  Not a pairing/TLS problem.
- After adb was fixed, hotplug auto-connect fired
  (`15:52:41 USB device ... attached; auto-connecting`) proving the
  paired-only gate passes. Every ADB stream then failed with
  `adb stream: EOF` because `RecordService` was not running: `dumpsys activity
  services` was empty and adbd logged
  `failed to connect to socket 'localabstract:sawt-plus' (Connection refused)`.
  Daemon `am start` only opens MainActivity and does not clear `userStopped`.
- Pairing: PC unpair at 15:35:30, re-pair 15:37:25 -> `paired with Sawt+`.
  Phone `shared_prefs/sawtplus.xml` has exactly one `paired_tokens` hash and it
  equals SHA-256 of the PC token (dc317afd...c66a) -> both sides are paired
  now. The suspected "unpair wiped USB pairing" chain is disproven for the
  current state (it could only have applied in the 15:35:30-15:37:25 window).
  The exact phone-side "Unpair all" log is gone (main logcat buffer rotates
  fast; oldest entry 15:55, only 7 `Sawt` lines left).
- Stop Service crash (confirmed 3x): `FATAL EXCEPTION: main`,
  `Unable to stop service ...RecordService: NetworkOnMainThreadException`,
  stack `RecordService.onDestroy -> stopWifi -> WifiServer.stopServer ->
  TlsFrontend.stop -> closeQuietly(TlsFrontend.java:279) ->
  ConscryptEngineSocket.close` -> SSL close does network I/O on the main thread.
  `am_crash`/`am_proc_died` at 15:34:22, 15:39:16, 15:39:29.
- Wi-Fi daemon log (window 14:09-15:57): 5x `i/o timeout`, 5x `connection
  reset by peer`, 4x EOF/1006, several `connection refused`, 5x
  `unexpected handshake message "stats"`, 1x IPv6 link-local rediscover
  `connect: invalid argument`. Resets at 15:34:27 and 15:39:19/30 line up
  exactly with the Stop-Service crashes. The `stats` failure is a real phone
  bug: `WifiServer.authenticate` does `clients.put` before `sendReady`
  (WifiServer.java:406-408) while `sendStats` runs every 2 s
  (WifiServer.java:165) -> stats can precede `ready` and the daemon aborts.
- The "~2 s reconnect" appearance is the web UI poll
  (`internal/web/dist/index.html:1934` `setInterval(poll, 2000)`); the daemon
  backoff is 1/2/4/8/16 s.
- Battery: `dumpsys deviceidle whitelist` contains
  `user,com.sawtplus.app,10374` = Unrestricted. PC Wi-Fi
  `iw dev wlp0s29u1u3 get power_save` = on (NM profile value 0/default);
  link 2462 MHz, -56 dBm, 19.5/65 Mbit; phone RSSI -66, 58 Mbps,
  `wifi_power_save=120`. Both ends use Wi-Fi power save.
- Close origin in the daemon log: `reset by peer` = phone/intermediary closed;
  `i/o timeout` = daemon timed out waiting for frames (phone silent/link
  stall); `connection refused` = phone TLS server not listening (service
  stopped/crashed); `stats` = daemon itself aborted the handshake.

No code or config changed. `adb` was restarted (host-side only) and the daemon
auto-connect left armed. Next (await user agreement): fix Stop-Service crash
(off-main-thread TLS close), the stats/ready ordering race, then decide on
power-save mitigation and the stale-adb UX.


- Problem: 2.1.0 could not pair over Wi-Fi. TCP 8765 was open and mDNS was
  advertised, but every TLS handshake failed (OpenSSL `decode_error`/EOF,
  Java `Remote host terminated the handshake`, Go `EOF`). Console could not
  use its `DIGEST_SHA256`-only keystore key for TLS server auth; Java-WebSocket
  `SSLSocketChannel2` was also unsafe server-side.
- Fix (user-approved): deleted `TlsServerFactory.java`; added `TlsFrontend`
  (blocking `SSLServerSocket`, TLS 1.2/1.3, explicit handshake timeout, HTTP
  upgrade head parsing, injects `X-Sawt-Internal` secret + `X-Forwarded-For`,
  byte-bridges to an internal loopback WebSocket server). `WifiServer` binds
  the WebSocket server on loopback port 0 (atomic kernel choice), waits up to
  3 s for the actual bound port before starting `TlsFrontend`, so mDNS/TLS are
  never advertised if the internal bind fails; it rejects missing/wrong
  secret and uses the forwarded client IP. `TlsCertificate` uses alias
  `sawtplus_tls_v2`, deletes legacy alias, EC key with `DIGEST_NONE`, cert
  notBefore -1 day.
- Version bumped to `versionCode 5` / `versionName 2.1.1` and
  `internal/version.Version = "2.1.1"`; Go binary rebuilt (`sawt 2.1.1`).
- Verified on RMX3710 over USB ADB: APK installs; `openssl s_client` now gets
  `CN=SawtPlus 89160db0`, `ECDHE-ECDSA-AES128-GCM-SHA256`; fresh pair after
  `pm clear` succeeds (`234225`), streaming at ~50 frames/s; manual
  disconnect/connect and reconnect after force-stop/relaunch both return to
  streaming in ~4-6 s. Client IP is forwarded correctly
  (`Wi-Fi client connected ... (from 192.168.161.8)`).
- Audio caveat: capture works (RMS ≈ 400-700, real PCM in `/api/stream`), but
  this test link is poor: PC Wi-Fi `Power save: on`, RTT spikes 200-800 ms,
  25-100% ping loss, occasional 2-4 s stalls and pong timeouts. Zero-filled
  gaps in `parecord` come from PipeWire underruns caused by those stalls, not
  from the app. Toggling phone Wi-Fi recovered streaming. Separate follow-up.
- Reviewer pass found a loopback port race (pre-picked port freed before
  bind), a socket-set leak, a header slowloris window, and a null race in
  `TlsFrontend.stop()`; all fixed before packaging. `startServer()` now
  returns success/failure and `RecordService` keeps the error and allows a
  retry instead of clearing it.
- Artifacts refreshed: `apk/sawt-plus-2.1.1.apk` (6,037,412 bytes,
  sha256 `151a25bf...a55091a7e1`; `versionCode 5`, `versionName 2.1.1`),
  `bin/sawt-linux-amd64` (9,080,994, sha256 `0e4bd54e...d3de5bb1`),
  `docs/release-notes/v2.1.1.md`, `SHA256SUMS`.
- Tests: `go build ./...` and `go vet ./...` pass. `go test ./...` has one
  environmental failure: `TestADBHotplugSerialsNoADB` expects no adb/device,
  but a real device is now attached; the other daemon tests pass.
- Re-verified after the hardening rebuild: TLS handshake works, internal port
  is the real bound port (`46877`), and the daemon reconnects after
  force-stop/relaunch (error → streaming in ~6 s, ~50 frames/s).
- Done: release commit `265768b`, annotated tag `v2.1.1`, workflow run
  <https://github.com/apot4711-web/sawt-plus/actions/runs/40092845298>
  succeeded. Release published at
  <https://github.com/apot4711-web/sawt-plus/releases/tag/v2.1.1> with
  `sawt-plus.apk` (6,037,412), `sawt-linux-amd64` (9,080,994),
  `sawt-plus-source.zip` (513,409) and `SHA256SUMS`; downloaded APK/bin hashes
  match the local sums. Bilingual notes came from
  `docs/release-notes/v2.1.1.md`.
- `main` was behind a remote README-only commit (`dc1b0e7`); merged it as
  `f96d67d` and pushed. Tag `v2.1.1` remains on the release commit.
- Next: users must unpair both sides once and re-pair (new certificate).
  Audio jitter on this test link is a separate follow-up (PC Wi-Fi power save).
- Blocked: none.

## Session 30 (2026-10-24) — release Sawt+ 2.1.0 (security release)

- User asked to publish the rebuilt app to GitHub Releases.
- Version bumped to `versionCode 4` / `versionName 2.1.0`
  (`app/build.gradle`), and `internal/version.Version` 2.0.0 → 2.1.0 so the
  daemon/CLI report the release version.
- Added `docs/release-notes/v2.1.0.md` (bilingual: TLS-only Wi-Fi, ADB token
  handshake, GPLv3, pinning, hotplug auto-connect, unpair all).
- Verified: `go build ./...` and `go test ./...` pass; `go vet` not run.
  `bin/sawt-linux-amd64` rebuilt static stripped (`sawt 2.1.0`,
  9,080,994 bytes). APK rebuilt clean with repo JDK 17 (`BUILD SUCCESSFUL`);
  `com.sawtplus.app` versionCode 4 / versionName 2.1.0, same debug cert
  `b3504a...9e78`, 5,988,624 bytes.
- Artifacts: `apk/sawt-plus-2.1.0.apk` (old 2.0.2 removed so
  `apk/*.apk` stays single), updated `SHA256SUMS`:
  apk `d5f2c2f2...b3681f4`, bin `570cc3b8...201835`.
- Release path: commit + annotated tag `v2.1.0` + push; the GitHub Actions
  workflow (`.github/workflows/release.yml`) created the release from the tag.
- Rebased onto `origin/main` (`1b96a0e`, remote STATE-only commit); merged the
  remote Session 28 with local notes, renumbered local build/release sessions
  to 29/30. Release commit `f53186b`; tag `v2.1.0` pushed.
- Done: release published at
  <https://github.com/apot4711-web/sawt-plus/releases/tag/v2.1.0> with
  `sawt-plus.apk` (5,988,624), `sawt-linux-amd64` (9,080,994),
  `sawt-plus-source.zip`, `SHA256SUMS`; APK/bin hashes match the local sums.
  Local `gh` is still not authenticated (workflow used `GITHUB_TOKEN`).
- Next: nothing pending for this release.

## Session 29 (2026-10-24) — debug APK build after security commit

- Problem: `:app:compileDebugJavaWithJavac` failed with
  `jlink executable /usr/lib/jvm/java-25-openjdk/bin/jlink does not exist`.
  Root cause: Gradle ran on the system JDK 25 (no `jlink`, Gradle 8.9 does
  not support it) instead of the repo JDK 17; `.toolchain/env.sh` was not
  sourced. Not a code error.
- Fix (commands run from repo root):
  `source .toolchain/env.sh && ./gradlew --stop && ./gradlew :app:assembleDebug --offline`
- Result: BUILD SUCCESSFUL in 37s (32 tasks). Debug APK rebuilt at
  `app/build/outputs/apk/debug/app-debug.apk`.
- Next: run the full Go checks (`go build ./...`, `go test ./...`) before any
  release; app assets/version were not changed.

## Session 28 (2026-10-24) — security fix campaign (complete)

- Goal (user request + challenge): fix all 10 critical/important findings from
  the security review without introducing regressions. One subagent per
  finding, run sequentially to avoid edit conflicts; final reviewer pass plus
  build/test verification at the end.
- Toolchain note: `go` is not installed here and `.toolchain/` (Android SDK)
  is absent — asked the user for approval to download Go 1.26 (~70 MB into
  ~/workspace/.toolchains/go) and to run scripts/setup-android.sh (~800 MB
  into .toolchain/). Code fixes proceed now; compile verification follows
  approval.
- Done: I4 (Go) — WebSocket read limit 16 MB → two-stage: 64 KiB during the
  JSON handshake, then negotiated-frame×4 clamped to a 256 KiB ceiling after
  "ready" (internal/daemon/daemon.go). Verified by diff review; `go build`
  pending toolchain approval.
- Done: I3 (Go) — per-frame status broadcast storm → added
  `broadcastStatusThrottled()` (max 1 push / 500 ms on the audio path;
  state transitions and user actions still push immediately). Deadlock audit
  passed (no lock held across the call at any of the 5 call sites).
- Toolchain: Go 1.26.0 installed at ~/workspace/.toolchains/go (user
  approved). `go build ./...`, `go vet ./...`, `go test ./...` all green
  with fixes I4+I3 in the tree.
- Done: I2 (Go) — brute-force protection on Pair(): per-address exponential
  backoff (2s→60s), lockout for 5 min after 8 consecutive rejections,
  honors the phone's retry_after hint; transport errors never throttle.
  Added pair_throttle_test.go (6 tests, all pass). `go build`/`go vet`/
  `go test ./...` green.
- Done: C1 (Android, critical) — ADB socket now requires a pairing-token
  handshake (`TOKEN <token>`) with constant-time verification before any
  audio flows; both sides updated (AdbServer.java, TokenStore.java,
  RecordService.java, internal/adb via daemon.go adbToken/streamADB).
  Breaking: ADB transport now requires one prior Wi-Fi pairing. `go build`/
  `go vet`/`go test` green; `gofmt -w` applied (whitespace-only).
- Done: I1 (Android) — mic capture no longer dies silently: on read error,
  running=false (truthful state), recorder released, and StreamHub retries
  re-acquiring the mic with exponential backoff (2s→30s, max 6 attempts)
  only while clients are connected; restarts cancelled on stop/shutdown.
- Done: I4 (Android) — startRecording() failures are caught, recorder
  released, running reset, and routed through the I1 restart scheduler
  (capped at 6 attempts — proven no infinite loop); also fixed a latent
  stall where isRunning() would lie true after a start failure.
- Done: I3 (Android) — token revocation: settings now has "Paired devices"
  with "Unpair all" (both locales); revoking drops live Wi-Fi + ADB sessions
  immediately and future hellos fail closed (pair_required). Chose unpair-all
  (not per-device) because the store keeps only token hashes, no identities.
- Done: I2 (Android) — Wi-Fi transport is now TLS-only (wss://): new
  TlsCertificate.java (EC P-256 self-signed cert in AndroidKeyStore, SHA-256
  fingerprint), TlsServerFactory (TLS 1.2+ only, fail-closed), fingerprint
  published via mDNS TXT + paired/pair_required/ready JSON + settings UI
  (both locales); manifest usesCleartextTraffic=false. Wire contract for
  the Go client documented in WifiServer Javadoc.
- Done: I1 (Go) — TLS client with cert pinning: new internal/daemon/tls.go
  (SHA-256 pinning via VerifyConnection only, TLS 1.2+, TOFU at pairing,
  fail-closed mismatch with re-pair guidance); wss:// everywhere; CertSHA256
  persisted per device; mDNS TXT sha256 parsed in discovery; 7 new/updated
  tests green (build/vet/gofmt/test all pass).
- Done: C1 (Go) — device identity pinning on rediscover: mDNS name now only
  selects a candidate; adoption requires the candidate to present the pinned
  TLS cert in a tokenless handshake (fail-closed; legacy unpinned entries
  never follow mDNS). Full token-path audit in report; 8 new subtests green.
  ALL 10 SECURITY FIXES COMPLETE.
- Done: license switched MIT → GPLv3 (user-approved 2026-10-24). Verified:
  gdzx/audiosource is MIT (GPL-compatible, copyrights preserved in LICENSE);
  scrcpy/sndcpy (Romain Vimont) are Apache-2.0 (one-way compatible with
  GPLv3). LICENSE now = original copyright notices + official GPL-3.0 text;
  README.md + README.en.md badges/sections updated. Not committed.
- Done: USB hotplug auto-connect (user-approved): new
  internal/daemon/adb_hotplug.go — 3s `adb devices` watcher; auto-connects on
  appearance transition only when idle + AutoConnect on; paired-only
  (explicit adb:<serial> entry, else exactly-one-paired-device rule matching
  adbToken()); never interrupts active streams; explicit Disconnect respected;
  10 new subtests green. ALL FEATURE WORK COMPLETE.
- Done: FINAL REVIEW — verdict SHIPPABLE after one real Android build on
  Fedora. Reviewer found + fixed 1 genuine blocker: X500Principal("CN=Sawt+
  ...") throws IllegalArgumentException ('+' is reserved in RFC 2253 DNs) —
  would have prevented the Wi-Fi server from starting on ANY phone; CN now
  "SawtPlus <id>", verified with JDK. AGENTS.md stale transport docs
  updated (wss://). Go: build/vet/gofmt/test all green (25+ new tests).
  Android: not compilable in sandbox (pre-existing Gradle IPC failure) —
  user must run :app:assembleDebug on Fedora (must confirm TlsServerFactory
  vs Java-WebSocket 1.5.7). CHANGELOG.md written (2.1.0 draft).
- Done: README framing fix (user request, pre-push): Wi-Fi was described
  as "primary" and USB/ADB as "fallback/backup" in README.md + README.en.md
  (intro, features table, how-it-works flow + diagram). Reworded to present
  both as equal first-class connections. Historical release notes untouched.
- Done: web UI toast fix (user-reported): `.toast` in
  internal/web/dist/index.html used `inset-inline: 50%` (left+right 50% in
  RTL = zero-width box, text overflowing a 36px strip) plus a wrong-direction
  translateX(50%). Fixed to `left: 50%` + `translateX(-50%)` (and matching
  hidden state) — centered bar, width fits message up to 560px. Go build OK.
  Joins the pending push.
- PUSHED to GitHub main (2026-10-24): commit 7e274209, 30 files — the 10
  security fixes, USB hotplug auto-connect, GPLv3, README Wi-Fi/USB parity,
  web toast fix, CHANGELOG.md. Verified: remote main head == 7e274209 and
  the toast fix is present in remote index.html. Excluded as always:
  STATE.md, AGENTS.md (private) and assets/logo-concepts/ (undecided
  concepts; preview PNGs are binary and cannot go in a snapshot push).
- Still open: Android build on Fedora (user's machine, ~/c/audiosource-plus).
  User pulled 7e274209 there (HEAD == origin/main, confirmed). First build
  `./gradlew :app:assembleDebug` FAILED at :app:compileDebugJavaWithJavac:
  Gradle ran on Java 25 (/usr/lib/jvm/java-25-openjdk) whose install lacks
  `jlink`, needed by AGP 8.7.3 to build the androidJdkImage from
  core-for-system-modules.jar. Project requires JDK 17
  (sourceCompatibility/targetCompatibility VERSION_17). Fix: install
  java-17-openjdk-devel and run with JAVA_HOME=/usr/lib/jvm/java-17-openjdk
  (or set org.gradle.java.home in ~/.gradle/gradle.properties). Failure is
  pre-compilation: it says NOTHING about the app code or the TLS CN fix.
  User will have their local Fedora agent fix the build. No APK built yet,
  so nothing uploaded to Releases. Release 2.1.0 (with APK) waits on that
  build.
- No commits (per AGENTS.md rule). NOTE 2026-10-24: user EXPLICITLY
  authorized pushing STATE.md itself to GitHub (after being warned it
  becomes public), so their local Fedora agent can pull the current
  status. This is a one-time authorization for STATE.md only; AGENTS.md
  stays unpushed.

## Session 27 (2026-10-23) — Sawt+ logo concepts (review pending)

- Goal: replace the microphone-in-a-disc launcher mark with a flat,
  single-layer logo; the user is exploring ideas from the name and Arabic
  script.
- Constraints: no inner disc or nested background; the mic is allowed but
  overused; palette is navy `#0B1220`, emerald `#34D399`, cyan `#22D3EE`,
  light `#E6EDF7`; Android adaptive icons still need one background layer; do
  not change app assets or publish until the user approves; no downloads or
  commits.
- Done: five transparent SVGs under `assets/logo-concepts/`:
  `01-sad-plus.svg` (Sad+ / ص+ monogram; glyph path from Droid Arabic Kufi
  via `pango-view`; plus placed outside for 48 px legibility),
  `02-plus-wave.svg`, `03-ripple-plus.svg`, `04-mic-plus.svg`,
  `05-s-wave.svg`.
- Previews: `preview-dark.png`, `preview-light.png`, and `preview.html`
  (1200x440 contact sheet with 80/56/48 px rows). Verified visually; the size
  labels no longer overlap.
- No app icon or resource files were changed. The working tree has the
  untracked `assets/logo-concepts/` directory plus this `STATE.md` update.
- Next: the user picks a concept or requests edits. After approval, implement
  the adaptive foreground/monochrome/legacy icons, rebuild, and only release
  if explicitly requested.
- Update (2026-10-24, session 28): user asked for a NEW direction — no mic
  (cliché); combine three elements: Arabic ص from the name + sound waves +
  plus. Three new concepts built under `assets/logo-concepts/`:
  `06-wave-sad.svg` («الصاد الموجية»: Kufi ص vector path, wave arcs from
  the bowl, plus above the ascender), `07-speaking-wave.svg` («الموجة
  الناطقة»: 9 waveform bars, tallest replaced by plus), `08-sonic-plus.svg`
  («الزائد الصوتي»: plus with sine-wave vertical stroke + ص-bowl smile
  curve). Previews: `preview2-dark.png` / `preview2-light.png`. Agent
  assessment: 07 reads best at small sizes; 06 most distinctive.
- Update (2026-10-24, session 28, later): user rejected compositional logos
  as uncreative — wants TRUE FUSION (one inseparable mark). Built
  `09-one-line.svg` («الخط الواحد»): a SINGLE continuous parametric stroke —
  sine wave → Kufi squarish bowl → ascender from bowl's right edge →
  self-crossing plus at top. One path, one color (#34D399). Previews:
  `preview3-dark.png` / `preview3-light.png`. Honest assessment: fusion is
  real (inseparable); plus+waves unmistakable; ص reads stylized (needs a
  second glance). Parametric ceiling reached; next lever = professional
  calligraphic pass on bowl geometry.
- Update (2026-10-24, session 28, later): user dictated their OWN design;
  built as `10-sad-mic.svg` (+`10b` filled-mic variant): rounded square
  (bowl of ص) with cyan plus INSIDE, U-arc attached to its left (ص's curved
  descender), mic capsule (pill: rect + semicircle caps) nestled in the U,
  three cyan slashes left of mic. Previews: `preview4-dark.png` /
  `preview4-light.png` (A left, B right). Reads as intended; abstract mark
  first, ص second.
- Update (2026-10-24, session 28, later): user gave 4 corrections on v1
  ("خايس لكن قربت"): (1) U glued seamlessly to square (single continuous
  form), (2) even computed spacing between U and mic, (3) left slashes
  DELETED ("wave/earthquake/lightning"), (4) three lines moved INSIDE the
  mic capsule as grille lines. Built `11-sad-mic-v2.svg` (stroked capsule)
  + `11b` (filled capsule, grille as mask slits); previews
  `preview5-dark.png` / `preview5-light.png`. Verified: seamless joint,
  even gaps, grille reads as mic.
- Next: user picks one of the 11 concepts or requests edits.

## Session 26 (2026-10-23) — release Sawt+ 2.0.2 (brand identity)

- Goal (user request): build the updated app and publish it to GitHub Releases.
- Version: `versionCode 3`, `versionName 2.0.2`.
- Prepared:
  - `app/build.gradle` bumped to code 3 / name 2.0.2.
  - Rebuilt the debug APK offline; replaced
    `apk/sawt-plus-2.0.1.apk` with `apk/sawt-plus-2.0.2.apk` (exactly one APK).
    APK 5,976,332 bytes, SHA-256
    `d1e1ba564e11fe35be14422eef7273da0783970e54fbb1aeb19b28162122e61f`.
    Verified: app id `com.sawtplus.app`, versionCode 3, versionName 2.0.2,
    minSdk 21, targetSdk 35, label `Sawt+`, same debug cert
    `b3504a9678cd616de0bbdc28f8a3b1c2ff617914fb8b089b39ff617738aa9e78`;
    `colorPrimary` has light `#0f766e` / night `#34d399`.
  - Rebuilt `bin/sawt-linux-amd64` from the current tree
    (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w"`),
    9,056,418 bytes, SHA-256
    `39689b053d62d2dec049203168fafefc02f94b8241094b6e0976aaba60dcb805`;
    still prints `sawt 2.0.0` and now embeds the Sawt+ web UI/theme toggle.
  - Regenerated `SHA256SUMS`; `sha256sum -c` passes.
  - Added bilingual `docs/release-notes/v2.0.2.md` (EN + AR, no emojis).
  - Updated the artifact name in `AGENTS.md`.
- Verified: `./gradlew :app:assembleDebug --offline` BUILD SUCCESSFUL;
  `go build ./...` and `go test ./...` green; `git diff --cached --check` clean.
- Reviewer pass: found the release-notes claim that the PC side was unchanged;
  fixed to say the protocol/Go version are unchanged while the embedded web UI
  was updated. Re-verified hashes, badging, cert, and the `sawtplus-theme`
  marker.
- Publish: commit `5e4d2a1` (`release: Sawt+ 2.0.2 with brand palette and icon`)
  pushed to `origin/main`; annotated tag `v2.0.2` pushed. Workflow run
  `39959615132` completed successfully:
  <https://github.com/apot4711-web/sawt-plus/actions/runs/39959615132>
- Release `v2.0.2` is public with four assets; server-computed SHA-256 digests
  match the local files:
  - `sawt-plus.apk` 5,976,332 bytes,
    `d1e1ba564e11fe35be14422eef7273da0783970e54fbb1aeb19b28162122e61f`
  - `sawt-linux-amd64` 9,056,418 bytes,
    `39689b053d62d2dec049203168fafefc02f94b8241094b6e0976aaba60dcb805`
  - `sawt-plus-source.zip` 305,694 bytes,
    `0c5f71f2057105fb69af14e2898a7724523f81a96e4f4aae03bd22cf55d883a8`
  - `SHA256SUMS` 250 bytes.
  Release URL: <https://github.com/apot4711-web/sawt-plus/releases/tag/v2.0.2>
- Not done: the 2.0.2 APK was not installed on a phone (ask before `adb
  install`); no visual device check yet. `STATE.md` stays local/gitignored.

## Session 25 (2026-10-23) — phase 3: Android palette and launcher icon

- Done (source first; committed and pushed at the end, no artifact
  replacement, no installs):
  - `values/colors.xml`: full light Sawt+ palette (primary `#0F766E`,
    secondary `#0E7490`, surface `#F8FAFC`, containers `#FFFFFF`..`#D1DDEB`,
    onSurface `#0F172A`, onSurfaceVariant `#475569`, outline `#64748B` /
    outlineVariant `#CBD5E1`, error pair).
  - `values-night/colors.xml`: dark palette (primary `#34D399`, secondary
    `#22D3EE`, surface `#0B1220`, containers `#080E19`..`#22314F`, onSurface
    `#E6EDF7`, onSurfaceVariant `#9FB0C7`, outline `#3B4A63` /
    outlineVariant `#243349`, error pair).
  - `values/styles.xml`: `AppTheme` now maps every M3 color attr
    (`colorPrimary`..`colorErrorContainer`, surface container levels, outline,
    inverse) to the new resources while keeping
    `Theme.Material3.DayNight.NoActionBar`. Window/content backgrounds use
    `?attr/colorSurface`; status/navigation bars use a new dark navy
    `colorSystemBar` (`#0B1220`) so the white system icons stay legible on
    API 21–34. `Widget.Sawt.Card` is unchanged
    (`?attr/colorSurfaceContainer`).
  - `MainActivity.applySystemBarAppearance()` runs after `setContentView`.
    On API 35+ (forced edge-to-edge, where `statusBarColor` is ignored) it sets
    light status/navigation icon appearance from the current night mode via
    `WindowInsetsControllerCompat`; below 35 the navy bars keep white icons.
  - DynamicColors behavior was already correct in
    `MainActivity.java:88-90` (applied only when the saved theme is `system`);
    the palette definitions do not block it.
  - Launcher icon: background is now `#0B1220`
    (`values/ic_launcher_background.xml`); `drawable/ic_launcher_foreground.xml`
    is a mint/cyan microphone (`#34D399` capsule, `#22D3EE` stand) on a
    `#131D31` disc; new `drawable/ic_launcher_monochrome.xml`; both
    `mipmap-anydpi-v26/ic_launcher*.xml` include `<monochrome>`.
  - Regenerated all 10 legacy raster icons (mdpi 48 .. xxxhdpi 192, square and
    round) locally with ImageMagick 7 + Librsvg 2.62 from temporary SVGs in
    `/tmp/opencode/sawt-icons/`; no downloads.
- Verified:
  - `source .toolchain/env.sh && ./gradlew :app:assembleDebug --offline`
    -> BUILD SUCCESSFUL.
  - `aapt2 dump resources` on the debug APK shows both configs, e.g.
    `colorPrimary` `#ff0f766e` (default) / `#ff34d399` (night),
    `colorSurface` `#fff8fafc` / `#ff0b1220`,
    `ic_launcher_background` `#ff0b1220`; badging still `2.0.1`/code 2.
  - Contrast ratios (WCAG): light onPrimary/primary 5.47:1, light
    primary/surface 5.23:1, dark text/surface 15.89:1, dark muted/surface
    8.48:1, dark onPrimary/primary 8.47:1; all >= 5.2:1.
  - `rg` finds no remaining `#DC2534`/`#DA2534`; `git diff --check` clean.
  - A `reviewer` pass was run before committing. It found the untracked
    monochrome drawable and the light-system-bar icon contrast issue; both are
    fixed (the new `ic_launcher_monochrome.xml` is included in `f18ed15`).
    Remaining low-priority notes left unchanged: `colorTertiary*` roles are not
    mapped (unused by current layouts), and the dark `colorOutline`/`#3B4A63`
    is below WCAG 3:1 for component boundaries (the palette was user-approved).
  - Not run: Gradle lint (known offline `lint-gradle` gap), Go build/tests (no
    Go changes), device/emulator rendering, installs.
- Committed/pushed (user requested "update GitHub"):
  - `735bd6d` `feat(ui): apply Sawt+ palette and persisted theme toggle` for
    `internal/web/dist/index.html` (phase 2).
  - `f18ed15` `feat(android): apply Sawt+ palette and launcher icon` for the
    Android palette, system-bar fix, and launcher icon (phase 3), including the
    new `ic_launcher_monochrome.xml`.
  - Pushed `54af86b..f18ed15` to `origin/main`. `STATE.md` is gitignored and was
    not committed. `apk/` and `bin/` were not replaced.
- Next / blocked:
  - Visual check on a device or emulator is still pending; ask before
    `adb install`.
  - Do not replace `apk/sawt-plus-2.0.1.apk` or `bin/sawt-linux-amd64` unless
    requested.

Resume prompt for the next session:

> Read `AGENTS.md` and `STATE.md` first. Session 24 phases 1–2 and session 25
> phase 3 (Android palette + launcher icon) are done, verified offline, and
> pushed to `origin/main` (`735bd6d`, `f18ed15`). Next: visual check of the app
> themes/icons on a device, then the remaining low-priority notes. Ask before
> installs; keep changes inside the repo and do not replace `apk/` or `bin/`.

## Session 24 (2026-10-23) — brand identity plan and delegation rule

- Goal: adopt the README banner palette as the visual identity for the Android
  app, its launcher icon, and the local web UI, with coherent light and dark
  themes and no emojis.
- Decisions confirmed by the user:
  - Android keeps the System/Light/Dark setting with System as the default.
    Material You dynamic colors stay enabled in System mode only; explicit
    Light/Dark use the fixed Sawt+ palette.
  - The web UI gets a dark/light toggle with dark as the default and the choice
    persisted in `localStorage`.
  - Source changes plus build verification only for now. Do not replace
    `apk/sawt-plus-2.0.1.apk` or `bin/sawt-linux-amd64` and do not bump the
    version.
  - Keep READMEs emoji-free. Do not move the badges; the reported "+" issue is
    the bidi rendering of `Sawt+` inside Arabic text.
- Palette to use (shared by the app and the web UI):
  - Dark: surface `#0B1220`, surface-container-low `#0F172A`, containers
    `#131D31`..`#22314F`, primary `#34D399`, secondary `#22D3EE`,
    text `#E6EDF7`, muted `#9FB0C7`, outline `#243349`/`#3B4A63`.
  - Light: surface `#F8FAFC`/`#FFFFFF`, containers `#F1F5F9`..`#D1DDEB`,
    primary `#0F766E`, secondary `#0E7490`, text `#0F172A`, muted `#475569`,
    outline `#CBD5E1`/`#64748B`.
  - Core text/background pairs are at least 5.3:1 contrast.
- Planned work:
  - README (phase 1, done): `assets/banner-ar.svg` (mirrored layout,
    `Sawt+` LTR-isolated, Arabic tagline below it) and
    `assets/banner-en.svg`; subtle SMIL waveform animation; every `Sawt+` in
    Arabic text isolated with `<bdi>`/LTR; image-slot comments kept.
  - Web (phase 2, done): palette, dark default, toggle and persistence are
    implemented and verified; see "Phase 2 results" below.
  - Android (phase 3, next): add light colors in
    `app/src/main/res/values/colors.xml` and dark colors in
    `values-night/colors.xml`, map them to Material 3 attrs in `styles.xml`,
    keep the `DynamicColors` call for System mode, and replace the red launcher
    icon (`#DC2534`) with the navy/emerald identity. Update the adaptive
    foreground and regenerate all legacy and round PNG icons locally with
    librsvg/ImageMagick. Build with
    `./gradlew :app:assembleDebug --offline`.
  - Future screenshots: when the user supplies them, add them under
    `assets/media/` and optionally convert them to GIF/WebP.
- Added a "Delegation (required)" section to `AGENTS.md`: use `explore` for
  broad read-only codebase work, `scout` for external sources, and `reviewer`
  for diff review, without duplicating their work in the main context.
- Phase 1 results (2026-10-23, docs/assets only):
  - Added `assets/banner-ar.svg`: 1200x280, mirrored layout (mic/brand right,
    waveform left), `Sawt+` with `direction="ltr" unicode-bidi="isolate"`,
    Arabic tagline `ميكروفون لاسلكي لحاسوب لينكس`, and subtle SMIL animation.
  - Added `assets/banner-en.svg`: the English banner with the same animation.
    `assets/banner.svg` is untouched for compatibility.
  - `README.md:4` now uses `banner-ar.svg` with Arabic-only alt text;
    `README.en.md:4` uses `banner-en.svg`; `README.md:64` wraps `Sawt+` in
    `<bdi dir="ltr"><strong>…</strong></bdi>`.
  - Verified: both SVGs parse as XML; ImageMagick 7 (RSVG delegate) renders
    both at 1200x280; no emoji in either README; all non-comment relative
    images and all relative links resolve; `git diff --check` clean.
  - Not run: Go build/tests and Gradle (no source changes).
  - Phase 2 results (2026-10-23, web UI only):
    - `internal/web/dist/index.html`: replaced the purple Material 3 tokens
      with the Sawt+ palette. Dark is now the CSS default (`:root`), and
      `:root[data-theme="light"]` holds the light palette; the
      `prefers-color-scheme` block was removed.
    - Added a `themeBtn` icon button next to `langBtn` (sun/moon sprite
      symbols `i-sun`/`i-moon`), defaulting to dark. The choice is persisted in
      `localStorage["sawtplus-theme"]`; a small head script applies the light
      attribute before paint to avoid a flash.
    - Added `lightMode`/`darkMode` labels in Arabic and English; `applyTheme()`
      runs from `applyLanguage()` so the aria-label/title/icon stay translated.
    - Verified: extracted inline JS passes `node --check`; core contrast pairs
      are >= 5.47:1 (dark text 15.9:1, light primary button 5.47:1);
      `go build ./...` green. No Gradle run.
  - Published (user-approved): `abb43ee` (delegation rules in `AGENTS.md`) and
    `54af86b` (banners + README), pushed to `origin/main`
    (`8b72468..54af86b`). `STATE.md` stayed local/uncommitted.
- Next: phase 3 (Android colors/icons) completed in session 25. Ask before
  committing or publishing.

Resume prompt for the next session:

> Read `AGENTS.md` and `STATE.md` first. Session 24 phases 1 and 2 are done and
> verified (README banners, web palette + theme toggle); continue with phase 3
> (Android colors/icons). Delegate broad read-only work to
> `explore`/`scout` and do not duplicate it. Keep changes inside the repo, do
> not replace `apk/` or `bin/`, and do not commit until I ask.

## Session 23 (2026-10-23) — Arabic-first README redesign

- Done: made Arabic the primary README, following the MCP-Agent language-switch
  pattern and Konveyor-style presentation (no emojis, per user request).
  - `README.md` is now the full Arabic README (was `README.ar.md`).
  - `README.en.md` is the full English README (was `README.md`).
  - `docs/advanced.md` now links to `../README.en.md`; `docs/advanced.ar.md`
    links to `../README.md`.
- Layout: centered hero with `assets/banner.svg`, language switch in the middle,
  shields.io badges, quick-link TOC, requirements/controls/troubleshooting
  tables, FAQ `<details>`, footer, and back-to-top anchors. No emojis.
- Added invisible `<!-- IMAGE SLOT: ... -->` markers in both READMEs for
  `assets/media/hero.png`, `assets/media/how-it-works.png`, and
  `assets/media/web-ui.png`; replace the comment with the `<img>` markup when
  the user provides the images.
- Verified with a read-only Python check: no emoji codepoints, every rendered
  relative link exists, every TOC anchor has a matching `id`, and
  `assets/banner.svg` parses as XML. `git diff --check` is clean. No Go/Android
  build needed (docs-only change).
- Committed as `8b72468` (`docs: make README Arabic-first and redesign
  layout`) and pushed to `origin/main` (`defe19d..8b72468`). `STATE.md` stays
  local/gitignored and was not included in the commit.

## Session 22 (2026-10-22) — frame-based activity indicator

- Goal: mark a device active from actual PCM arrivals instead of only the
  connection state, so saved devices stay identifiable when their address
  changes.
- Done in `internal/daemon/daemon.go`:
  - `Status` now has `LastFrameAt time.Time` (`json:"last_frame_at"`) and
    `Active bool` (`json:"active"`).
  - `ProcessPCM` records `time.Now()` in `status.LastFrameAt` under `d.mu`.
  - `Status()` computes `Active` when the last frame is at most 3 seconds old
    (`activeWindow` constant).
  - `SavedDevice` now exposes `Active` (`json:"active"`). `SavedDevices()`
    matches the current status by `DeviceID` or by normalized address
    (`config.NormalizeAddress`) and only marks it active inside the 3 s window.
- Done in `internal/web/dist/index.html`:
  - Added `normalizeAddr` (strips `ws://`/`wss://`, whitespace, trailing `/`,
    defaults a missing port to `8765`) and used it in `sameDevice` for every
    address comparison.
  - `frameIsRecent`/`activeDevice` accept `last_frame_at` <= 3 s, status
    `active`, or a saved device with `active`. Connecting/pairing still count
    as active; idle/error/stopped devices drop out after the window.
  - `refreshActivity` runs from the 1 s uptime tick and re-renders device
    groups when the active set changes (so expiry is reflected without a new
    status event).
  - SSE fallback: `startPolling` polls `/api/status` + `/api/saved` every 2 s
    when `EventSource` errors, and `stopPolling` resumes on `onopen`.
- `internal/api/server.go` needed no code change: `handleStatus`,
  `handleSaved`, and the SSE event already serialize `daemon.Status` and
  `daemon.SavedDevice` directly, so the new JSON fields are passed through.
- Compatibility checks: stopped/error streams become inactive after 3 s;
  connecting/pairing remain visible; address differences are normalized on
  both daemon (`SavedDevices`) and browser (`sameDevice`) sides; ADB addresses
  (contain `:`) normalize unchanged.
- Commands: no tests, build, lint, or commits run, per the request. Only file
  edits plus read-only `git status`, `git log`, grep/sed inspection.
- Files changed: `internal/daemon/daemon.go`, `internal/web/dist/index.html`,
  `STATE.md`. Not rebuilt: `bin/sawt-linux-amd64` still embeds the previous UI
  (see session 20).

## Session 21 (2026-10-22) — fix "unauthorized" when opening the UI manually

- Cause: the web UI requires the local API token for every `/api/` call
  (`internal/api/server.go:99-127`). Opening `http://127.0.0.1:8787/` directly
  has no token/cookie, so search (and other actions) fail with
  `unauthorized; open the UI with: sawt web`.
- Fix/behaviour: `sawt web` opens
  `http://127.0.0.1:8787/?token=<api.token>`; the token is exchanged for the
  HttpOnly `sawt_api` cookie (`server.go:101-113`). The token lives in
  `~/.config/sawt-plus/api.token` (mode 0600).
- Done: ran `./bin/sawt-linux-amd64 web` (opens the browser authenticated) and
  captured the URL with `web --no-open`. The daemon is running on 8787.
- Rule: always open the UI via `sawt web`, not a bare `127.0.0.1:8787`.

## User preferences

- Communicate in Arabic.
- README is Arabic-first: `README.md` is the full Arabic README and
  `README.en.md` is the full English translation, with a visible language
  switch at the top of both files. Keep them free of emojis.
- The project should be useful to anyone, not tied to the test phone: prioritize
  broad device compatibility (target Android 5.0 / API 21 and up).
- Be cautious: ask before downloads, installs, or changes outside the repo.
- Terminal-first UX: control everything with CLI commands. Do NOT build a TUI.
- Optional graphical UI: browser page at localhost, opened on demand, similar
  to Syncthing. The daemon should run headless in the terminal.
- Control must exist on BOTH surfaces: the phone app and the PC/browser.
  Consider a phone-only user and a PC-only user. The PC daemon stays the audio
  backend (not just a monitor).
- Name the input-level control "مستوى الصوت" in the UI; avoid the technical
  word "gain" in user-facing text.
- Android app should follow Material You 3 (Material 3) and support Arabic.
- Workflow: the main conversation is for management/planning. Delegate broad
  read-only work to `explore` (codebase), `scout` (external sources), and
  `reviewer` (diffs), and do not duplicate their work in the main context.
  Start a fresh session per phase and rely on `STATE.md` as the handoff.
- The project will be published on GitHub; remove incidental personal data
  (phone model, local IP, pairing codes, local paths). Session 8 renamed the
  public identity to Sawt+ (`github.com/apot4711-web/sawt-plus`,
  `com.sawtplus.app`). `STATE.md` stays local and is git-ignored.
- The user has more feedback; collect it before large refactors. Deferred
  notes go in the section below.

## Handoff protocol

Read this file first. Update it after every milestone, failure, or blocker.
Never start a new phase while the previous one is unverified. Ask before any
download, install, or change outside this repository.

## Archive

Older history lives in STATE.archive.md (sessions 20 and earlier, early decision/done/verified lists, and the superseded resume/publication notes).
