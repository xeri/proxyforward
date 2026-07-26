package gateway

import (
	"net"
	"strconv"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// squat holds a port for the duration of a test step, so a bind of that port
// genuinely fails. Returns the release func rather than a t.Cleanup, because
// these tests need the port back *during* the test.
func squat(t *testing.T, port int) func() {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("squat port %d: %v", port, err)
	}
	released := false
	return func() {
		if !released {
			released = true
			ln.Close()
		}
	}
}

// pinnedSession admits a session scoped to exactly one port, so a bind of that
// port has nowhere to reassign to and genuinely fails — the only way a gateway
// bind errors rather than silently landing on an alternative.
func (f *groupFixture) pinnedSession(t *testing.T, agentID string, port int) *agentSession {
	t.Helper()
	s := &agentSession{agentID: agentID, scope: Scope{Ports: []int{port}}, logger: discardLogger()}
	if _, err := f.a.admit(s); err != nil {
		t.Fatalf("admit %s: %v", agentID, err)
	}
	return s
}

// enrolledSession admits a session whose agent is enrolled with an authoritative
// desired config — the gateway-config shape, which is what the retry reads from.
func (f *groupFixture) enrolledSession(t *testing.T, agentID string, desired []control.TunnelSpec) *agentSession {
	t.Helper()
	ticket, err := f.store.IssueEnrollment(false, time.Time{}, Scope{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Enroll(pub(9), agentID, ticket, time.Now()); err != nil {
		t.Fatalf("enroll %s: %v", agentID, err)
	}
	if _, ok := f.store.AdoptConfig(agentID, desired); !ok {
		t.Fatalf("adopt config for %s", agentID)
	}
	return f.admit(t, agentID)
}

// TestRetryRebindsFailedTunnel: a tunnel whose bind failed does not stay down
// until the agent happens to reconnect. The periodic retry re-attempts it, and
// when the port frees the tunnel comes up on its own — with a recovery event and
// the session's failed flag cleared.
func TestRetryRebindsFailedTunnel(t *testing.T) {
	f := newGroupFixture(t)
	port := freePort(t)
	release := squat(t, port)
	defer release()

	sess := f.pinnedSession(t, "agt_a", port)
	spec := control.TunnelSpec{ID: "t_a", Name: "smp", Type: config.TunnelTCP, PublicPort: port}
	outcomes, ran := f.a.reconcile(sess, []control.TunnelSpec{spec}, f.g.policy(), f.g.handleClient)
	if !ran || len(outcomes) != 1 || outcomes[0].Err == nil {
		t.Fatalf("setup: bind of a squatted, scope-pinned port should fail; outcomes = %+v", outcomes)
	}
	if !sess.bindFailed.Load() {
		t.Fatal("a failed bind must mark the session so the retry knows to look at it")
	}

	// Retrying while the port is still held changes nothing and stays quiet.
	f.g.RetryFailedBinds()
	if !sess.bindFailed.Load() {
		t.Fatal("the retry cleared the failed flag while the port was still held")
	}
	if got := f.boundPort("agt_a", "t_a"); got != 0 {
		t.Fatalf("tunnel bound on port %d while its port was squatted", got)
	}

	release()
	f.g.RetryFailedBinds()

	if got := f.boundPort("agt_a", "t_a"); got != port {
		t.Fatalf("after the port freed, tunnel is on port %d, want %d", got, port)
	}
	if sess.bindFailed.Load() {
		t.Fatal("the failed flag should clear once every tunnel is bound")
	}
	if !hasEvent(f.a.eventsSince(0), EventTunnelRecovered, "t_a") {
		t.Fatalf("expected a %q event for the recovered tunnel", EventTunnelRecovered)
	}
}

// TestRetryIsNoOpWhenHealthy: the ticker runs forever on a healthy gateway, so it
// must not disturb anything — same listener objects, no events, no churn for live
// players riding those listeners.
func TestRetryIsNoOpWhenHealthy(t *testing.T) {
	f := newGroupFixture(t)
	sess := f.admit(t, "agt_a")
	spec := control.TunnelSpec{ID: "t_a", Name: "smp", Type: config.TunnelTCP, PublicPort: freePort(t)}
	if _, ran := f.a.reconcile(sess, []control.TunnelSpec{spec}, f.g.policy(), f.g.handleClient); !ran {
		t.Fatal("setup reconcile did not run")
	}
	before := f.listenerPtr("agt_a", "t_a")
	if before == nil {
		t.Fatal("setup: tunnel did not bind")
	}
	evBefore := len(f.a.eventsSince(0))

	f.g.RetryFailedBinds()

	if after := f.listenerPtr("agt_a", "t_a"); after != before {
		t.Fatal("the retry rebound a healthy listener; live connections would have been dropped")
	}
	if got := len(f.a.eventsSince(0)); got != evBefore {
		t.Fatalf("the retry recorded %d new events on a healthy gateway", got-evBefore)
	}
}

// TestRetryRebindsFailedGroupAndJoinsMembers: a group whose socket failed to bind
// at startup leaves every member dark — SetGroup rolls back at runtime, so startup
// is the only way to reach that state, and nothing retried it. The retry binds the
// group and then re-runs each session's reconcile, so members join the group they
// could not join before.
func TestRetryRebindsFailedGroupAndJoinsMembers(t *testing.T) {
	f := newGroupFixture(t)
	port := freePort(t)
	release := squat(t, port)
	defer release()

	// Store the group and drive the startup path with the port held: the socket
	// fails, so the group is configured but unbound.
	if err := f.store.SetGroup(GroupSpec{ID: "grp_a", Name: "grp", PublicPort: port, Proto: "tcp", Policy: PolicySpread}); err != nil {
		t.Fatalf("store SetGroup: %v", err)
	}
	f.g.bindConfiguredGroups()
	if f.groupIsBound("grp_a") {
		t.Fatal("setup: the group bound despite its port being squatted")
	}

	// A member tagged into the unbound group cannot join it. The membership is
	// authoritative config, so it lives in the store — which is also where the
	// retry reads a gateway-config session's desired set from.
	sess := f.enrolledSession(t, "agt_a", []control.TunnelSpec{tag("t_a", "grp_a")})
	sess.setSession(liveSession{})
	f.a.reconcileGroupMembers(sess, []control.TunnelSpec{tag("t_a", "grp_a")})
	if got := f.snapOrder("grp_a"); len(got) != 0 {
		t.Fatalf("member joined an unbound group: %+v", got)
	}

	release()
	f.g.RetryFailedBinds()

	if !f.groupIsBound("grp_a") {
		t.Fatal("the retry did not bind the group once its port freed")
	}
	if got := f.snapOrder("grp_a"); len(got) != 1 {
		t.Fatalf("after the group bound, its member should have joined; snap = %+v", got)
	}
	if !hasEvent(f.a.eventsSince(0), EventGroupRecovered, "") {
		t.Fatalf("expected a %q event", EventGroupRecovered)
	}
}

// boundPort reports the port a session's tunnel is listening on, or 0 when it has
// no listener. Read on the actor goroutine.
func (f *groupFixture) boundPort(agentID, tunnelID string) int {
	var port int
	f.a.do(func() {
		if pl, ok := f.a.listeners[agentID][tunnelID]; ok {
			port = listenerPort(pl.ln)
		}
	})
	return port
}

// listenerPtr returns the live listener object, so a test can prove a reconcile
// left it alone rather than rebuilding an equivalent one.
func (f *groupFixture) listenerPtr(agentID, tunnelID string) *publicListener {
	var pl *publicListener
	f.a.do(func() { pl = f.a.listeners[agentID][tunnelID] })
	return pl
}

// groupIsBound reports whether a group has a live shared socket.
func (f *groupFixture) groupIsBound(groupID string) bool {
	var ok bool
	f.a.do(func() { _, ok = f.a.groupListeners[groupID] })
	return ok
}

// hasEvent reports whether the ring holds an event of kind (optionally for a
// specific tunnel).
func hasEvent(events []GatewayEvent, kind, tunnelID string) bool {
	for _, e := range events {
		if e.Kind == kind && (tunnelID == "" || e.TunnelID == tunnelID) {
			return true
		}
	}
	return false
}
