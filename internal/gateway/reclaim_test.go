package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// freePort reserves and releases a port, so a test can name a concrete contested
// port that is free right now.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

// reclaimFixture is a gateway with a live actor and an enrollment store holding two
// agents, each with one bound tunnel: the incumbent on the contested port, the
// requester on an ephemeral one — the shape left behind by an auto-reassign.
type reclaimFixture struct {
	g         *Gateway
	a         *actor
	store     *AgentStore
	incumbent *agentSession
	requester *agentSession
	port      int
}

func newReclaimFixture(t *testing.T, requesterEnrolled, incumbentEnrolled bool) *reclaimFixture {
	t.Helper()
	store, err := LoadAgentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for key, id := range map[byte]string{1: "agt_incumbent", 2: "agt_requester"} {
		ticket, err := store.IssueEnrollment(false, time.Time{}, Scope{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Enroll(pub(key), id, ticket, time.Now()); err != nil {
			t.Fatalf("enroll %s: %v", id, err)
		}
	}

	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	g := &Gateway{cfg: config.Default()}
	g.cfg.Gateway.BindAddr = "127.0.0.1" // never bind test listeners on every interface
	g.agents = store
	g.actor.Store(a)

	mk := func(id string, enrolled bool) *agentSession {
		s := &agentSession{agentID: id, scope: Scope{}, enrolled: enrolled, logger: discardLogger()}
		if _, err := a.admit(s); err != nil {
			t.Fatalf("admit %s: %v", id, err)
		}
		return s
	}
	inc := mk("agt_incumbent", incumbentEnrolled)
	req := mk("agt_requester", requesterEnrolled)

	port := freePort(t)
	seed := func(sess *agentSession, tunnelID string, p int) {
		spec := control.TunnelSpec{ID: tunnelID, Name: tunnelID, Type: config.TunnelTCP, PublicPort: p}
		if _, ok := store.AdoptConfig(sess.agentID, []control.TunnelSpec{spec}); !ok {
			t.Fatalf("seed config for %s", sess.agentID)
		}
		if _, err := a.bindTunnel(sess, spec, "127.0.0.1", nil, func(*publicListener, net.Conn) {}); err != nil {
			t.Fatalf("bind %s: %v", tunnelID, err)
		}
	}
	seed(inc, "tnl_incumbent", port)
	seed(req, "tnl_requester", 0)

	return &reclaimFixture{g: g, a: a, store: store, incumbent: inc, requester: req, port: port}
}

// storedPort returns the public port an agent's authoritative config holds for a
// tunnel, plus that config's generation.
func storedPort(t *testing.T, s *AgentStore, agentID, tunnelID string) (int, uint64) {
	t.Helper()
	specs, gen, ok := s.DesiredConfig(agentID)
	if !ok {
		t.Fatalf("no stored config for %s", agentID)
	}
	for _, sp := range specs {
		if sp.ID == tunnelID {
			return sp.PublicPort, gen
		}
	}
	t.Fatalf("tunnel %s missing from %s's config", tunnelID, agentID)
	return 0, 0
}

func findEvent(a *actor, kind string) *GatewayEvent {
	evs := a.eventsSince(0)
	for i := range evs {
		if evs[i].Kind == kind {
			return &evs[i]
		}
	}
	return nil
}

// TestReclaimPort: an operator hands a contested public port to one enrolled agent.
// The enrolled incumbent is bumped off it first (so the port is free before the
// requester binds), both agents' authoritative configs are rewritten and re-adopted
// (a bumped generation is what a push carries), the listeners actually move, and the
// action is recorded as a port-reclaimed event. (#27)
func TestReclaimPort(t *testing.T) {
	f := newReclaimFixture(t, true, true)

	_, incGen0 := storedPort(t, f.store, "agt_incumbent", "tnl_incumbent")
	_, reqGen0 := storedPort(t, f.store, "agt_requester", "tnl_requester")

	ok, err := f.g.ReclaimPort("agt_requester", "tnl_requester", f.port)
	if err != nil {
		t.Fatalf("ReclaimPort: %v", err)
	}
	if !ok {
		t.Fatal("ReclaimPort between two enrolled agents must succeed")
	}

	// The requester now owns the contested port, in the store and on a real listener.
	reqPort, reqGen := storedPort(t, f.store, "agt_requester", "tnl_requester")
	if reqPort != f.port {
		t.Fatalf("requester's stored port = %d, want the reclaimed %d", reqPort, f.port)
	}
	if bound, ok := f.g.TunnelPort("tnl_requester"); !ok || bound != f.port {
		t.Fatalf("requester's listener on %d (ok=%v), want %d", bound, ok, f.port)
	}
	if reqGen <= reqGen0 {
		t.Fatalf("requester's generation = %d, want a bump above %d", reqGen, reqGen0)
	}

	// The incumbent was moved off it — to a concrete ephemeral port, never left
	// contesting the reclaimed one.
	incPort, incGen := storedPort(t, f.store, "agt_incumbent", "tnl_incumbent")
	if incPort == f.port {
		t.Fatalf("incumbent's stored port is still the reclaimed %d", f.port)
	}
	if incPort == 0 {
		t.Fatal("incumbent's stored port must be the concrete port it bound, not 0")
	}
	if bound, ok := f.g.TunnelPort("tnl_incumbent"); !ok || bound == f.port {
		t.Fatalf("incumbent's listener on %d (ok=%v), want any port but %d", bound, ok, f.port)
	}
	if incGen <= incGen0 {
		t.Fatalf("incumbent's generation = %d, want a bump above %d", incGen, incGen0)
	}

	ev := findEvent(f.a, EventPortReclaimed)
	if ev == nil {
		t.Fatalf("expected a %q event", EventPortReclaimed)
	}
	if ev.AgentID != "agt_requester" || ev.TunnelID != "tnl_requester" || ev.RequestedPort != f.port {
		t.Fatalf("event = %+v, want it to name the requester's tunnel and port %d", *ev, f.port)
	}
}

// TestReclaimPortRefusesLegacyParty: the gateway can only rewrite a per-identity
// agent's config — a shared-token agent pushes its own. If either side of the trade
// is legacy the op is refused and nothing moves. (#27)
func TestReclaimPortRefusesLegacyParty(t *testing.T) {
	cases := []struct {
		name                                 string
		requesterEnrolled, incumbentEnrolled bool
	}{
		{"legacy requester", false, true},
		{"legacy incumbent", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newReclaimFixture(t, tc.requesterEnrolled, tc.incumbentEnrolled)

			ok, err := f.g.ReclaimPort("agt_requester", "tnl_requester", f.port)
			if err != nil {
				t.Fatalf("a refusal is not an error: %v", err)
			}
			if ok {
				t.Fatal("reclaim must be refused when either party is a shared-token agent")
			}
			// Nothing moved: the incumbent keeps the port, the requester doesn't get it.
			if incPort, _ := storedPort(t, f.store, "agt_incumbent", "tnl_incumbent"); incPort != f.port {
				t.Fatalf("refused reclaim moved the incumbent to %d", incPort)
			}
			if reqPort, _ := storedPort(t, f.store, "agt_requester", "tnl_requester"); reqPort == f.port {
				t.Fatal("refused reclaim handed the port to the requester anyway")
			}
			if ev := findEvent(f.a, EventPortReclaimed); ev != nil {
				t.Fatalf("a refused reclaim must record no event, got %+v", *ev)
			}
		})
	}
}

// TestAgentDesiredConfigReturnsStoredSet: the drill-in's read-only config panel gets
// the gateway-held tunnel set and its generation for an enrolled agent, and an
// honest Enrolled=false (never a misleading empty set) for a shared-token or unknown
// agent, whose config is agent-pushed. (#29)
func TestAgentDesiredConfigReturnsStoredSet(t *testing.T) {
	store, err := LoadAgentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.IssueEnrollment(false, time.Time{}, Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enroll(pub(1), "agt_one", ticket, time.Now()); err != nil {
		t.Fatal(err)
	}
	specs := []control.TunnelSpec{
		{ID: "tnl_smp", Name: "SMP", Type: config.TunnelTCP, PublicPort: 25565},
		{ID: "tnl_lobby", Name: "Lobby", Type: config.TunnelTCP, PublicPort: 25566},
	}
	gen, ok := store.AdoptConfig("agt_one", specs)
	if !ok {
		t.Fatal("seed AdoptConfig")
	}

	g := &Gateway{cfg: config.Default()}
	g.agents = store

	view := g.AgentDesiredConfig("agt_one")
	if !view.Enrolled {
		t.Fatal("an enrolled agent's config view must report Enrolled")
	}
	if view.Generation != gen {
		t.Fatalf("generation = %d, want the stored %d", view.Generation, gen)
	}
	if len(view.Tunnels) != len(specs) {
		t.Fatalf("got %d tunnels, want %d", len(view.Tunnels), len(specs))
	}
	for i, sp := range view.Tunnels {
		if sp.ID != specs[i].ID || sp.PublicPort != specs[i].PublicPort {
			t.Fatalf("tunnel %d = %+v, want %+v", i, sp, specs[i])
		}
	}

	// A shared-token / unknown agent: nothing gateway-held to show, said honestly.
	unknown := g.AgentDesiredConfig("agentid")
	if unknown.Enrolled {
		t.Fatal("an unknown/shared-token agent must not report Enrolled")
	}
	if len(unknown.Tunnels) != 0 || unknown.Generation != 0 {
		t.Fatalf("unknown agent view = %+v, want the zero value", unknown)
	}
}
