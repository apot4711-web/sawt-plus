package daemon

import (
	"testing"

	"github.com/apot4711-web/sawt-plus/internal/config"
)

// hotplugTestDaemon builds a Daemon whose config holds exactly the given
// device entries. No adb binary is needed: pairedADBTarget is pure config
// matching.
func hotplugTestDaemon(t *testing.T, devices []config.DeviceEntry) *Daemon {
	t.Helper()
	d := newTestDaemon(t)
	d.cfgMu.Lock()
	d.cfg.Devices = devices
	d.cfgMu.Unlock()
	return d
}

func TestPairedADBTarget(t *testing.T) {
	wifiPaired := config.DeviceEntry{ID: "p1", Name: "Phone", Address: "192.168.1.5:8765", Token: "tok1"}
	wifiPaired2 := config.DeviceEntry{ID: "p2", Name: "Phone2", Address: "192.168.1.6:8765", Token: "tok2"}
	adbPaired := config.DeviceEntry{ID: "a1", Name: "PhoneUSB", Address: "adb:SERIAL1", Token: "tokA"}
	adbUnpaired := config.DeviceEntry{ID: "a2", Name: "PhoneUSB2", Address: "adb:SERIAL2"}

	tests := []struct {
		name    string
		devices []config.DeviceEntry
		serial  string
		want    string
		wantOK  bool
	}{
		{"empty serial", []config.DeviceEntry{wifiPaired}, "", "", false},
		{"no devices at all", nil, "SERIAL9", "", false},
		{
			"explicit adb entry with token wins",
			[]config.DeviceEntry{adbPaired}, "SERIAL1", "adb:SERIAL1", true,
		},
		{
			// Unpaired serials must never auto-connect: with no paired
			// device anywhere, there is no credential to present.
			"unpaired adb entry, nothing paired",
			[]config.DeviceEntry{adbUnpaired}, "SERIAL2", "", false,
		},
		{
			"unknown serial, nothing paired",
			[]config.DeviceEntry{adbUnpaired}, "SERIAL9", "", false,
		},
		{
			// Single paired device: the adbToken() rule — the phone's own
			// TOKEN handshake is the final gate for unknown serials.
			"single wifi-paired device, unknown serial",
			[]config.DeviceEntry{wifiPaired}, "SERIAL9", "adb:SERIAL9", true,
		},
		{
			// Ambiguous: two paired devices, serial matches neither —
			// refuse, exactly like `sawt adb run` does.
			"two paired devices, ambiguous",
			[]config.DeviceEntry{wifiPaired, wifiPaired2}, "SERIAL9", "", false,
		},
		{
			// Explicit per-device record takes precedence even when the
			// overall setup is ambiguous.
			"explicit adb entry wins over ambiguity",
			[]config.DeviceEntry{adbPaired, wifiPaired, wifiPaired2}, "SERIAL1", "adb:SERIAL1", true,
		},
		{
			// Tokenless adb entry does not count as paired; the single
			// wifi-paired device still authorizes the serial.
			"tokenless adb entry ignored for pairing count",
			[]config.DeviceEntry{adbUnpaired, wifiPaired}, "SERIAL2", "adb:SERIAL2", true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := hotplugTestDaemon(t, tc.devices)
			got, ok := d.pairedADBTarget(tc.serial)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("pairedADBTarget(%q) = (%q, %v), want (%q, %v)",
					tc.serial, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestADBHotplugSerialsNoADB verifies the watcher degrades to "nothing
// attached" where adb is not installed (this test environment).
func TestADBHotplugSerialsNoADB(t *testing.T) {
	d := newTestDaemon(t)
	if got := d.adbHotplugSerials(t.Context()); len(got) != 0 {
		t.Fatalf("adbHotplugSerials without adb = %v, want empty", got)
	}
}
