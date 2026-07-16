package gateway

import (
	"testing"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// TestValidateSpecScope: a per-agent scope restricts which public ports a tunnel
// may bind, an empty scope allows anything, and an ephemeral (0) port is never
// scope-checked (the gateway picks it). (scope)
func TestValidateSpecScope(t *testing.T) {
	g := &Gateway{cfg: config.Default()}
	scoped := Scope{Ports: []int{25565}}

	tcp := func(id string, port int) control.TunnelSpec {
		return control.TunnelSpec{ID: id, Name: id, Type: config.TunnelTCP, PublicPort: port}
	}

	if berr := g.validateSpec(tcp("t1", 25565), scoped); berr != nil {
		t.Fatalf("in-scope port rejected: %v", berr)
	}
	berr := g.validateSpec(tcp("t1", 25566), scoped)
	if berr == nil || berr.code != control.ErrCodePortNotAllowed {
		t.Fatalf("out-of-scope port: want port_not_allowed, got %v", berr)
	}
	if berr := g.validateSpec(tcp("t1", 25566), Scope{}); berr != nil {
		t.Fatalf("empty scope must allow any port: %v", berr)
	}
	if berr := g.validateSpec(tcp("t1", 0), scoped); berr != nil {
		t.Fatalf("ephemeral port must not be scope-checked: %v", berr)
	}

	// Tunnel-ID scope.
	idScope := Scope{TunnelIDs: []string{"tnl_ok"}}
	if berr := g.validateSpec(tcp("tnl_ok", 0), idScope); berr != nil {
		t.Fatalf("in-scope tunnel rejected: %v", berr)
	}
	if berr := g.validateSpec(tcp("tnl_no", 0), idScope); berr == nil || berr.code != control.ErrCodeBadTunnel {
		t.Fatalf("out-of-scope tunnel: want bad_tunnel, got %v", berr)
	}
}
