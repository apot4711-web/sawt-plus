// Package discovery finds Sawt devices on the local network.
//
// Discovery is multi-layered: mDNS (Go), then avahi-browse on Linux as a
// fallback, then an active scan of the local /24 on the server port. Results
// from all layers are merged and de-duplicated.
package discovery

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	ServiceType = "_sawtplus._tcp"
	// DefaultPort is the port the phone app tries first.
	DefaultPort = 8765
)

type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Version   string `json:"version"`
	Pairing   bool   `json:"pairing"`
	Transport string `json:"transport"`
}

func (d Device) Label() string {
	if d.Name != "" {
		return d.Name
	}
	return d.Address
}

// Browse waits up to timeout for Sawt devices and merges the results
// of all discovery layers.
func Browse(ctx context.Context, timeout time.Duration) ([]Device, error) {
	browseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type layerResult struct {
		layer   string
		devices []Device
		err     error
	}
	results := make(chan layerResult, 3)

	go func() {
		devices, err := browseMDNS(browseCtx)
		results <- layerResult{"mDNS", devices, err}
	}()
	go func() {
		devices, err := browseAvahi(browseCtx)
		results <- layerResult{"avahi", devices, err}
	}()
	go func() {
		devices, err := scanSubnet(browseCtx)
		results <- layerResult{"scan", devices, err}
	}()

	byID := make(map[string]Device)
	mDNSFailed := false
	for i := 0; i < 3; i++ {
		select {
		case res := <-results:
			if res.err != nil && res.layer == "mDNS" {
				mDNSFailed = true
			}
			for _, d := range res.devices {
				mergeDevice(byID, d)
			}
		case <-browseCtx.Done():
			i = 3
		}
	}

	if len(byID) == 0 {
		if mDNSFailed {
			return nil, fmt.Errorf("no Sawt devices found (mDNS may be blocked; allow UDP 5353 or check the firewall)")
		}
		return nil, nil
	}
	devices := make([]Device, 0, len(byID))
	for _, d := range byID {
		if d.Transport == "" {
			d.Transport = "wifi"
		}
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Label() < devices[j].Label() })
	return devices, nil
}

// mergeDevice keeps the richest information for a device. Later layers may
// only know the address, so existing fields win.
func mergeDevice(byID map[string]Device, d Device) {
	if d.ID == "" {
		d.ID = d.Address
	}
	if d.ID == "" {
		return
	}
	prev, ok := byID[d.ID]
	if !ok {
		byID[d.ID] = d
		return
	}
	// The active scan only knows the IP; prefer richer mDNS/avahi data.
	scanOnly := prev.Version == "scan"
	if (prev.Name == "" || scanOnly) && d.Name != "" {
		prev.Name = d.Name
	}
	if (prev.Host == "" || scanOnly) && d.Host != "" {
		prev.Host = d.Host
	}
	if prev.Address == "" {
		prev.Address = d.Address
	}
	if prev.Port == 0 {
		prev.Port = d.Port
	}
	if (prev.Version == "" || prev.Version == "scan") && d.Version != "" {
		prev.Version = d.Version
	}
	prev.Pairing = prev.Pairing || d.Pairing
	byID[d.ID] = prev
}

// ---------------------------------------------------------------------------
// Layer 1: mDNS
// ---------------------------------------------------------------------------

func browseMDNS(ctx context.Context) ([]Device, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("create mDNS resolver: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry, 16)
	go func() {
		if err := resolver.Browse(ctx, ServiceType, "local.", entries); err != nil {
			// The channel simply stays empty; the timeout handles the rest.
			_ = err
		}
	}()

	byID := make(map[string]Device)
	for {
		select {
		case <-ctx.Done():
			devices := make([]Device, 0, len(byID))
			for _, d := range byID {
				devices = append(devices, d)
			}
			return devices, nil
		case entry := <-entries:
			if entry == nil {
				continue
			}
			d := fromEntry(entry)
			mergeDevice(byID, d)
		}
	}
}

func fromEntry(e *zeroconf.ServiceEntry) Device {
	d := Device{
		Name:    e.Instance,
		Host:    e.HostName,
		Port:    e.Port,
		Version: "",
	}
	for _, txt := range e.Text {
		key, value, ok := strings.Cut(txt, "=")
		if !ok {
			continue
		}
		switch key {
		case "id":
			d.ID = value
		case "name":
			if value != "" {
				d.Name = value
			}
		case "v":
			d.Version = value
		case "pair":
			d.Pairing = value == "1" || strings.EqualFold(value, "true")
		}
	}

	var ip net.IP
	if len(e.AddrIPv4) > 0 {
		ip = e.AddrIPv4[0]
	} else if len(e.AddrIPv6) > 0 {
		ip = e.AddrIPv6[0]
	}
	if ip == nil {
		if addrs, err := net.LookupIP(e.HostName); err == nil {
			for _, addr := range addrs {
				if v4 := addr.To4(); v4 != nil {
					ip = v4
					break
				}
			}
			if ip == nil && len(addrs) > 0 {
				ip = addrs[0]
			}
		}
	}
	if ip != nil {
		d.Host = ip.String()
		d.Address = net.JoinHostPort(ip.String(), strconv.Itoa(e.Port))
	}
	return d
}

// ---------------------------------------------------------------------------
// Layer 2: avahi-browse (Linux)
// ---------------------------------------------------------------------------

func browseAvahi(ctx context.Context) ([]Device, error) {
	path, err := exec.LookPath("avahi-browse")
	if err != nil {
		return nil, err
	}
	// avahi-browse can linger after dumping its cache, so give it a short
	// budget and keep whatever it printed. -t: terminate after the cache;
	// -p: parseable; -r: resolve addresses.
	avahiCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(avahiCtx, path, "-rtp", ServiceType)
	out, err := cmd.Output()
	devices := parseAvahi(string(out))
	if err != nil && len(devices) == 0 {
		return nil, err
	}
	return devices, nil
}

var avahiQuoted = regexp.MustCompile(`"([^"]*)"`)

// parseAvahi parses the semicolon-separated output of `avahi-browse -rtp`.
// Resolved rows start with "=" (added) or "=" for cached entries.
func parseAvahi(out string) []Device {
	var devices []Device
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "=") {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) < 9 {
			continue
		}
		address := strings.TrimSpace(fields[7])
		port, err := strconv.Atoi(strings.TrimSpace(fields[8]))
		if err != nil || address == "" {
			continue
		}
		d := Device{
			Name:    unescapeAvahi(fields[3]),
			Host:    unescapeAvahi(fields[6]),
			Address: net.JoinHostPort(address, strconv.Itoa(port)),
			Port:    port,
		}
		txt := strings.Join(fields[9:], ";")
		for _, item := range avahiQuoted.FindAllStringSubmatch(txt, -1) {
			key, value, ok := strings.Cut(item[1], "=")
			if !ok {
				continue
			}
			switch key {
			case "id":
				d.ID = value
			case "name":
				if value != "" {
					d.Name = unescapeAvahi(value)
				}
			case "v":
				d.Version = value
			case "pair":
				d.Pairing = value == "1" || strings.EqualFold(value, "true")
			}
		}
		if d.ID == "" {
			d.ID = d.Address
		}
		devices = append(devices, d)
	}
	return devices
}

func unescapeAvahi(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	s = strings.ReplaceAll(s, `\032`, " ")
	return s
}

// ---------------------------------------------------------------------------
// Layer 3: active local /24 scan
// ---------------------------------------------------------------------------

func scanSubnet(ctx context.Context) ([]Device, error) {
	nets, err := localNetworks()
	if err != nil {
		return nil, err
	}
	if len(nets) == 0 {
		return nil, fmt.Errorf("no local IPv4 network")
	}

	var (
		mu       sync.Mutex
		devices  []Device
		wg       sync.WaitGroup
		localSet = localAddresses()
	)
	sem := make(chan struct{}, 128)
	for _, network := range nets {
		for host := 1; host <= 254; host++ {
			ip := make(net.IP, len(network.IP))
			copy(ip, network.IP)
			ip[len(ip)-1] = byte(host)
			if localSet[ip.String()] {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(ip net.IP) {
				defer wg.Done()
				defer func() { <-sem }()
				if d, ok := probeHost(ctx, ip); ok {
					mu.Lock()
					devices = append(devices, d)
					mu.Unlock()
				}
			}(ip)
		}
	}
	wg.Wait()
	return devices, nil
}

// probeHost reports a device if the default port accepts a WebSocket upgrade.
// The upgrade check avoids matching unrelated TCP services. When the app had
// to shift to another port, mDNS/avahi still report the real one.
func probeHost(ctx context.Context, ip net.IP) (Device, bool) {
	if !isWebSocketServer(ctx, ip, DefaultPort) {
		return Device{}, false
	}
	return Device{
		ID:      net.JoinHostPort(ip.String(), strconv.Itoa(DefaultPort)),
		Name:    ip.String(),
		Host:    ip.String(),
		Address: net.JoinHostPort(ip.String(), strconv.Itoa(DefaultPort)),
		Port:    DefaultPort,
		Version: "scan",
	}, true
}

func isWebSocketServer(ctx context.Context, ip net.IP, port int) bool {
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(600 * time.Millisecond))
	req := "GET / HTTP/1.1\r\nHost: " + ip.String() + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return false
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(buf[:n]), "HTTP/1.1 101")
}

// localNetworks returns the /24 networks around each non-loopback IPv4
// address. Docker/VPN interfaces are included but capped to keep it fast.
func localNetworks() ([]*net.IPNet, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var nets []*net.IPNet
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() || !ipNet.IP.IsPrivate() {
			continue
		}
		network := &net.IPNet{IP: ip4.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}
		key := network.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		nets = append(nets, network)
		if len(nets) == 4 {
			break
		}
	}
	return nets, nil
}

func localAddresses() map[string]bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := make(map[string]bool)
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
			out[ipNet.IP.String()] = true
		}
	}
	return out
}
