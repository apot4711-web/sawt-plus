package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/apot4711-web/sawt-plus/internal/config"
	"github.com/apot4711-web/sawt-plus/internal/protocol"
)

type fakeSource struct {
	mu     sync.Mutex
	bytes  int
	closed bool
}

func (f *fakeSource) Write(pcm []byte) {
	f.mu.Lock()
	f.bytes += len(pcm)
	f.mu.Unlock()
}
func (f *fakeSource) Stats() (uint64, uint64, int32) { return 0, 0, 1 }
func (f *fakeSource) Name() string                   { return "fake" }
func (f *fakeSource) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
func (f *fakeSource) written() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bytes
}

func TestStreamFromFakePhone(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	frames := 20
	// TLS-only transport: the fake phone serves wss:// with a self-signed
	// cert; the daemon pins it via TOFU on first connect.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		hello, err := protocol.Decode(data)
		if err != nil || hello.Type != "hello" {
			return
		}
		ready, _ := protocol.Encode(protocol.Message{
			Type: "ready", Version: protocol.Version, Rate: 48000,
			Channels: 1, Format: "s16le", FrameMS: 20, Clients: 1, Name: "Fake Phone",
		})
		if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
			return
		}
		go func() {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		frame := make([]byte, 48000*20/1000*2)
		for i := 0; i < frames; i++ {
			if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Daemon.Reconnect = false
	cfg.Audio.SourceName = "test-source"
	d := New(cfg)
	src := &fakeSource{}
	d.source = src
	defer d.Shutdown()

	if err := d.StartStream(u.Host); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if d.Status().Frames >= uint64(frames) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	status := d.Status()
	if status.State != StateStreaming && status.Frames == 0 {
		t.Fatalf("state = %s, frames = %d", status.State, status.Frames)
	}
	if status.Frames < uint64(frames) {
		t.Fatalf("frames = %d, want >= %d", status.Frames, frames)
	}
	if src.written() == 0 {
		t.Fatal("source received no PCM")
	}
	if status.Device != "Fake Phone" {
		t.Fatalf("device = %q", status.Device)
	}
}

// TestTLSPinningEnforced verifies the wss:// certificate-pinning behavior:
// TOFU accepts and observes on first use, a correct pin connects, and a
// wrong pin fails closed before any token is sent.
func TestTLSPinningEnforced(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		time.Sleep(300 * time.Millisecond)
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	realFP := certFingerprint(server.Certificate().Raw)
	if !validFingerprint(realFP) {
		t.Fatalf("test server fingerprint invalid: %q", realFP)
	}

	cfg := config.Default()
	cfg.Daemon.Reconnect = false
	d := New(cfg)
	defer d.Shutdown()
	ctx := context.Background()

	// 1. TOFU: no pin stored → accept and observe the fingerprint.
	ws, observed, err := d.dialWSS(ctx, u.Host, "")
	if err != nil {
		t.Fatalf("TOFU dial: %v", err)
	}
	ws.Close()
	if observed != realFP {
		t.Fatalf("observed = %q, want %q", observed, realFP)
	}

	// 2. Correct pin → connect.
	ws, _, err = d.dialWSS(ctx, u.Host, realFP)
	if err != nil {
		t.Fatalf("pinned dial: %v", err)
	}
	ws.Close()

	// 3. Wrong pin → fail closed during the TLS handshake.
	wrong := strings.Repeat("b", 64)
	if _, _, err := d.dialWSS(ctx, u.Host, wrong); err == nil ||
		!strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("want fingerprint mismatch error, got %v", err)
	}

	// 4. streamWiFi surfaces the mismatch as a connect error (token never sent).
	entry := config.DeviceEntry{Address: u.Host, Token: "tok", Name: "Fake", CertSHA256: wrong}
	if err := d.streamWiFi(ctx, entry); err == nil ||
		!strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("streamWiFi: want fingerprint mismatch error, got %v", err)
	}
}

func TestSubscribeCancelDuringBroadcast(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := New(config.Default())
	defer d.Shutdown()

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				d.Log("info", "broadcast")
			}
		}
	}()
	for i := 0; i < 300; i++ {
		_, cancel := d.Subscribe()
		cancel()
	}
	close(done)
}

func TestFeedCancelDuringBroadcast(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := New(config.Default())
	defer d.Shutdown()

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				d.broadcastPCM(make([]byte, 16))
			}
		}
	}()
	for i := 0; i < 300; i++ {
		_, cancel := d.Feed()
		cancel()
	}
	close(done)
}

func TestStartStreamAfterShutdown(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Devices = []config.DeviceEntry{{ID: "dev", Name: "Phone", Address: "192.168.1.10:8765"}}
	d := New(cfg)
	d.Shutdown()

	if err := d.StartStream("Phone"); !errors.Is(err, ErrClosed) {
		t.Fatalf("StartStream after Shutdown = %v, want ErrClosed", err)
	}
}

func TestResolveDeviceByNameAndID(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Devices = []config.DeviceEntry{{
		ID: "dev-1", Name: "Phone A", Address: "192.168.1.10:8765", Token: "tok",
	}}
	d := New(cfg)
	defer d.Shutdown()

	for _, target := range []string{"Phone A", "dev-1"} {
		entry, transport, err := d.ResolveDevice(target)
		if err != nil {
			t.Fatalf("ResolveDevice(%q): %v", target, err)
		}
		if transport != "wifi" {
			t.Fatalf("transport = %q", transport)
		}
		if entry.Address != "192.168.1.10:8765" {
			t.Fatalf("ResolveDevice(%q).Address = %q, want saved address", target, entry.Address)
		}
		if entry.ID != "dev-1" || entry.Token != "tok" {
			t.Fatalf("ResolveDevice(%q) = %+v, want saved identity/token", target, entry)
		}
	}
}

func TestResolveDeviceByAddress(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Devices = []config.DeviceEntry{{ID: "dev-1", Name: "Phone A", Address: "192.168.1.10:8765"}}
	d := New(cfg)
	defer d.Shutdown()

	entry, _, err := d.ResolveDevice("192.168.1.10")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Address != "192.168.1.10:8765" || entry.ID != "dev-1" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
}

func TestUnpairByName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Devices = []config.DeviceEntry{{
		ID: "dev-1", Name: "Phone A", Address: "192.168.1.10:8765", Token: "tok",
	}}
	d := New(cfg)
	defer d.Shutdown()

	if err := d.Unpair("Phone A"); err != nil {
		t.Fatalf("Unpair by name: %v", err)
	}
	if got := d.SavedDevices(); len(got) != 0 {
		t.Fatalf("device still saved: %+v", got)
	}
}
