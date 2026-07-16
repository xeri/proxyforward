package app

import (
	"fmt"

	"proxyforward/internal/engine"
	"proxyforward/internal/gateway"
)

// Agent management (gateway role). These bindings drive the GUI agent roster,
// enrollment codes, and per-agent revoke/rename/scope. Each runs one op over the
// shared JSON envelope (analyticsCall), so it works both in-process and attached
// to a service daemon; all degrade to an empty/error result off the gateway role.

// ListAgents returns the gateway's agent roster: every enrolled agent (online or
// not) plus any connected shared-token agent, with identity, scope, and live
// status. Empty on any error (agent role, detached, old daemon).
func (a *App) ListAgents() []gateway.AgentView {
	var out []gateway.AgentView
	if err := a.analyticsCall(engine.OpListAgents, nil, &out); err != nil || out == nil {
		return []gateway.AgentView{}
	}
	return out
}

// IssuePairingCode mints a pxf:// pairing code carrying a fresh enrollment ticket.
// reusable=false is the single-use default; ttlSecs=0 never expires; ports/tunnels
// (empty = unrestricted) become the enrolling agent's bind scope.
func (a *App) IssuePairingCode(reusable bool, ttlSecs int64, ports []int, tunnels []string) (string, error) {
	req := struct {
		Reusable bool     `json:"reusable"`
		TTLSecs  int64    `json:"ttlSecs"`
		Ports    []int    `json:"ports"`
		Tunnels  []string `json:"tunnels"`
	}{reusable, ttlSecs, ports, tunnels}
	var resp struct {
		Code string `json:"code"`
	}
	if err := a.analyticsCall(engine.OpIssuePairing, req, &resp); err != nil {
		return "", err
	}
	return resp.Code, nil
}

// RevokeAgent removes an agent from the allowlist and evicts its live session, so
// the next connect is a clean "access revoked".
func (a *App) RevokeAgent(agentID string) error {
	return a.agentMutate(engine.OpRevokeAgent, struct {
		AgentID string `json:"agentId"`
	}{agentID})
}

// RenameAgent sets an agent's freely-editable display nickname.
func (a *App) RenameAgent(agentID, nickname string) error {
	return a.agentMutate(engine.OpRenameAgent, struct {
		AgentID  string `json:"agentId"`
		Nickname string `json:"nickname"`
	}{agentID, nickname})
}

// SetAgentScope replaces an agent's bind scope (empty ports+tunnels = unrestricted).
func (a *App) SetAgentScope(agentID string, ports []int, tunnels []string) error {
	return a.agentMutate(engine.OpSetAgentScope, struct {
		AgentID string   `json:"agentId"`
		Ports   []int    `json:"ports"`
		Tunnels []string `json:"tunnels"`
	}{agentID, ports, tunnels})
}

// agentMutate runs a mutating admin op and turns a not-found result (the agentID
// isn't in the allowlist) into an error the UI can surface.
func (a *App) agentMutate(op string, req any) error {
	var resp struct {
		OK bool `json:"ok"`
	}
	if err := a.analyticsCall(op, req, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("that agent is no longer in the roster — it may have already been removed")
	}
	return nil
}

// GatewayEvents returns the gateway's notable auto-fixes and detected conflicts
// (port reassignment, suspected clone) recorded since the cursor — 0 returns all
// retained — for the GUI event log. Empty on any error.
func (a *App) GatewayEvents(sinceSeq uint64) []gateway.GatewayEvent {
	req := struct {
		SinceSeq uint64 `json:"sinceSeq"`
	}{sinceSeq}
	var out []gateway.GatewayEvent
	if err := a.analyticsCall(engine.OpGatewayEvents, req, &out); err != nil || out == nil {
		return []gateway.GatewayEvent{}
	}
	return out
}
