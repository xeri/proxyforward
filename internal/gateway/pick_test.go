package gateway

import (
	"net"
	"sync/atomic"
	"testing"

	"proxyforward/internal/transport"
)

// liveSession is a minimal transport.Session, used only to make a member's
// sess.session() non-nil for eligibility tests — its methods are never exercised.
type liveSession struct{}

func (liveSession) OpenStream() (transport.Stream, error)   { return nil, net.ErrClosed }
func (liveSession) AcceptStream() (transport.Stream, error) { return nil, net.ErrClosed }
func (liveSession) Close() error                            { return nil }
func (liveSession) CloseChan() <-chan struct{}              { return nil }
func (liveSession) RemoteAddr() net.Addr                    { return &net.TCPAddr{} }

// newMember builds an eligible member (live session, backend health UP) with the
// given ordinal and live-connection count.
func newMember(agentID, tunnelID string, ordinal uint64, conns int64) *groupMember {
	sess := &agentSession{agentID: agentID}
	sess.setSession(liveSession{})
	sess.health.Store(tunnelID, true)
	m := &groupMember{sess: sess, tunnelID: tunnelID, joinOrdinal: ordinal}
	m.liveConns.Store(conns)
	return m
}

// TestPickLeastConnections: spread routes to the member with the fewest live
// connections. (groups)
func TestPickLeastConnections(t *testing.T) {
	var rr atomic.Uint64
	members := []*groupMember{
		newMember("a", "ta", 1, 3),
		newMember("b", "tb", 2, 1), // the least loaded
		newMember("c", "tc", 3, 2),
	}
	got := pick(PolicySpread, members, nil, &rr)
	if got == nil || got.tunnelID != "tb" {
		t.Fatalf("least-connections pick = %v, want tb", got)
	}
}

// TestPickSpreadRoundRobinTiebreak: with every member equally loaded, spread rotates
// deterministically over the sorted snapshot, so three picks cover all three members
// exactly once. (groups)
func TestPickSpreadRoundRobinTiebreak(t *testing.T) {
	var rr atomic.Uint64
	members := []*groupMember{
		newMember("a", "ta", 1, 0),
		newMember("b", "tb", 2, 0),
		newMember("c", "tc", 3, 0),
	}
	seen := map[string]int{}
	for i := 0; i < 3; i++ {
		m := pick(PolicySpread, members, nil, &rr)
		seen[m.tunnelID]++
	}
	for _, id := range []string{"ta", "tb", "tc"} {
		if seen[id] != 1 {
			t.Fatalf("round-robin over 3 equal members did not cover each once: %v", seen)
		}
	}
}

// TestPickStandbyLowestOrdinal: standby always picks the lowest-ordinal candidate;
// when it drops out (excluded), the next ordinal takes over (failover). (groups)
func TestPickStandbyLowestOrdinal(t *testing.T) {
	var rr atomic.Uint64
	primary := newMember("a", "ta", 1, 5) // most loaded, but lowest ordinal
	second := newMember("b", "tb", 2, 0)
	third := newMember("c", "tc", 3, 0)
	members := []*groupMember{primary, second, third}

	if got := pick(PolicyStandby, members, nil, &rr); got != primary {
		t.Fatalf("standby must pick the lowest ordinal regardless of load, got %v", got)
	}
	// Primary drops out: failover to the next ordinal.
	if got := pick(PolicyStandby, members, map[*groupMember]bool{primary: true}, &rr); got != second {
		t.Fatalf("standby failover must pick the next ordinal, got %v", got)
	}
}

// TestPickExcludesIneligible: down-health, cold (no report), paused, and
// dead-session members are never returned; excluded (already-tried) members are
// skipped for re-pick. (groups)
func TestPickExcludesIneligible(t *testing.T) {
	var rr atomic.Uint64

	down := newMember("a", "ta", 1, 0)
	down.sess.health.Store("ta", false)

	cold := newMember("b", "tb", 2, 0)
	cold.sess.health.Delete("tb") // never reported

	paused := newMember("c", "tc", 3, 0)
	paused.sess.pauseTheirs.Store(newPauseSet([]string{"tc"}, false))

	dead := newMember("d", "td", 4, 0)
	dead.sess.setSession(nil) // no live session
	// setSession(nil) stores a box whose s is nil, so session() returns nil.

	healthy := newMember("e", "te", 5, 0)

	members := []*groupMember{down, cold, paused, dead, healthy}
	for _, policy := range []string{PolicySpread, PolicyStandby} {
		got := pick(policy, members, nil, &rr)
		if got != healthy {
			t.Fatalf("%s: only the healthy member is eligible, got %v", policy, got)
		}
	}

	// Excluding the sole eligible member yields no pick.
	if got := pick(PolicySpread, members, map[*groupMember]bool{healthy: true}, &rr); got != nil {
		t.Fatalf("excluding the only eligible member should yield nil, got %v", got)
	}
}

// TestEligible: the three gates — live session, neither side paused, positive health. (groups)
func TestEligible(t *testing.T) {
	m := newMember("a", "ta", 1, 0)
	if !eligible(m) {
		t.Fatal("a live, unpaused, healthy member must be eligible")
	}
	// Gateway-side pause covers it.
	m.sess.pauseMine.Store(newPauseSet(nil, true))
	if eligible(m) {
		t.Fatal("an agent-wide gateway pause must make it ineligible")
	}
	m.sess.pauseMine.Store(nil)
	if !eligible(m) {
		t.Fatal("clearing the pause must restore eligibility")
	}
	// Cold health (unknown) is NOT eligible — the group divergence from solo.
	m.sess.health.Delete("ta")
	if eligible(m) {
		t.Fatal("a cold member (no health report) must be ineligible in a group")
	}
}
