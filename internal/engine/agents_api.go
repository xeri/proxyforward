package engine

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"proxyforward/internal/gateway"
	"proxyforward/internal/link"
)

// Gateway agent-administration op names. Like the analytics ops these are the
// stable wire contract between the GUI and the daemon, dispatched over the same
// JSON envelope so each App binding is one small typed wrapper. They act on the
// live gateway (allowlist + sessions + event ring), never the analytics store.
const (
	OpListAgents    = "list_agents"
	OpIssuePairing  = "issue_pairing"
	OpRevokeAgent   = "revoke_agent"
	OpRenameAgent   = "rename_agent"
	OpSetAgentScope = "set_agent_scope"
	OpGatewayEvents = "gateway_events"
)

// isAgentAdminOp reports whether op is a gateway agent-administration op, so
// analyticsOp can route it before the analytics-store availability check.
func isAgentAdminOp(op string) bool {
	switch op {
	case OpListAgents, OpIssuePairing, OpRevokeAgent, OpRenameAgent, OpSetAgentScope, OpGatewayEvents:
		return true
	}
	return false
}

// okResult is the response for a mutating admin op: whether the target agent was
// found (a rename/revoke/scope of an unknown agentID reports ok=false).
type okResult struct {
	OK bool `json:"ok"`
}

// agentAdminOp dispatches one gateway agent-administration op. Every op requires
// the gateway role; on an agent-role daemon it reports so rather than panicking.
func (e *Engine) agentAdminOp(op string, body json.RawMessage) (json.RawMessage, error) {
	if e.Gateway == nil {
		return nil, fmt.Errorf("agent administration is only available on the gateway")
	}
	switch op {
	case OpListAgents:
		return encodeResult(e.Gateway.ListAgentViews(), nil)
	case OpGatewayEvents:
		var q struct {
			SinceSeq uint64 `json:"sinceSeq"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(e.Gateway.Events(q.SinceSeq), nil)
	case OpIssuePairing:
		var q struct {
			Reusable bool     `json:"reusable"`
			TTLSecs  int64    `json:"ttlSecs"`
			Ports    []int    `json:"ports"`
			Tunnels  []string `json:"tunnels"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		var exp time.Time
		if q.TTLSecs > 0 {
			exp = time.Now().Add(time.Duration(q.TTLSecs) * time.Second)
		}
		code, err := e.issuePairingCode(q.Reusable, exp, gateway.Scope{Ports: q.Ports, TunnelIDs: q.Tunnels})
		if err != nil {
			return nil, err
		}
		return encodeResult(struct {
			Code string `json:"code"`
		}{code}, nil)
	case OpRevokeAgent:
		var q struct {
			AgentID string `json:"agentId"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{e.Gateway.RevokeAgent(q.AgentID)}, nil)
	case OpRenameAgent:
		var q struct {
			AgentID  string `json:"agentId"`
			Nickname string `json:"nickname"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{e.Gateway.RenameAgent(q.AgentID, q.Nickname)}, nil)
	case OpSetAgentScope:
		var q struct {
			AgentID string   `json:"agentId"`
			Ports   []int    `json:"ports"`
			Tunnels []string `json:"tunnels"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{e.Gateway.SetAgentScope(q.AgentID, gateway.Scope{Ports: q.Ports, TunnelIDs: q.Tunnels})}, nil)
	default:
		return nil, fmt.Errorf("unknown agent admin op %q", op)
	}
}

// issuePairingCode mints an enrollment ticket scoped as requested and returns a
// pxf:// pairing code carrying it — the enrollment analogue of PairingCode (which
// embeds the legacy shared token). reusable=false is the single-use default; a zero
// exp never expires.
func (e *Engine) issuePairingCode(reusable bool, exp time.Time, scope gateway.Scope) (string, error) {
	if e.Gateway == nil {
		return "", fmt.Errorf("pairing codes come from the gateway role")
	}
	ticket, err := e.Gateway.IssuePairingTicket(reusable, exp, scope)
	if err != nil {
		return "", err
	}
	addr := e.Gateway.ControlAddr()
	if addr == nil {
		return "", fmt.Errorf("gateway is still starting")
	}
	host := e.cfg.Gateway.PublicHost
	if host == "" {
		host = "YOUR-PUBLIC-ADDRESS"
	}
	code := link.PairingCode{
		Host:        host,
		Port:        addr.(*net.TCPAddr).Port,
		Token:       ticket,
		Fingerprint: e.Gateway.Fingerprint(),
	}
	return code.String(), nil
}
