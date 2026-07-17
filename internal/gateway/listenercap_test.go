package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// bindN opens n listeners for sess under pol, failing the test if any bind the
// cap should permit is refused.
func bindN(t *testing.T, a *actor, sess *agentSession, pol bindPolicy, n int, idFmt string) {
	t.Helper()
	for i := range n {
		spec := control.TunnelSpec{ID: fmt.Sprintf(idFmt, i), Name: "x", Type: config.TunnelTCP}
		if _, err := a.bindTunnel(sess, spec, pol, func(*publicListener, net.Conn) {}); err != nil {
			t.Fatalf("bind %d of %d must succeed: %v", i+1, n, err)
		}
	}
}

// TestListenerCapBoundsAnUnscopedAgent is the hole itself.
//
// Every other resource an agent costs the gateway is bounded: connections
// globally and per source network (connGate), unauthenticated sockets
// (preAuthGate), bandwidth (bwcap). Listeners — the only resource an agent
// allocates by *asking* — were bounded by nothing but its scope, and the default
// scope is empty, which means "any port, any tunnel id". So the default agent
// could ask for a listener per invented tunnel id and get one every time, until
// the host's ephemeral range was exhausted: no more tunnels for any other agent,
// and no more outbound connections for the machine itself. An authenticated peer
// is not a trusted one — the shared-token path admits anyone holding one string,
// and an enrolled agent can be compromised.
func TestListenerCapBoundsAnUnscopedAgent(t *testing.T) {
	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	// The permissive default: no port scope, no tunnel-id scope, no allowlist.
	// Nothing here constrains the agent except the count.
	sess := &agentSession{agentID: "agt_greedy", scope: Scope{}}
	if _, err := a.admit(sess); err != nil {
		t.Fatalf("admit: %v", err)
	}
	const limit = 3
	pol := bindPolicy{bindAddr: "127.0.0.1", maxTunnels: limit}
	bindN(t, a, sess, pol, limit, "tnl_%d")

	// One past the cap: an invented id, an ephemeral port. Scope has nothing to
	// refuse and the allowlist is empty, so if the count is not a limit, nothing is.
	over := control.TunnelSpec{ID: "tnl_one_more", Name: "x", Type: config.TunnelTCP}
	_, err := a.bindTunnel(sess, over, pol, func(*publicListener, net.Conn) {})
	if err == nil {
		t.Fatal("an unscoped agent bound a listener past the cap: it can invent tunnel ids until the ephemeral range is gone")
	}
	if !errors.Is(err, errTooManyListeners) {
		t.Errorf("refusal is not the cap's: %v", err)
	}
}

// TestListenerCapCountsGrowthNotRebinding: a config hot-apply re-binds a tunnel
// that already exists, and bindLocked implements that as close-then-open. Counting
// before the replacement would make an agent sitting at its cap unable to change
// any tunnel it already has — the cap would brick reconfiguration rather than
// bound it. Unbinding must likewise return the budget, or the cap is a lifetime
// quota that a long-lived agent silently runs out of.
func TestListenerCapCountsGrowthNotRebinding(t *testing.T) {
	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	sess := &agentSession{agentID: "agt_at_cap"}
	if _, err := a.admit(sess); err != nil {
		t.Fatalf("admit: %v", err)
	}
	const limit = 2
	pol := bindPolicy{bindAddr: "127.0.0.1", maxTunnels: limit}
	bindN(t, a, sess, pol, limit, "tnl_%d")

	// Re-bind an existing tunnel while at the cap: a replacement, not growth.
	same := control.TunnelSpec{ID: "tnl_0", Name: "renamed", Type: config.TunnelTCP}
	if _, err := a.bindTunnel(sess, same, pol, func(*publicListener, net.Conn) {}); err != nil {
		t.Fatalf("an agent at its cap cannot reconfigure a tunnel it already holds: %v", err)
	}

	// Give one back, and the freed slot must be usable.
	a.unbindTunnel(sess, "tnl_1")
	fresh := control.TunnelSpec{ID: "tnl_fresh", Name: "x", Type: config.TunnelTCP}
	if _, err := a.bindTunnel(sess, fresh, pol, func(*publicListener, net.Conn) {}); err != nil {
		t.Fatalf("unbinding did not return the slot; the cap is a lifetime quota: %v", err)
	}
}

// TestListenerCapIsPerAgent: the cap exists to stop one agent starving the
// others, so it must be charged per agent. A global counter would let a greedy
// agent lock out honest ones — the very thing it is meant to prevent, just at a
// different address.
func TestListenerCapIsPerAgent(t *testing.T) {
	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	const limit = 2
	pol := bindPolicy{bindAddr: "127.0.0.1", maxTunnels: limit}

	greedy := &agentSession{agentID: "agt_greedy"}
	if _, err := a.admit(greedy); err != nil {
		t.Fatalf("admit: %v", err)
	}
	bindN(t, a, greedy, pol, limit, "greedy_%d")

	honest := &agentSession{agentID: "agt_honest"}
	if _, err := a.admit(honest); err != nil {
		t.Fatalf("admit: %v", err)
	}
	spec := control.TunnelSpec{ID: "honest_0", Name: "mc", Type: config.TunnelTCP}
	if _, err := a.bindTunnel(honest, spec, pol, func(*publicListener, net.Conn) {}); err != nil {
		t.Fatalf("one agent at its cap blocked another agent's first tunnel: %v", err)
	}
}

// TestBindErrCodeDoesNotBlameThePort: the wire's bind codes are display-only, so
// their whole job is telling an operator where to look. "port_in_use" for a cap
// refusal sends them hunting for a process holding a port that nothing holds.
func TestBindErrCodeDoesNotBlameThePort(t *testing.T) {
	capped := fmt.Errorf("tunnel %q: %w (32)", "mc", errTooManyListeners)
	if got := bindErrCode(capped); got != control.ErrCodePortNotAllowed {
		t.Errorf("cap refusal reported as %q, want %q", got, control.ErrCodePortNotAllowed)
	}
	if got := bindErrCode(errors.New("listen tcp :25565: bind: address already in use")); got != control.ErrCodePortInUse {
		t.Errorf("a real port clash reported as %q, want %q", got, control.ErrCodePortInUse)
	}
}
