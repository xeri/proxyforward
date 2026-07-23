package gateway

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/conntrack"
	"proxyforward/internal/control"
	"proxyforward/internal/relay"
)

// groupFixture is a gateway with a live actor and an enrollment store, the minimum
// to drive SetGroup/DeleteGroup and the group socket lifecycle.
type groupFixture struct {
	g     *Gateway
	a     *actor
	store *AgentStore
}

func newGroupFixture(t *testing.T) *groupFixture {
	t.Helper()
	store, err := LoadAgentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	g := &Gateway{cfg: config.Default(), logger: discardLogger(), Conns: conntrack.NewRegistry()}
	g.cfg.Gateway.BindAddr = "127.0.0.1" // never bind test listeners on every interface
	g.gate = newConnGate(g.cfg.Gateway.MaxConnsGlobal, g.cfg.Gateway.MaxConnsPerIP)
	g.agents = store
	g.actor.Store(a)
	return &groupFixture{g: g, a: a, store: store}
}

// admit builds and admits a bare agent session (unrestricted scope), the minimum a
// membership test needs — reconcileGroupMembers keys off admission, not a live
// transport (that matters only for the pick, Phase 4).
func (f *groupFixture) admit(t *testing.T, id string) *agentSession {
	t.Helper()
	s := &agentSession{agentID: id, scope: Scope{}, logger: discardLogger()}
	if _, err := f.a.admit(s); err != nil {
		t.Fatalf("admit %s: %v", id, err)
	}
	return s
}

// snapOrder reads a group's snapshot member identities in sorted (joinOrdinal)
// order, on the actor goroutine so it never races the copy-on-write swap.
func (f *groupFixture) snapOrder(groupID string) []agentTun {
	var out []agentTun
	f.a.do(func() {
		gl := f.a.groupListeners[groupID]
		if gl == nil {
			return
		}
		for _, m := range *gl.snap.Load() {
			out = append(out, agentTun{AgentID: m.sess.agentID, TunnelID: m.tunnelID})
		}
	})
	return out
}

// ordinalOf returns a member's joinOrdinal (0 if absent), read on the actor goroutine.
func (f *groupFixture) ordinalOf(groupID string, key agentTun) uint64 {
	var ord uint64
	f.a.do(func() {
		if gl := f.a.groupListeners[groupID]; gl != nil {
			if m, ok := gl.members[key]; ok {
				ord = m.joinOrdinal
			}
		}
	})
	return ord
}

// bindSolo binds a plain solo listener for sess on port, as the pre-group state a
// conflict/convert test starts from.
func (f *groupFixture) bindSolo(t *testing.T, sess *agentSession, tunnelID string, port int) {
	t.Helper()
	spec := control.TunnelSpec{ID: tunnelID, Name: tunnelID, Type: config.TunnelTCP, PublicPort: port}
	if _, err := f.a.bindTunnel(sess, spec, bindPolicy{bindAddr: "127.0.0.1"}, func(*publicListener, net.Conn) {}); err != nil {
		t.Fatalf("bind solo %s: %v", tunnelID, err)
	}
}

// soloHolder reports which (agentID, tunnelID) solo listener holds a TCP port, or
// ("","") when none does. Read on the actor goroutine.
func (f *groupFixture) soloHolder(port int) (string, string) {
	var agentID, tunnelID string
	f.a.do(func() {
		if sess, tid := f.a.portHolder(port, config.TunnelTCP); sess != nil {
			agentID, tunnelID = sess.agentID, tid
		}
	})
	return agentID, tunnelID
}

// portIsFree reports whether a fresh listener can take the port right now.
func portIsFree(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// TestGroupSocketBoundAtConfigTime: a group's shared socket is bound when the group
// is configured (not on first member) and closed only at DeleteGroup, and the port
// is provably free the instant DeleteGroup returns (the group ghost-listener
// guarantee: one accept loop, closed and awaited on the actor goroutine). (groups)
func TestGroupSocketBoundAtConfigTime(t *testing.T) {
	f := newGroupFixture(t)
	port := freePort(t)

	if err := f.g.SetGroup(GroupSpec{ID: "grp_a", Name: "A", PublicPort: port, Proto: "tcp", Policy: PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	// Configured and listed, with no member yet — an empty-but-configured group.
	if gs := f.g.ListGroups(); len(gs) != 1 || gs[0].ID != "grp_a" {
		t.Fatalf("ListGroups = %+v", gs)
	}
	// The socket is up: nothing else can take the port.
	if portIsFree(port) {
		t.Fatalf("group socket is not bound on port %d", port)
	}

	// DeleteGroup closes it and the port is free the moment it returns.
	if !f.g.DeleteGroup("grp_a") {
		t.Fatal("DeleteGroup should report the group was found")
	}
	if !portIsFree(port) {
		t.Fatalf("group port %d not free immediately after DeleteGroup (ghost listener)", port)
	}
	if len(f.g.ListGroups()) != 0 {
		t.Fatalf("group not removed from the store: %+v", f.g.ListGroups())
	}
}

// tag is a group-tagged desired tunnel spec for a member.
func tag(tunnelID, groupID string) control.TunnelSpec {
	return control.TunnelSpec{ID: tunnelID, Name: tunnelID, Type: config.TunnelTCP, GroupID: groupID}
}

// TestGroupJoinStampsMonotonicOrdinals: joins stamp increasing ordinals; a re-sync
// keeps a member's ordinal (standby priority is not reset by a benign reconcile); and
// a member that leaves and rejoins gets a new, HIGHER ordinal — so a recovered
// ex-primary never reclaims primary (standby no-failback, by construction). (groups)
func TestGroupJoinStampsMonotonicOrdinals(t *testing.T) {
	f := newGroupFixture(t)
	if err := f.g.SetGroup(GroupSpec{ID: "grp_a", PublicPort: freePort(t), Proto: "tcp", Policy: PolicyStandby}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	sessA := f.admit(t, "agt_a")
	sessB := f.admit(t, "agt_b")
	keyA := agentTun{AgentID: "agt_a", TunnelID: "t_a"}
	keyB := agentTun{AgentID: "agt_b", TunnelID: "t_b"}

	f.a.reconcileGroupMembers(sessA, []control.TunnelSpec{tag("t_a", "grp_a")})
	f.a.reconcileGroupMembers(sessB, []control.TunnelSpec{tag("t_b", "grp_a")})
	if o := f.ordinalOf("grp_a", keyA); o != 1 {
		t.Fatalf("first join ordinal = %d, want 1", o)
	}
	if o := f.ordinalOf("grp_a", keyB); o != 2 {
		t.Fatalf("second join ordinal = %d, want 2", o)
	}
	// The sorted snapshot puts the lowest ordinal (the standby primary) first.
	if got := f.snapOrder("grp_a"); len(got) != 2 || got[0] != keyA || got[1] != keyB {
		t.Fatalf("snap order = %+v, want [A, B]", got)
	}

	// A benign re-sync must not re-stamp A's ordinal.
	f.a.reconcileGroupMembers(sessA, []control.TunnelSpec{tag("t_a", "grp_a")})
	if o := f.ordinalOf("grp_a", keyA); o != 1 {
		t.Fatalf("re-sync changed A's ordinal to %d, want a stable 1", o)
	}

	// A leaves (disconnect) and rejoins with a fresh session: its new ordinal is the
	// highest, so B (ordinal 2) is now the standby primary — no failback to A.
	f.a.disconnected(sessA)
	sessA2 := f.admit(t, "agt_a")
	f.a.reconcileGroupMembers(sessA2, []control.TunnelSpec{tag("t_a", "grp_a")})
	if o := f.ordinalOf("grp_a", keyA); o != 3 {
		t.Fatalf("rejoin ordinal = %d, want a new higher 3 (no-failback)", o)
	}
	if got := f.snapOrder("grp_a"); len(got) != 2 || got[0] != keyB {
		t.Fatalf("after rejoin, standby primary should be B (lowest ordinal), snap = %+v", got)
	}
}

// TestGroupEvictionRemovesMemberFromSnap: evicting one member drops it from the
// group snapshot while the shared socket stays up and the other members keep serving —
// the group form of eviction isolation. (groups)
func TestGroupEvictionRemovesMemberFromSnap(t *testing.T) {
	f := newGroupFixture(t)
	port := freePort(t)
	if err := f.g.SetGroup(GroupSpec{ID: "grp_a", PublicPort: port, Proto: "tcp", Policy: PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	sessA := f.admit(t, "agt_a")
	sessB := f.admit(t, "agt_b")
	f.a.reconcileGroupMembers(sessA, []control.TunnelSpec{tag("t_a", "grp_a")})
	f.a.reconcileGroupMembers(sessB, []control.TunnelSpec{tag("t_b", "grp_a")})
	if got := f.snapOrder("grp_a"); len(got) != 2 {
		t.Fatalf("expected 2 members before eviction, got %+v", got)
	}

	f.a.disconnected(sessA) // evict A
	got := f.snapOrder("grp_a")
	if len(got) != 1 || got[0] != (agentTun{AgentID: "agt_b", TunnelID: "t_b"}) {
		t.Fatalf("after evicting A, snap = %+v, want only B", got)
	}
	// The shared socket is untouched: still bound.
	if portIsFree(port) {
		t.Fatalf("group socket dropped when a member was evicted (port %d free)", port)
	}
}

// TestGroupUntagReclaimsToSolo: an untagged tunnel leaves the group and reverts to a
// solo listener on its own port (declared-port reclaim on untag). (groups)
func TestGroupUntagReclaimsToSolo(t *testing.T) {
	f := newGroupFixture(t)
	if err := f.g.SetGroup(GroupSpec{ID: "grp_a", PublicPort: freePort(t), Proto: "tcp", Policy: PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	sess := f.admit(t, "agt_a")
	f.a.reconcileGroupMembers(sess, []control.TunnelSpec{tag("t_a", "grp_a")})
	if got := f.snapOrder("grp_a"); len(got) != 1 {
		t.Fatalf("expected the member to join, got %+v", got)
	}
	// Untag: an empty grouped set means the member left the group.
	f.a.reconcileGroupMembers(sess, nil)
	if got := f.snapOrder("grp_a"); len(got) != 0 {
		t.Fatalf("after untag the group should be empty, got %+v", got)
	}
}

// TestGroupPortConflictRejected: SetGroup onto a port held by a non-member listener
// is rejected (no group created, the incumbent listener untouched — a live listener
// is never stolen), while the convert-in-the-same-update flow — the port's holder is
// a tunnel being moved into this very group — succeeds by tearing the solo listener
// down as the group binds. (groups)
func TestGroupPortConflictRejected(t *testing.T) {
	// (a) Rejection: a non-member solo tunnel holds the port.
	f := newGroupFixture(t)
	port := freePort(t)
	solo := f.admit(t, "agt_solo")
	f.bindSolo(t, solo, "t_solo", port)

	err := f.g.SetGroup(GroupSpec{ID: "grp_x", PublicPort: port, Proto: "tcp", Policy: PolicySpread})
	if err == nil {
		t.Fatal("SetGroup onto a port held by a non-member listener must be rejected")
	}
	if len(f.g.ListGroups()) != 0 {
		t.Fatalf("a rejected SetGroup must not create the group (store rollback): %+v", f.g.ListGroups())
	}
	if a, tid := f.soloHolder(port); a != "agt_solo" || tid != "t_solo" {
		t.Fatalf("the incumbent solo listener was disturbed: holder=(%q,%q)", a, tid)
	}

	// (b) Convert: the holder is a tunnel tagged into the group being created.
	f2 := newGroupFixture(t)
	port2 := freePort(t)
	conv := f2.admit(t, "agt_conv")
	// The member's stored config tags t_conv into grp_y, and it is currently bound solo
	// on port2 — the state before the group exists, which makes it "movable into" grp_y.
	seedAgent(f2.store, "agt_conv", control.TunnelSpec{ID: "t_conv", Name: "t_conv", Type: config.TunnelTCP, PublicPort: port2, GroupID: "grp_y"})
	f2.bindSolo(t, conv, "t_conv", port2)

	if err := f2.g.SetGroup(GroupSpec{ID: "grp_y", PublicPort: port2, Proto: "tcp", Policy: PolicySpread}); err != nil {
		t.Fatalf("convert-in-same-update flow must succeed, got: %v", err)
	}
	if len(f2.g.ListGroups()) != 1 {
		t.Fatalf("group should exist after a successful convert: %+v", f2.g.ListGroups())
	}
	// The solo listener was torn down as the group bound: no solo holder, group up.
	if a, tid := f2.soloHolder(port2); a != "" || tid != "" {
		t.Fatalf("the moved-in solo listener should be gone, still held by (%q,%q)", a, tid)
	}
	if portIsFree(port2) {
		t.Fatalf("group socket not bound on the converted port %d", port2)
	}
}

// TestGroupRemovalDrainsInFlightLegs: DeleteGroup closes the shared socket AND drains
// the group's in-flight player legs (closing the tracked client conn unwinds the data
// leg), so removal leaves nothing running against a group that no longer exists. Here
// the tracked conn stands in for a live player leg (handleGroupClient populates it for
// real in the routing phase). (groups)
func TestGroupRemovalDrainsInFlightLegs(t *testing.T) {
	f := newGroupFixture(t)
	port := freePort(t)
	if err := f.g.SetGroup(GroupSpec{ID: "grp_a", PublicPort: port, Proto: "tcp", Policy: PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	player, gatewaySide := net.Pipe()
	defer player.Close()
	// Register the gateway-held player leg as in-flight on the group.
	f.a.do(func() { f.a.groupListeners["grp_a"].conns.Store("conn1", gatewaySide) })

	if !f.g.DeleteGroup("grp_a") {
		t.Fatal("DeleteGroup should report the group was found")
	}
	// The socket is closed (port free) and the in-flight leg was drained (a write to
	// the drained end fails).
	if !portIsFree(port) {
		t.Fatalf("group socket still bound after DeleteGroup on port %d", port)
	}
	gatewaySide.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := gatewaySide.Write([]byte("x")); err == nil {
		t.Fatal("in-flight player leg was not drained on DeleteGroup")
	}
}

// TestGroupBindsConfiguredGroupsAtStartup: a group already in the store (from a prior
// run) is bound by bindConfiguredGroups before any agent connects, so it claims its
// fixed port ahead of the solo tunnels that reassign around it. (groups)
func TestGroupBindsConfiguredGroupsAtStartup(t *testing.T) {
	f := newGroupFixture(t)
	port := freePort(t)
	// Persist a group directly (as a prior run would have), without binding.
	if err := f.store.SetGroup(GroupSpec{ID: "grp_boot", PublicPort: port, Proto: "tcp", Policy: PolicyStandby}); err != nil {
		t.Fatalf("store.SetGroup: %v", err)
	}
	if !portIsFree(port) {
		t.Fatalf("port %d should be free before startup bind", port)
	}
	f.g.bindConfiguredGroups()
	if portIsFree(port) {
		t.Fatalf("bindConfiguredGroups did not bind grp_boot on port %d", port)
	}
}

// failDP is a data plane whose openFlow always fails, counting its calls — for
// exercising the bounded re-pick without a live agent.
type failDP struct{ calls *atomic.Int64 }

func (d failDP) openFlow(*agentSession, string) (relay.Conn, func(), error) {
	d.calls.Add(1)
	return nil, nil, errNoDataLeg
}

// joinLiveMember admits a session, gives it a live (fake) transport + UP health and a
// data plane, and joins it to the group — the minimum for handleGroupClient to select
// and try it.
func (f *groupFixture) joinLiveMember(t *testing.T, agentID, tunnelID, groupID string, dp dataPlane) *agentSession {
	t.Helper()
	s := f.admit(t, agentID)
	s.setSession(liveSession{})
	s.health.Store(tunnelID, true)
	s.dp = dp
	sctx, cancel := context.WithCancel(context.Background())
	s.ctx, s.cancel = sctx, cancel
	t.Cleanup(cancel)
	f.a.reconcileGroupMembers(s, []control.TunnelSpec{tag(tunnelID, groupID)})
	return s
}

// TestGroupRepickOnOpenFlowFailure: when a chosen member's data leg won't open, the
// player is re-picked onto another member; when every eligible member fails, the group
// falls to its offline responder. Here both members fail, so both are tried (bounded
// re-pick) and then offline answers. (groups)
func TestGroupRepickOnOpenFlowFailure(t *testing.T) {
	f := newGroupFixture(t)
	port := freePort(t)
	if err := f.g.SetGroup(GroupSpec{ID: "grp_a", Name: "grp", PublicPort: port, Proto: "tcp", Policy: PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	var calls atomic.Int64
	f.joinLiveMember(t, "agt_a", "t_a", "grp_a", failDP{calls: &calls})
	f.joinLiveMember(t, "agt_b", "t_b", "grp_a", failDP{calls: &calls})

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial group port: %v", err)
	}
	defer conn.Close()
	// No OfflineMOTD, so handleGroupClient closes after both picks fail: the read
	// returns EOF. Wait for that, then assert both members were tried.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	conn.Read(buf) // expected EOF/close

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("bounded re-pick should try both members exactly once each; openFlow calls = %d, want 2", got)
	}
}

// TestGroupPickRaceUnderChurn: hammer join/leave against the accept goroutine's pick
// to prove the snap copy-on-write is sound (meaningful under -race in CI). (groups)
func TestGroupPickRaceUnderChurn(t *testing.T) {
	f := newGroupFixture(t)
	if err := f.g.SetGroup(GroupSpec{ID: "grp_a", PublicPort: freePort(t), Proto: "tcp", Policy: PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	var gl *groupListener
	f.a.do(func() { gl = f.a.groupListeners["grp_a"] })

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // reader: pick over the live snapshot, as the accept path does
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = pick(PolicySpread, *gl.snap.Load(), nil, &gl.rr)
		}
	}()

	// writer: churn membership on the actor.
	var sessions []*agentSession
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("agt_%d", i)
		tid := fmt.Sprintf("t%d", i)
		s := f.admit(t, id)
		s.setSession(liveSession{})
		s.health.Store(tid, true)
		f.a.reconcileGroupMembers(s, []control.TunnelSpec{tag(tid, "grp_a")})
		sessions = append(sessions, s)
		if i%3 == 0 && i > 0 {
			f.a.disconnected(sessions[i-2]) // leave an earlier one
		}
	}
	close(stop)
	wg.Wait()
}
