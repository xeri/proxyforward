package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/relay"
)

// pooledFake is a relay.Conn stand-in for the pool tests: it records Close and,
// with writeErr set, stands in for a parked connection whose peer has gone away
// — the failure the take-time header write exists to surface. The remaining
// methods promote from the nil embedded interface and would panic if called —
// they never are here.
type pooledFake struct {
	net.Conn
	id       int
	writeErr error
	closed   atomic.Bool
}

func (c *pooledFake) Close() error      { c.closed.Store(true); return nil }
func (c *pooledFake) CloseWrite() error { return nil }
func (c *pooledFake) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(p), nil
}
func (c *pooledFake) SetWriteDeadline(time.Time) error { return nil }

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

// poolFlowSession builds the least session openFlow needs: a control stream
// whose frames a test can read back, a per-conn data plane, and a pool. Its ctx
// is already cancelled, so the dial-back's wait for the agent ends at once —
// these tests are about which path is taken, not how long it waits.
func poolFlowSession(t *testing.T, g *Gateway, pool *connPool) (*agentSession, *captureStream) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctrl := &captureStream{}
	s := &agentSession{agentID: "agt_a", logger: discardLogger(), ctx: ctx, cancel: cancel, pool: pool}
	s.setCtrl(ctrl)
	s.dp = perConnDataPlane{g: g}
	return s, ctrl
}

// openDataConnIDs returns the connIDs of every open_data frame the gateway sent
// — i.e. every time it asked the agent to dial a connection back.
func openDataConnIDs(t *testing.T, c *captureStream) []string {
	t.Helper()
	var out []string
	for _, env := range c.envelopes(t) {
		if env.Type != control.TypeOpenData {
			continue
		}
		od, err := control.Decode[control.OpenData](env)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, od.ConnID)
	}
	return out
}

// TestConnPoolExhaustedFallsBackToOpenData: an empty pool is not an error, it
// is just the state the gateway was in before pooling existed — the player is
// served by asking the agent to dial back, exactly as before. A pool that runs
// dry under a join burst must degrade to the old path, never refuse a player.
// (conn-pool)
func TestConnPoolExhaustedFallsBackToOpenData(t *testing.T) {
	g := &Gateway{logger: discardLogger()}
	sess, ctrl := poolFlowSession(t, g, newConnPool(2)) // granted, but empty

	leg, cleanup, err := sess.dp.openFlow(sess, control.OpenConn{TunnelID: "t_a", ConnID: "7"})
	if err == nil {
		cleanup()
		leg.Close()
		t.Fatal("openFlow produced a leg with no pool and no agent to dial back")
	}
	if ids := openDataConnIDs(t, ctrl); len(ids) != 1 || ids[0] != "7" {
		t.Errorf("open_data frames %v, want exactly one for conn 7 — an empty pool must fall back to the dial-back path", ids)
	}
	if hits := g.PooledLegs(); hits != 0 {
		t.Errorf("%d pooled legs counted with an empty pool", hits)
	}
}

// TestConnPoolDeadParkedConnFallsBack: nobody reads a parked connection, so the
// header write at take time is the only thing that can discover its peer went
// away. One that fails it is dropped and the next parked connection is tried —
// and only when none is left does the gateway pay for a dial-back. A dead
// parked connection must never cost the player more than not pooling would.
// (conn-pool)
func TestConnPoolDeadParkedConnFallsBack(t *testing.T) {
	g := &Gateway{logger: discardLogger()}
	pool := newConnPool(2)
	dead := &pooledFake{id: 1, writeErr: errors.New("parked peer went away")}
	livePeer, live := tcpPair(t)
	if !pool.park(dead) || !pool.park(live.(relay.Conn)) {
		t.Fatal("parking two connections into a pool of two was refused")
	}
	sess, ctrl := poolFlowSession(t, g, pool)

	leg, cleanup, err := sess.dp.openFlow(sess, control.OpenConn{TunnelID: "t_a", ConnID: "7"})
	if err != nil {
		t.Fatalf("openFlow gave up with a live connection still parked: %v", err)
	}
	defer cleanup()
	if leg != live {
		t.Errorf("openFlow returned %v, want the live parked connection", leg)
	}
	if !dead.closed.Load() {
		t.Error("the dead parked connection was dropped without being closed")
	}
	if ids := openDataConnIDs(t, ctrl); len(ids) != 0 {
		t.Errorf("gateway sent open_data %v with a live connection still parked", ids)
	}
	if hits := g.PooledLegs(); hits != 1 {
		t.Errorf("%d pooled legs, want 1", hits)
	}

	// The header went to the connection that survived, and names this player.
	_ = livePeer.SetReadDeadline(time.Now().Add(5 * time.Second))
	env, err := control.ReadMsg(livePeer, control.MaxFrame)
	if err != nil {
		t.Fatalf("read the header off the taken connection: %v", err)
	}
	oc, err := control.Decode[control.OpenConn](env)
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != control.TypeOpenConn || oc.ConnID != "7" {
		t.Errorf("taken connection carried %s %+v, want open_conn for conn 7", env.Type, oc)
	}
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
