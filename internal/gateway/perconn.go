package gateway

import (
	"context"
	"net"
	"sync"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/stats"
)

// dataDialTimeout bounds how long a player waits for the agent to dial back a
// per-conn data connection before the gateway falls back to the offline
// responder. Kept strictly longer than a data conn's own pre-auth deadline
// (preAuthTimeout) so a pre-auth failure on the agent's dial-back surfaces
// first. A healthy dial-back completes in well under a round-trip (the TLS
// session resumes), so this only ever fires on a genuinely failed dial-back.
const dataDialTimeout = 12 * time.Second

// pendingConn is a per-conn data connection the gateway has asked an agent to
// dial back. handleClient parks on it (take); the control accept path fills it
// when the matching KindData connection arrives (deliver). The handoff is
// exactly-once and loser-closes: whichever side runs after the other has marked
// the entry done closes the conn it holds, so no descriptor leaks on a
// timeout/dial-back race (goleak does not catch a leaked conn).
type pendingConn struct {
	agentID string              // only this agent may answer (shared token today)
	link    *stats.LinkCounters // this agent's session counters, for byte accounting
	ready   chan struct{}

	mu   sync.Mutex
	conn net.Conn
	done bool
}

// deliver hands a dialed-back data conn to the waiting handleClient. It returns
// false if the waiter already gave up (timed out / evicted), in which case the
// caller must close conn.
func (pc *pendingConn) deliver(conn net.Conn) bool {
	pc.mu.Lock()
	if pc.done {
		pc.mu.Unlock()
		return false
	}
	pc.conn = conn
	pc.done = true
	pc.mu.Unlock()
	close(pc.ready)
	return true
}

// take blocks until deliver hands over a conn, ctx is cancelled (the agent was
// evicted), or timeout fires. It returns nil on failure, marking the entry done
// so a racing deliver loses and closes its conn instead of leaking it.
func (pc *pendingConn) take(ctx context.Context, timeout time.Duration) net.Conn {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-pc.ready:
		return pc.conn
	case <-ctx.Done():
	case <-timer.C:
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.done {
		// deliver won the race between our select waking and this lock; use its
		// conn rather than leaking it.
		return pc.conn
	}
	pc.done = true
	return nil
}

// handleDataConn matches an authenticated KindData dial-back to the player
// waiting for it and hands over the raw, byte-counted conn. Called from the
// control accept path once the KindData hello authenticates; the conn's
// pre-auth deadline is cleared here on a match.
func (g *Gateway) handleDataConn(conn net.Conn, hello *control.Hello, identity Identity) {
	if hello.ConnID == "" {
		conn.Close()
		return
	}
	v, ok := g.pendingData.Load(hello.ConnID)
	if !ok {
		// A valid agent's late dial-back after the player gave up: not an auth
		// failure (it is the agent's own IP), just close it.
		conn.Close()
		return
	}
	pc := v.(*pendingConn)
	if pc.agentID != identity.AgentID {
		// All agents share the gateway token today, so this check — not the
		// token — is what stops agent B answering agent A's open_data. It is
		// load-bearing until per-agent identity lands, and is the natural place
		// that identity plugs in.
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})
	// Count this data conn's bytes into the same process + session link totals
	// as the control conn, so the GUI's link card reflects per-conn payload.
	dataConn := stats.NewCountingConn(conn, &g.linkTotals, pc.link)
	if !pc.deliver(dataConn) {
		dataConn.Close()
	}
}

// openDataConn drives the per-conn data plane for one player: register a
// pending slot, ask the agent to dial back a dedicated conn, and wait for it.
// Returns nil (→ offline responder) if the agent never dials back in time or is
// evicted meanwhile. The returned conn's bytes are already counted.
func (g *Gateway) openDataConn(sess *agentSession, connID string) net.Conn {
	pc := &pendingConn{agentID: sess.agentID, link: &sess.link, ready: make(chan struct{})}
	g.pendingData.Store(connID, pc)
	defer g.pendingData.Delete(connID)
	if err := sess.writeControl(control.TypeOpenData, control.OpenData{ConnID: connID}); err != nil {
		sess.logger.Debug("open_data write failed", "conn_id", connID, "err", err)
		return nil
	}
	return pc.take(sess.ctx, dataDialTimeout)
}
