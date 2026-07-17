package app

import (
	"proxyforward/internal/agent"
	"proxyforward/internal/engine"
)

// Config consent (agent role). These bindings drive the agent GUI's approval
// surfaces: what the gateway has proposed, what this machine has proposed, and the
// two decisions. Like the gateway's agent-management bindings each runs one op over
// the shared JSON envelope (analyticsCall), so they work in-process and attached,
// and degrade to a zero view off the agent role.

// AgentConsent returns the agent's consent state: whether edits here need the
// gateway's approval at all (ConsentActive), a gateway-authored edit awaiting this
// machine's decision, this machine's edit awaiting the gateway's, and why the
// gateway last declined one. The zero view (nothing pending, ConsentActive false)
// on any error — gateway role, detached, or an old daemon — so the UI falls back to
// the plain editor rather than promising an approval that will never arrive.
func (a *App) AgentConsent() agent.PendingView {
	var out agent.PendingView
	if err := a.analyticsCall(engine.OpAgentConsent, nil, &out); err != nil {
		return agent.PendingView{}
	}
	return out
}

// ApproveGatewayProposal accepts the gateway's proposed tunnel edit. It applies
// nothing locally: the gateway adopts the set on this decision and pushes it back,
// and that push is what applies and persists it.
func (a *App) ApproveGatewayProposal(proposalID string) error {
	var resp struct {
		OK bool `json:"ok"`
	}
	return a.analyticsCall(engine.OpApproveGatewayProposal, struct {
		ProposalID string `json:"proposalId"`
	}{proposalID}, &resp)
}

// RejectGatewayProposal declines the gateway's proposed tunnel edit; the reason
// reaches the operator's event log verbatim. Nothing here changes.
func (a *App) RejectGatewayProposal(proposalID, reason string) error {
	var resp struct {
		OK bool `json:"ok"`
	}
	return a.analyticsCall(engine.OpRejectGatewayProposal, struct {
		ProposalID string `json:"proposalId"`
		Reason     string `json:"reason"`
	}{proposalID, reason}, &resp)
}
