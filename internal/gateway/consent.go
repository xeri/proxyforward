package gateway

// Mutual-consent config editing (control.CapConfigConsent): authored tunnel-set
// edits — an agent editing its own tunnels, or the gateway operator editing an
// agent's — travel as a config_proposal the other side must approve before
// anything applies. Arbitration (connect-sync, drift correction, port reclaim,
// auto-reassign) stays on push_config: immediate gateway authority with a notice,
// never a veto.
//
// Pending proposals are session-scoped by design: they live on the agentSession,
// die with it on disconnect, and the proposer re-proposes on reconnect — nothing
// here persists. Ownership: the pending slots are atomic pointers written by the
// control read loop and the GUI ops (claimed by CompareAndSwap so a decision
// applies exactly once); nothing in this file touches actor-owned maps off the
// actor goroutine.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"proxyforward/internal/control"
)

// pendingProposal is one held consent proposal: received-at for an inbound
// (agent-authored) one, sent-at for an outbound (gateway-authored) one.
type pendingProposal struct {
	proposal control.ConfigProposal
	at       time.Time
}

// ProposalView is a pending consent proposal as surfaced to the GUI (on
// AgentConfigView): what is proposed, against which generation, and since when.
type ProposalView struct {
	ProposalID string               `json:"proposalId"`
	BaseGen    uint64               `json:"baseGen"`
	Tunnels    []control.TunnelSpec `json:"tunnels"`
	AtMs       int64                `json:"atMs"`
}

func proposalView(pp *pendingProposal) *ProposalView {
	if pp == nil {
		return nil
	}
	return &ProposalView{
		ProposalID: pp.proposal.ProposalID,
		BaseGen:    pp.proposal.BaseGen,
		Tunnels:    pp.proposal.Tunnels,
		AtMs:       pp.at.UnixMilli(),
	}
}

// newProposalID mints a fresh correlation ID for a gateway-authored proposal.
func newProposalID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "prp_" + hex.EncodeToString(b[:])
}

// liveSession finds the connected session for an agentID (nil when offline). One
// actor hop; the returned pointer is safe to use off the actor because everything
// read through it is admission-immutable or atomic.
func (g *Gateway) liveSession(agentID string) *agentSession {
	a := g.act()
	if a == nil {
		return nil
	}
	var out *agentSession
	a.do(func() { out = a.agents[agentID] })
	return out
}

// recordEventNow appends one event to the actor-owned ring from any goroutine.
func (g *Gateway) recordEventNow(ev GatewayEvent) {
	if a := g.act(); a != nil {
		a.do(func() { a.recordEvent(ev) })
	}
}

// holdInboundProposal parks an agent-authored proposal for the operator's decision.
// A corrupt frame (hash not matching the set) is answered with an immediate
// rejection rather than held — the operator would be approving something the agent
// didn't author. A newer proposal from the same agent replaces the held one: the
// agent re-proposed, so the older edit is moot.
func (g *Gateway) holdInboundProposal(sess *agentSession, prop control.ConfigProposal) {
	if prop.Hash != "" && prop.Hash != control.HashTunnels(prop.Tunnels) {
		sess.logger.Warn("refusing config proposal with mismatched hash", "proposal", prop.ProposalID)
		_ = sess.writeControl(control.TypeConfigDecision, control.ConfigDecision{
			ProposalID: prop.ProposalID,
			Reason:     "the proposal arrived corrupted (hash mismatch) — send the edit again",
		})
		return
	}
	sess.pendingInbound.Store(&pendingProposal{proposal: prop, at: time.Now()})
	sess.logger.Info("holding agent config proposal for approval", "proposal", prop.ProposalID, "tunnels", len(prop.Tunnels))
}

// takePending claims a pending proposal by ID from one slot, so a decision applies
// exactly once even if two operators race. Nil when nothing (or something else) is
// pending there.
func takePending(slot *atomic.Pointer[pendingProposal], proposalID string) *pendingProposal {
	pp := slot.Load()
	if pp == nil || pp.proposal.ProposalID != proposalID || !slot.CompareAndSwap(pp, nil) {
		return nil
	}
	return pp
}

// ApproveAgentProposal adopts an agent-authored pending proposal: validate,
// reconcile listeners, bump the generation, push the resolved truth, and answer
// the agent with an approving decision naming it. A proposal whose base
// generation no longer matches the stored one is refused — the truth moved while
// it waited (a reclaim, another adopt), and silently adopting it would clobber
// that — and the agent is told to re-propose against the current set.
func (g *Gateway) ApproveAgentProposal(agentID, proposalID string) error {
	sess := g.liveSession(agentID)
	if sess == nil {
		return fmt.Errorf("agent %s is not connected — its proposal ended with the session", agentID)
	}
	pp := takePending(&sess.pendingInbound, proposalID)
	if pp == nil {
		return fmt.Errorf("that proposal is no longer pending")
	}
	_, storedGen, ok := g.agents.DesiredConfig(agentID)
	if !ok {
		return fmt.Errorf("agent %s is not enrolled", agentID)
	}
	if pp.proposal.BaseGen != storedGen {
		_ = sess.writeControl(control.TypeConfigDecision, control.ConfigDecision{
			ProposalID: proposalID,
			Reason:     "the config changed while this proposal was pending — edit and propose again",
		})
		return fmt.Errorf("the config changed while this proposal was pending; the agent must re-propose")
	}
	gen, hash, ok := g.adoptAndPush(sess, pp.proposal.Tunnels)
	if !ok {
		return fmt.Errorf("gateway is shutting down")
	}
	if err := sess.writeControl(control.TypeConfigDecision, control.ConfigDecision{
		ProposalID: proposalID, Approved: true, Generation: gen, Hash: hash,
	}); err != nil {
		// The set is adopted and pushed regardless; the agent converges via the push.
		sess.logger.Warn("approval decision did not reach the agent", "err", err)
	}
	sess.logger.Info("approved agent config proposal", "proposal", proposalID, "generation", gen)
	return nil
}

// RejectAgentProposal declines an agent-authored pending proposal, carrying the
// operator's reason back verbatim. Nothing about the authoritative config changes.
func (g *Gateway) RejectAgentProposal(agentID, proposalID, reason string) error {
	sess := g.liveSession(agentID)
	if sess == nil {
		return fmt.Errorf("agent %s is not connected — its proposal ended with the session", agentID)
	}
	if takePending(&sess.pendingInbound, proposalID) == nil {
		return fmt.Errorf("that proposal is no longer pending")
	}
	if reason == "" {
		reason = "the gateway operator declined this change"
	}
	if err := sess.writeControl(control.TypeConfigDecision, control.ConfigDecision{
		ProposalID: proposalID, Reason: reason,
	}); err != nil {
		sess.logger.Warn("rejection decision did not reach the agent", "err", err)
	}
	sess.logger.Info("rejected agent config proposal", "proposal", proposalID, "reason", reason)
	return nil
}

// ProposeAgentConfig sends a gateway-authored tunnel-set edit to a connected
// consent agent for its approval. Nothing applies until the agent answers; the
// pending proposal is surfaced on AgentConfigView and resolved by the agent's
// config_decision. Specs are validated against the agent's scope up front so the
// editor fails fast instead of minting a proposal doomed at approval. A newer
// proposal replaces a still-pending older one (the operator re-edited).
func (g *Gateway) ProposeAgentConfig(agentID string, tunnels []control.TunnelSpec) error {
	if g.agents == nil {
		return fmt.Errorf("gateway not started")
	}
	sess := g.liveSession(agentID)
	if sess == nil {
		return fmt.Errorf("agent %s is not connected — proposals need a live session", agentID)
	}
	if !sess.enrolled {
		return fmt.Errorf("agent %s uses the legacy shared token; its config is agent-pushed and cannot be edited here", agentID)
	}
	if !sess.Has(control.CapConfigConsent) {
		return fmt.Errorf("agent %s runs a build without config-consent — update it to edit its tunnels from here", agentID)
	}
	for _, spec := range tunnels {
		if berr := g.validateSpec(spec, sess.scope); berr != nil {
			return fmt.Errorf("tunnel %q: %s", spec.Name, berr.msg)
		}
	}
	_, gen, _ := g.agents.DesiredConfig(agentID)
	prop := control.ConfigProposal{
		ProposalID: newProposalID(),
		BaseGen:    gen,
		Tunnels:    tunnels,
		Hash:       control.HashTunnels(tunnels),
	}
	pp := &pendingProposal{proposal: prop, at: time.Now()}
	sess.pendingOutbound.Store(pp)
	if err := sess.writeControl(control.TypeConfigProposal, prop); err != nil {
		sess.pendingOutbound.CompareAndSwap(pp, nil)
		return fmt.Errorf("sending the proposal failed: %w", err)
	}
	sess.logger.Info("proposed config edit to agent", "proposal", prop.ProposalID, "tunnels", len(tunnels))
	return nil
}

// resolveOutboundDecision applies the agent's answer to a gateway-authored
// proposal. Approved: adopt + push — the push, not the decision, is what applies
// the change (auto-apply of adopted truth), so both sides converge exactly as on
// every other config path. Rejected (or gone stale while the agent decided): adopt
// nothing and record a declined event so the operator learns the reason.
func (g *Gateway) resolveOutboundDecision(sess *agentSession, dec control.ConfigDecision) {
	pp := takePending(&sess.pendingOutbound, dec.ProposalID)
	if pp == nil {
		sess.logger.Debug("ignoring decision for an unknown or superseded proposal", "proposal", dec.ProposalID)
		return
	}
	if !dec.Approved {
		reason := dec.Reason
		if reason == "" {
			reason = "the agent declined"
		}
		g.recordEventNow(GatewayEvent{
			Kind: EventProposalDeclined, AgentID: sess.agentID,
			Message: fmt.Sprintf("Agent %s declined the proposed config change: %s", sess.agentID, reason),
		})
		sess.logger.Info("agent declined config proposal", "proposal", dec.ProposalID, "reason", reason)
		return
	}
	_, storedGen, ok := g.agents.DesiredConfig(sess.agentID)
	if !ok {
		return
	}
	if pp.proposal.BaseGen != storedGen {
		g.recordEventNow(GatewayEvent{
			Kind: EventProposalDeclined, AgentID: sess.agentID,
			Message: fmt.Sprintf("The approved change for %s was dropped: its config moved while awaiting approval — propose it again.", sess.agentID),
		})
		sess.logger.Warn("approved proposal went stale; dropped", "proposal", dec.ProposalID, "base_gen", pp.proposal.BaseGen, "stored_gen", storedGen)
		return
	}
	gen, _, ok := g.adoptAndPush(sess, pp.proposal.Tunnels)
	if !ok {
		sess.logger.Warn("could not adopt approved proposal; gateway shutting down", "proposal", dec.ProposalID)
		return
	}
	sess.logger.Info("agent approved config proposal", "proposal", dec.ProposalID, "generation", gen)
}
