package daemon

import (
	"strings"
	"testing"
	"time"
)

// Pairing-throttle transition tests (pure logic, no I/O).

func TestPairWaitNoState(t *testing.T) {
	d := &Daemon{}
	wait, err := d.pairWait("192.168.1.10:9000")
	if err != nil || wait != 0 {
		t.Fatalf("fresh target: want (0, nil), got (%v, %v)", wait, err)
	}
}

func TestPairBackoffGrows(t *testing.T) {
	d := &Daemon{}
	addr := "192.168.1.10:9000"
	var prev time.Duration
	for i := 0; i < 3; i++ {
		d.pairNote(addr, false, 0)
		wait, err := d.pairWait(addr)
		if err != nil {
			t.Fatalf("attempt %d: unexpected error: %v", i+1, err)
		}
		if wait <= 0 {
			t.Fatalf("attempt %d: want positive backoff, got %v", i+1, wait)
		}
		if i > 0 && wait <= prev {
			t.Fatalf("backoff not growing: %v then %v", prev, wait)
		}
		prev = wait
	}
	// First failure → base backoff (2s); allow scheduling slack on the low side.
	if prev < pairBaseBackoff || prev > 4*pairBaseBackoff {
		t.Fatalf("3rd failure backoff = %v, want ~%v", prev, 4*pairBaseBackoff)
	}
}

func TestPairLockoutAfterMaxFails(t *testing.T) {
	d := &Daemon{}
	addr := "192.168.1.10:9000"
	for i := 0; i < pairMaxFails; i++ {
		d.pairNote(addr, false, 0)
	}
	_, err := d.pairWait(addr)
	if err == nil || !strings.Contains(err.Error(), "too many failed pairing attempts") {
		t.Fatalf("want lockout error, got %v", err)
	}
}

func TestPairSuccessClearsThrottle(t *testing.T) {
	d := &Daemon{}
	addr := "192.168.1.10:9000"
	d.pairNote(addr, false, 0)
	d.pairNote(addr, true, 0)
	wait, err := d.pairWait(addr)
	if err != nil || wait != 0 {
		t.Fatalf("after success: want (0, nil), got (%v, %v)", wait, err)
	}
}

func TestPairRetryAfterExtends(t *testing.T) {
	d := &Daemon{}
	addr := "192.168.1.10:9000"
	d.pairNote(addr, false, 10*time.Minute)
	wait, err := d.pairWait(addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wait < 9*time.Minute {
		t.Fatalf("retry_after hint not honored: wait = %v", wait)
	}
}

func TestPairThrottlePerAddress(t *testing.T) {
	d := &Daemon{}
	for i := 0; i < pairMaxFails; i++ {
		d.pairNote("192.168.1.10:9000", false, 0)
	}
	wait, err := d.pairWait("192.168.1.11:9000")
	if err != nil || wait != 0 {
		t.Fatalf("other address must be unaffected, got (%v, %v)", wait, err)
	}
}
