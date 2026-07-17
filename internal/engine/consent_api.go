package engine

import (
	"encoding/json"
	"fmt"
)

// Agent-role consent op names (control.CapConfigConsent). The mirror image of the
// gateway's agent-administration ops: these act on the live agent's session state,
// never the analytics store, and ride the same JSON envelope so each App binding
// stays one small typed wrapper.
const (
	OpAgentConsent           = "agent_consent"            // read: pending state both ways
	OpApproveGatewayProposal = "approve_gateway_proposal" // decide: accept the gateway's edit
	OpRejectGatewayProposal  = "reject_gateway_proposal"  // decide: decline it, with a reason
)

// isAgentConsentOp reports whether op is an agent-role consent op, so analyticsOp
// can route it before the analytics-store availability check — consent must work
// even when the stats database is down.
func isAgentConsentOp(op string) bool {
	switch op {
	case OpAgentConsent, OpApproveGatewayProposal, OpRejectGatewayProposal:
		return true
	}
	return false
}

// agentConsentOp dispatches one agent-role consent op. Every op requires the agent
// role; on a gateway-role daemon it says so rather than panicking.
func (e *Engine) agentConsentOp(op string, body json.RawMessage) (json.RawMessage, error) {
	if e.Agent == nil {
		return nil, fmt.Errorf("config approvals are only available on the agent")
	}
	switch op {
	case OpAgentConsent:
		return encodeResult(e.Agent.PendingConsent(), nil)
	case OpApproveGatewayProposal:
		var q struct {
			ProposalID string `json:"proposalId"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{true}, e.Agent.ApproveGatewayProposal(q.ProposalID))
	case OpRejectGatewayProposal:
		var q struct {
			ProposalID string `json:"proposalId"`
			Reason     string `json:"reason"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		return encodeResult(okResult{true}, e.Agent.RejectGatewayProposal(q.ProposalID, q.Reason))
	default:
		return nil, fmt.Errorf("unknown agent consent op %q", op)
	}
}
