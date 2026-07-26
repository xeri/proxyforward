package gateway

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/relay"
)

// pooledFake is a relay.Conn stand-in that only records Close; the pool touches
// nothing else. The remaining methods promote from the nil embedded interface
// and would panic if called — they never are here.
type pooledFake struct {
	net.Conn
	id     int
	closed atomic.Bool
}

func (c *pooledFake) Close() error      { c.closed.Store(true); return nil }
func (c *pooledFake) CloseWrite() error { return nil }

// TestConnPoolParkTakeDrain covers the whole ownership contract: a parked
// connection is handed out oldest-first and still open (the splice owns it from
// then on), an empty pool says so rather than blocking (the caller dials back),
// and drain closes exactly what is still parked and refuses everything after —
// so a dial-back racing an eviction closes its own connection instead of
// leaking it into a dead pool.
func TestConnPoolParkTakeDrain(t *testing.T) {
	p := newConnPool(4)
	first, second := &pooledFake{id: 1}, &pooledFake{id: 2}
	for _, c := range []*pooledFake{first, second} {
		if !p.park(c) {
			t.Fatalf("park %d into a pool of 4 was refused", c.id)
		}
	}
	if p.depth() != 2 {
		t.Fatalf("pool depth %d after two parks, want 2", p.depth())
	}

	// Oldest first: the one likeliest to have gone stale is tried while the
	// caller can still fall back.
	got := p.take()
	if got != relay.Conn(first) {
		t.Fatalf("take returned %v, want the first-parked connection", got)
	}
	if first.closed.Load() {
		t.Fatal("a taken connection must stay open — the splice owns it now")
	}

	p.drain()
	if !second.closed.Load() {
		t.Fatal("drain left a parked connection open — nothing else would ever close it")
	}
	if first.closed.Load() {
		t.Fatal("drain closed a connection it no longer owned")
	}
	if p.take() != nil {
		t.Fatal("take from a drained pool must report empty")
	}
	late := &pooledFake{id: 3}
	if p.park(late) {
		t.Fatal("park into a drained pool must lose, so the caller closes the connection")
	}
	if p.depth() != 0 {
		t.Fatalf("drained pool depth %d, want 0", p.depth())
	}
}

// TestConnPoolRespectsCap: the grant is a ceiling on both sides. The gateway
// answers a request with at most its own cap (so the agent never parks a
// connection the gateway will not hold, and the gateway never has to close an
// "extra" the agent would redial in a loop), and the pool itself refuses a park
// past the grant it published.
func TestConnPoolRespectsCap(t *testing.T) {
	grants := []struct {
		name     string
		hello    control.Hello
		caps     []string
		tcp      bool
		wantSize int
	}{
		{"clamped to the gateway cap", control.Hello{PoolSize: 99}, []string{control.CapPerConn, control.CapConnPool}, true, maxPooledPerSession},
		{"honors a smaller request", control.Hello{PoolSize: 2}, []string{control.CapPerConn, control.CapConnPool}, true, 2},
		{"no request, no pool", control.Hello{}, []string{control.CapPerConn, control.CapConnPool}, true, 0},
		{"negative request, no pool", control.Hello{PoolSize: -1}, []string{control.CapPerConn, control.CapConnPool}, true, 0},
		// Pooling a mux stream is meaningless — OpenStream is free — so the
		// capability alone grants nothing.
		{"without per-conn, no pool", control.Hello{PoolSize: 4}, []string{control.CapConnPool}, true, 0},
		{"without the capability, no pool", control.Hello{PoolSize: 4}, []string{control.CapPerConn}, true, 0},
		// A QUIC session has no accepted TCP connection to park.
		{"not on the tcp accept path, no pool", control.Hello{PoolSize: 4}, []string{control.CapPerConn, control.CapConnPool}, false, 0},
	}
	for _, tc := range grants {
		t.Run(tc.name, func(t *testing.T) {
			if got := poolGrantFor(&tc.hello, control.NewCapSet(tc.caps), tc.tcp); got != tc.wantSize {
				t.Errorf("poolGrantFor = %d, want %d", got, tc.wantSize)
			}
		})
	}

	p := newConnPool(2)
	for i := 0; i < 2; i++ {
		if !p.park(&pooledFake{id: i}) {
			t.Fatalf("park %d into a pool of 2 was refused", i)
		}
	}
	extra := &pooledFake{id: 99}
	if p.park(extra) {
		t.Fatal("park past the granted size must lose so the caller closes it")
	}
	if p.depth() != 2 {
		t.Fatalf("pool depth %d after a refused park, want 2", p.depth())
	}
}

// tcpPair returns a connected pair of real TCP connections: near is the one a
// caller under test is handed, far is its peer (whose reads observe a close).
func tcpPair(t *testing.T) (near, far net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type accepted struct {
		c   net.Conn
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		ch <- accepted{c, err}
	}()
	far, err = net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a := <-ch
	if a.err != nil {
		t.Fatal(a.err)
	}
	t.Cleanup(func() { a.c.Close(); far.Close() })
	return a.c, far
}

// TestHandleDataConnRejectsPooledFromWrongAgent: a parked connection is filed
// under the identity it just proved, never the one it claims to serve. Agent B —
// however honestly enrolled — must not be able to leave a connection where
// agent A's players would be served on it, which is the same rule the connID
// dial-back path enforces with pc.agentID. A connection with no home is closed,
// not left dangling.
func TestHandleDataConnRejectsPooledFromWrongAgent(t *testing.T) {
	f := newGroupFixture(t)
	sess := f.admit(t, "agt_a")
	sess.pool = newConnPool(2)

	near, far := tcpPair(t)
	f.g.handleDataConn(near, &control.Hello{Kind: control.KindData, Pooled: true}, Identity{AgentID: "agt_b"})
	if depth := sess.pool.depth(); depth != 0 {
		t.Fatalf("another agent's connection landed in agt_a's pool (depth %d)", depth)
	}
	_ = far.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := far.Read(make([]byte, 1)); err == nil {
		t.Fatal("a pooled connection with no home must be closed, not left open")
	} else if err != io.EOF {
		t.Logf("peer read after close: %v", err) // RST instead of FIN is fine too
	}

	// The same hello from the agent that owns the session parks normally.
	own, _ := tcpPair(t)
	f.g.handleDataConn(own, &control.Hello{Kind: control.KindData, Pooled: true}, Identity{AgentID: "agt_a"})
	if depth := sess.pool.depth(); depth != 1 {
		t.Fatalf("pool depth %d after the owning agent parked a connection, want 1", depth)
	}
}
