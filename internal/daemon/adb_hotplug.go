package daemon

import (
	"context"
	"sort"
	"time"

	"github.com/apot4711-web/sawt-plus/internal/adb"
)

// adbHotplugInterval is how often the daemon polls `adb devices` while it
// runs. Only appearance transitions (absent -> present) can trigger an
// auto-connect, so a quiet poll is cheap and silent. The poll is uniform in
// every state on purpose: the appearance-transition baseline must stay
// correct even while a stream is active, otherwise a phone plugged in
// mid-stream would look "new" later and connect unexpectedly.
const adbHotplugInterval = 3 * time.Second

// startADBHotplug launches the USB hotplug watcher. It is a silent no-op
// when adb is not installed. The watcher is tied to the daemon's lifetime:
// Shutdown cancels its context and waits for it via d.wg.
func (d *Daemon) startADBHotplug() {
	if !adb.Available() {
		return
	}
	d.wg.Add(1)
	go d.adbHotplugLoop(d.baseCtx)
}

// adbHotplugLoop watches `adb devices` and auto-connects a previously-paired
// phone the moment it is plugged in over USB.
//
// Rules:
//   - Only appearance transitions (absent -> present) trigger a connect, and
//     only while the daemon is idle (StateIdle). An active stream — any
//     transport — is never interrupted or switched mid-stream. In
//     particular, an explicit Disconnect is respected: the still-plugged
//     phone does not reconnect until it is unplugged and plugged in again.
//   - Eligibility is decided by pairedADBTarget: unknown serials and
//     ambiguous setups (no paired device, or several) are never connected.
//   - Honors cfg.Daemon.AutoConnect, re-read every poll, so it can be
//     toggled at runtime just like StartAutoConnect.
//   - Connection failures are handled by adbLoop's existing backoff/retry;
//     the watcher fires once per appearance and then goes back to watching.
//   - Unplug teardown needs no new code: streamADB's read failure ends the
//     stream, adbLoop backs off and parks in `adb wait-for-device` until
//     the phone returns or the stream is cancelled.
//   - If several eligible phones appear in the same poll, the first serial
//     in sorted order wins; the rest are ignored until unplug/replug.
func (d *Daemon) adbHotplugLoop(ctx context.Context) {
	defer d.wg.Done()
	ticker := time.NewTicker(adbHotplugInterval)
	defer ticker.Stop()
	seen := make(map[string]bool)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		present := d.adbHotplugSerials(ctx)
		fresh := make([]string, 0, len(present))
		for _, serial := range present {
			if !seen[serial] {
				fresh = append(fresh, serial)
			}
		}
		seen = make(map[string]bool, len(present))
		for _, serial := range present {
			seen[serial] = true
		}
		if !d.Config().Daemon.AutoConnect {
			continue
		}
		if d.Status().State != StateIdle {
			continue
		}
		for _, serial := range fresh {
			target, ok := d.pairedADBTarget(serial)
			if !ok {
				continue
			}
			d.Log("info", "USB device %s attached; auto-connecting", serial)
			if err := d.StartStream(target); err != nil {
				d.Log("warn", "adb hotplug auto-connect: %v", err)
			}
			break
		}
	}
}

// adbHotplugSerials returns the sorted serials of attached USB devices in
// "device" (authorized) state. Unauthorized or offline devices are ignored:
// the daemon must never auto-connect a phone the user has not authorized
// for USB debugging on this PC.
func (d *Daemon) adbHotplugSerials(ctx context.Context) []string {
	var out []string
	for _, dev := range d.adbDevices(ctx) {
		if dev.Host == "" {
			continue
		}
		out = append(out, dev.Host)
	}
	sort.Strings(out)
	return out
}

// pairedADBTarget maps an attached USB serial to a stream target eligible
// for hotplug auto-connect. A serial is eligible only if it belongs to a
// previously-paired phone:
//
//  1. A saved device entry with address "adb:<serial>" and a non-empty
//     token (explicit per-device record) wins.
//  2. Otherwise, exactly one paired device exists in the config: the ADB
//     credential is then that device's token — the same rule manual
//     `sawt adb run` follows (see adbToken). The phone itself verifies the
//     TOKEN handshake before any audio flows, so a never-paired phone can
//     never complete the connection: its AdbServer rejects a token it was
//     never paired with.
//
// With zero paired devices, or several (ambiguous — the serial cannot be
// matched to one of them), ok is false and nothing is auto-connected. The
// user pairs or picks explicitly, exactly as with `sawt adb run`.
func (d *Daemon) pairedADBTarget(serial string) (target string, ok bool) {
	if serial == "" {
		return "", false
	}
	want := "adb:" + serial
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	paired := 0
	for _, dev := range d.cfg.Devices {
		if dev.Token == "" {
			continue
		}
		paired++
		if dev.Address == want {
			return want, true
		}
	}
	if paired == 1 {
		return want, true
	}
	return "", false
}
