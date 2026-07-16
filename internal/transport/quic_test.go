package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"go.uber.org/goleak"

	"proxyforward/internal/link"
	"proxyforward/internal/stats"
)

// goleak guards that a fully-closed QUIC listener/dial leaves no lingering
// quic-go goroutines — the same contract the e2e suite enforces, checked here at
// the source so a transport-level leak fails fast.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type tlsStater interface {
	TLSConnectionState() tls.ConnectionState
}

// quicLoopback stands up a real QUIC server+client over loopback UDP using the
// production TLS configs (pinned self-signed cert), completes the handshake, and
// returns both sessions plus the client's link-byte counter. Everything is torn
// down via t.Cleanup.
func quicLoopback(t *testing.T) (server, client Session, clientTotals *stats.LinkCounters) {
	t.Helper()
	cert, fp, err := link.LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatalf("cert: %v", err)
	}

	spc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("server udp: %v", err)
	}
	ln, err := ListenQUIC(spc, link.GatewayTLSConfig(cert), nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	cpc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("client udp: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientTotals = &stats.LinkCounters{}
	type dialRes struct {
		s   Session
		err error
	}
	dialed := make(chan dialRes, 1)
	go func() {
		s, err := DialQUIC(ctx, cpc, ln.Addr().String(), link.AgentTLSConfig(fp), clientTotals, nil)
		dialed <- dialRes{s, err}
	}()

	server, err = ln.Accept(ctx)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	dr := <-dialed
	if dr.err != nil {
		t.Fatalf("dial: %v", dr.err)
	}
	client = dr.s

	t.Cleanup(func() {
		client.Close() // closes conn + client transport + client socket
		server.Close() // closes the accepted conn
		ln.Close()     // closes listener + shared transport + server socket
	})
	return server, client, clientTotals
}

// TestQUICHandshakeUsesMLKEM guards that carrying the production TLS configs over
// QUIC (with withALPN injecting ALPN) still negotiates the X25519MLKEM768 PQ
// hybrid on both sides — i.e. withALPN did not clobber CurvePreferences. The
// link package can't host this (it must not import quic-go), so it lives here.
func TestQUICHandshakeUsesMLKEM(t *testing.T) {
	server, client, _ := quicLoopback(t)
	for _, tc := range []struct {
		name string
		sess Session
	}{{"server", server}, {"client", client}} {
		st, ok := tc.sess.(tlsStater)
		if !ok {
			t.Fatalf("%s: session does not expose TLS state", tc.name)
		}
		if got := st.TLSConnectionState().CurveID; got != tls.X25519MLKEM768 {
			t.Errorf("%s: CurveID = %v, want X25519MLKEM768 (PQ hybrid dropped)", tc.name, got)
		}
	}
}

// TestQUICHalfClose proves quic-go's stream Close matches yamux CloseWrite: a
// CloseWrite surfaces io.EOF to the peer's reader while the reverse direction
// keeps carrying bytes. This is the invariant the splice's FIN propagation relies
// on (relay.Splice / TestFinalBytesThroughTunnel).
func TestQUICHalfClose(t *testing.T) {
	server, client, clientTotals := quicLoopback(t)

	cs, err := client.OpenStream()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// The server accepts a stream only once the client has sent on it.
	if _, err := cs.Write([]byte("ping")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	ss, err := server.AcceptStream()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(ss, buf); err != nil {
		t.Fatalf("server read: %v", err)
	}
	if !bytes.Equal(buf, []byte("ping")) {
		t.Fatalf("server got %q, want ping", buf)
	}

	// Client half-closes its send side; the server must see EOF after draining.
	if err := cs.CloseWrite(); err != nil {
		t.Fatalf("client CloseWrite: %v", err)
	}
	ss.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := ss.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("server read after client CloseWrite: err = %v, want io.EOF", err)
	}

	// Reverse direction still flows after the client's CloseWrite.
	if _, err := ss.Write([]byte("pong")); err != nil {
		t.Fatalf("server write: %v", err)
	}
	cs.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(cs, buf); err != nil {
		t.Fatalf("client read reverse: %v", err)
	}
	if !bytes.Equal(buf, []byte("pong")) {
		t.Fatalf("client got %q, want pong", buf)
	}

	// Stream payload is counted into the client's link totals (4 out "ping", 4
	// in "pong"). Exact-equal, since nothing else crossed this session's streams.
	if in, out := clientTotals.Bytes(); in != 4 || out != 4 {
		t.Errorf("client link bytes = (in %d, out %d), want (4, 4)", in, out)
	}
}
