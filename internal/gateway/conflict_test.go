package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"reflect"
	"testing"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestReassignCandidates: when a requested public port is in use, the gateway
// offers a policy-valid alternative. Unconstrained → an OS-ephemeral port (0). A
// scope or allowlist confines the alternatives to the allowed set (ascending,
// excluding the requested port); nothing outside policy is ever offered, and an
// exhausted set yields nothing (the caller then hard-fails as before). (conflict, #2)
func TestReassignCandidates(t *testing.T) {
	tests := []struct {
		name      string
		requested int
		scope     Scope
		allowlist []int
		want      []int
	}{
		{"unconstrained falls back to ephemeral", 25565, Scope{}, nil, []int{0}},
		{"allowlist confines and excludes requested", 25565, Scope{}, []int{25567, 25565, 25566}, []int{25566, 25567}},
		{"scope confines to its ports", 25565, Scope{Ports: []int{25565, 25566}}, nil, []int{25566}},
		{"scope intersect allowlist", 25565, Scope{Ports: []int{25565, 25566}}, []int{25566, 25567}, []int{25566}},
		{"ephemeral request never reassigns", 0, Scope{}, nil, nil},
		{"exhausted allowed set yields nothing", 25565, Scope{}, []int{25565}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reassignCandidates(tt.requested, tt.scope, tt.allowlist)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("reassignCandidates(%d, %+v, %v) = %v, want %v", tt.requested, tt.scope, tt.allowlist, got, tt.want)
			}
		})
	}
}

// TestEventRingCapAndCursor: the gateway event ring keeps the most recent events
// up to its cap (dropping oldest), assigns monotonically increasing seqs, and
// since(seq) returns only events newer than the cursor — the incremental-poll
// contract the GUI event log reads. (conflict)
func TestEventRingCapAndCursor(t *testing.T) {
	r := newEventRing(3)
	for i := 0; i < 5; i++ {
		r.push(GatewayEvent{Kind: EventPortReassigned, AgentID: "agt_a"})
	}
	got := r.since(0)
	if len(got) != 3 {
		t.Fatalf("since(0) len = %d, want 3 (capped, oldest dropped)", len(got))
	}
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("seqs = %d..%d, want 3..5", got[0].Seq, got[2].Seq)
	}
	if n := len(r.since(4)); n != 1 {
		t.Fatalf("since(4) len = %d, want 1", n)
	}
	if n := len(r.since(5)); n != 0 {
		t.Fatalf("since(5) len = %d, want 0 (fully drained)", n)
	}
}

// TestBindLockedReassignsBusyPort: a bind to an in-use public port is no longer a
// hard failure — the actor binds a free alternative so the tunnel comes up, keeps
// the tunnel's *requested* spec (so a later reconcile of the same spec is a no-op
// rather than re-contending), and records a port-reassigned event naming both the
// requested and the actual port. (conflict, #2)
func TestBindLockedReassignsBusyPort(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer occupied.Close()
	busy := occupied.Addr().(*net.TCPAddr).Port

	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	sess := &agentSession{agentID: "agt_test", scope: Scope{}}
	if _, err := a.admit(sess); err != nil {
		t.Fatalf("admit: %v", err)
	}
	spec := control.TunnelSpec{ID: "tnl_smp", Name: "smp", Type: config.TunnelTCP, PublicPort: busy}
	got, err := a.bindTunnel(sess, spec, "127.0.0.1", nil, func(*publicListener, net.Conn) {})
	if err != nil {
		t.Fatalf("bindTunnel on busy port should reassign, got err: %v", err)
	}
	if got == busy {
		t.Fatalf("expected reassignment off the busy port %d, got the same port", busy)
	}
	if got == 0 {
		t.Fatalf("reassigned port must be concrete, got 0")
	}

	// The snapshot keeps the requested port so a reconcile of the same spec is a no-op.
	var snap TunnelSnapshot
	for _, ts := range a.tunnels() {
		if ts.ID == "tnl_smp" {
			snap = ts
		}
	}
	if snap.RequestedPort != busy {
		t.Fatalf("snapshot RequestedPort = %d, want the requested %d", snap.RequestedPort, busy)
	}
	if snap.PublicPort != got {
		t.Fatalf("snapshot PublicPort = %d, want the actual bound %d", snap.PublicPort, got)
	}

	var ev *GatewayEvent
	for i, e := range a.eventsSince(0) {
		if e.Kind == EventPortReassigned {
			ev = &a.eventsSince(0)[i]
		}
	}
	if ev == nil {
		t.Fatalf("expected a %q event", EventPortReassigned)
	}
	if ev.RequestedPort != busy || ev.ActualPort != got {
		t.Fatalf("event ports = requested %d / actual %d, want %d / %d", ev.RequestedPort, ev.ActualPort, busy, got)
	}
}

// TestAdmitFlagsSuspectedClone: two machines from different IPs rapidly contesting
// the same agentID (the signature of a shared/cloned key) supersede as before but
// also raise a clone-suspected event, so the GUI can nudge the user to re-enroll.
// A single supersede (a laptop that changed networks) does NOT flag — one
// reconnect from a new IP is normal, only a rapid two-sided contest is a clone. (conflict, #1)
func TestAdmitFlagsSuspectedClone(t *testing.T) {
	mk := func(ip string) *agentSession { return &agentSession{agentID: "agt_x", remoteIP: ip} }

	t.Run("rapid two-sided contest flags", func(t *testing.T) {
		a := newActor(discardLogger())
		ctx, cancel := context.WithCancel(context.Background())
		go a.run(ctx)
		t.Cleanup(cancel)
		if _, err := a.admit(mk("1.1.1.1")); err != nil {
			t.Fatalf("admit 1: %v", err)
		}
		if _, err := a.admit(mk("2.2.2.2")); err != nil { // supersede #1, no flag yet
			t.Fatalf("admit 2: %v", err)
		}
		if _, err := a.admit(mk("1.1.1.1")); err != nil { // supersede #2 rapid + different IP → flag
			t.Fatalf("admit 3: %v", err)
		}
		if !hasEventKind(a.eventsSince(0), EventCloneSuspected) {
			t.Fatalf("expected a %q event after a rapid two-sided contest", EventCloneSuspected)
		}
	})

	t.Run("single supersede from a new IP does not flag", func(t *testing.T) {
		a := newActor(discardLogger())
		ctx, cancel := context.WithCancel(context.Background())
		go a.run(ctx)
		t.Cleanup(cancel)
		if _, err := a.admit(mk("1.1.1.1")); err != nil {
			t.Fatalf("admit 1: %v", err)
		}
		if _, err := a.admit(mk("2.2.2.2")); err != nil { // network change: one supersede
			t.Fatalf("admit 2: %v", err)
		}
		if hasEventKind(a.eventsSince(0), EventCloneSuspected) {
			t.Fatalf("a single supersede from a new IP must not look like a clone")
		}
	})
}

func hasEventKind(evs []GatewayEvent, kind string) bool {
	for _, e := range evs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}
