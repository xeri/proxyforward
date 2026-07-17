package e2e

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"proxyforward/internal/agent"
	"proxyforward/internal/config"
	"proxyforward/internal/control"
	"proxyforward/internal/gateway"
)

// freeTCPPort reserves and releases a port, so a test can name a concrete port that
// is free right now.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

// waitPendingInbound polls until the gateway holds an agent-authored proposal.
func waitPendingInbound(t *testing.T, gw *gateway.Gateway, agentID string) *gateway.ProposalView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v := gw.AgentDesiredConfig(agentID); v.PendingInbound != nil {
			return v.PendingInbound
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the gateway never received the agent's proposal")
	return nil
}

// waitIncomingProposal polls until the agent holds a gateway-authored proposal.
func waitIncomingProposal(t *testing.T, a *agent.Agent) *agent.ProposalView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v := a.PendingConsent(); v.Incoming != nil {
			return v.Incoming
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the agent never received the gateway's proposal")
	return nil
}

// TestConfigConsentNegotiated: an enrolled agent and a current gateway settle on
// config-consent, so edits on this link are approval-gated rather than auto-applied.
// (config-consent)
func TestConfigConsentNegotiated(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	h.waitPublicPort()
	waitConfigGen(t, h.gw, 1)

	if !h.agent.ConsentActive() {
		t.Fatal("an enrolled agent on a current gateway must negotiate config-consent")
	}
	rec := waitConfigGen(t, h.gw, 1)
	if v := h.gw.AgentDesiredConfig(rec.AgentID); !v.ConsentActive {
		t.Fatal("the gateway must report the session as consent-capable")
	}
}

// TestConfigConsentAgentEditNeedsApproval: on a consent session an agent's own edit
// applies nothing â€” the gateway holds it for the operator. Only approval adopts it,
// bumps the generation, and pushes it back, which is what brings the tunnel live.
// (config-consent)
func TestConfigConsentAgentEditNeedsApproval(t *testing.T) {
	echoA, closeA := echoServer(t)
	defer closeA()
	h := newHarness(t, echoA)
	h.waitPublicPort()
	rec := waitConfigGen(t, h.gw, 1)

	echoB, closeB := echoServer(t)
	defer closeB()
	portA, ok := h.agent.TunnelPublicPort(h.tunnelID)
	if !ok {
		t.Fatal("tunnel A has no resolved port before the edit")
	}
	tunnelB := config.NewID()
	if _, err := h.agent.ProposeTunnels([]config.Tunnel{
		{ID: h.tunnelID, Name: "test", Type: config.TunnelTCP, LocalAddr: echoA, PublicPort: portA, Enabled: true},
		{ID: tunnelB, Name: "b", Type: config.TunnelTCP, LocalAddr: echoB, PublicPort: 0, Enabled: true},
	}); err != nil {
		t.Fatalf("ProposeTunnels: %v", err)
	}

	pending := waitPendingInbound(t, h.gw, rec.AgentID)
	if len(pending.Tunnels) != 2 {
		t.Fatalf("the held proposal must carry the whole proposed set: %+v", pending.Tunnels)
	}
	// Held, not applied: no adopt, no generation bump, no listener for B.
	time.Sleep(200 * time.Millisecond)
	if agents := h.gw.ListAgents(); agents[0].ConfigGen != 1 {
		t.Fatalf("an unapproved edit must not adopt: generation %d", agents[0].ConfigGen)
	}
	if _, live := h.agent.TunnelPublicPort(tunnelB); live {
		t.Fatal("an unapproved edit must not bring a tunnel live")
	}

	if err := h.gw.ApproveAgentProposal(rec.AgentID, pending.ProposalID); err != nil {
		t.Fatalf("ApproveAgentProposal: %v", err)
	}
	waitConfigGen(t, h.gw, 2)
	roundTrip(t, waitPortForTunnel(t, h.agent, tunnelB), []byte("approved tunnel works"))
	// A stays live on its original port through the approval.
	roundTrip(t, fmt.Sprintf("127.0.0.1:%d", portA), []byte("existing tunnel survives"))
	if v := h.gw.AgentDesiredConfig(rec.AgentID); v.PendingInbound != nil {
		t.Fatal("approval must clear the held proposal")
	}
}

// TestConfigConsentRejectedEditChangesNothing: a declined edit leaves the gateway's
// authoritative set and the agent's running tunnels exactly as they were, and the
// operator's reason reaches the agent verbatim so the no-op save can explain itself.
// (config-consent)
func TestConfigConsentRejectedEditChangesNothing(t *testing.T) {
	echoA, closeA := echoServer(t)
	defer closeA()
	h := newHarness(t, echoA)
	addrA := h.waitPublicPort()
	rec := waitConfigGen(t, h.gw, 1)

	echoB, closeB := echoServer(t)
	defer closeB()
	portA, _ := h.agent.TunnelPublicPort(h.tunnelID)
	tunnelB := config.NewID()
	if _, err := h.agent.ProposeTunnels([]config.Tunnel{
		{ID: h.tunnelID, Name: "test", Type: config.TunnelTCP, LocalAddr: echoA, PublicPort: portA, Enabled: true},
		{ID: tunnelB, Name: "b", Type: config.TunnelTCP, LocalAddr: echoB, PublicPort: 0, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	pending := waitPendingInbound(t, h.gw, rec.AgentID)

	const reason = "that tunnel is not approved for this host"
	if err := h.gw.RejectAgentProposal(rec.AgentID, pending.ProposalID, reason); err != nil {
		t.Fatalf("RejectAgentProposal: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h.agent.PendingConsent().LastDecline == reason {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := h.agent.PendingConsent(); got.LastDecline != reason {
		t.Fatalf("the agent must learn why it was declined, got %+v", got)
	}
	if agents := h.gw.ListAgents(); agents[0].ConfigGen != 1 {
		t.Fatalf("a rejected edit must not adopt: generation %d", agents[0].ConfigGen)
	}
	if _, live := h.agent.TunnelPublicPort(tunnelB); live {
		t.Fatal("a rejected edit must not bring a tunnel live")
	}
	roundTrip(t, addrA, []byte("original tunnel untouched"))
}

// TestConfigConsentGatewayAuthoredEditNeedsAgentApproval: the operator's edit is
// held on the agent until its user accepts. Approval makes the gateway adopt and
// push, and the push is what applies it â€” the same commit point every other
// authoritative change uses. (config-consent)
func TestConfigConsentGatewayAuthoredEditNeedsAgentApproval(t *testing.T) {
	echoA, closeA := echoServer(t)
	defer closeA()
	h := newHarness(t, echoA)
	h.waitPublicPort()
	rec := waitConfigGen(t, h.gw, 1)

	// The operator re-pins the agent's tunnel onto a specific port.
	newPort := freeTCPPort(t)
	if err := h.gw.ProposeAgentConfig(rec.AgentID, []control.TunnelSpec{
		{ID: h.tunnelID, Name: "test", Type: config.TunnelTCP, PublicPort: newPort},
	}); err != nil {
		t.Fatalf("ProposeAgentConfig: %v", err)
	}

	incoming := waitIncomingProposal(t, h.agent)
	if len(incoming.Tunnels) != 1 || incoming.Tunnels[0].PublicPort != newPort {
		t.Fatalf("the agent must see exactly what the operator proposed: %+v", incoming.Tunnels)
	}
	// Held, not applied â€” the operator cannot move the agent unilaterally.
	if agents := h.gw.ListAgents(); agents[0].ConfigGen != 1 {
		t.Fatalf("a gateway proposal must not adopt before the agent agrees: %d", agents[0].ConfigGen)
	}

	if err := h.agent.ApproveGatewayProposal(incoming.ProposalID); err != nil {
		t.Fatalf("ApproveGatewayProposal: %v", err)
	}
	waitConfigGen(t, h.gw, 2)

	// The push commits it end to end: the agent learns the operator's port and serves.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p, ok := h.agent.TunnelPublicPort(h.tunnelID); ok && p == newPort {
			roundTrip(t, fmt.Sprintf("127.0.0.1:%d", newPort), []byte("the operator's port works"))
			if v := h.agent.PendingConsent(); v.Incoming != nil {
				t.Fatal("approval must clear the held proposal")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the approved port %d never went live", newPort)
}

// TestConfigConsentAgentDeclinesGatewayEdit: the agent's refusal is final â€” the
// gateway adopts nothing and records why, so the operator sees the decline instead
// of a change that silently never happened. (config-consent)
func TestConfigConsentAgentDeclinesGatewayEdit(t *testing.T) {
	echoA, closeA := echoServer(t)
	defer closeA()
	h := newHarness(t, echoA)
	addrA := h.waitPublicPort()
	rec := waitConfigGen(t, h.gw, 1)

	if err := h.gw.ProposeAgentConfig(rec.AgentID, []control.TunnelSpec{
		{ID: h.tunnelID, Name: "test", Type: config.TunnelTCP, PublicPort: freeTCPPort(t)},
	}); err != nil {
		t.Fatal(err)
	}
	incoming := waitIncomingProposal(t, h.agent)

	const reason = "that port is my LAN printer"
	if err := h.agent.RejectGatewayProposal(incoming.ProposalID, reason); err != nil {
		t.Fatalf("RejectGatewayProposal: %v", err)
	}

	// The gateway records the decline (with the agent's reason) and adopts nothing.
	deadline := time.Now().Add(10 * time.Second)
	var found *gateway.GatewayEvent
	for time.Now().Before(deadline) && found == nil {
		for _, ev := range h.gw.Events(0) {
			if ev.Kind == gateway.EventProposalDeclined {
				e := ev
				found = &e
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if found == nil {
		t.Fatal("the gateway must record that the agent declined")
	}
	if !strings.Contains(found.Message, reason) {
		t.Fatalf("the decline must carry the agent's reason: %q", found.Message)
	}
	if agents := h.gw.ListAgents(); agents[0].ConfigGen != 1 {
		t.Fatalf("a declined proposal must not adopt: generation %d", agents[0].ConfigGen)
	}
	roundTrip(t, addrA, []byte("declined edit left the tunnel alone"))
}
