package link

import (
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPairingRoundTrip(t *testing.T) {
	cases := []PairingCode{
		{Host: "gw.example.com", Port: 8474, Token: "abc123", Fingerprint: "sha256:" + strings.Repeat("ab", 32)},
		{Host: "203.0.113.7", Port: 443, Token: "t0k", Fingerprint: "sha256:" + strings.Repeat("0f", 32)},
		{Host: "2001:db8::1", Port: 8474, Token: "v6token", Fingerprint: "sha256:" + strings.Repeat("9c", 32)},
	}
	for _, in := range cases {
		t.Run(in.Host, func(t *testing.T) {
			s := in.String()
			got, err := ParsePairingCode(s)
			if err != nil {
				t.Fatalf("parse %q: %v", s, err)
			}
			if got != in {
				t.Fatalf("round trip: got %+v want %+v", got, in)
			}
		})
	}
}

func TestPairingParseWhitespaceTolerant(t *testing.T) {
	in := PairingCode{Host: "gw", Port: 1, Token: "t", Fingerprint: "sha256:" + strings.Repeat("aa", 32)}
	if _, err := ParsePairingCode("  " + in.String() + "\r\n"); err != nil {
		t.Fatalf("should tolerate copy-paste whitespace: %v", err)
	}
}

func TestPairingParseRejects(t *testing.T) {
	fp := "sha256:" + strings.Repeat("ab", 32)
	bad := map[string]string{
		"wrong scheme":    "https://gw:8474/tok#" + fp,
		"no host":         "pf1://:8474/tok#" + fp,
		"no port":         "pf1://gw/tok#" + fp,
		"bad port":        "pf1://gw:99999/tok#" + fp,
		"no token":        "pf1://gw:8474/#" + fp,
		"no fingerprint":  "pf1://gw:8474/tok",
		"short fp":        "pf1://gw:8474/tok#sha256:abcd",
		"non-hex fp":      "pf1://gw:8474/tok#sha256:" + strings.Repeat("zz", 32),
		"md5 fingerprint": "pf1://gw:8474/tok#md5:" + strings.Repeat("ab", 32),
	}
	for name, s := range bad {
		if _, err := ParsePairingCode(s); err == nil {
			t.Errorf("%s: expected error for %q", name, s)
		}
	}
}

func TestLoadOrCreateCertPersists(t *testing.T) {
	dir := t.TempDir()
	_, fp1, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fp1, "sha256:") {
		t.Fatalf("fingerprint format: %q", fp1)
	}
	_, fp2, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatalf("cert must persist across loads: %q != %q", fp1, fp2)
	}
	_, fp3, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if fp3 == fp1 {
		t.Fatal("different dirs must generate different certs")
	}
}

// TestPinnedTLSHandshake proves an agent connects when the pin matches and
// refuses when it doesn't.
func TestPinnedTLSHandshake(t *testing.T) {
	dir := t.TempDir()
	cert, fp, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", GatewayTLSConfig(cert))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				c.Read(make([]byte, 1)) // drive the handshake
				c.Close()
			}(c)
		}
	}()

	dial := func(pin string) error {
		d := net.Dialer{Timeout: 5 * time.Second}
		raw, err := d.Dial("tcp", ln.Addr().String())
		if err != nil {
			return err
		}
		defer raw.Close()
		tc := tls.Client(raw, AgentTLSConfig(pin))
		defer tc.Close()
		tc.SetDeadline(time.Now().Add(5 * time.Second))
		return tc.Handshake()
	}

	if err := dial(fp); err != nil {
		t.Fatalf("handshake with correct pin failed: %v", err)
	}
	wrong := "sha256:" + strings.Repeat("00", 32)
	if err := dial(wrong); err == nil {
		t.Fatal("handshake with wrong pin must fail")
	} else if !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("expected fingerprint mismatch error, got: %v", err)
	}
}

func TestBackoffGrowsAndResets(t *testing.T) {
	b := &Backoff{Base: time.Second, Max: 30 * time.Second, StableAfter: time.Minute}
	seen := make([]time.Duration, 6)
	for i := range seen {
		seen[i] = b.Next()
	}
	for i, d := range seen {
		ceil := time.Second << i
		if ceil > 30*time.Second {
			ceil = 30 * time.Second
		}
		if d < 1 || d > ceil {
			t.Errorf("attempt %d: delay %v outside (0, %v]", i, d, ceil)
		}
	}
	// A short-lived connection must not reset the sequence.
	b.ConnectionEnded(2 * time.Second)
	if d := b.Next(); d > 30*time.Second {
		t.Errorf("delay after flap: %v", d)
	}
	// A stable connection resets to base.
	b.ConnectionEnded(2 * time.Minute)
	for i := 0; i < 20; i++ {
		b2 := *b
		if d := b2.Next(); d > time.Second {
			t.Fatalf("after stable reset, first delay should be <= base: %v", d)
		}
	}
}
