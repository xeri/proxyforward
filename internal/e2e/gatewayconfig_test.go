package e2e

import (
	"fmt"
	"testing"
	"time"

	"proxyforward/internal/agent"
	"proxyforward/internal/config"
	"proxyforward/internal/gateway"
)

// waitConfigGen polls until the gateway's single enrolled agent has reached (at least)
// the given authoritative config generation, returning its record.
func waitConfigGen(t *testing.T, gw *gateway.Gateway, want uint64) gateway.AgentRecord {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		agents := gw.ListAgents()
		if len(agents) == 1 && agents[0].ConfigGen >= want {
			return agents[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("gateway never reached config generation %d", want)
	return gateway.AgentRecord{}
}

// waitPortForTunnel polls until the agent has learned a specific tunnel's public port.
func waitPortForTunnel(t *testing.T, a *agent.Agent, tunnelID string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if port, ok := a.TunnelPublicPort(tunnelID); ok {
			return fmt.Sprintf("127.0.0.1:%d", port)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tunnel %s never became live", tunnelID)
	return ""
}

// TestGatewayConfigSeed: an enrolled agent with no prior generation seeds the gateway
// on first contact; the gateway adopts the set as generation 1, stores it with the
// resolved (concrete) public port, and pushes it back so the tunnel goes live.
// (gateway-config)
func TestGatewayConfigSeed(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarnessWith(t, echoAddr, harnessOpts{enroll: true})
	addr := h.waitPublicPort()

	rec := waitConfigGen(t, h.gw, 1)
	if rec.ConfigGen != 1 {
		t.Fatalf("want generation 1 after seed, got %d", rec.ConfigGen)
	}
	if len(rec.DesiredTunnels) != 1 || rec.DesiredTunnels[0].ID != h.tunnelID {
		t.Fatalf("gateway did not store the seeded tunnel: %+v", rec.DesiredTunnels)
	}
	// The seed carried an ephemeral (0) port; the stored authoritative set must carry
	// the concrete port the gateway bound, so a reconnect rebinds the same port.
	if rec.DesiredTunnels[0].PublicPort == 0 {
		t.Fatal("stored config must carry the resolved port, not 0")
	}
	roundTrip(t, addr, []byte("seeded config works"))
}

// TestGatewayConfigPromote: a local edit is promoted to the gateway, which adopts it
// (generation bumps to 2), resolves the added tunnel's port, and pushes the set back
// so the new tunnel goes live — deterministic reconciliation, not last-write-wins.
// (gateway-config)
func TestGatewayConfigPromote(t *testing.T) {
	echoA, closeA := echoServer(t)
	defer closeA()
	h := newHarnessWith(t, echoA, harnessOpts{enroll: true})
	h.waitPublicPort()
	waitConfigGen(t, h.gw, 1)

	echoB, closeB := echoServer(t)
	defer closeB()

	// Promote adding tunnel B. Keep A pinned at its resolved port so it does not flap.
	portA, ok := h.agent.TunnelPublicPort(h.tunnelID)
	if !ok {
		t.Fatal("tunnel A has no resolved port before promote")
	}
	tunnelB := config.NewID()
	h.agent.ApplyTunnels([]config.Tunnel{
		{ID: h.tunnelID, Name: "test", Type: config.TunnelTCP, LocalAddr: echoA, PublicPort: portA, Enabled: true},
		{ID: tunnelB, Name: "b", Type: config.TunnelTCP, LocalAddr: echoB, PublicPort: 0, Enabled: true},
	})

	rec := waitConfigGen(t, h.gw, 2)
	if len(rec.DesiredTunnels) != 2 {
		t.Fatalf("gateway did not adopt both tunnels at gen %d: %+v", rec.ConfigGen, rec.DesiredTunnels)
	}
	addrB := waitPortForTunnel(t, h.agent, tunnelB)
	roundTrip(t, addrB, []byte("promoted tunnel works"))
	// A stays live on its original port through the promote.
	roundTrip(t, fmt.Sprintf("127.0.0.1:%d", portA), []byte("existing tunnel survives"))
}

// TestGatewayConfigDriftOnReconnect: after an agent restart its in-memory generation
// resets, so its hello reports a stale view; the gateway detects the drift against its
// stored authoritative set and pushes it, bringing the tunnel back live without
// re-adopting (the generation must not churn). (gateway-config)
func TestGatewayConfigDriftOnReconnect(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarnessWith(t, echoAddr, harnessOpts{enroll: true})
	h.waitPublicPort()
	waitConfigGen(t, h.gw, 1)

	// Restart: same identity (the key persists in agentDir), fresh in-memory state.
	h.stopAgent()
	h.startAgent()

	// The gateway re-pushes its authoritative gen-1 set; the tunnel comes back live.
	addr := h.waitPublicPort()
	roundTrip(t, addr, []byte("resynced after reconnect"))

	// The reconnect was a re-push, not a re-adopt: the generation is unchanged. Give a
	// beat for any stray seed to be processed (and refused) before asserting.
	time.Sleep(200 * time.Millisecond)
	agents := h.gw.ListAgents()
	if len(agents) != 1 || agents[0].ConfigGen != 1 {
		t.Fatalf("reconnect must not bump the generation: %+v", agents)
	}
}

// TestGatewayConfigScopeNarrowingHidesTunnel: narrowing an agent's scope after a
// tunnel was adopted must not leave the agent showing a phantom tunnel. On the next
// reconnect the gateway reconciles and pushes only the in-scope set, so the
// now-out-of-scope tunnel is neither bound here nor learned by the agent. (gateway-config)
func TestGatewayConfigScopeNarrowingHidesTunnel(t *testing.T) {
	echoA, closeA := echoServer(t)
	defer closeA()
	h := newHarnessWith(t, echoA, harnessOpts{enroll: true})
	h.waitPublicPort()
	rec := waitConfigGen(t, h.gw, 1)

	// Adopt a second tunnel B alongside A (A pinned to its resolved port so it does
	// not flap through the promote).
	echoB, closeB := echoServer(t)
	defer closeB()
	portA, ok := h.agent.TunnelPublicPort(h.tunnelID)
	if !ok {
		t.Fatal("tunnel A has no resolved port before promote")
	}
	tunnelB := config.NewID()
	h.agent.ApplyTunnels([]config.Tunnel{
		{ID: h.tunnelID, Name: "test", Type: config.TunnelTCP, LocalAddr: echoA, PublicPort: portA, Enabled: true},
		{ID: tunnelB, Name: "b", Type: config.TunnelTCP, LocalAddr: echoB, PublicPort: 0, Enabled: true},
	})
	waitConfigGen(t, h.gw, 2)
	waitPortForTunnel(t, h.agent, tunnelB)

	// Narrow the agent's scope to tunnel A only, then reconnect it.
	if !h.gw.SetAgentScope(rec.AgentID, gateway.Scope{TunnelIDs: []string{h.tunnelID}}) {
		t.Fatal("SetAgentScope reported the agent was not found")
	}
	h.stopAgent()
	h.startAgent()

	// A comes back live; B must be neither pushed to the agent nor bound.
	h.waitPublicPort()
	roundTrip(t, fmt.Sprintf("127.0.0.1:%d", portA), []byte("in-scope tunnel survives"))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.agent.TunnelPublicPort(tunnelB); !ok {
			return // B correctly excluded from the pushed set
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("out-of-scope tunnel B was still pushed to the agent after scope narrowing")
}
