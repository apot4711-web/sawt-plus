package config

import "testing"

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
