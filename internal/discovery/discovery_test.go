package discovery

import "testing"

func TestParseAvahi(t *testing.T) {
	out := `+;wlan0;IPv4;Sawt-abc;_sawtplus._tcp;local
=;wlan0;IPv4;Sawt\032Phone;_sawtplus._tcp;local;phone.local;192.168.1.5;8765;"id=abc" "name=Sawt Phone" "v=1" "pair=1"
=;docker0;IPv6;Other;_sawtplus._tcp;local;other.local;fe80::1;8766
`
	devices := parseAvahi(out)
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2: %+v", len(devices), devices)
	}
	d := devices[0]
	if d.ID != "abc" || d.Address != "192.168.1.5:8765" || d.Port != 8765 {
		t.Errorf("unexpected device: %+v", d)
	}
	if d.Name != "Sawt Phone" || d.Version != "1" || !d.Pairing {
		t.Errorf("unexpected TXT fields: %+v", d)
	}
	if devices[1].ID != "[fe80::1]:8766" {
		t.Errorf("address-only device id = %q", devices[1].ID)
	}
}

func TestMergeDevice(t *testing.T) {
	byID := make(map[string]Device)
	mergeDevice(byID, Device{ID: "abc", Name: "scan", Address: "192.168.1.5:8765", Version: "scan"})
	mergeDevice(byID, Device{ID: "abc", Name: "Phone", Host: "phone.local", Address: "192.168.1.5:8765", Version: "1", Pairing: true})
	d := byID["abc"]
	if d.Name != "Phone" || d.Version != "1" || !d.Pairing {
		t.Errorf("merge lost info: %+v", d)
	}
}
