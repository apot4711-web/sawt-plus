# Sawt+ 2.1.0 — Security release

## ⚠️ BREAKING — update the app and the PC binary together

- **Wi-Fi is now TLS-only (`wss://`).** The phone serves a self-signed
  certificate; the PC pins its SHA-256 fingerprint (shown in Settings →
  Certificate fingerprint). Old PC binaries cannot connect to the new app,
  and vice versa.
- **ADB now requires a prior Wi-Fi pairing.** The phone's ADB socket demands
  `TOKEN <pairing-token>` before any audio flows. If you've only ever used
  USB, pair once over Wi-Fi first (`sawt pair HOST:PORT CODE`).
- **License changed MIT → GPLv3.** The project stays open forever: anyone
  distributing it (or modified versions) must keep the source open.

## Security fixes

- Any app on the phone could previously tap the mic through the
  unauthenticated ADB socket — now closed by the token handshake.
- Pairing tokens no longer travel in cleartext (TLS everywhere on Wi-Fi).
- Certificate pinning (TOFU) on every connect; a mismatched certificate
  aborts the connection before any secret is sent.
- mDNS spoofing can no longer steal the token: rediscovery verifies identity
  with a tokenless TLS handshake; legacy pairings must re-pair once to
  establish the pin.
- Pairing-code brute force: per-address exponential backoff (2s→60s),
  5-minute lockout after 8 rejections, honors the phone's `retry_after`.
- Mic capture now restarts with backoff after interruptions (e.g. phone
  calls) instead of dying silently; `startRecording()` failures are
  contained.
- New "Unpair all" in Settings: revokes every token and drops live Wi-Fi +
  ADB sessions immediately (PCs must re-pair; also run `sawt unpair` on the
  PC or re-pair).
- Hardened internals: WebSocket read limits sized to the negotiated frame
  (was 16 MB), status broadcasts throttled to 2/sec on the audio path.

## New feature

- **USB hotplug auto-connect:** with the daemon running, plugging in a
  previously-paired phone connects automatically — no command needed.
  Unplug to disconnect. Only ever paired devices; disable with
  `auto_connect = false`.
