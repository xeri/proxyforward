package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// captureStream is a transport.Stream that records every frame the agent writes,
// so a test can assert exactly what reached the gateway. Reads EOF immediately â€”
// no test drives the agent's read loop through it.
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

// lastFrame decodes the newest captured frame of one type, or nil.
func lastFrame[T any](t *testing.T, c *captureStream, msgType string) *T {
	t.Helper()
	envs := c.envelopes(t)
	for i := len(envs) - 1; i >= 0; i-- {
		if envs[i].Type != msgType {
			continue
		}
		got, err := control.Decode[T](envs[i])
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	return nil
}

func mkEnv(t *testing.T, msgType string, payload any) *control.Envelope {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &control.Envelope{Type: msgType, Data: data}
}

func tunnel(id, name string, port int) config.Tunnel {
	return config.Tunnel{
		ID: id, Name: name, Type: config.TunnelTCP,
		LocalAddr: "127.0.0.1:25565", PublicPort: port, Enabled: true,
	}
}

// consentFixture is an agent with one enabled tunnel and a live fake session whose
// caps are the test's to choose â€” the steady state an edit starts from.
type consentFixture struct {
	a    *Agent
	s    *session
	ctrl *captureStream
}

func newConsentFixture(t *testing.T, caps ...string) *consentFixture {
	t.Helper()
	if caps == nil {
		caps = []string{control.CapGatewayConfig, control.CapConfigConsent}
	}
	cfg := config.Default()
	cfg.Role = config.RoleAgent
	cfg.Agent.Tunnels = []config.Tunnel{tunnel("tnl_smp", "SMP", 25565)}
	a := New(cfg, t.TempDir(), slog.New(slog.DiscardHandler))
	ctrl := &captureStream{}
	s := &session{agent: a, ctrl: ctrl, caps: control.NewCapSet(caps)}
	a.curSession.Store(s)
	return &consentFixture{a: a, s: s, ctrl: ctrl}
}

// edited is the fixture's tunnel moved to a different public port.
func edited() []config.Tunnel { return []config.Tunnel{tunnel("tnl_smp", "SMP", 25600)} }

// TestProposeTunnelsChangesNothingLocally: a local edit on a consent session goes
// out as a config_proposal carrying the current generation as its base, and the
// running set is untouched until the gateway's push says otherwise.
func TestProposeTunnelsChangesNothingLocally(t *testing.T) {
	f := newConsentFixture(t)
	f.a.configGen.Store(4)

	proposed, err := f.a.ProposeTunnels(edited())
	if err != nil {
		t.Fatalf("ProposeTunnels: %v", err)
	}
	if !proposed {
		t.Fatal("a public-port change is the gateway's business — it must be proposed")
	}

	prop := lastFrame[control.ConfigProposal](t, f.ctrl, control.TypeConfigProposal)
	if prop == nil {
		t.Fatal("no config_proposal reached the gateway")
	}
	if prop.ProposalID == "" || prop.BaseGen != 4 {
		t.Fatalf("proposal = %+v, want a fresh id based on generation 4", *prop)
	}
	if len(prop.Tunnels) != 1 || prop.Tunnels[0].PublicPort != 25600 {
		t.Fatalf("proposal tunnels = %+v, want the edited port 25600", prop.Tunnels)
	}
	if prop.Hash != control.HashTunnels(prop.Tunnels) {
		t.Fatal("proposal hash must cover its own set")
	}
	// Nothing applied: the running config still holds the pre-edit port.
	if got := f.a.snapshotTunnels(); len(got) != 1 || got[0].PublicPort != 25565 {
		t.Fatalf("a proposal must not change the running set, got %+v", got)
	}
	v := f.a.PendingConsent()
	if !v.ConsentActive || v.Outgoing == nil || v.Outgoing.ProposalID != prop.ProposalID {
		t.Fatalf("the edit must surface as pending, got %+v", v)
	}
}

// TestLocalDecisionApprovedThenPushCommits: approval alone changes nothing â€” the
// gateway's authoritative push is the commit â€” and it clears the pending mark.
func TestLocalDecisionApprovedThenPushCommits(t *testing.T) {
	f := newConsentFixture(t)
	if _, err := f.a.ProposeTunnels(edited()); err != nil {
		t.Fatal(err)
	}
	prop := lastFrame[control.ConfigProposal](t, f.ctrl, control.TypeConfigProposal)

	dec := control.ConfigDecision{ProposalID: prop.ProposalID, Approved: true, Generation: 5, Hash: prop.Hash}
	if err := f.s.handleControlMsg(mkEnv(t, control.TypeConfigDecision, dec)); err != nil {
		t.Fatalf("handleControlMsg: %v", err)
	}
	if v := f.a.PendingConsent(); v.Outgoing != nil || v.LastDecline != "" {
		t.Fatalf("approval must clear the pending edit with no decline, got %+v", v)
	}
	// The decision itself applies nothing; the push that follows does.
	if got := f.a.snapshotTunnels(); got[0].PublicPort != 25565 {
		t.Fatalf("the decision must not apply the edit, got port %d", got[0].PublicPort)
	}

	push := control.PushConfig{Generation: 5, Tunnels: enabledSpecs(edited())}
	push.Hash = control.HashTunnels(push.Tunnels)
	if err := f.s.handleControlMsg(mkEnv(t, control.TypePushConfig, push)); err != nil {
		t.Fatal(err)
	}
	if got := f.a.snapshotTunnels(); len(got) != 1 || got[0].PublicPort != 25600 {
		t.Fatalf("the push must commit the approved edit, got %+v", got)
	}
	if f.a.configGen.Load() != 5 {
		t.Fatalf("generation = %d, want the pushed 5", f.a.configGen.Load())
	}
	if ack := lastFrame[control.ConfigAck](t, f.ctrl, control.TypeConfigAck); ack == nil || ack.Generation != 5 {
		t.Fatalf("the agent must ack the pushed generation, got %+v", ack)
	}
}

// TestLocalOnlyEditNeedsNoApproval: an edit the gateway cannot see — here a local
// backend address — changes nothing on its side, so it applies at once instead of
// waiting on an operator who has no stake in it. Consent governs the gateway's set,
// not this machine's wiring.
func TestLocalOnlyEditNeedsNoApproval(t *testing.T) {
	f := newConsentFixture(t)
	moved := []config.Tunnel{tunnel("tnl_smp", "SMP", 25565)}
	moved[0].LocalAddr = "127.0.0.1:19132"

	proposed, err := f.a.ProposeTunnels(moved)
	if err != nil {
		t.Fatalf("ProposeTunnels: %v", err)
	}
	if proposed {
		t.Fatal("a local-address-only edit must not need the gateway's approval")
	}
	if lastFrame[control.ConfigProposal](t, f.ctrl, control.TypeConfigProposal) != nil {
		t.Fatal("a local-only edit must not reach the gateway at all")
	}
	if got := f.a.snapshotTunnels(); got[0].LocalAddr != "127.0.0.1:19132" {
		t.Fatalf("a local-only edit must apply immediately, got %q", got[0].LocalAddr)
	}
	if v := f.a.PendingConsent(); v.Outgoing != nil {
		t.Fatal("a local-only edit leaves nothing pending")
	}
}

// TestApprovedPushKeepsAgentLocalFields: the wire carries only the gateway's half of
// a tunnel, so an approved new tunnel comes back with no local address. The authored
// draft behind the pending proposal must fill it in — otherwise the tunnel goes live
// publicly with nowhere to dial locally.
func TestApprovedPushKeepsAgentLocalFields(t *testing.T) {
	f := newConsentFixture(t)
	added := []config.Tunnel{
		tunnel("tnl_smp", "SMP", 25565),
		{ID: "tnl_new", Name: "New", Type: config.TunnelTCP, LocalAddr: "127.0.0.1:19132", PublicPort: 0, Enabled: true},
	}
	added[1].Options.ProxyProtocolV2 = true // agent-local too: the wire never carries it

	if _, err := f.a.ProposeTunnels(added); err != nil {
		t.Fatal(err)
	}
	// The gateway approves, resolving the ephemeral port, and pushes the adopted set
	// — which names the new tunnel but carries none of its agent-local fields.
	pushed := []control.TunnelSpec{
		{ID: "tnl_smp", Name: "SMP", Type: config.TunnelTCP, PublicPort: 25565},
		{ID: "tnl_new", Name: "New", Type: config.TunnelTCP, PublicPort: 41000},
	}
	push := control.PushConfig{Generation: 2, Tunnels: pushed, Hash: control.HashTunnels(pushed)}
	if err := f.s.handleControlMsg(mkEnv(t, control.TypePushConfig, push)); err != nil {
		t.Fatal(err)
	}

	var got *config.Tunnel
	for i, tn := range f.a.snapshotTunnels() {
		if tn.ID == "tnl_new" {
			got = &f.a.snapshotTunnels()[i]
			break
		}
	}
	if got == nil {
		t.Fatal("the approved tunnel never reached the running set")
	}
	if got.LocalAddr != "127.0.0.1:19132" {
		t.Fatalf("the approved tunnel lost its local address: %q — it would be undialable", got.LocalAddr)
	}
	if !got.Options.ProxyProtocolV2 {
		t.Fatal("the approved tunnel lost its agent-local PROXY-protocol option")
	}
	// The gateway still owns its half: the resolved port wins over the proposed 0.
	if got.PublicPort != 41000 {
		t.Fatalf("public port = %d, want the gateway's resolved 41000", got.PublicPort)
	}
}

// TestLocalDecisionRejectedKeepsConfigAndReason: a declined edit leaves the running
// set alone (there was never anything to roll back) and keeps the gateway's reason
// for the GUI to explain the no-op save.
func TestLocalDecisionRejectedKeepsConfigAndReason(t *testing.T) {
	f := newConsentFixture(t)
	if _, err := f.a.ProposeTunnels(edited()); err != nil {
		t.Fatal(err)
	}
	prop := lastFrame[control.ConfigProposal](t, f.ctrl, control.TypeConfigProposal)

	dec := control.ConfigDecision{ProposalID: prop.ProposalID, Reason: "port 25600 is reserved"}
	if err := f.s.handleControlMsg(mkEnv(t, control.TypeConfigDecision, dec)); err != nil {
		t.Fatal(err)
	}
	if got := f.a.snapshotTunnels(); got[0].PublicPort != 25565 {
		t.Fatalf("a rejected edit must leave the running set alone, got port %d", got[0].PublicPort)
	}
	v := f.a.PendingConsent()
	if v.Outgoing != nil {
		t.Fatal("rejection must clear the pending edit")
	}
	if v.LastDecline != "port 25600 is reserved" {
		t.Fatalf("the gateway's reason must survive for the GUI, got %q", v.LastDecline)
	}
}

// TestGatewayProposalHeldUntilDecided: an incoming gateway edit is held for the
// user, never auto-applied; approving answers yes and applies nothing locally
// (the push does), rejecting carries the reason back.
func TestGatewayProposalHeldUntilDecided(t *testing.T) {
	incoming := func(t *testing.T) control.ConfigProposal {
		specs := enabledSpecs(edited())
		return control.ConfigProposal{
			ProposalID: "prp_gw", BaseGen: 1, Tunnels: specs, Hash: control.HashTunnels(specs),
		}
	}

	t.Run("approve", func(t *testing.T) {
		f := newConsentFixture(t)
		if err := f.s.handleControlMsg(mkEnv(t, control.TypeConfigProposal, incoming(t))); err != nil {
			t.Fatal(err)
		}
		v := f.a.PendingConsent()
		if v.Incoming == nil || v.Incoming.ProposalID != "prp_gw" {
			t.Fatalf("the gateway's proposal must be held for the user, got %+v", v)
		}
		if got := f.a.snapshotTunnels(); got[0].PublicPort != 25565 {
			t.Fatal("a held proposal must not apply")
		}

		if err := f.a.ApproveGatewayProposal("prp_gw"); err != nil {
			t.Fatalf("ApproveGatewayProposal: %v", err)
		}
		dec := lastFrame[control.ConfigDecision](t, f.ctrl, control.TypeConfigDecision)
		if dec == nil || !dec.Approved || dec.ProposalID != "prp_gw" {
			t.Fatalf("the gateway must get an approving decision, got %+v", dec)
		}
		if f.a.PendingConsent().Incoming != nil {
			t.Fatal("approval must clear the held proposal")
		}
		// Still not applied locally â€” the gateway's push is the commit.
		if got := f.a.snapshotTunnels(); got[0].PublicPort != 25565 {
			t.Fatal("approving must not apply locally; the push commits")
		}
	})

	t.Run("reject", func(t *testing.T) {
		f := newConsentFixture(t)
		if err := f.s.handleControlMsg(mkEnv(t, control.TypeConfigProposal, incoming(t))); err != nil {
			t.Fatal(err)
		}
		if err := f.a.RejectGatewayProposal("prp_gw", "that port is my LAN printer"); err != nil {
			t.Fatalf("RejectGatewayProposal: %v", err)
		}
		dec := lastFrame[control.ConfigDecision](t, f.ctrl, control.TypeConfigDecision)
		if dec == nil || dec.Approved || dec.Reason != "that port is my LAN printer" {
			t.Fatalf("the gateway must get the rejection with its reason, got %+v", dec)
		}
		if f.a.PendingConsent().Incoming != nil {
			t.Fatal("rejection must clear the held proposal")
		}
		if got := f.a.snapshotTunnels(); got[0].PublicPort != 25565 {
			t.Fatal("a rejected proposal must change nothing")
		}
	})
}

// TestGatewayProposalCorruptHashRefused: a proposal whose hash doesn't cover its
// set is refused outright â€” the user must never be asked to approve something the
// operator didn't author.
func TestGatewayProposalCorruptHashRefused(t *testing.T) {
	f := newConsentFixture(t)
	prop := control.ConfigProposal{
		ProposalID: "prp_bad", Tunnels: enabledSpecs(edited()), Hash: "deadbeef",
	}
	if err := f.s.handleControlMsg(mkEnv(t, control.TypeConfigProposal, prop)); err != nil {
		t.Fatalf("a corrupt proposal must not kill the session: %v", err)
	}
	if f.a.PendingConsent().Incoming != nil {
		t.Fatal("a hash-mismatched proposal must not be held")
	}
	dec := lastFrame[control.ConfigDecision](t, f.ctrl, control.TypeConfigDecision)
	if dec == nil || dec.Approved || dec.Reason == "" {
		t.Fatalf("a corrupt proposal must be refused with a reason, got %+v", dec)
	}
}

// TestConsentIgnoredWithoutCap: consent frames on a session that never negotiated
// the capability are ignored (a nonconforming peer is not fatal), and edits keep
// the old auto-adopt propose_config path.
func TestConsentIgnoredWithoutCap(t *testing.T) {
	f := newConsentFixture(t, control.CapGatewayConfig) // gateway-config only

	if f.a.ConsentActive() {
		t.Fatal("consent must not engage without the capability")
	}
	prop := control.ConfigProposal{ProposalID: "prp_x", Tunnels: enabledSpecs(edited())}
	if err := f.s.handleControlMsg(mkEnv(t, control.TypeConfigProposal, prop)); err != nil {
		t.Fatalf("a consent frame on a legacy session must be ignored, not an error: %v", err)
	}
	if f.a.PendingConsent().Incoming != nil {
		t.Fatal("no capability: nothing may be held")
	}

	// The legacy path is untouched: an edit auto-proposes and applies locally.
	f.a.ApplyTunnels(edited())
	if lastFrame[control.ProposeConfig](t, f.ctrl, control.TypeProposeConfig) == nil {
		t.Fatal("a gateway-config session must still send propose_config")
	}
	if got := f.a.snapshotTunnels(); got[0].PublicPort != 25600 {
		t.Fatalf("the legacy path still applies locally, got port %d", got[0].PublicPort)
	}
}

// TestApplyTunnelsRefusesOnConsentSession: the hot-apply path must never write
// behind the operator's back once consent is negotiated â€” callers propose instead.
func TestApplyTunnelsRefusesOnConsentSession(t *testing.T) {
	f := newConsentFixture(t)
	f.a.ApplyTunnels(edited())
	if got := f.a.snapshotTunnels(); got[0].PublicPort != 25565 {
		t.Fatalf("ApplyTunnels must refuse on a consent session, got port %d", got[0].PublicPort)
	}
	if lastFrame[control.ProposeConfig](t, f.ctrl, control.TypeProposeConfig) != nil {
		t.Fatal("ApplyTunnels must not send the auto-adopt propose_config under consent")
	}
}

// TestConsentDropsWithTheSession: pending state is session-scoped â€” a disconnect
// drops it, and the GUI honestly reports no consent rather than a stuck proposal.
func TestConsentDropsWithTheSession(t *testing.T) {
	f := newConsentFixture(t)
	if _, err := f.a.ProposeTunnels(edited()); err != nil {
		t.Fatal(err)
	}
	if f.a.PendingConsent().Outgoing == nil {
		t.Fatal("expected a pending edit")
	}
	f.a.curSession.Store(nil) // the session dies

	v := f.a.PendingConsent()
	if v.ConsentActive || v.Incoming != nil || v.Outgoing != nil {
		t.Fatalf("pending state must not outlive the session, got %+v", v)
	}
	if _, err := f.a.ProposeTunnels(edited()); err == nil {
		t.Fatal("proposing with no session must fail honestly")
	}
	if err := f.a.ApproveGatewayProposal("prp_gw"); err == nil {
		t.Fatal("approving with no session must fail honestly")
	}
}

// TestPushPersistsThroughPersister: the push is the commit, so it must reach the
// disk hook â€” otherwise an approved edit is lost on restart and the gateway has to
// re-push it every connect.
func TestPushPersistsThroughPersister(t *testing.T) {
	f := newConsentFixture(t)
	var (
		mu       sync.Mutex
		saved    []config.Tunnel
		savedGen uint64
	)
	f.a.SetConfigPersister(func(tunnels []config.Tunnel, generation uint64) error {
		mu.Lock()
		defer mu.Unlock()
		saved, savedGen = tunnels, generation
		return nil
	})

	push := control.PushConfig{Generation: 7, Tunnels: enabledSpecs(edited())}
	push.Hash = control.HashTunnels(push.Tunnels)
	if err := f.s.handleControlMsg(mkEnv(t, control.TypePushConfig, push)); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if savedGen != 7 || len(saved) != 1 || saved[0].PublicPort != 25600 {
		t.Fatalf("the push must persist the committed set, got gen %d %+v", savedGen, saved)
	}
	// Agent-local fields the wire never carries survive the round trip.
	if saved[0].LocalAddr != "127.0.0.1:25565" {
		t.Fatalf("persisting must keep agent-local fields, got %q", saved[0].LocalAddr)
	}
}
