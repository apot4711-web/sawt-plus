package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/apot4711-web/sawt-plus/internal/config"
	"github.com/apot4711-web/sawt-plus/internal/discovery"
)

// fakeWSSPhone serves wss:// with a self-signed certificate like the real
// phone app, completing the WebSocket handshake without requiring a token.
func fakeWSSPhone(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		// No token expected: the identity check must complete before any
		// application data is exchanged.
		_, _, _ = c.ReadMessage()
	}))
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return server, u.Host
}

// newTestDaemon builds a usable Daemon with an isolated config dir.
func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := New(config.Default())
	t.Cleanup(d.Shutdown)
	return d
}

func TestVerifyRediscoverCandidate(t *testing.T) {
	server, addr := fakeWSSPhone(t)
	pin := certFingerprint(server.Certificate().Raw)
	wrongPin := strings.Repeat("b", 64)

	d := newTestDaemon(t)
	ctx := context.Background()

	t.Run("matching pin verifies", func(t *testing.T) {
		entry := &config.DeviceEntry{Name: "Phone", CertSHA256: pin}
		if !d.verifyRediscoverCandidate(ctx, entry, addr) {
			t.Fatal("endpoint presenting the pinned certificate must verify")
		}
	})

	t.Run("mismatched pin fails closed", func(t *testing.T) {
		// Simulates a spoofed mDNS entry: the name resolved to an endpoint
		// that cannot present the pinned certificate.
		entry := &config.DeviceEntry{Name: "Phone", CertSHA256: wrongPin}
		if d.verifyRediscoverCandidate(ctx, entry, addr) {
			t.Fatal("endpoint with a wrong certificate must not verify")
		}
	})

	t.Run("absent pin fails closed without dialing", func(t *testing.T) {
		// Legacy entry paired before TLS existed: nothing to verify against,
		// so mDNS must never be followed. The unroutable address proves no
		// network I/O happens on this path.
		entry := &config.DeviceEntry{Name: "Phone"}
		if d.verifyRediscoverCandidate(ctx, entry, "192.0.2.1:8765") {
			t.Fatal("entry without a pinned certificate must not verify")
		}
	})
}

func TestAdoptRediscoveredAddress(t *testing.T) {
	newEntry := func() config.DeviceEntry {
		return config.DeviceEntry{
			ID: "phone-1", Name: "My Phone",
			Address: "192.168.1.10:8765", Token: "secret-token",
			CertSHA256: strings.Repeat("a", 64),
		}
	}
	spoofed := discovery.Device{
		ID: "attacker", Name: "My Phone", // right name, wrong endpoint
		Address: "192.168.1.99:8765", Transport: "wifi",
	}
	real := discovery.Device{
		ID: "phone-1", Name: "My Phone",
		Address: "192.168.1.11:8765", Transport: "wifi",
	}
	deny := func(context.Context, *config.DeviceEntry, string) bool { return false }
	allow := func(context.Context, *config.DeviceEntry, string) bool { return true }

	t.Run("spoofed name rejected when identity check fails", func(t *testing.T) {
		d := newTestDaemon(t)
		entry := newEntry()
		if d.adoptRediscoveredAddress(context.Background(), &entry, []discovery.Device{spoofed}, deny) {
			t.Fatal("spoofed mDNS entry must not be adopted")
		}
		if entry.Address != "192.168.1.10:8765" {
			t.Fatalf("address changed to %q; must stay unchanged", entry.Address)
		}
		if entry.Token != "secret-token" || entry.CertSHA256 != strings.Repeat("a", 64) {
			t.Fatal("token/pin must never be altered by a rejected candidate")
		}
	})

	t.Run("spoofed entry skipped, verified one adopted", func(t *testing.T) {
		d := newTestDaemon(t)
		entry := newEntry()
		d.cfg.UpsertDevice(entry)
		// First candidate fails the pin check (spoof), second passes.
		verify := func(_ context.Context, _ *config.DeviceEntry, addr string) bool {
			return addr == "192.168.1.11:8765"
		}
		if !d.adoptRediscoveredAddress(context.Background(), &entry, []discovery.Device{spoofed, real}, verify) {
			t.Fatal("verified candidate must be adopted")
		}
		if entry.Address != "192.168.1.11:8765" {
			t.Fatalf("address = %q, want the verified candidate", entry.Address)
		}
		if _, ok := d.cfg.FindDevice("192.168.1.10:8765"); ok {
			t.Fatal("stale address must be removed from config")
		}
		if _, ok := d.cfg.FindDevice("192.168.1.11:8765"); !ok {
			t.Fatal("adopted address must be persisted in config")
		}
	})

	t.Run("candidate at unchanged address ignored", func(t *testing.T) {
		d := newTestDaemon(t)
		entry := newEntry()
		same := discovery.Device{ID: "phone-1", Name: "My Phone", Address: "192.168.1.10:8765", Transport: "wifi"}
		if d.adoptRediscoveredAddress(context.Background(), &entry, []discovery.Device{same}, allow) {
			t.Fatal("candidate at the already-saved address must not trigger adoption")
		}
	})

	t.Run("adb and empty candidates skipped", func(t *testing.T) {
		d := newTestDaemon(t)
		entry := newEntry()
		devices := []discovery.Device{
			{ID: "phone-1", Name: "My Phone", Address: "adb:ABC123", Transport: "adb"},
			{ID: "phone-1", Name: "My Phone", Address: "", Transport: "wifi"},
		}
		if d.adoptRediscoveredAddress(context.Background(), &entry, devices, allow) {
			t.Fatal("adb/empty candidates must be skipped")
		}
	})
}

func TestRediscoverWithoutIdentity(t *testing.T) {
	// No ID and no name: returns false before any network I/O.
	d := newTestDaemon(t)
	if d.rediscover(context.Background(), &config.DeviceEntry{Address: "192.168.1.10:8765"}) {
		t.Fatal("entry with no ID/name must not rediscover")
	}
}
