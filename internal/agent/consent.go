package agent

// Mutual-consent config editing, agent side (control.CapConfigConsent). An
// authored tunnel-set edit — this machine's user editing its tunnels, or the
// gateway operator editing them from the other end — is a *proposal* the other
// side must approve; nothing changes until it does.
//
// The commit point is always the gateway's authoritative push_config, never a
// local write: approving (either direction) makes the gateway adopt, bump the
// generation, and push the resolved truth, which applyPushedConfig applies and
// persists. That keeps consent identical to every other config path (first-contact
// seed, drift correction, port reclaim) and means a rejected edit leaves the
// running set, the config file, and the gateway untouched — there is nothing to
// roll back.
//
// Ownership: both pending slots live on the session, so they are session-scoped by
// construction — a disconnect drops them and the proposer re-proposes. They are
// atomics because the control read loop and the GUI ops race on them, and are
// claimed by CompareAndSwap so one decision resolves exactly once.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// pendingConsent is one held proposal: received-at for an incoming
// (gateway-authored) one, sent-at for an outgoing (locally-authored) one.
type pendingConsent struct {
	proposal control.ConfigProposal
	at       time.Time
	// draft is the authored tunnel set behind an outgoing proposal, kept in full
	// config form because the wire carries only the gateway's half of a tunnel. When
	// the approval's push comes back it names tunnels by ID with no LocalAddr or PP2
	// — fields the gateway never had — so applyPushedConfig overlays this draft to
	// restore them. Nil on an incoming proposal.
	draft []config.Tunnel
}

// ProposalView is a pending consent proposal as the agent's GUI renders it.
type ProposalView struct {
	ProposalID string               `json:"proposalId"`
	Tunnels    []control.TunnelSpec `json:"tunnels"`
	AtMs       int64                `json:"atMs"`
}

// PendingView is the agent's whole consent state for the GUI: whether edits here
// need the gateway's approval at all, and either side's proposal in flight.
type PendingView struct {
	// ConsentActive is true only on a live session that negotiated config-consent.
	// False means edits apply the old way (link down, legacy gateway, shared token),
	// which the UI must say honestly rather than imply an approval that never comes.
	ConsentActive bool `json:"consentActive"`
	// Incoming is a gateway-authored edit awaiting this machine's decision.
	Incoming *ProposalView `json:"incoming,omitempty"`
	// Outgoing is this agent's own edit awaiting the gateway's decision.
	Outgoing *ProposalView `json:"outgoing,omitempty"`
	// LastDecline is why the gateway last refused a local edit, kept so a save that
	// silently did nothing can explain itself; cleared when the next edit is proposed.
	LastDecline string `json:"lastDecline,omitempty"`
}

func proposalView(pc *pendingConsent) *ProposalView {
	if pc == nil {
		return nil
	}
	return &ProposalView{
		ProposalID: pc.proposal.ProposalID,
		Tunnels:    pc.proposal.Tunnels,
		AtMs:       pc.at.UnixMilli(),
	}
}

// newProposalID mints a fresh correlation ID for a locally-authored proposal.
func newProposalID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "prp_" + hex.EncodeToString(b[:])
}

// takePending claims a pending proposal by ID, so a decision resolves exactly once
// even if the read loop and a GUI op race. Nil when nothing (or something else) is
// pending there.
func takePending(slot *atomic.Pointer[pendingConsent], proposalID string) *pendingConsent {
	pc := slot.Load()
	if pc == nil || pc.proposal.ProposalID != proposalID || !slot.CompareAndSwap(pc, nil) {
		return nil
	}
	return pc
}

// consentSession returns the live session when it negotiated config-consent, else
// nil — the single gate every consent entry point checks.
func (a *Agent) consentSession() *session {
	sess := a.curSession.Load()
	if sess == nil || !sess.Has(control.CapConfigConsent) {
		return nil
	}
	return sess
}

// ConsentActive reports whether tunnel edits on this agent are gateway-approved
// (a live config-consent session). The GUI switches its save affordance on it.
func (a *Agent) ConsentActive() bool { return a.consentSession() != nil }

// PendingConsent is the agent's consent state for the GUI. Zero value (nothing
// pending, ConsentActive false) whenever the link is down or the peer is legacy.
func (a *Agent) PendingConsent() PendingView {
	sess := a.consentSession()
	if sess == nil {
		return PendingView{}
	}
	v := PendingView{
		ConsentActive: true,
		Incoming:      proposalView(sess.pendingIncoming.Load()),
		Outgoing:      proposalView(sess.pendingLocal.Load()),
	}
	if d := sess.lastDecline.Load(); d != nil {
		v.LastDecline = *d
	}
	return v
}

// ProposeTunnels submits a locally-authored tunnel edit for the gateway's
// approval, changing nothing here — the approval returns as an authoritative push,
// which is what applies it. Replacing a still-pending proposal is deliberate: the
// user edited again, so the older one is moot (the gateway drops it by ID).
//
// It reports whether the edit actually needed approval. An edit the gateway cannot
// see — a local address, a PROXY-protocol toggle, a disabled draft — changes
// nothing on its side, so it is applied here and now and reported as proposed=false:
// consent governs the gateway-visible set, never this machine's own wiring, and
// making an operator rubber-stamp a LAN address would be consent theatre.
func (a *Agent) ProposeTunnels(tunnels []config.Tunnel) (proposed bool, err error) {
	sess := a.consentSession()
	if sess == nil {
		return false, fmt.Errorf("the gateway link is down or does not support approvals — reconnect and try again")
	}
	specs := enabledSpecs(tunnels)
	if control.HashTunnels(specs) == control.HashTunnels(enabledSpecs(a.snapshotTunnels())) {
		a.swapLocal(tunnels)
		a.logger.Info("applied a local-only tunnel edit; nothing the gateway owns changed")
		return false, nil
	}
	prop := control.ConfigProposal{
		ProposalID: newProposalID(),
		BaseGen:    a.configGen.Load(),
		Tunnels:    specs,
		Hash:       control.HashTunnels(specs),
	}
	pc := &pendingConsent{proposal: prop, at: time.Now(), draft: append([]config.Tunnel(nil), tunnels...)}
	sess.pendingLocal.Store(pc)
	sess.lastDecline.Store(nil)
	if err := sess.write(control.TypeConfigProposal, prop); err != nil {
		sess.pendingLocal.CompareAndSwap(pc, nil)
		return false, fmt.Errorf("sending the edit to the gateway failed: %w", err)
	}
	a.logger.Info("proposed tunnel edit to gateway for approval", "proposal", prop.ProposalID, "tunnels", len(specs))
	return true, nil
}

// pendingDraft returns the authored set behind an outgoing proposal, or nil. The
// approval's push lands before its decision (one ordered control stream), so the
// draft is still here when the set it authored comes back to be applied.
func (a *Agent) pendingDraft() []config.Tunnel {
	sess := a.curSession.Load()
	if sess == nil {
		return nil
	}
	if pc := sess.pendingLocal.Load(); pc != nil {
		return pc.draft
	}
	return nil
}

// ApproveGatewayProposal accepts a gateway-authored edit. It applies nothing
// itself: the gateway adopts the set on this decision and pushes it back, and that
// push is the commit — so the agent converges through exactly the same path as any
// other authoritative change.
func (a *Agent) ApproveGatewayProposal(proposalID string) error {
	sess := a.consentSession()
	if sess == nil {
		return fmt.Errorf("the gateway link is down — its proposal ended with the session")
	}
	if takePending(&sess.pendingIncoming, proposalID) == nil {
		return fmt.Errorf("that proposal is no longer pending")
	}
	if err := sess.write(control.TypeConfigDecision, control.ConfigDecision{
		ProposalID: proposalID, Approved: true,
	}); err != nil {
		return fmt.Errorf("sending the approval failed: %w", err)
	}
	a.logger.Info("approved gateway config proposal", "proposal", proposalID)
	return nil
}

// RejectGatewayProposal declines a gateway-authored edit; the reason reaches the
// operator's event log verbatim. Nothing here changes either way.
func (a *Agent) RejectGatewayProposal(proposalID, reason string) error {
	sess := a.consentSession()
	if sess == nil {
		return fmt.Errorf("the gateway link is down — its proposal ended with the session")
	}
	if takePending(&sess.pendingIncoming, proposalID) == nil {
		return fmt.Errorf("that proposal is no longer pending")
	}
	if reason == "" {
		reason = "declined on the agent"
	}
	if err := sess.write(control.TypeConfigDecision, control.ConfigDecision{
		ProposalID: proposalID, Reason: reason,
	}); err != nil {
		return fmt.Errorf("sending the rejection failed: %w", err)
	}
	a.logger.Info("rejected gateway config proposal", "proposal", proposalID, "reason", reason)
	return nil
}

// holdGatewayProposal parks a gateway-authored edit for the user's decision. A
// frame whose hash doesn't cover its set is refused outright rather than shown —
// the user would be approving something the operator didn't author.
func (s *session) holdGatewayProposal(prop control.ConfigProposal) {
	if prop.Hash != "" && prop.Hash != control.HashTunnels(prop.Tunnels) {
		s.agent.logger.Warn("refusing gateway config proposal with mismatched hash", "proposal", prop.ProposalID)
		_ = s.write(control.TypeConfigDecision, control.ConfigDecision{
			ProposalID: prop.ProposalID,
			Reason:     "the proposal arrived corrupted (hash mismatch) — send it again",
		})
		return
	}
	s.pendingIncoming.Store(&pendingConsent{proposal: prop, at: time.Now()})
	s.agent.logger.Info("gateway proposed a config change; awaiting approval",
		"proposal", prop.ProposalID, "tunnels", len(prop.Tunnels))
}

// resolveLocalDecision applies the gateway's verdict on this agent's own proposal.
// Approved: the authoritative push does the applying, so there is nothing to do but
// clear the pending mark. Rejected: keep the reason for the GUI — the running set
// was never changed, so a rejection needs no rollback.
func (s *session) resolveLocalDecision(dec control.ConfigDecision) {
	if takePending(&s.pendingLocal, dec.ProposalID) == nil {
		s.agent.logger.Debug("ignoring decision for an unknown or superseded proposal", "proposal", dec.ProposalID)
		return
	}
	if dec.Approved {
		s.lastDecline.Store(nil)
		s.agent.logger.Info("gateway approved the tunnel edit", "proposal", dec.ProposalID, "generation", dec.Generation)
		return
	}
	reason := dec.Reason
	if reason == "" {
		reason = "the gateway operator declined the change"
	}
	s.lastDecline.Store(&reason)
	s.agent.logger.Warn("gateway declined the tunnel edit", "proposal", dec.ProposalID, "reason", reason)
}
