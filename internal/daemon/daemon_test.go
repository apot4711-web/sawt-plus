package daemon

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/apot4711/sawt-plus/internal/config"
	"github.com/apot4711/sawt-plus/internal/protocol"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
