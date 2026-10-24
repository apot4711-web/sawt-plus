package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
	"golang.org/x/sys/unix"
)

// Config is the on-disk configuration for the sawt daemon.
// ErrDaemonRunning is returned when another sawt daemon already holds the
// single-instance lock.
var ErrDaemonRunning = errors.New("another sawt daemon is already running")

// DaemonLock is the process-wide single-instance lock held by the daemon.
type DaemonLock struct {
	f *os.File
}

// AcquireDaemonLock takes the daemon lock without blocking. The lock is
// released when the returned DaemonLock is closed or the process exits.
func AcquireDaemonLock() (*DaemonLock, error) {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	path := filepath.Join(Dir(), "daemon.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrDaemonRunning
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return &DaemonLock{f: f}, nil
}

// Close releases the daemon lock.
func (l *DaemonLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}

type Config struct {
	Audio   AudioConfig   `toml:"audio"`
	Daemon  DaemonConfig  `toml:"daemon"`
	UI      UIConfig      `toml:"ui"`
	Devices []DeviceEntry `toml:"devices"`
}

type AudioConfig struct {
	Rate        int     `toml:"rate"`
	Channels    int     `toml:"channels"`
	FrameMS     int     `toml:"frame_ms"`
	Gain        float64 `toml:"gain"`
	Mute        bool    `toml:"mute"`
	KeepSource  bool    `toml:"keep_source"`
	SourceName  string  `toml:"source_name"`
	Description string  `toml:"description"`
	PipeSize    int     `toml:"pipe_size"`
}

type DaemonConfig struct {
	WebAddr     string `toml:"web_addr"`
	AutoConnect bool   `toml:"auto_connect"`
	Reconnect   bool   `toml:"reconnect"`
}

type UIConfig struct {
	Theme string `toml:"theme"`
}

type DeviceEntry struct {
	ID      string `toml:"id,omitempty"`
	Name    string `toml:"name,omitempty"`
	Address string `toml:"address"`
	Token   string `toml:"token,omitempty"`
	// CertSHA256 pins the phone's TLS certificate (SHA-256 of the leaf
	// cert DER, lowercase hex). Stored at pairing time (TOFU); every later
	// connect aborts on mismatch.
	CertSHA256 string `toml:"cert_sha256,omitempty"`
}

func Default() *Config {
	return &Config{
		Audio: AudioConfig{
			Rate:        48000,
			Channels:    1,
			FrameMS:     20,
			Gain:        1.0,
			KeepSource:  true,
			SourceName:  "sawt-plus",
			Description: "Android Microphone",
			PipeSize:    8192,
		},
		Daemon: DaemonConfig{
			WebAddr:     "127.0.0.1:8787",
			AutoConnect: true,
			Reconnect:   true,
		},
		UI: UIConfig{Theme: "auto"},
	}
}

func Dir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "sawt-plus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".sawt"
	}
	return filepath.Join(home, ".config", "sawt-plus")
}

func Path() string {
	return filepath.Join(Dir(), "config.toml")
}

// APITokenPath is the file that holds the local API token. It is kept apart
// from config.toml so the token can never leak through /api/config.
func APITokenPath() string {
	return filepath.Join(Dir(), "api.token")
}

// LoadAPIToken returns the local API token, creating it atomically on first
// use. The token is written with mode 0600 inside the private config directory.
func LoadAPIToken() (string, error) {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	path := APITokenPath()
	for attempt := 0; attempt < 6; attempt++ {
		data, err := os.ReadFile(path)
		if err == nil {
			token := strings.TrimSpace(string(data))
			if len(token) >= 32 {
				// Legacy files may be world-readable; tighten them on load.
				_ = os.Chmod(path, 0o600)
				return token, nil
			}
			// Another process may be in the middle of writing the file. After
			// several attempts treat it as corrupt and regenerate it.
			if attempt >= 4 {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return "", fmt.Errorf("remove invalid %s: %w", path, err)
				}
				continue
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read %s: %w", path, err)
		}

		token, err := newAPIToken()
		if err != nil {
			return "", err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", fmt.Errorf("create %s: %w", path, err)
		}
		_, werr := f.WriteString(token + "\n")
		serr := f.Sync()
		cerr := f.Close()
		if werr != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("write %s: %w", path, werr)
		}
		if serr != nil {
			return "", fmt.Errorf("sync %s: %w", path, serr)
		}
		if cerr != nil {
			return "", fmt.Errorf("close %s: %w", path, cerr)
		}
		return token, nil
	}
	return "", fmt.Errorf("could not initialize %s (delete it and retry)", path)
}

func newAPIToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate API token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func RuntimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "sawt-plus")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("sawt-plus-%d", os.Getuid()))
}

func Load() (*Config, error) {
	path := Path()
	cfg := Default()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg.normalize()
	return cfg, nil
}

// Normalize fills in defaults for zero values so callers outside this package
// can validate an incoming configuration before saving it.
func (c *Config) Normalize() { c.normalize() }

func (c *Config) normalize() {
	def := Default()
	if c.Audio.Rate <= 0 {
		c.Audio.Rate = def.Audio.Rate
	}
	if c.Audio.Channels <= 0 {
		c.Audio.Channels = def.Audio.Channels
	}
	if c.Audio.FrameMS <= 0 {
		c.Audio.FrameMS = def.Audio.FrameMS
	}
	if c.Audio.Gain < 0 {
		c.Audio.Gain = def.Audio.Gain
	}
	if c.Audio.SourceName == "" {
		c.Audio.SourceName = def.Audio.SourceName
	}
	if c.Audio.Description == "" {
		c.Audio.Description = def.Audio.Description
	}
	if c.Audio.PipeSize <= 0 {
		c.Audio.PipeSize = def.Audio.PipeSize
	}
	if c.Daemon.WebAddr == "" {
		c.Daemon.WebAddr = def.Daemon.WebAddr
	}
	if c.UI.Theme == "" {
		c.UI.Theme = def.UI.Theme
	}
}

func (c *Config) Save() error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var b strings.Builder
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	// Write to a private temporary file and rename it into place so a crash or
	// power loss cannot leave a truncated config.toml (and lose pairing tokens).
	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

func (c Config) FrameBytes() int {
	return c.Audio.Rate * c.Audio.FrameMS / 1000 * c.Audio.Channels * 2
}

func (c *Config) FindDevice(key string) (DeviceEntry, bool) {
	for _, d := range c.Devices {
		if d.ID == key || d.Name == key || d.Address == key {
			return d, true
		}
	}
	return DeviceEntry{}, false
}

func (c *Config) UpsertDevice(d DeviceEntry) {
	for i := range c.Devices {
		if (d.ID != "" && c.Devices[i].ID == d.ID) || (d.Address != "" && c.Devices[i].Address == d.Address) {
			if d.Name != "" {
				c.Devices[i].Name = d.Name
			}
			if d.Address != "" {
				c.Devices[i].Address = d.Address
			}
			if d.Token != "" {
				c.Devices[i].Token = d.Token
			}
			if d.CertSHA256 != "" {
				c.Devices[i].CertSHA256 = d.CertSHA256
			}
			return
		}
	}
	c.Devices = append(c.Devices, d)
}

// RemoveDevice deletes every saved entry matching the key (id, name, or
// address) and reports whether anything was removed.
func (c *Config) RemoveDevice(key string) bool {
	kept := c.Devices[:0]
	removed := false
	for _, d := range c.Devices {
		if d.ID == key || d.Name == key || d.Address == key {
			removed = true
			continue
		}
		kept = append(kept, d)
	}
	c.Devices = kept
	return removed
}

func NormalizeAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "ws://")
	addr = strings.TrimPrefix(addr, "wss://")
	// Users often paste addresses with spaces around the colon or between the
	// host and port; ignore all whitespace before parsing.
	addr = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, addr)
	addr = strings.TrimSuffix(addr, "/")
	if addr == "" {
		return addr
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		if strings.Count(addr, ":") == 0 {
			addr = net.JoinHostPort(addr, "8765")
		}
	}
	return addr
}

// ParseGain parses "1.5x", "1.5", or "150%" as a gain multiplier in the
// range 0..8.
func ParseGain(value string) (float64, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return 0, fmt.Errorf("gain is empty")
	}
	percent := strings.HasSuffix(value, "%")
	value = strings.TrimSuffix(strings.TrimSuffix(value, "%"), "x")
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid gain %q", value)
	}
	if percent {
		n /= 100
	}
	if n < 0 || n > 8 {
		return 0, fmt.Errorf("gain must be between 0 and 8 (0%%..800%%)")
	}
	return n, nil
}
