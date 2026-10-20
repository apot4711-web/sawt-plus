// Package audio feeds PCM frames into PipeWire/PulseAudio through a pipe
// source module.
package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/apot4711-web/sawt-plus/internal/config"
)

type Source struct {
	cfg      config.AudioConfig
	fifo     string
	moduleID string

	mu     sync.Mutex
	file   *os.File
	ch     chan []byte
	done   chan struct{}
	closed atomic.Bool
	wg     sync.WaitGroup

	written atomic.Uint64
	dropped atomic.Uint64
	readers atomic.Int32
}

func NewSource(cfg config.AudioConfig) (*Source, error) {
	if _, err := exec.LookPath("pactl"); err != nil {
		return nil, errors.New("pactl not found: install PulseAudio utilities or PipeWire's pulse compatibility package")
	}
	dir := config.RuntimeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create runtime directory: %w", err)
	}
	return &Source{
		cfg:  cfg,
		fifo: filepath.Join(dir, cfg.SourceName+".fifo"),
		ch:   make(chan []byte, 48),
		done: make(chan struct{}),
	}, nil
}

func (s *Source) Name() string { return s.cfg.SourceName }
func (s *Source) FIFO() string { return s.fifo }

func (s *Source) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file != nil {
		return nil
	}

	if err := s.unloadExisting(ctx); err != nil {
		return err
	}
	_ = os.Remove(s.fifo)
	if err := unix.Mkfifo(s.fifo, 0o600); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create FIFO: %w", err)
	}

	args := []string{
		"load-module", "module-pipe-source",
		"source_name=" + s.cfg.SourceName,
		fmt.Sprintf("channels=%d", s.cfg.Channels),
		"format=s16le",
		fmt.Sprintf("rate=%d", s.cfg.Rate),
		"file=" + s.fifo,
		fmt.Sprintf(`source_properties=device.description="%s"`, sanitizeProperty(s.cfg.Description)),
	}
	out, err := runPactl(ctx, args...)
	if err != nil {
		_ = os.Remove(s.fifo)
		return err
	}
	s.moduleID = strings.TrimSpace(out)

	file, err := os.OpenFile(s.fifo, os.O_RDWR|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		_ = s.unload(ctx, s.moduleID)
		return fmt.Errorf("open FIFO %s: %w", s.fifo, err)
	}
	if s.cfg.PipeSize > 0 {
		if _, err := unix.FcntlInt(file.Fd(), unix.F_SETPIPE_SZ, s.cfg.PipeSize); err != nil {
			// Not fatal: the default pipe size still works, just with more latency.
		}
	}
	s.file = file
	s.closed.Store(false)
	s.done = make(chan struct{})

	s.wg.Add(1)
	go s.writer()

	return nil
}

func (s *Source) writer() {
	defer s.wg.Done()
	for {
		select {
		case <-s.done:
			return
		case frame := <-s.ch:
			if len(frame) == 0 {
				continue
			}
			s.writeFrame(frame)
		}
	}
}

func (s *Source) writeFrame(frame []byte) {
	s.mu.Lock()
	file := s.file
	s.mu.Unlock()
	if file == nil {
		return
	}

	for len(frame) > 0 {
		n, err := file.Write(frame)
		if n > 0 {
			s.written.Add(uint64(n))
			frame = frame[n:]
		}
		if err == nil {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			s.dropped.Add(1)
			return
		}
		s.dropped.Add(1)
		return
	}
}

// Write queues a PCM frame without blocking the network reader.
func (s *Source) Write(pcm []byte) {
	if s.closed.Load() || len(pcm) == 0 {
		return
	}
	frame := append([]byte(nil), pcm...)
	select {
	case s.ch <- frame:
	default:
		s.dropped.Add(1)
	}
}

func (s *Source) Stats() (written, dropped uint64, readers int32) {
	return s.written.Load(), s.dropped.Load(), s.readers.Load()
}

func (s *Source) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	s.mu.Lock()
	if s.file != nil {
		close(s.done)
		_ = s.file.Close()
		s.file = nil
	}
	moduleID := s.moduleID
	s.moduleID = ""
	s.mu.Unlock()

	s.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if moduleID != "" {
		_ = s.unload(ctx, moduleID)
	}
	_ = os.Remove(s.fifo)
	return nil
}

func (s *Source) unloadExisting(ctx context.Context) error {
	out, err := runPactl(ctx, "list", "modules", "short")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || fields[1] != "module-pipe-source" {
			continue
		}
		if hasSourceName(fields[2], s.cfg.SourceName) {
			if err := s.unload(ctx, fields[0]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Source) unload(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	_, err := runPactl(ctx, "unload-module", id)
	return err
}

func hasSourceName(args, name string) bool {
	for _, field := range strings.Fields(args) {
		if field == "source_name="+name || field == fmt.Sprintf(`source_name="%s"`, name) {
			return true
		}
	}
	return false
}

func runPactl(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pactl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("pactl %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func sanitizeProperty(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// Available reports whether a PulseAudio-compatible server is reachable.
func Available(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runPactl(ctx, "info")
	if err != nil {
		return err
	}
	if !strings.Contains(out, "Server Name:") {
		return errors.New("unexpected pactl info output")
	}
	return nil
}
