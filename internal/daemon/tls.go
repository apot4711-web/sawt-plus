package daemon

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	"github.com/apot4711-web/sawt-plus/internal/discovery"
)

// This file implements the PC side of the wss:// transport. The phone serves
// a self-signed certificate that is NOT anchored to any CA, so the system
// trust store is never used. Instead the client pins the certificate's
// SHA-256 fingerprint (TOFU): the fingerprint is stored alongside the
// pairing token at pairing time, and every later connect aborts before any
// token is sent when the presented certificate does not match.

// certFingerprint returns the SHA-256 fingerprint (lowercase hex) of a
// DER-encoded X.509 certificate, matching the phone's cert_sha256.
func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// validFingerprint reports whether s looks like a SHA-256 fingerprint:
// exactly 64 lowercase hex characters.
func validFingerprint(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// pinningTLSConfig returns a TLS client config that pins the server
// certificate by fingerprint. InsecureSkipVerify disables the default chain
// verification against system roots; VerifyConnection is the ONLY check, so
// there is no fallback to system trust. If expectFP is empty the presented
// certificate is accepted and its fingerprint recorded in *observedFP (TOFU
// at pairing time); otherwise a mismatch aborts the handshake.
func pinningTLSConfig(expectFP string, observedFP *string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("tls: server presented no certificate")
			}
			fp := certFingerprint(cs.PeerCertificates[0].Raw)
			if observedFP != nil {
				*observedFP = fp
			}
			if expectFP == "" {
				return nil
			}
			if subtle.ConstantTimeCompare([]byte(fp), []byte(expectFP)) != 1 {
				return fmt.Errorf("tls: certificate fingerprint mismatch "+
					"(expected %.12s…, got %.12s…): the phone's certificate changed — "+
					"re-pair with a new 6-digit code", expectFP, fp)
			}
			return nil
		},
	}
}

// checkAdvertisedFP fails closed when the server contradicts the pinned
// fingerprint inside its own JSON control messages (defense in depth; the
// TLS pin established above is authoritative).
func checkAdvertisedFP(advertised, observed string) error {
	if advertised == "" || observed == "" {
		return nil
	}
	if !validFingerprint(advertised) {
		return nil // ignore malformed; the TLS pin is authoritative
	}
	if subtle.ConstantTimeCompare([]byte(advertised), []byte(observed)) != 1 {
		return fmt.Errorf("tls: server advertised cert_sha256 %.12s… but presented %.12s…",
			advertised, observed)
	}
	return nil
}

// dialWSS dials wss://addr/ with certificate pinning and returns the
// connection plus the fingerprint of the presented leaf certificate.
// expectFP may be "" for TOFU (pairing); otherwise a mismatch aborts before
// any application data is sent.
func (d *Daemon) dialWSS(ctx context.Context, addr, expectFP string) (*websocket.Conn, string, error) {
	var observed string
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig:  pinningTLSConfig(expectFP, &observed),
	}
	ws, _, err := dialer.DialContext(ctx, "wss://"+addr+"/", nil)
	if err != nil {
		return nil, "", err
	}
	return ws, observed, nil
}

// mdnsFingerprintHint returns the "sha256" TXT attribute advertised for addr
// via mDNS, or "" when unavailable or malformed. It is only a TOFU hint:
// mDNS is unauthenticated, so the 6-digit pairing code remains the real
// authentication at pairing time.
func (d *Daemon) mdnsFingerprintHint(ctx context.Context, addr string) string {
	devices, err := discovery.BrowseMDNS(ctx, 2*time.Second)
	if err != nil && len(devices) == 0 {
		return ""
	}
	for _, dev := range devices {
		if dev.Address == addr && validFingerprint(dev.CertSHA256) {
			return dev.CertSHA256
		}
	}
	return ""
}
