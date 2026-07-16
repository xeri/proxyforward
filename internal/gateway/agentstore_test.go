package gateway

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"proxyforward/internal/control"
)

func pub(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// TestAgentStoreEnrollLookupRevoke: a single-use ticket enrolls one agent, is then
// spent, and revoke flips the record. (identity)
func TestAgentStoreEnrollLookupRevoke(t *testing.T) {
	s, err := LoadAgentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ticket, err := s.IssueEnrollment(false, time.Time{}, Scope{})
	if err != nil {
		t.Fatal(err)
	}

	rec, err := s.Enroll(pub(1), "agt_one", ticket, now)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if rec.AgentID != "agt_one" {
		t.Fatalf("record agentID: %q", rec.AgentID)
	}
	if got, ok := s.Lookup(pub(1)); !ok || got.AgentID != "agt_one" || got.Revoked {
		t.Fatalf("lookup after enroll: %+v ok=%v", got, ok)
	}

	// A single-use ticket cannot enroll a second identity.
	if _, err := s.Enroll(pub(2), "agt_two", ticket, now); !errors.Is(err, ErrTicketConsumed) {
		t.Fatalf("reused single-use ticket: want ErrTicketConsumed, got %v", err)
	}

	if !s.Revoke("agt_one") {
		t.Fatal("revoke should report the agent was found")
	}
	if got, _ := s.Lookup(pub(1)); !got.Revoked {
		t.Fatalf("record should be revoked: %+v", got)
	}
	if s.Revoke("agt_missing") {
		t.Fatal("revoking an unknown agent should report not-found")
	}
}

// TestAgentStoreReusableTicket: a reusable ticket enrolls many agents. (identity)
func TestAgentStoreReusableTicket(t *testing.T) {
	s, _ := LoadAgentStore(t.TempDir())
	now := time.Now()
	ticket, err := s.IssueEnrollment(true, time.Time{}, Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(pub(1), "agt_one", ticket, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(pub(2), "agt_two", ticket, now); err != nil {
		t.Fatalf("reusable ticket should enroll again: %v", err)
	}
}

// TestAgentStoreExpiredTicket: an expired ticket is refused. (identity)
func TestAgentStoreExpiredTicket(t *testing.T) {
	s, _ := LoadAgentStore(t.TempDir())
	now := time.Now()
	ticket, _ := s.IssueEnrollment(false, now.Add(-time.Minute), Scope{})
	if _, err := s.Enroll(pub(1), "agt_one", ticket, now); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("want ErrTicketExpired, got %v", err)
	}
	if _, err := s.Enroll(pub(1), "agt_one", "no-such-ticket", now); !errors.Is(err, ErrTicketUnknown) {
		t.Fatalf("want ErrTicketUnknown, got %v", err)
	}
}

// TestAgentStorePersistence: enrollments and spent tickets survive a reload from
// disk. (identity)
func TestAgentStorePersistence(t *testing.T) {
	dir := t.TempDir()
	s1, _ := LoadAgentStore(dir)
	now := time.Now()
	ticket, _ := s1.IssueEnrollment(false, time.Time{}, Scope{Ports: []int{25565}})
	if _, err := s1.Enroll(pub(7), "agt_seven", ticket, now); err != nil {
		t.Fatal(err)
	}

	s2, err := LoadAgentStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Lookup(pub(7))
	if !ok || got.AgentID != "agt_seven" || len(got.Scope.Ports) != 1 || got.Scope.Ports[0] != 25565 {
		t.Fatalf("record did not persist: %+v ok=%v", got, ok)
	}
	// The spent single-use ticket stays spent across the reload.
	if _, err := s2.Enroll(pub(8), "agt_eight", ticket, now); !errors.Is(err, ErrTicketConsumed) {
		t.Fatalf("spent ticket after reload: want ErrTicketConsumed, got %v", err)
	}
}

// TestAgentStoreDesiredConfig: the gateway-authoritative tunnel set is keyed to an
// agent identity, its generation bumps monotonically on each adopt, and both survive
// a reload from disk. (gateway-config)
func TestAgentStoreDesiredConfig(t *testing.T) {
	dir := t.TempDir()
	s, _ := LoadAgentStore(dir)
	now := time.Now()
	ticket, _ := s.IssueEnrollment(false, time.Time{}, Scope{})
	if _, err := s.Enroll(pub(3), "agt_three", ticket, now); err != nil {
		t.Fatal(err)
	}

	// A freshly enrolled agent is known but has no config yet: generation 0.
	if specs, gen, ok := s.DesiredConfig("agt_three"); !ok || gen != 0 || len(specs) != 0 {
		t.Fatalf("fresh agent config: specs=%v gen=%d ok=%v", specs, gen, ok)
	}
	// An unknown agent reports not-found (distinct from the empty-config case).
	if _, _, ok := s.DesiredConfig("agt_missing"); ok {
		t.Fatal("unknown agent must report not-found")
	}

	// Adopting a set bumps the generation from 0 to 1.
	set1 := []control.TunnelSpec{{ID: "tnl_a", Name: "mc", Type: "tcp", PublicPort: 25565}}
	if gen, ok := s.AdoptConfig("agt_three", set1); !ok || gen != 1 {
		t.Fatalf("first adopt: gen=%d ok=%v", gen, ok)
	}
	// A second adopt bumps monotonically to 2.
	set2 := append(append([]control.TunnelSpec(nil), set1...), control.TunnelSpec{ID: "tnl_b", Name: "web", Type: "tcp", PublicPort: 8080})
	if gen, ok := s.AdoptConfig("agt_three", set2); !ok || gen != 2 {
		t.Fatalf("second adopt: gen=%d ok=%v", gen, ok)
	}
	// Adopting for an unknown agent reports not-found without panicking.
	if _, ok := s.AdoptConfig("agt_missing", set1); ok {
		t.Fatal("adopt for unknown agent must report not-found")
	}

	// The stored config and generation survive a reload.
	s2, err := LoadAgentStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	specs, gen, ok := s2.DesiredConfig("agt_three")
	if !ok || gen != 2 || len(specs) != 2 {
		t.Fatalf("config did not persist: specs=%v gen=%d ok=%v", specs, gen, ok)
	}
	if control.HashTunnels(specs) != control.HashTunnels(set2) {
		t.Fatalf("persisted config content drifted: %v", specs)
	}
}

// TestScopeAllows: an empty scope is permissive; a set scope restricts. (scope)
func TestScopeAllows(t *testing.T) {
	if !(Scope{}).AllowsPort(25565) || !(Scope{}).AllowsTunnel("tnl_x") {
		t.Fatal("empty scope must allow anything")
	}
	sc := Scope{Ports: []int{25565}, TunnelIDs: []string{"tnl_x"}}
	if !sc.AllowsPort(25565) || sc.AllowsPort(25566) {
		t.Fatal("port scope enforcement wrong")
	}
	if !sc.AllowsTunnel("tnl_x") || sc.AllowsTunnel("tnl_y") {
		t.Fatal("tunnel scope enforcement wrong")
	}
}
