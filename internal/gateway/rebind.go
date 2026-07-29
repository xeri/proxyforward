package gateway

import (
	"context"
	"fmt"
	"time"

	"proxyforward/internal/control"
)

// Periodic rebind retry.
//
// A gateway bind can fail for a reason that later goes away: an outside process
// (a stale server, another tunnel tool, a service that starts late in boot)
// holding the port, or a group socket losing the same race at startup. Nothing
// retried either, so a tunnel that lost that race stayed down until the agent
// happened to reconnect — and a group whose STARTUP bind failed left every member
// dark until the gateway restarted, because runtime SetGroup rolls back on
// failure and startup is the only other way in.
//
// The driver is a single ticker on the gateway's run context. It re-runs the
// existing reconcile head, so recovery reuses every rule that governs a first
// bind — scope, allowlist, reassignment, the tunnel cap — rather than a second,
// divergent implementation of "bind this". A healthy gateway pays one actor query
// per tick and nothing else: bound listeners compare equal (sameListener) and are
// left untouched, so live players are never disturbed.
const rebindRetryInterval = 30 * time.Second

// runRebindRetry ticks retryFailedBinds until the gateway's run context ends.
func (g *Gateway) runRebindRetry(ctx context.Context) {
	t := time.NewTicker(rebindRetryInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.RetryFailedBinds()
		}
	}
}

// RetryFailedBinds re-attempts everything that is configured but not bound. It is
// what the ticker calls, and it is safe to call at any time — an operator who has
// just killed the process squatting a port need not wait out the interval.
// Groups go first: a member cannot join a group whose socket is missing, so a
// group that comes up this tick makes every session's membership stale.
func (g *Gateway) RetryFailedBinds() {
	a := g.act()
	if a == nil {
		return
	}
	groupBound := g.retryFailedGroups(a)
	for _, sess := range a.sessions() {
		// A session with nothing failed is skipped — unless a group just bound,
		// in which case its members must be given the chance to join.
		if !groupBound && !sess.bindFailed.Load() {
			continue
		}
		g.retrySessionBinds(a, sess)
	}
}

// retryFailedGroups binds every configured group that has no socket, and reports
// whether any of them came up.
func (g *Gateway) retryFailedGroups(a *actor) bool {
	recovered := false
	for _, spec := range g.agents.Groups() {
		if a.groupIsBound(spec.ID) {
			continue
		}
		movable := g.movableInto(spec.ID)
		var err error
		if !a.do(func() { err = a.bindGroup(spec, movable, g.cfg.Gateway.BindAddr, g.handleGroupClient) }) {
			return recovered // shutting down
		}
		if err != nil {
			// Expected while the port is still held; the next tick tries again.
			g.logger.Debug("group rebind retry failed", "group", spec.ID, "port", spec.PublicPort, "err", err)
			continue
		}
		recovered = true
		g.refusals.groupRecovered()
		a.do(func() {
			a.recordEvent(GatewayEvent{
				Kind:       EventGroupRecovered,
				ActualPort: spec.PublicPort,
				Message:    fmt.Sprintf("group %q is serving on port %d again — the process that held the port is gone", spec.Name, spec.PublicPort),
			})
		})
		g.logger.Info("group bound on retry", "group", spec.ID, "port", spec.PublicPort)
	}
	return recovered
}

// retrySessionBinds re-runs one session's reconcile head. Tunnels already bound
// are no-ops; failed ones re-attempt through bindLocked with all of its usual
// policy. Anything that comes up is announced, and a gateway-config session whose
// recovery landed different ports is re-adopted and pushed so the agent's view
// matches the listeners again.
func (g *Gateway) retrySessionBinds(a *actor, sess *agentSession) {
	solo, grouped, ok := g.desiredForRetry(sess)
	if !ok {
		return
	}
	before := a.boundPorts(sess.agentID)
	outcomes, ran := a.reconcile(sess, solo, g.policy(), g.handleClient)
	if !ran {
		return
	}
	a.reconcileGroupMembers(sess, grouped)

	var recovered []reconcileOutcome
	for _, o := range outcomes {
		if o.Err == nil && before[o.ID] == 0 {
			recovered = append(recovered, o)
		}
	}
	if len(recovered) == 0 {
		return
	}
	// Recoveries are the other half of the bind-failure counters: a gateway that
	// fails binds and recovers them is riding out a boot race, one that only
	// fails them has a squatter nobody has cleared.
	g.refusals.tunnelsRecovered(len(recovered))
	a.do(func() {
		for _, o := range recovered {
			a.recordEvent(GatewayEvent{
				Kind:       EventTunnelRecovered,
				AgentID:    sess.agentID,
				TunnelID:   o.ID,
				ActualPort: o.Port,
				Message:    fmt.Sprintf("tunnel %q is bound on port %d again — the process that held the port is gone", o.ID, o.Port),
			})
		}
	})
	g.logger.Info("tunnels bound on retry", "agent", sess.agentID, "count", len(recovered))

	// Ports the agent does not know about are ports it will report wrong. Only
	// re-adopt when the resolved set actually differs — a recovery that landed
	// the requested port pushes nothing and does not bump the generation.
	resolved := resolvePorts(solo, outcomes)
	if control.HashTunnels(resolved) == control.HashTunnels(solo) {
		return
	}
	if _, _, ok := g.agents.DesiredConfig(sess.agentID); ok {
		g.adoptAndPush(sess, append(resolved, grouped...))
	}
}

// desiredForRetry returns the (solo, grouped) sets to re-reconcile for sess. A
// gateway-config session reads the authoritative store — the same head
// pushConfigOnConnect runs, so scope changes since the last sync are honoured. A
// legacy tunnel-sync agent has no stored config, so its last synced set stands
// in, reconciled exactly as syncTunnels reconciled it (unpartitioned).
func (g *Gateway) desiredForRetry(sess *agentSession) (solo, grouped []control.TunnelSpec, ok bool) {
	if stored, gen, found := g.agents.DesiredConfig(sess.agentID); found && gen > 0 {
		solo, grouped = g.partitionDesired(g.validSpecs(sess, stored))
		return solo, grouped, true
	}
	if last := sess.lastDesired.Load(); last != nil {
		return *last, nil, true
	}
	return nil, nil, false
}
