package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// WifiPowerSaveWarning returns a ready-to-print hint when the Linux Wi-Fi
// interface that carries the default route has 802.11 power save enabled.
//
// Power save is the most common host-side cause of the "stream connects but
// audio stutters / the phone looks silent" symptom: the access point buffers
// frames for a sleeping client, so 20 ms PCM frames arrive in bursts and the
// daemon sees multi-second stalls. The phone already takes a high-performance
// WifiLock; this covers the PC side.
//
// It returns "" when the platform is not Linux, iw is missing, the interface
// is not Wi-Fi, or power save is already off. It never changes host settings.
func WifiPowerSaveWarning() string {
	iface := defaultRouteInterface()
	if iface == "" {
		return ""
	}
	if _, err := exec.LookPath("iw"); err != nil {
		return ""
	}
	out, err := exec.Command("iw", "dev", iface, "get", "power_save").CombinedOutput()
	if err != nil {
		return ""
	}
	if !strings.Contains(strings.ToLower(string(out)), "power save: on") {
		return ""
	}
	return fmt.Sprintf(
		"Wi-Fi power save is ON on %s and can cause audio dropouts; "+
			"disable it with: sudo iw dev %s set power_save off "+
			"(NetworkManager: set wifi.powersave=2 and reconnect)",
		iface, iface)
}

// defaultRouteInterface parses /proc/net/route and returns the interface of
// the default route (destination 00000000). No network access, no `ip`
// dependency, no root.
func defaultRouteInterface() string {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == "00000000" {
			return fields[0]
		}
	}
	return ""
}
