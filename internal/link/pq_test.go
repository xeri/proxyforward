package link

import (
	"crypto/tls"
	"net"
	"testing"
	"time"
)

// TestControlHandshakeUsesMLKEM proves the gateway↔agent TLS handshake
// negotiates the post-quantum hybrid key exchange (X25519MLKEM768) by default.
// Key exchange is the half of PQ that a harvest-now-decrypt-later attacker
// targets — the confidentiality of tunneled bytes — so it is the urgent half.
// It costs nothing here because neither TLS config pins CurvePreferences, so
// Go's default (which includes the hybrid on 1.25+) applies. The per-conn data
// connections reuse these exact configs, so this covers them too. (The ECDSA
// identity — the authentication half — is deliberately left classical: forging
// a signature needs a quantum computer at attack time, so there is no
// retroactive risk and it is safe to migrate later.)
func TestControlHandshakeUsesMLKEM(t *testing.T) {
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

	serverCurve := make(chan tls.CurveID, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverCurve <- 0
			return
		}
		defer c.Close()
		tc := c.(*tls.Conn)
		tc.SetDeadline(time.Now().Add(5 * time.Second))
		if err := tc.Handshake(); err != nil {
			serverCurve <- 0
			return
		}
		serverCurve <- tc.ConnectionState().CurveID
	}()

	raw, err := (&net.Dialer{Timeout: 5 * time.Second}).Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tc := tls.Client(raw, AgentTLSConfig(fp))
	defer tc.Close()
	tc.SetDeadline(time.Now().Add(5 * time.Second))
	if err := tc.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}

	if got := tc.ConnectionState().CurveID; got != tls.X25519MLKEM768 {
		t.Fatalf("client negotiated key exchange = %v, want X25519MLKEM768 (did someone pin CurvePreferences?)", got)
	}
	select {
	case got := <-serverCurve:
		if got != tls.X25519MLKEM768 {
			t.Fatalf("server negotiated key exchange = %v, want X25519MLKEM768", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server handshake did not complete")
	}
}

// TestNoCurvePreferencesPinned guards the free-PQ property: pinning
// CurvePreferences on either config would silently drop the X25519MLKEM768
// hybrid negotiated above. Leave both nil.
func TestNoCurvePreferencesPinned(t *testing.T) {
	cert, _, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cp := GatewayTLSConfig(cert).CurvePreferences; cp != nil {
		t.Fatalf("GatewayTLSConfig pins CurvePreferences=%v; leaving it nil keeps the PQ hybrid", cp)
	}
	if cp := AgentTLSConfig("sha256:pin").CurvePreferences; cp != nil {
		t.Fatalf("AgentTLSConfig pins CurvePreferences=%v; leaving it nil keeps the PQ hybrid", cp)
	}
}
