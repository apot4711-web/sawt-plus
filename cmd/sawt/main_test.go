package main

import "testing"

func TestParsePairURI(t *testing.T) {
	target, code, err := parsePairURI("sawtplus://pair?host=192.168.1.5&port=9000&code=123456")
	if err != nil {
		t.Fatal(err)
	}
	if target != "192.168.1.5:9000" || code != "123456" {
		t.Fatalf("target=%q code=%q", target, code)
	}
}

func TestParsePairURIDefaultPort(t *testing.T) {
	target, _, err := parsePairURI("sawtplus://pair?host=phone.local&code=000001")
	if err != nil {
		t.Fatal(err)
	}
	if target != "phone.local:8765" {
		t.Fatalf("target = %q", target)
	}
}

func TestParsePairURIRejectsOtherSchemes(t *testing.T) {
	if _, _, err := parsePairURI("https://example.com/pair?host=x&code=1"); err == nil {
		t.Fatal("expected an error")
	}
}
