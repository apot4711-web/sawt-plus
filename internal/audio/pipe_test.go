package audio

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apot4711/sawt-plus/internal/config"
)

func TestPipeSourceRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PipeWire integration test")
	}
	if _, err := exec.LookPath("pactl"); err != nil {
		t.Skip("pactl not available")
	}
	ctx := context.Background()
	if err := Available(ctx); err != nil {
		t.Skipf("PulseAudio/PipeWire not available: %v", err)
	}

	cfg := config.Default().Audio
	cfg.SourceName = "sawt-test"
	cfg.Description = "Sawt Test"
	cfg.PipeSize = 8192

	src, err := NewSource(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Start(ctx); err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = src.Close()
		}
	}()

	frame := Silence(48000, 1, 20)
	for i := 0; i < 25; i++ {
		src.Write(frame)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	written, dropped, _ := src.Stats()
	if written == 0 {
		t.Fatalf("no bytes written (dropped=%d)", dropped)
	}
	t.Logf("written=%d dropped=%d", written, dropped)

	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true

	out, err := exec.Command("pactl", "list", "modules", "short").Output()
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadUint32(new(uint32)) == 1 {
		_ = out
	}
	if containsSource(string(out), cfg.SourceName) {
		t.Fatalf("module still loaded after Close")
	}
}

func containsSource(out, name string) bool {
	for _, line := range splitLines(out) {
		if len(line) > 0 && (contains(line, "source_name="+name)) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
