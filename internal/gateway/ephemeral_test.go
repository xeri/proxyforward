package gateway

import (
	"context"
	"net"
	"slices"
	"testing"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// TestAllowedPortsNeverHandsOutTheEphemeralRange pins the rule the whole fix
// turns on: 0 is not a port, it is "any port in the ephemeral range". Returning
// it next to a constrained set — or instead of one — means the constraint the
// operator wrote down does not exist.
func TestAllowedPortsNeverHandsOutTheEphemeralRange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scope     Scope
		allowlist []int
		want      []int
	}{{
		name: "unconstrained: any ephemeral port is policy-valid",
		want: []int{0},
	}, {
		name:  "scoped: choose from the scope, never the ephemeral range",
		scope: Scope{Ports: []int{25566, 25565}},
		want:  []int{25565, 25566},
	}, {
		name:      "allowlisted: choose from the allowlist",
		allowlist: []int{25565, 25570},
		want:      []int{25565, 25570},
	}, {
		name:      "both: the scope narrows, the allowlist filters",
		scope:     Scope{Ports: []int{25565, 25566, 25567}},
		allowlist: []int{25566, 25567, 25599},
		want:      []int{25566, 25567},
	}, {
		// The operator allowed one set, the agent was scoped to another, and they
		// do not meet. The honest answer is "nothing", not "have an ephemeral one".
		name:      "disjoint scope and allowlist permit nothing",
		scope:     Scope{Ports: []int{25565}},
		allowlist: []int{25566},
		want:      nil,
	}, {
		name:  "a scope of only invalid ports permits nothing",
		scope: Scope{Ports: []int{0, -1}},
		want:  nil,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := allowedPorts(tc.scope, tc.allowlist)
			if !slices.Equal(got, tc.want) {
				t.Errorf("allowedPorts = %v, want %v", got, tc.want)
			}
			if len(tc.want) > 1 && slices.Contains(got, 0) {
				t.Error("0 was offered alongside concrete ports: it means the whole ephemeral range, so the set is not a constraint")
			}
		})
	}
}

// TestEphemeralRequestStaysInsideScope is the escape itself. An agent's scope is
// the operator's sentence "you may expose this port and no other". validateSpec
// waived both the scope check and the gateway allowlist whenever PublicPort was 0,
// and bindLocked then asked the OS for any free port — so an agent scoped to one
// port bound a public listener on a port nobody allowed, pointed at whatever local
// address it liked, simply by asking for 0. With TunnelIDs unrestricted (the
// default) it could do that once per invented tunnel id, without limit.
//
// reassignCandidates already refused to do this on the fallback path ("a
// constrained gateway must stay inside its allowed set and never hand out an
// arbitrary port"). The primary path just did not ask.
func TestEphemeralRequestStaysInsideScope(t *testing.T) {
	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	// Two allowed ports so the choice is real, and so a bound one still leaves an
	// alternative — this must pick an allowed port, not merely refuse.
	const allowedA, allowedB = 25565, 25566
	sess := &agentSession{agentID: "agt_scoped", scope: Scope{Ports: []int{allowedA, allowedB}}}
	if _, err := a.admit(sess); err != nil {
		t.Fatalf("admit: %v", err)
	}

	spec := control.TunnelSpec{ID: "tnl_x", Name: "x", Type: config.TunnelTCP, PublicPort: 0}
	got, err := a.bindTunnel(sess, spec, bindPolicy{bindAddr: "127.0.0.1"}, func(*publicListener, net.Conn) {})
	if err != nil {
		t.Fatalf("an ephemeral request inside a scope with free ports must bind one: %v", err)
	}
	if got != allowedA && got != allowedB {
		t.Fatalf("ephemeral request bound port %d, outside the agent's scope %v — the scope is decoration", got, sess.scope.Ports)
	}
}

// TestEphemeralRequestRespectsTheGatewayAllowlist: the same waiver skipped the
// operator's gateway-wide allowlist, which is a stronger statement than a scope —
// "no agent may ever bind outside this set".
func TestEphemeralRequestRespectsTheGatewayAllowlist(t *testing.T) {
	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	sess := &agentSession{agentID: "agt_any", scope: Scope{}} // unscoped agent
	if _, err := a.admit(sess); err != nil {
		t.Fatalf("admit: %v", err)
	}
	allowlist := []int{25565, 25566}

	spec := control.TunnelSpec{ID: "tnl_y", Name: "y", Type: config.TunnelTCP, PublicPort: 0}
	got, err := a.bindTunnel(sess, spec, bindPolicy{bindAddr: "127.0.0.1", allowlist: allowlist}, func(*publicListener, net.Conn) {})
	if err != nil {
		t.Fatalf("an ephemeral request with free allowlisted ports must bind one: %v", err)
	}
	if !slices.Contains(allowlist, got) {
		t.Fatalf("ephemeral request bound port %d, outside the gateway allowlist %v", got, allowlist)
	}
}

// TestEphemeralRequestStaysUnconstrainedByDefault: the guard must not cost the
// common case anything. With no scope and no allowlist, 0 still means "any free
// ephemeral port", which is what every default install does.
func TestEphemeralRequestStaysUnconstrainedByDefault(t *testing.T) {
	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	sess := &agentSession{agentID: "agt_free", scope: Scope{}}
	if _, err := a.admit(sess); err != nil {
		t.Fatalf("admit: %v", err)
	}
	spec := control.TunnelSpec{ID: "tnl_z", Name: "z", Type: config.TunnelTCP, PublicPort: 0}
	got, err := a.bindTunnel(sess, spec, bindPolicy{bindAddr: "127.0.0.1"}, func(*publicListener, net.Conn) {})
	if err != nil {
		t.Fatalf("an unconstrained ephemeral bind failed: %v", err)
	}
	if got == 0 {
		t.Fatal("bind reported port 0; the resolved port must be concrete")
	}
}
