package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"testing"

	"proxyforward/internal/config"
	"proxyforward/internal/gateway"
)

// startedGateway builds and starts a gateway-role Gateway on an ephemeral control
// port — enough for the group admin ops to reach a live actor and store.
func startedGateway(t *testing.T) *gateway.Gateway {
	t.Helper()
	cfg := config.Default()
	cfg.Role = config.RoleGateway
	cfg.Gateway.BindAddr = "127.0.0.1"
	cfg.Gateway.ControlPort = 0
	gw := gateway.New(cfg, t.TempDir(), slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	if err := gw.Start(ctx); err != nil {
		cancel()
		t.Fatalf("gateway start: %v", err)
	}
	t.Cleanup(func() { cancel(); gw.Shutdown() })
	return gw
}

func freePortEngine(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

// TestGroupAdminOpsRoundTrip drives the group ops through the same JSON envelope the
// GUI uses (agentAdminOp), proving the engine dispatch, field shapes, and gateway
// wiring: create → list → reject-bad → delete.
func TestGroupAdminOpsRoundTrip(t *testing.T) {
	e := &Engine{Gateway: startedGateway(t)}

	// Create a group with an empty id — the op mints one and echoes it back.
	port := freePortEngine(t)
	setBody, _ := json.Marshal(map[string]any{"name": "cluster", "publicPort": port, "policy": "spread"})
	raw, err := e.agentAdminOp(OpSetGroup, setBody)
	if err != nil {
		t.Fatalf("set_group: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("set_group returned no id: raw=%s err=%v", raw, err)
	}

	// List groups → the new group appears with its live (empty) member set.
	raw, err = e.agentAdminOp(OpListGroups, nil)
	if err != nil {
		t.Fatalf("list_groups: %v", err)
	}
	var groups []gateway.GroupStatus
	if err := json.Unmarshal(raw, &groups); err != nil {
		t.Fatalf("decode list_groups: %v", err)
	}
	if len(groups) != 1 || groups[0].Spec.ID != created.ID || groups[0].Spec.PublicPort != port {
		t.Fatalf("list_groups = %+v, want one group %s on port %d", groups, created.ID, port)
	}

	// A bad policy is rejected verbatim (the error surfaces to the GUI).
	badBody, _ := json.Marshal(map[string]any{"name": "x", "publicPort": freePortEngine(t), "policy": "bogus"})
	if _, err := e.agentAdminOp(OpSetGroup, badBody); err == nil {
		t.Fatal("set_group with a bad policy must error")
	}

	// Delete → the group is gone.
	delBody, _ := json.Marshal(map[string]string{"id": created.ID})
	raw, err = e.agentAdminOp(OpDeleteGroup, delBody)
	if err != nil {
		t.Fatalf("delete_group: %v", err)
	}
	var okr struct {
		OK bool `json:"ok"`
	}
	json.Unmarshal(raw, &okr)
	if !okr.OK {
		t.Fatal("delete_group ok=false for an existing group")
	}
	raw, _ = e.agentAdminOp(OpListGroups, nil)
	groups = nil
	json.Unmarshal(raw, &groups)
	if len(groups) != 0 {
		t.Fatalf("group not removed after delete: %+v", groups)
	}
}
