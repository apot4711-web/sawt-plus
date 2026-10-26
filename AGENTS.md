# Agent Rules

## Absolute rules

- NEVER download, install, update, or remove software/dependencies without
  explicit user approval. First state the exact command, expected size, and
  location, then wait.
- Keep all changes inside this repository. Do not modify system packages,
  global configs, or `~/.gradle` without permission.
- Never touch the original upstream project checkout or the installed F-Droid
  app.
- Never commit secrets, tokens, keystores, or `local.properties`.
- Do not commit at all unless the user explicitly asks.

## Context safety (required)

- At the start of every session, read `STATE.md` before doing anything else.
- After every milestone, decision, failure, or blocker, update `STATE.md`
  (Done / Next / Blocked / files / commands / test results).
- Keep work in small verified steps. Use the todo list and do not start the
  next phase before the current one compiles/tests.
- If context is running low: stop feature work, write a complete handoff into
  `STATE.md`, and tell the user. Do not guess or redo finished work.

## Phone UI testing (required)

- The user drives all phone UI interaction: taps, Stop/Start, transport and
  permission toggles, pairing, and settings changes. Never tap or automate the
  phone UI (`input tap/swipe/keyevent`, uiautomator clicks, etc.).
- When a test needs a UI action, ask the user to do it and wait for their
  confirmation. Do not guess that the action happened.
- Verify results from the host only: `adb logcat`, `dumpsys activity
  services/activities`, the daemon API (`/api/status`, `/api/stream`), and the
  CLI. A read-only UI dump is allowed for diagnosis, but never click it.

## Delegation (required)

- Delegate broad read-only work to a subagent so the main context stays small:
  use `explore` for codebase search, reading, and analysis; `scout` for external
  docs, dependency source, and upstream implementations; and `reviewer` to
  inspect a diff before committing.
- Give the subagent a precise question and ask for a concise result with
  `file:line` references. Do not duplicate its work in the main context; read
  only the specific files needed to decide or edit.
- Keep edits, builds, tests, and final verification in the main agent, or use
  `builder` after the plan is approved.
- A single known file or a narrow lookup can be read directly.

## Project

- Go client/daemon: `cmd/`, `internal/`
- Android app: `app/src/main/java/com/sawtplus/app/`
- App id: `com.sawtplus.app`
- Ready artifacts: `apk/sawt-plus-2.1.2.apk` and `bin/sawt-linux-amd64`.
  Users install these; do not put Android build instructions in README.
- Protocol: WebSocket text control + binary `s16le` 48000 Hz mono 20 ms.
- Default transport: Wi-Fi (local network, `ws://`); ADB is the fallback.
- UX: terminal-first CLI. Do NOT build a TUI. The GUI is an optional web page
  at localhost, opened on demand (Syncthing-style). The daemon is headless.

## Build and test

```bash
source .toolchain/env.sh

go build ./...
go test ./...

./gradlew :app:assembleDebug
```

The local toolchain is private to the repo:

- JDK 17: `.toolchain/jdk17`
- Android SDK 35: `.toolchain/android-sdk`
- Recreate it with `scripts/setup-android.sh` (asks before large downloads).
