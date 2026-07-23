package gateway

import (
	"context"
	"net"
	"strconv"
	"testing"

	"proxyforward/internal/config"
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

	g := &Gateway{cfg: config.Default(), logger: discardLogger()}
	g.cfg.Gateway.BindAddr = "127.0.0.1" // never bind test listeners on every interface
	g.agents = store
	g.actor.Store(a)
	return &groupFixture{g: g, a: a, store: store}
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
