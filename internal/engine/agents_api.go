package engine

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
	"proxyforward/internal/gateway"
	"proxyforward/internal/link"
)

// Gateway agent-administration op names. Like the analytics ops these are the
// stable wire contract between the GUI and the daemon, dispatched over the same
// JSON envelope so each App binding is one small typed wrapper. They act on the
// live gateway (allowlist + sessions + event ring), never the analytics store.
const (
	OpListAgents         = "list_agents"
	OpIssuePairing       = "issue_pairing"
	OpRevokeAgent        = "revoke_agent"
	OpRenameAgent        = "rename_agent"
	OpSetAgentScope      = "set_agent_scope"
	OpGatewayEvents      = "gateway_events"
	OpAgentDesiredConfig = "agent_desired_config"
	OpReclaimPort        = "reclaim_port"
	// Invite ops: every live pairing code is a roster row, so a standing
	// credential is never invisible. Revoking one never touches enrolled agents.
	OpListInvites  = "list_invites"
	OpRevokeInvite = "revoke_invite"
	// Consent-protocol ops (config-consent): the operator's decision on an
	// agent-authored proposal, and the gateway-authored editor's submit.
	OpApproveProposal    = "approve_agent_proposal"
	OpRejectProposal     = "reject_agent_proposal"
	OpProposeAgentConfig = "propose_agent_config"
	// Tunnel-group ops (load-balancing / redundancy): list groups with live member
	// state, upsert/remove a group, and tag/untag a member's tunnel. Grouping is
	// arbitration, so a set/delete/tag applies immediately with no member consent.
	OpListGroups     = "list_groups"
	OpSetGroup       = "set_group"
	OpDeleteGroup    = "delete_group"
	OpSetTunnelGroup = "set_tunnel_group"
)

// isAgentAdminOp reports whether op is a gateway agent-administration op, so
// analyticsOp can route it before the analytics-store availability check.
func isAgentAdminOp(op string) bool {
	switch op {
	case OpListAgents, OpIssuePairing, OpRevokeAgent, OpRenameAgent, OpSetAgentScope, OpGatewayEvents,
		OpAgentDesiredConfig, OpReclaimPort, OpApproveProposal, OpRejectProposal, OpProposeAgentConfig,
		OpListInvites, OpRevokeInvite,
		OpListGroups, OpSetGroup, OpDeleteGroup, OpSetTunnelGroup:
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
			Label    string   `json:"label"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		var exp time.Time
		if q.TTLSecs > 0 {
			exp = time.Now().Add(time.Duration(q.TTLSecs) * time.Second)
		}
		code, err := e.issuePairingCode(q.Reusable, exp, gateway.Scope{Ports: q.Ports, TunnelIDs: q.Tunnels}, q.Label)
		if err != nil {
			return nil, err
		}
		return encodeResult(struct {
			Code string `json:"code"`
		}{code}, nil)
	case OpListInvites:
		return encodeResult(e.listInvites(), nil)
	case OpRevokeInvite:
		var q struct {
			Handle string `json:"handle"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{e.Gateway.RevokeInvite(q.Handle)}, nil)
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
	case OpAgentDesiredConfig:
		var q struct {
			AgentID string `json:"agentId"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(e.Gateway.AgentDesiredConfig(q.AgentID), nil)
	case OpReclaimPort:
		var q struct {
			AgentID  string `json:"agentId"`
			TunnelID string `json:"tunnelId"`
			Port     int    `json:"port"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		ok, err := e.Gateway.ReclaimPort(q.AgentID, q.TunnelID, q.Port)
		if err != nil {
			return nil, err
		}
		return encodeResult(okResult{ok}, nil)
	case OpApproveProposal:
		var q struct {
			AgentID    string `json:"agentId"`
			ProposalID string `json:"proposalId"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{true}, e.Gateway.ApproveAgentProposal(q.AgentID, q.ProposalID))
	case OpRejectProposal:
		var q struct {
			AgentID    string `json:"agentId"`
			ProposalID string `json:"proposalId"`
			Reason     string `json:"reason"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{true}, e.Gateway.RejectAgentProposal(q.AgentID, q.ProposalID, q.Reason))
	case OpProposeAgentConfig:
		var q struct {
			AgentID string               `json:"agentId"`
			Tunnels []control.TunnelSpec `json:"tunnels"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{true}, e.Gateway.ProposeAgentConfig(q.AgentID, q.Tunnels))
	case OpListGroups:
		return encodeResult(e.Gateway.GroupStatuses(), nil)
	case OpSetGroup:
		var q struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			PublicPort  int    `json:"publicPort"`
			Proto       string `json:"proto"`
			Policy      string `json:"policy"`
			OfflineMOTD string `json:"offlineMotd"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		if q.ID == "" {
			q.ID = "grp_" + config.NewID() // create: mint a fresh id
		}
		if q.Proto == "" {
			q.Proto = gateway.GroupProtoTCP
		}
		spec := gateway.GroupSpec{ID: q.ID, Name: q.Name, PublicPort: q.PublicPort, Proto: q.Proto, Policy: q.Policy, OfflineMOTD: q.OfflineMOTD}
		// Echo the (possibly minted) id so the GUI can select the group it just saved.
		return encodeResult(struct {
			ID string `json:"id"`
		}{q.ID}, e.Gateway.SetGroup(spec))
	case OpDeleteGroup:
		var q struct {
			ID string `json:"id"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{e.Gateway.DeleteGroup(q.ID)}, nil)
	case OpSetTunnelGroup:
		var q struct {
			AgentID  string `json:"agentId"`
			TunnelID string `json:"tunnelId"`
			GroupID  string `json:"groupId"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		ok, err := e.Gateway.SetTunnelGroup(q.AgentID, q.TunnelID, q.GroupID)
		return encodeResult(okResult{ok}, err)
	default:
		return nil, fmt.Errorf("unknown agent admin op %q", op)
	}
}

// issuePairingCode mints an enrollment ticket scoped and labeled as requested
// and returns a pxf:// pairing code carrying it. reusable=false is the
// single-use default; a zero exp never expires.
func (e *Engine) issuePairingCode(reusable bool, exp time.Time, scope gateway.Scope, label string) (string, error) {
	if e.Gateway == nil {
		return "", fmt.Errorf("pairing codes come from the gateway role")
	}
	ticket, err := e.Gateway.IssuePairingTicket(reusable, exp, scope, label)
	if err != nil {
		return "", err
	}
	return e.pairingCodeFor(ticket)
}

// pairingCodeFor composes the full pxf:// code for a ticket — issuance and the
// invite roster's copy action share this one assembly.
func (e *Engine) pairingCodeFor(ticket string) (string, error) {
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

// InviteView is one live pairing code for the GUI roster: identity and policy
// plus the composed pxf:// code for the copy action. The code is recomposed from
// the stored ticket on every list — the pipe already carries freshly-issued
// codes, and the roster's copy button is the same operator asking again.
type InviteView struct {
	Handle       string   `json:"handle"`
	Label        string   `json:"label"`
	Reusable     bool     `json:"reusable"`
	IssuedAtMs   int64    `json:"issuedAtMs"`
	ExpiresAtMs  int64    `json:"expiresAtMs"` // 0 = never expires
	Uses         int      `json:"uses"`
	ScopePorts   []int    `json:"scopePorts"`
	ScopeTunnels []string `json:"scopeTunnels"`
	Code         string   `json:"code"`
}

// listInvites maps the gateway's live tickets into GUI rows, newest first.
func (e *Engine) listInvites() []InviteView {
	invites := e.Gateway.ListInvites()
	out := make([]InviteView, 0, len(invites))
	for _, in := range invites {
		v := InviteView{
			Handle: in.Handle, Label: in.Label, Reusable: in.Reusable,
			IssuedAtMs: in.IssuedAt.UnixMilli(), Uses: in.Uses,
			ScopePorts: in.Scope.Ports, ScopeTunnels: in.Scope.TunnelIDs,
		}
		if !in.Exp.IsZero() {
			v.ExpiresAtMs = in.Exp.UnixMilli()
		}
		if code, err := e.pairingCodeFor(in.Ticket); err == nil {
			v.Code = code
		}
		out = append(out, v)
	}
	return out
}
