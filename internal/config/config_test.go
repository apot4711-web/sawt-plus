package config

import (
	"errors"
	"os"
	"testing"
)

func TestNormalizeAddress(t *testing.T) {
	cases := map[string]string{
		"192.168.1.2":              "192.168.1.2:8765",
		"192.168.1.2:9000":         "192.168.1.2:9000",
		"ws://192.168.1.2:99/":     "192.168.1.2:99",
		"192.168.1.2 : 8765":       "192.168.1.2:8765",
		" 192.168.1.2:8765 ":       "192.168.1.2:8765",
		"ws:// 192.168.1.2 : 99 /": "192.168.1.2:99",
		"  ":                       "",
	}
	for in, want := range cases {
		if got := NormalizeAddress(in); got != want {
			t.Errorf("NormalizeAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseGain(t *testing.T) {
	cases := map[string]float64{
		"1.5x": 1.5,
		"1.5":  1.5,
		"150%": 1.5,
		"0":    0,
		"8x":   8,
	}
	for in, want := range cases {
		got, err := ParseGain(in)
		if err != nil || got != want {
			t.Errorf("ParseGain(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "loud", "-1", "900%"} {
		if _, err := ParseGain(in); err == nil {
			t.Errorf("ParseGain(%q) should fail", in)
		}
	}
}

func TestRemoveDevice(t *testing.T) {
	cfg := &Config{Devices: []DeviceEntry{
		{ID: "a", Name: "Phone A", Address: "192.168.1.10:8765", Token: "t1"},
		{ID: "b", Name: "Phone B", Address: "192.168.1.11:8765", Token: "t2"},
	}}
	if !cfg.RemoveDevice("192.168.1.10:8765") {
		t.Fatal("RemoveDevice returned false")
	}
	if len(cfg.Devices) != 1 || cfg.Devices[0].ID != "b" {
		t.Fatalf("unexpected devices after remove: %+v", cfg.Devices)
	}
	if cfg.RemoveDevice("missing") {
		t.Fatal("RemoveDevice(missing) returned true")
	}
}

func TestLoadAPITokenCreatesPrivateFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	token, err := LoadAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 32 {
		t.Fatalf("token too short: %q", token)
	}
	info, err := os.Stat(APITokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %o, want 600", perm)
	}
	again, err := LoadAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if again != token {
		t.Fatal("token must be stable across loads")
	}
}

func TestAcquireDaemonLock(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	first, err := AcquireDaemonLock()
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer first.Close()

	if _, err := AcquireDaemonLock(); !errors.Is(err, ErrDaemonRunning) {
		t.Fatalf("second lock = %v, want ErrDaemonRunning", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := AcquireDaemonLock()
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = again.Close()
}

func TestGainZeroIsPreserved(t *testing.T) {
	cfg := Default()
	cfg.Audio.Gain = 0
	cfg.Normalize()
	if cfg.Audio.Gain != 0 {
		t.Fatalf("gain = %v, want 0", cfg.Audio.Gain)
	}
}

func TestTokenFilePermissionsTightened(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	token := "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(APITokenPath(), []byte(token+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAPIToken()
	if err != nil || got != token {
		t.Fatalf("LoadAPIToken() = %q, %v", got, err)
	}
	info, err := os.Stat(APITokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %o, want 600", info.Mode().Perm())
	}
}
