package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// pauseSet truth table: nil covers nothing, all covers everything, otherwise
// membership. (pause)
func TestPauseSetCovers(t *testing.T) {
	cases := []struct {
		name string
		set  *pauseSet
		id   string
		want bool
	}{
		{"nil covers nothing", nil, "tnl_a", false},
		{"empty covers nothing", newPauseSet(nil, false), "tnl_a", false},
		{"member covered", newPauseSet([]string{"tnl_a"}, false), "tnl_a", true},
		{"non-member not covered", newPauseSet([]string{"tnl_a"}, false), "tnl_b", false},
		{"all covers member", newPauseSet(nil, true), "tnl_a", true},
		{"all covers stranger", newPauseSet([]string{"tnl_a"}, true), "tnl_zzz", true},
	}
	for _, c := range cases {
		if got := c.set.covers(c.id); got != c.want {
			t.Errorf("%s: covers(%q) = %v, want %v", c.name, c.id, got, c.want)
		}
	}
}

func TestEqualPauseSets(t *testing.T) {
	a := newPauseSet([]string{"tnl_a"}, false)
	if !equalPauseSets(nil, newPauseSet(nil, false)) {
		t.Error("nil and empty must compare equal (both mean nothing paused)")
	}
	if equalPauseSets(a, nil) {
		t.Error("a populated set must not equal nil")
	}
	if !equalPauseSets(a, newPauseSet([]string{"tnl_a"}, false)) {
		t.Error("same members must compare equal")
	}
	if equalPauseSets(a, newPauseSet([]string{"tnl_a"}, true)) {
		t.Error("the all bit must break equality")
	}
}

// The combined listener bit: either side's set pauses; clearing one side's bit
// leaves the other side's pause standing — each side clears only its own, so a
// gateway "resume" can never override the agent's own pause (nor vice versa).
// A listener bound while a bit is already set is born paused. (pause)
func TestRefreshPauseFoldsBothSides(t *testing.T) {
	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	sess := &agentSession{agentID: "agt_pause", scope: Scope{}}
	// The agent's bit exists before any listener: the bind must see it.
	sess.pauseTheirs.Store(newPauseSet([]string{"tnl_p"}, false))
	if _, err := a.admit(sess); err != nil {
		t.Fatalf("admit: %v", err)
	}
	spec := control.TunnelSpec{ID: "tnl_p", Name: "p", Type: config.TunnelTCP, PublicPort: 0}
	if _, err := a.bindTunnel(sess, spec, bindPolicy{bindAddr: "127.0.0.1"}, func(*publicListener, net.Conn) {}); err != nil {
		t.Fatalf("bindTunnel: %v", err)
	}
	pl := func() *publicListener {
		var got *publicListener
		a.do(func() { got = a.listeners[sess.agentID]["tnl_p"] })
		return got
	}()
	if pl == nil {
		t.Fatal("listener not published")
	}
	if !pl.paused.Load() {
		t.Fatal("a listener bound under an existing pause bit must be born paused")
	}

	// The gateway's bit joins; the agent's clears — still paused (the gateway's
	// own bit stands).
	sess.pauseMine.Store(newPauseSet(nil, true))
	sess.pauseTheirs.Store(newPauseSet(nil, false))
	a.do(func() { a.refreshPause(sess) })
	if !pl.paused.Load() {
		t.Fatal("clearing the agent's bit must not clear the gateway's")
	}

	// The gateway's clears too — open.
	sess.pauseMine.Store(newPauseSet(nil, false))
	a.do(func() { a.refreshPause(sess) })
	if pl.paused.Load() {
		t.Fatal("with both sets empty the tunnel must serve players")
	}
}

// The gateway's own bits persist and reload — a restart re-seeds sessions from
// the store at admission. (pause)
func TestAgentStorePauseBitsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadAgentStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := s.IssueEnrollment(false, time.Time{}, Scope{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(pub(1), "agt_pausestore", tk, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !s.SetPause("agt_pausestore", []string{"tnl_x"}, false) {
		t.Fatal("SetPause on an enrolled agent must succeed")
	}
	if s.SetPause("agt_missing", nil, true) {
		t.Fatal("SetPause on an unknown agent must report false")
	}

	reloaded, err := LoadAgentStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids, all, ok := reloaded.PauseBits("agt_pausestore")
	if !ok || all || len(ids) != 1 || ids[0] != "tnl_x" {
		t.Fatalf("PauseBits after reload = %v %v %v, want [tnl_x] false true", ids, all, ok)
	}
}
