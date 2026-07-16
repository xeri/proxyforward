package gateway

import (
	"errors"

	"proxyforward/internal/control"
	"proxyforward/internal/relay"
)

// errNoDataLeg means a player's data leg could not be acquired; handleClient
// answers with the offline responder instead of dropping the player.
var errNoDataLeg = errors.New("gateway: no data leg")

// dataPlane acquires the data leg for one player, hiding the mux-vs-per-conn
// choice so handleClient stays transport-agnostic — the leg is always a
// relay.Conn spliced identically. The choice is made once per session at
// admission (pickDataPlane) and is immutable thereafter. A future QUIC session
// slots in behind muxDataPlane: its streams already satisfy transport.Stream (a
// relay.Conn), so handleClient needs no change to carry them.
type dataPlane interface {
	// openFlow returns the player's data leg plus a cleanup to run when the leg
	// closes. A non-nil error tells the caller to serve the offline responder.
	openFlow(sess *agentSession, connID string) (leg relay.Conn, cleanup func(), err error)
}

// muxDataPlane opens a stream on the agent's shared session (yamux today, any
// transport.Session tomorrow). The legacy/default transport: all players
// multiplex over the one gateway↔agent link.
type muxDataPlane struct{}

func (muxDataPlane) openFlow(sess *agentSession, _ string) (relay.Conn, func(), error) {
	mux := sess.session()
	if mux == nil {
		return nil, nil, errNoDataLeg
	}
	st, err := mux.OpenStream()
	if err != nil {
		return nil, nil, err
	}
	// A mux stream is torn down by the session; no per-flow cleanup needed.
	return st, func() {}, nil
}

// perConnDataPlane signals the agent to dial back a dedicated TCP+TLS connection
// per player (no cross-player head-of-line blocking on the gateway↔agent hop).
// The dialed conn is tracked in sess.dataConns so eviction (closeAll) can drain
// it — its splice rides a dedicated conn, not the mux, so closing the mux alone
// cannot; cleanup removes that entry when the splice ends.
type perConnDataPlane struct{ g *Gateway }

func (p perConnDataPlane) openFlow(sess *agentSession, connID string) (relay.Conn, func(), error) {
	raw := p.g.openDataConn(sess, connID)
	if raw == nil {
		return nil, nil, errNoDataLeg
	}
	rc, ok := raw.(relay.Conn)
	if !ok {
		// The counting-conn wrapper always preserves CloseWrite, so a failed
		// assertion is a programming error, not a runtime condition. Drop the
		// conn rather than splice something that cannot half-close.
		raw.Close()
		return nil, nil, errNoDataLeg
	}
	sess.dataConns.Store(connID, raw)
	return rc, func() { sess.dataConns.Delete(connID) }, nil
}

// pickDataPlane selects a session's data plane from its negotiated capabilities,
// once at admission. The result is immutable for the session's lifetime.
func (g *Gateway) pickDataPlane(caps control.CapSet) dataPlane {
	if caps.Has(control.CapPerConn) {
		return perConnDataPlane{g: g}
	}
	return muxDataPlane{}
}
