package gateway

import (
	"net"
	"sync"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/link"
	"proxyforward/internal/relay"
	"proxyforward/internal/stats"
)

// maxPooledPerSession is the gateway's own ceiling on parked data connections
// per agent session, and thus the ceiling on the grant it answers a request
// with. Small on purpose: the pool exists to cover the join burst that follows
// a server appearing in someone's list, not to hold a connection per player.
// Eight covers a lobby's worth of simultaneous joins while keeping the parked
// set — descriptors on both machines, plus a NAT mapping between them — in the
// same order as the number of players actually arriving at once.
//
// It is also what makes the grant safe: the answer can only shrink the request,
// so a well-behaved agent never parks a connection the gateway will not hold,
// and the gateway never has to close an "extra" that the agent would then
// redial in a loop.
const maxPooledPerSession = 8

// connPool holds the data connections one agent has dialled, authenticated and
// parked at the gateway ahead of any player (control.CapConnPool). Taking one
// turns a player's join from "control round trip, TCP handshake, TLS handshake,
// then bytes" into "write the header, then bytes".
//
// Ownership: a connection belongs to the pool from park until take. The pool is
// created at admission, lives on the session, and is drained exactly once by
// closeAll — after which park refuses, so a dial-back racing an eviction closes
// its connection instead of leaking it into a dead pool.
type connPool struct {
	// grant is what the agent was told it may park (HelloOK.PoolSize) and is
	// therefore also the cap enforced here. Immutable after construction.
	grant int

	mu     sync.Mutex
	conns  []relay.Conn
	closed bool
}

func newConnPool(grant int) *connPool {
	return &connPool{grant: grant, conns: make([]relay.Conn, 0, grant)}
}

// granted reports the pool's size for the hello_ok; nil (no pooling) is 0, which
// is exactly what a legacy gateway says by saying nothing.
func (p *connPool) granted() int {
	if p == nil {
		return 0
	}
	return p.grant
}

// park files an authenticated data connection for later use. It returns false —
// caller closes — when the pool is full or already drained. A full pool is not
// an error: the grant is a ceiling the agent respects, so this only fires on a
// race with a take that had not yet been refilled against.
func (p *connPool) park(c relay.Conn) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || len(p.conns) >= p.grant {
		return false
	}
	p.conns = append(p.conns, c)
	return true
}

// take removes and returns the connection parked longest ago, or nil when the
// pool is empty (the caller then dials back, exactly as before pooling). Oldest
// first because the oldest is the likeliest to have gone stale, and the caller
// discovers that on the header write while it can still fall back — taking the
// newest instead would leave one connection ageing at the bottom forever.
func (p *connPool) take() relay.Conn {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.conns) == 0 {
		return nil
	}
	c := p.conns[0]
	copy(p.conns, p.conns[1:])
	p.conns[len(p.conns)-1] = nil // don't pin the connection in the backing array
	p.conns = p.conns[:len(p.conns)-1]
	return c
}

// drain closes every parked connection and closes the pool to further parks.
// Idempotent, and safe to call while a dial-back is in flight: the late park
// loses and closes its own connection.
func (p *connPool) drain() {
	if p == nil {
		return
	}
	p.mu.Lock()
	conns := p.conns
	p.conns, p.closed = nil, true
	p.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// depth reports how many connections are parked right now, for tests and
// diagnostics.
func (p *connPool) depth() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

// poolGrantFor answers an agent's requested pool size: its request clamped to
// this gateway's ceiling, and 0 unless pooling was negotiated. Both capabilities
// are required — a pool of mux streams is meaningless (OpenStream is free), so
// CapConnPool without CapPerConn grants nothing. tcp reports whether the session
// arrived on the TCP accept path, the only one that can produce a parked
// connection at all; a QUIC session gets no grant however it negotiates.
func poolGrantFor(hello *control.Hello, caps control.CapSet, tcp bool) int {
	if !tcp || !caps.Has(control.CapConnPool) || !caps.Has(control.CapPerConn) {
		return 0
	}
	if hello.PoolSize <= 0 {
		return 0
	}
	return min(hello.PoolSize, maxPooledPerSession)
}

// PooledLegs reports how many players have been served on a connection the
// agent had already parked, instead of one dialled back for them — the measure
// of what connection pooling is actually saving. Cumulative over this gateway's
// lifetime.
func (g *Gateway) PooledLegs() int64 { return g.pooledLegs.Load() }

// parkPooledConn files an authenticated, connID-less data connection in its own
// agent's pool. The session is looked up by the identity the connection just
// proved, so a connection can only ever be parked for the agent that dialled it:
// agent B, however honestly enrolled, cannot leave a connection where agent A's
// players would be served on it.
//
// A pool that is absent (no capability), full, or drained means the connection
// has no home — closing it costs the agent one redial and nothing else.
func (g *Gateway) parkPooledConn(conn net.Conn, identity Identity) {
	a := g.act()
	if a == nil {
		conn.Close()
		return
	}
	var sess *agentSession
	a.do(func() { sess = a.agents[identity.AgentID] })
	if sess == nil || sess.pool == nil {
		conn.Close()
		return
	}
	// The pre-auth prologue's deadline dies with the prologue; from here the
	// connection is expected to sit silent, so keepalive is what watches it.
	_ = conn.SetDeadline(time.Time{})
	if err := link.KeepParkedAlive(conn); err != nil {
		sess.logger.Debug("parked data conn without keepalive", "err", err)
	}
	// Count its bytes into the same process + session link totals as every other
	// data connection, so the GUI's link card stays honest about per-conn payload.
	wrapped := stats.NewCountingConn(conn, &g.linkTotals, &sess.link)
	rc, ok := wrapped.(relay.Conn)
	if !ok {
		// The counting wrapper preserves CloseWrite, so this is a programming
		// error rather than a runtime condition — but a leg that cannot half-close
		// must never reach Splice.
		wrapped.Close()
		return
	}
	if !sess.pool.park(rc) {
		rc.Close()
	}
}
