package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk configuration for the sawt daemon.
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
	if c.Audio.Gain <= 0 {
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
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	return os.WriteFile(Path(), []byte(b.String()), 0o600)
}

func (c *Config) FrameBytes() int {
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
