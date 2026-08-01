// Package link owns the trust and lifecycle of the gateway↔agent connection:
// self-signed cert generation and pinning, pairing codes, and reconnect
// backoff. TLS here is authenticated by fingerprint pinning plus a shared
// token — no CA involved.
package link

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"proxyforward/internal/secureio"
	"strings"
	"sync/atomic"
	"time"
)

// Fingerprint returns "sha256:<hex>" of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Pin returns the first n bytes of a certificate's fingerprint, in the same
// "sha256:<hex>" spelling — the form a pairing code carries and an agent stores
// until its first handshake teaches it the whole hash.
func Pin(der []byte, n int) string {
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:n])
}

// MinPinBytes is the shortest prefix of a certificate hash this code will trust,
// and it is a security parameter rather than a formatting choice. A pin is what
// stands in for the whole certificate, so an attacker who can produce a *second*
// certificate agreeing with the gateway's on every pinned bit owns the connection.
// The gateway generates its own certificate and nothing an attacker sends
// influences it, so that search is a fixed-target preimage — 2^(8n) trials, each
// one a P-256 keygen and a self-sign, not a bare hash. At 10 bytes that is 2^80,
// the same bar agentFingerprintBytes is held to and for the same reason. Below it
// pinMatches refuses outright: a short pin is not a weaker pin, it is a pin that
// can be forged, and accepting one would let a truncated pairing code downgrade
// the trust root from the outside.
const MinPinBytes = 10

// IsShortPin reports whether a fingerprint is a pin rather than the whole hash —
// which is to say, whether it came from a v2 pairing code or an agent that has not
// connected yet. Surfaces use it to tell an old code from a current one.
func IsShortPin(fingerprint string) bool {
	return len(fingerprint) < len("sha256:")+sha256.Size*2
}

// pinMatches reports whether a certificate's full fingerprint begins with the
// pinned prefix, in constant time. A full-length pin is the ordinary case and
// compares whole; a truncated one (from a v2 pairing code) compares over its own
// width, floored at MinPinBytes.
func pinMatches(full, pin string) bool {
	const scheme = "sha256:"
	full, pin = strings.ToLower(full), strings.ToLower(pin)
	if !strings.HasPrefix(full, scheme) || !strings.HasPrefix(pin, scheme) {
		return false
	}
	nibbles := len(pin) - len(scheme)
	if nibbles%2 != 0 || nibbles < MinPinBytes*2 || len(pin) > len(full) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(full[:len(pin)]), []byte(pin)) == 1
}

// LoadOrCreateCert returns the gateway's TLS certificate, generating a
// self-signed ECDSA P-256 cert on first run and persisting it under dir.
func LoadOrCreateCert(dir string) (tls.Certificate, string, error) {
	certPath := filepath.Join(dir, "gateway.crt")
	keyPath := filepath.Join(dir, "gateway.key")

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err == nil {
		return cert, Fingerprint(cert.Certificate[0]), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, "", fmt.Errorf("load gateway cert: %w", err)
	}

	certPEM, keyPEM, err := generateCert()
	if err != nil {
		return tls.Certificate{}, "", err
	}
	// secureio.MkdirAll, not os.MkdirAll: the 0600 on the key write below is a
	// no-op on Windows, so the directory's ACL is the only thing protecting the
	// gateway's private key — and an attacker holding it can impersonate the
	// gateway to every agent that pinned this cert.
	if err := secureio.MkdirAll(dir); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("create cert dir: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("write gateway key: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("write gateway cert: %w", err)
	}
	cert, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("parse generated cert: %w", err)
	}
	return cert, Fingerprint(cert.Certificate[0]), nil
}

func generateCert() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "proxyforward-gateway"},
		NotBefore:    time.Now().Add(-time.Hour),
		// Long validity is fine: trust comes from pinning, not expiry.
		NotAfter:              time.Now().AddDate(20, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create cert: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// GatewayTLSConfig is the listener-side TLS setup. CurvePreferences is left
// unset deliberately: Go's default key-exchange list leads with the
// X25519MLKEM768 post-quantum hybrid, so tunneled bytes get PQ confidentiality
// (against harvest-now-decrypt-later) for free. Do not pin it — that would
// silently drop the hybrid (asserted by pq_test.go).
func GatewayTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
}

// PinnedPeer is one agent's view of one gateway's identity: the pin it was paired
// with, and the whole fingerprint the first handshake proved. It exists because
// those are not always the same string — a v2 pairing code carries a prefix — and
// the agent must sign the *whole* one to prove possession to the gateway
// (AgentAuthMessage), which knows only its own full fingerprint.
//
// Learning is one-way and first-write-wins: once a certificate has been accepted,
// every later handshake must present that exact certificate, not merely one
// agreeing with the pin. So the prefix is the trust root for a single moment — the
// pairing act — and the whole hash for every moment after it, within the process
// and (once persisted) beyond it.
type PinnedPeer struct {
	pin  string
	full atomic.Pointer[string]
}

// NewPinnedPeer starts from the pin a pairing code delivered, which may be the
// whole fingerprint (a v1 code, or an agent that has already connected once) or a
// prefix of it (a v2 code, before first contact).
func NewPinnedPeer(pin string) *PinnedPeer { return &PinnedPeer{pin: pin} }

// Fingerprint is the whole fingerprint a handshake proved, or "" before any has.
func (p *PinnedPeer) Fingerprint() string {
	if s := p.full.Load(); s != nil {
		return *s
	}
	return ""
}

func (p *PinnedPeer) verify(rawCerts [][]byte) error {
	if len(rawCerts) == 0 {
		return errors.New("gateway presented no certificate")
	}
	got := Fingerprint(rawCerts[0])
	if known := p.Fingerprint(); known != "" {
		if !pinMatches(got, known) {
			return fmt.Errorf("gateway presented a different certificate than the one this session pinned: expected %s, got %s", known, got)
		}
		return nil
	}
	if !pinMatches(got, p.pin) {
		return fmt.Errorf("gateway certificate does not match the pinned one: expected %s, got %s (re-pair if the gateway was reinstalled)", p.pin, got)
	}
	// Two first handshakes can race; whoever loses re-checks against the winner's
	// certificate rather than assuming its own was the one that landed.
	if !p.full.CompareAndSwap(nil, &got) && !pinMatches(got, p.Fingerprint()) {
		return fmt.Errorf("gateway presented a different certificate than the one this session pinned: expected %s, got %s", p.Fingerprint(), got)
	}
	return nil
}

// TLSConfig trusts exactly one certificate: the one whose SHA-256 fingerprint
// begins with the pin delivered out-of-band in the pairing code. Standard chain
// verification is disabled (self-signed) and replaced by the pin.
// CurvePreferences is left unset so the X25519MLKEM768 PQ hybrid is negotiated
// (see GatewayTLSConfig; asserted by pq_test.go).
func (p *PinnedPeer) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:            tls.VersionTLS13,
		InsecureSkipVerify:    true, // verification happens against the pin below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error { return p.verify(rawCerts) },
	}
}

// AgentTLSConfig is the pin check for a caller that has no use for the learned
// fingerprint — tests, and any dial whose identity is already settled.
func AgentTLSConfig(pin string) *tls.Config { return NewPinnedPeer(pin).TLSConfig() }
