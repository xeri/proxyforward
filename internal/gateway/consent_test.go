package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// captureStream is a transport.Stream that records every control frame written to
// it, so a test can assert exactly what the gateway sent an agent. Reads EOF
// immediately (no test drives the gateway's read loop through it).
type captureStream struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captureStream) Read([]byte) (int, error) { return 0, io.EOF }
func (c *captureStream) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}
func (c *captureStream) Close() error                     { return nil }
func (c *captureStream) CloseWrite() error                { return nil }
func (c *captureStream) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *captureStream) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *captureStream) SetDeadline(time.Time) error      { return nil }
func (c *captureStream) SetReadDeadline(time.Time) error  { return nil }
func (c *captureStream) SetWriteDeadline(time.Time) error { return nil }

// envelopes decodes every frame written so far, oldest first.
func (c *captureStream) envelopes(t *testing.T) []*control.Envelope {
	t.Helper()
	c.mu.Lock()
	data := append([]byte(nil), c.buf.Bytes()...)
	c.mu.Unlock()
	r := bytes.NewReader(data)
	var out []*control.Envelope
	for {
		env, err := control.ReadMsg(r, control.MaxFrame)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			t.Fatalf("decode captured frame: %v", err)
		}
		out = append(out, env)
	}
}

// lastOfType returns the newest captured frame of one type, decoded, or nil.
func lastDecision(t *testing.T, c *captureStream) *control.ConfigDecision {
	t.Helper()
	envs := c.envelopes(t)
	for i := len(envs) - 1; i >= 0; i-- {
		if envs[i].Type == control.TypeConfigDecision {
			dec, err := control.Decode[control.ConfigDecision](envs[i])
			if err != nil {
				t.Fatal(err)
			}
			return dec
		}
	}
	return nil
}

func lastPush(t *testing.T, c *captureStream) *control.PushConfig {
	t.Helper()
	envs := c.envelopes(t)
	for i := len(envs) - 1; i >= 0; i-- {
		if envs[i].Type == control.TypePushConfig {
			pc, err := control.Decode[control.PushConfig](envs[i])
			if err != nil {
				t.Fatal(err)
			}
			return pc
		}
	}
	return nil
}

func lastProposal(t *testing.T, c *captureStream) *control.ConfigProposal {
	t.Helper()
	envs := c.envelopes(t)
	for i := len(envs) - 1; i >= 0; i-- {
		if envs[i].Type == control.TypeConfigProposal {
			p, err := control.Decode[control.ConfigProposal](envs[i])
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
	}
	return nil
}

// mkEnv wraps a payload the way the wire would deliver it to handleControlMsg.
func mkEnv(t *testing.T, msgType string, payload any) *control.Envelope {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &control.Envelope{Type: msgType, Data: data}
}

// consentFixture is a gateway with a live actor and one enrolled, connected agent
// whose session negotiated CapConfigConsent, seeded with a one-tunnel authoritative
// config — the steady state a consent exchange starts from.
type consentFixture struct {
	g     *Gateway
	a     *actor
	store *AgentStore
	sess  *agentSession
	ctrl  *captureStream
	gen   uint64 // the seeded config's generation
}

func newConsentFixture(t *testing.T, caps ...string) *consentFixture {
	t.Helper()
	store, err := LoadAgentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.IssueEnrollment(false, time.Time{}, Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enroll(pub(1), "agt_one", ticket, time.Now()); err != nil {
		t.Fatal(err)
	}

	a := newActor(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go a.run(ctx)
	t.Cleanup(cancel)

	g := &Gateway{cfg: config.Default()}
	g.cfg.Gateway.BindAddr = "127.0.0.1"
	g.agents = store
	g.actor.Store(a)

	if caps == nil {
		caps = []string{control.CapGatewayConfig, control.CapConfigConsent}
	}
	sess := &agentSession{
		agentID: "agt_one", scope: Scope{}, enrolled: true,
		caps: control.NewCapSet(caps), logger: discardLogger(),
	}
	if _, err := a.admit(sess); err != nil {
		t.Fatal(err)
	}
	ctrl := &captureStream{}
	sess.setCtrl(ctrl)

	seed := control.TunnelSpec{ID: "tnl_smp", Name: "SMP", Type: config.TunnelTCP, PublicPort: 0}
	gen, ok := store.AdoptConfig("agt_one", []control.TunnelSpec{seed})
	if !ok {
		t.Fatal("seed AdoptConfig")
	}
	sess.agentConfigGen.Store(gen)
	if _, err := a.bindTunnel(sess, seed, "127.0.0.1", nil, func(*publicListener, net.Conn) {}); err != nil {
		t.Fatal(err)
	}
	return &consentFixture{g: g, a: a, store: store, sess: sess, ctrl: ctrl, gen: gen}
}

// editedSpecs is a one-tunnel authored edit against the fixture's seed: same
// tunnel, now pinned to a concrete free port.
func editedSpecs(t *testing.T) []control.TunnelSpec {
	t.Helper()
	return []control.TunnelSpec{{ID: "tnl_smp", Name: "SMP", Type: config.TunnelTCP, PublicPort: freePort(t)}}
}

// TestConsentHoldsInboundUntilApproved: an agent-authored config_proposal on a
// consent session is held for the operator — nothing adopts, nothing rebinds —
// then approval adopts it, bumps the generation, pushes the resolved set, and
// answers with an approving decision naming the new truth.
func TestConsentHoldsInboundUntilApproved(t *testing.T) {
	f := newConsentFixture(t)
	edit := editedSpecs(t)
	prop := control.ConfigProposal{
		ProposalID: "prp_test1", BaseGen: f.gen, Tunnels: edit,
		Hash: control.HashTunnels(edit),
	}
	if err := f.g.handleControlMsg(f.sess, nil, mkEnv(t, control.TypeConfigProposal, prop)); err != nil {
		t.Fatalf("handleControlMsg: %v", err)
	}

	// Held, not applied: the stored config and its generation are untouched.
	if _, gen, _ := f.store.DesiredConfig("agt_one"); gen != f.gen {
		t.Fatalf("proposal was adopted without approval: generation %d, want %d", gen, f.gen)
	}
	view := f.g.AgentDesiredConfig("agt_one")
	if view.PendingInbound == nil || view.PendingInbound.ProposalID != "prp_test1" {
		t.Fatalf("pending inbound proposal not surfaced: %+v", view.PendingInbound)
	}
	if !view.ConsentActive {
		t.Fatal("a live consent session must report ConsentActive")
	}

	if err := f.g.ApproveAgentProposal("agt_one", "prp_test1"); err != nil {
		t.Fatalf("ApproveAgentProposal: %v", err)
	}
	specs, gen, _ := f.store.DesiredConfig("agt_one")
	if gen <= f.gen {
		t.Fatalf("approval must bump the generation: %d, want > %d", gen, f.gen)
	}
	if len(specs) != 1 || specs[0].PublicPort != edit[0].PublicPort {
		t.Fatalf("adopted set = %+v, want the approved edit %+v", specs, edit)
	}
	if bound, ok := f.g.TunnelPort("tnl_smp"); !ok || bound != edit[0].PublicPort {
		t.Fatalf("listener on %d (ok=%v), want the approved %d", bound, ok, edit[0].PublicPort)
	}
	dec := lastDecision(t, f.ctrl)
	if dec == nil || !dec.Approved || dec.ProposalID != "prp_test1" {
		t.Fatalf("agent must get an approving decision, got %+v", dec)
	}
	if dec.Generation != gen || dec.Hash != control.HashTunnels(specs) {
		t.Fatalf("decision must name the adopted truth: %+v (gen %d)", dec, gen)
	}
	if push := lastPush(t, f.ctrl); push == nil || push.Generation != gen {
		t.Fatalf("approval must push the adopted set, got %+v", push)
	}
	if v := f.g.AgentDesiredConfig("agt_one"); v.PendingInbound != nil {
		t.Fatal("approval must clear the pending proposal")
	}
}

// TestConsentRejectKeepsConfigAndSendsReason: rejecting an agent's proposal changes
// nothing and carries the operator's reason back verbatim.
func TestConsentRejectKeepsConfigAndSendsReason(t *testing.T) {
	f := newConsentFixture(t)
	edit := editedSpecs(t)
	prop := control.ConfigProposal{ProposalID: "prp_rej", BaseGen: f.gen, Tunnels: edit, Hash: control.HashTunnels(edit)}
	if err := f.g.handleControlMsg(f.sess, nil, mkEnv(t, control.TypeConfigProposal, prop)); err != nil {
		t.Fatal(err)
	}
	if err := f.g.RejectAgentProposal("agt_one", "prp_rej", "that port is reserved"); err != nil {
		t.Fatalf("RejectAgentProposal: %v", err)
	}
	if _, gen, _ := f.store.DesiredConfig("agt_one"); gen != f.gen {
		t.Fatalf("rejection must not change the config: generation %d, want %d", gen, f.gen)
	}
	dec := lastDecision(t, f.ctrl)
	if dec == nil || dec.Approved || dec.ProposalID != "prp_rej" || dec.Reason != "that port is reserved" {
		t.Fatalf("agent must get the rejection with its reason, got %+v", dec)
	}
	if v := f.g.AgentDesiredConfig("agt_one"); v.PendingInbound != nil {
		t.Fatal("rejection must clear the pending proposal")
	}
}

// TestConsentStaleProposalRefusedOnApprove: if the authoritative config moved while
// a proposal waited (a reclaim, another adopt), approving it must refuse — silently
// adopting would clobber the newer truth — and the agent is told to re-propose.
func TestConsentStaleProposalRefusedOnApprove(t *testing.T) {
	f := newConsentFixture(t)
	edit := editedSpecs(t)
	prop := control.ConfigProposal{ProposalID: "prp_stale", BaseGen: f.gen, Tunnels: edit, Hash: control.HashTunnels(edit)}
	if err := f.g.handleControlMsg(f.sess, nil, mkEnv(t, control.TypeConfigProposal, prop)); err != nil {
		t.Fatal(err)
	}
	// The truth moves while the proposal is pending.
	moved, ok := f.store.AdoptConfig("agt_one", []control.TunnelSpec{{ID: "tnl_smp", Name: "SMP", Type: config.TunnelTCP, PublicPort: 0}})
	if !ok {
		t.Fatal("mid-flight AdoptConfig")
	}
	if err := f.g.ApproveAgentProposal("agt_one", "prp_stale"); err == nil {
		t.Fatal("approving a stale proposal must error")
	}
	if _, gen, _ := f.store.DesiredConfig("agt_one"); gen != moved {
		t.Fatalf("stale approval must not adopt: generation %d, want %d", gen, moved)
	}
	dec := lastDecision(t, f.ctrl)
	if dec == nil || dec.Approved || dec.ProposalID != "prp_stale" || dec.Reason == "" {
		t.Fatalf("agent must get a reasoned rejection for the stale proposal, got %+v", dec)
	}
}

// TestGatewayProposeAndAgentDecides: the gateway-authored path — propose sends a
// config_proposal (nothing applies), an approving decision adopts + pushes, and a
// rejection adopts nothing but records a declined event carrying the reason.
func TestGatewayProposeAndAgentDecides(t *testing.T) {
	t.Run("approved", func(t *testing.T) {
		f := newConsentFixture(t)
		edit := editedSpecs(t)
		if err := f.g.ProposeAgentConfig("agt_one", edit); err != nil {
			t.Fatalf("ProposeAgentConfig: %v", err)
		}
		sent := lastProposal(t, f.ctrl)
		if sent == nil || sent.ProposalID == "" || sent.BaseGen != f.gen {
			t.Fatalf("agent must receive the proposal with a fresh ID and the base generation, got %+v", sent)
		}
		if _, gen, _ := f.store.DesiredConfig("agt_one"); gen != f.gen {
			t.Fatalf("proposing must not adopt: generation %d, want %d", gen, f.gen)
		}
		view := f.g.AgentDesiredConfig("agt_one")
		if view.PendingOutbound == nil || view.PendingOutbound.ProposalID != sent.ProposalID {
			t.Fatalf("pending outbound proposal not surfaced: %+v", view.PendingOutbound)
		}

		dec := control.ConfigDecision{ProposalID: sent.ProposalID, Approved: true}
		if err := f.g.handleControlMsg(f.sess, nil, mkEnv(t, control.TypeConfigDecision, dec)); err != nil {
			t.Fatal(err)
		}
		specs, gen, _ := f.store.DesiredConfig("agt_one")
		if gen <= f.gen || len(specs) != 1 || specs[0].PublicPort != edit[0].PublicPort {
			t.Fatalf("approval must adopt the proposed set (gen %d > %d): %+v", gen, f.gen, specs)
		}
		if push := lastPush(t, f.ctrl); push == nil || push.Generation != gen {
			t.Fatalf("approval must push the adopted truth, got %+v", push)
		}
		if v := f.g.AgentDesiredConfig("agt_one"); v.PendingOutbound != nil {
			t.Fatal("the decision must clear the pending outbound proposal")
		}
	})

	t.Run("rejected", func(t *testing.T) {
		f := newConsentFixture(t)
		edit := editedSpecs(t)
		if err := f.g.ProposeAgentConfig("agt_one", edit); err != nil {
			t.Fatal(err)
		}
		sent := lastProposal(t, f.ctrl)
		dec := control.ConfigDecision{ProposalID: sent.ProposalID, Approved: false, Reason: "not while players are online"}
		if err := f.g.handleControlMsg(f.sess, nil, mkEnv(t, control.TypeConfigDecision, dec)); err != nil {
			t.Fatal(err)
		}
		if _, gen, _ := f.store.DesiredConfig("agt_one"); gen != f.gen {
			t.Fatalf("a rejected proposal must not adopt: generation %d, want %d", gen, f.gen)
		}
		ev := findEvent(f.a, EventProposalDeclined)
		if ev == nil || ev.AgentID != "agt_one" {
			t.Fatalf("a rejection must record a declined event, got %+v", ev)
		}
		if !strings.Contains(ev.Message, "not while players are online") {
			t.Fatalf("the declined event must carry the agent's reason: %q", ev.Message)
		}
		if v := f.g.AgentDesiredConfig("agt_one"); v.PendingOutbound != nil {
			t.Fatal("the decision must clear the pending outbound proposal")
		}
	})
}

// TestProposeAgentConfigRefusesHonestly: proposing to an offline, legacy-capability,
// or out-of-scope target fails fast with a reason — never a silent no-op.
func TestProposeAgentConfigRefusesHonestly(t *testing.T) {
	t.Run("offline agent", func(t *testing.T) {
		f := newConsentFixture(t)
		if err := f.g.ProposeAgentConfig("agt_ghost", editedSpecs(t)); err == nil {
			t.Fatal("proposing to an unconnected agent must error")
		}
	})
	t.Run("no consent capability", func(t *testing.T) {
		f := newConsentFixture(t, control.CapGatewayConfig) // gateway-config only, no consent
		if err := f.g.ProposeAgentConfig("agt_one", editedSpecs(t)); err == nil {
			t.Fatal("proposing to a session without config-consent must error")
		}
	})
	t.Run("spec outside policy", func(t *testing.T) {
		f := newConsentFixture(t)
		bad := []control.TunnelSpec{{ID: "tnl_smp", Name: "SMP", Type: "udp", PublicPort: 1}}
		if err := f.g.ProposeAgentConfig("agt_one", bad); err == nil {
			t.Fatal("an invalid spec must fail the propose, not a later approval")
		}
	})
}

// TestConsentGatedMessagesIgnoredWithoutCap: config_proposal/config_decision frames
// on a session that never negotiated config-consent are ignored (unknown-type
// tolerance for a nonconforming peer), and the auto-adopt propose_config path still
// works for a CapGatewayConfig-only peer — full backward compatibility.
func TestConsentGatedMessagesIgnoredWithoutCap(t *testing.T) {
	f := newConsentFixture(t, control.CapGatewayConfig)
	edit := editedSpecs(t)

	prop := control.ConfigProposal{ProposalID: "prp_x", BaseGen: f.gen, Tunnels: edit, Hash: control.HashTunnels(edit)}
	if err := f.g.handleControlMsg(f.sess, nil, mkEnv(t, control.TypeConfigProposal, prop)); err != nil {
		t.Fatalf("a consent frame on a legacy session must be ignored, not an error: %v", err)
	}
	if v := f.g.AgentDesiredConfig("agt_one"); v.PendingInbound != nil || v.ConsentActive {
		t.Fatalf("no consent: nothing may be held (%+v)", v)
	}
	if _, gen, _ := f.store.DesiredConfig("agt_one"); gen != f.gen {
		t.Fatal("a consent frame on a legacy session must not adopt anything")
	}

	// The legacy auto-adopt path is untouched: propose_config adopts immediately.
	if err := f.g.handleControlMsg(f.sess, nil, mkEnv(t, control.TypeProposeConfig, control.ProposeConfig{Tunnels: edit})); err != nil {
		t.Fatal(err)
	}
	specs, gen, _ := f.store.DesiredConfig("agt_one")
	if gen <= f.gen || len(specs) != 1 || specs[0].PublicPort != edit[0].PublicPort {
		t.Fatalf("legacy propose_config must still auto-adopt (gen %d > %d): %+v", gen, f.gen, specs)
	}
}

// TestConsentNegotiatedOnlyWhenEnrolled: consent is keyed to a durable identity
// exactly like the gateway-held config it layers on, so a shared-token agent that
// offers it has it negotiated away and keeps the agent-push path. Otherwise a
// self-asserted identity could hold an operator's approval hostage.
func TestConsentNegotiatedOnlyWhenEnrolled(t *testing.T) {
	offered := []string{control.CapTunnelSync, control.CapGatewayConfig, control.CapConfigConsent}
	cases := []struct {
		name       string
		identity   Identity
		wantConfig bool
	}{
		{"enrolled agent keeps consent", Identity{AgentID: "agt_one", PubKey: pub(1)}, true},
		{"shared-token agent loses consent", Identity{AgentID: "agt_one"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newActor(discardLogger())
			ctx, cancel := context.WithCancel(context.Background())
			go a.run(ctx)
			t.Cleanup(cancel)
			g := &Gateway{cfg: config.Default()}
			g.actor.Store(a)

			hello := &control.Hello{AgentID: tc.identity.AgentID, Capabilities: offered}
			sess, negotiated, err := g.buildAndAdmit(ctx, nil, hello, tc.identity, "127.0.0.1", discardLogger())
			if err != nil {
				t.Fatalf("buildAndAdmit: %v", err)
			}
			set := control.NewCapSet(negotiated)
			if set.Has(control.CapConfigConsent) != tc.wantConfig {
				t.Fatalf("negotiated %v, want config-consent present=%v", negotiated, tc.wantConfig)
			}
			// Consent must never outlive the config authority it layers on.
			if set.Has(control.CapConfigConsent) && !set.Has(control.CapGatewayConfig) {
				t.Fatalf("negotiated consent without gateway-config: %v", negotiated)
			}
			if sess.Has(control.CapConfigConsent) != tc.wantConfig {
				t.Fatal("the session's cap set must match the negotiated list")
			}
			// tunnel-sync survives either way — a shared-token agent still works.
			if !set.Has(control.CapTunnelSync) {
				t.Fatalf("tunnel-sync must survive negotiation: %v", negotiated)
			}
		})
	}
}

// TestConsentBlocksProposeConfigBypass: once consent is negotiated and the gateway
// holds adopted config, a bare propose_config (the auto-adopt path) is refused —
// otherwise a peer could bypass the operator's approval. Only the first-contact
// seed (no adopted config yet) still rides propose_config.
func TestConsentBlocksProposeConfigBypass(t *testing.T) {
	f := newConsentFixture(t)
	edit := editedSpecs(t)
	if err := f.g.handleControlMsg(f.sess, nil, mkEnv(t, control.TypeProposeConfig, control.ProposeConfig{Tunnels: edit})); err != nil {
		t.Fatalf("the bypass must be ignored, not an error: %v", err)
	}
	if _, gen, _ := f.store.DesiredConfig("agt_one"); gen != f.gen {
		t.Fatalf("propose_config on a consent session with adopted config must not adopt: generation %d, want %d", gen, f.gen)
	}
}
