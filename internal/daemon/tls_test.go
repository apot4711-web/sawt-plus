package daemon

import (
	"crypto/tls"
	"crypto/x509"
	"strings"
	"testing"
)

func TestValidFingerprint(t *testing.T) {
	good := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if !validFingerprint(good) {
		t.Fatalf("valid fingerprint rejected: %q", good)
	}
	for _, bad := range []string{
		"",
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85", // 63 chars
		good + "0", // 65 chars
		"E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855", // uppercase
		strings.Repeat("g", 64), // non-hex
		strings.Repeat(" ", 64),
	} {
		if validFingerprint(bad) {
			t.Fatalf("invalid fingerprint accepted: %q", bad)
		}
	}
}

func TestCertFingerprint(t *testing.T) {
	// SHA-256 of the empty string (standard test vector).
	if got := certFingerprint(nil); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("certFingerprint(nil) = %q", got)
	}
	if got := certFingerprint([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("certFingerprint(abc) = %q", got)
	}
}

func TestPinningTLSConfig(t *testing.T) {
	pin := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)

	mkState := func(raw []byte) tls.ConnectionState {
		return tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Raw: raw}}}
	}
	// certFingerprint([]byte("x")) is the "presented" fingerprint.
	presented := certFingerprint([]byte("x"))

	t.Run("no peer certificates", func(t *testing.T) {
		cfg := pinningTLSConfig("", nil)
		if err := cfg.VerifyConnection(tls.ConnectionState{}); err == nil {
			t.Fatal("want error for empty peer certificates")
		}
	})

	t.Run("TOFU accepts and observes", func(t *testing.T) {
		var observed string
		cfg := pinningTLSConfig("", &observed)
		if err := cfg.VerifyConnection(mkState([]byte("x"))); err != nil {
			t.Fatalf("TOFU: %v", err)
		}
		if observed != presented {
			t.Fatalf("observed = %q, want %q", observed, presented)
		}
	})

	t.Run("matching pin", func(t *testing.T) {
		cfg := pinningTLSConfig(presented, nil)
		if err := cfg.VerifyConnection(mkState([]byte("x"))); err != nil {
			t.Fatalf("matching pin: %v", err)
		}
	})

	t.Run("mismatched pin fails closed", func(t *testing.T) {
		cfg := pinningTLSConfig(other, nil)
		err := cfg.VerifyConnection(mkState([]byte("x")))
		if err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
			t.Fatalf("want fingerprint mismatch error, got %v", err)
		}
	})

	t.Run("min TLS 1.2, no system roots", func(t *testing.T) {
		cfg := pinningTLSConfig(pin, nil)
		if cfg.MinVersion != tls.VersionTLS12 {
			t.Fatalf("MinVersion = %x, want TLS 1.2", cfg.MinVersion)
		}
		if !cfg.InsecureSkipVerify {
			t.Fatal("InsecureSkipVerify must be true (VerifyConnection is the only check)")
		}
	})
}

func TestCheckAdvertisedFP(t *testing.T) {
	fp := strings.Repeat("a", 64)
	if err := checkAdvertisedFP("", fp); err != nil {
		t.Fatalf("empty advertised: %v", err)
	}
	if err := checkAdvertisedFP(fp, ""); err != nil {
		t.Fatalf("empty observed: %v", err)
	}
	if err := checkAdvertisedFP("bogus", fp); err != nil {
		t.Fatalf("malformed advertised should be ignored: %v", err)
	}
	if err := checkAdvertisedFP(fp, fp); err != nil {
		t.Fatalf("matching: %v", err)
	}
	if err := checkAdvertisedFP(fp, strings.Repeat("b", 64)); err == nil {
		t.Fatal("contradictory advertisement should fail closed")
	}
}
