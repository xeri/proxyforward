package gateway

import (
	"errors"
	"time"

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
	// openFlow returns the player's data leg — with oc, the header naming the
	// tunnel, already written on it — plus a cleanup to run when the leg closes.
	// The header write belongs here rather than at the call site because on a
	// pooled leg it is also the take-time liveness probe: a parked connection
	// whose peer went away fails it, and only this side knows there is another
	// connection to try. A non-nil error tells the caller the leg could not be
	// established at all (offline responder, or a group re-pick).
	openFlow(sess *agentSession, oc control.OpenConn) (leg relay.Conn, cleanup func(), err error)
}

// writeOpenConn writes the header that tells the agent which tunnel this leg
// serves and who the player is. It is the first thing on every data leg, under
// its own deadline so a peer that never drains cannot park the join.
func writeOpenConn(leg relay.Conn, oc control.OpenConn) error {
	_ = leg.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
	defer func() { _ = leg.SetWriteDeadline(time.Time{}) }()
	return control.WriteMsg(leg, control.TypeOpenConn, oc)
}

// muxDataPlane opens a stream on the agent's shared session (yamux today, any
// transport.Session tomorrow). The legacy/default transport: all players
// multiplex over the one gateway↔agent link.
type muxDataPlane struct{}

func (muxDataPlane) openFlow(sess *agentSession, oc control.OpenConn) (relay.Conn, func(), error) {
	mux := sess.session()
	if mux == nil {
		return nil, nil, errNoDataLeg
	}
	st, err := mux.OpenStream()
	if err != nil {
		return nil, nil, err
	}
	if err := writeOpenConn(st, oc); err != nil {
		st.Close()
		return nil, nil, err
	}
	// A mux stream is torn down by the session; no per-flow cleanup needed.
	return st, func() {}, nil
}

// perConnDataPlane serves each player on a dedicated TCP+TLS connection to the
// agent (no cross-player head-of-line blocking on the gateway↔agent hop): one
// the agent parked in advance if pooling was negotiated, otherwise one it is
// signalled to dial back now. Either way the conn is tracked in sess.dataConns
// so eviction (closeAll) can drain it — its splice rides a dedicated conn, not
// the mux, so closing the mux alone cannot; cleanup removes that entry when the
// splice ends.
type perConnDataPlane struct{ g *Gateway }

func (p perConnDataPlane) openFlow(sess *agentSession, oc control.OpenConn) (relay.Conn, func(), error) {
	// Parked first: the connection already exists, so the player's join costs one
	// header write instead of a control round trip plus a TCP and a TLS handshake
	// (dataDialTimeout's entire budget). That write is also the only liveness
	// signal a connection nobody was reading can give — one that fails is dropped
	// and the next is tried, so a dead parked conn costs at most what the
	// dial-back below would have cost anyway.
	for {
		parked := sess.pool.take()
		if parked == nil {
			break
		}
		if err := writeOpenConn(parked, oc); err != nil {
			sess.logger.Debug("parked data conn was dead on take", "conn_id", oc.ConnID, "err", err)
			parked.Close()
			continue
		}
		p.g.pooledLegs.Add(1)
		sess.dataConns.Store(oc.ConnID, parked)
		return parked, func() { sess.dataConns.Delete(oc.ConnID) }, nil
	}

	raw := p.g.openDataConn(sess, oc.ConnID)
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
	if err := writeOpenConn(rc, oc); err != nil {
		raw.Close()
		return nil, nil, err
	}
	sess.dataConns.Store(oc.ConnID, raw)
	return rc, func() { sess.dataConns.Delete(oc.ConnID) }, nil
}

// pickDataPlane selects a session's data plane from its negotiated capabilities,
// once at admission. The result is immutable for the session's lifetime.
func (g *Gateway) pickDataPlane(caps control.CapSet) dataPlane {
	if caps.Has(control.CapPerConn) {
		return perConnDataPlane{g: g}
	}
	return muxDataPlane{}
}
