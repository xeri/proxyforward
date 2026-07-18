package agent

import (
	"log/slog"
	"testing"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// A gateway push must never clear an agent's own pause bit: Paused is
// agent-local state (like LocalAddr and the PP2 option), and mergeTunnels
// carries the whole known struct through, overwriting only wire fields.
// (pause)
func TestMergeTunnelsPreservesLocalPauseBit(t *testing.T) {
	existing := []config.Tunnel{
		{ID: "tnl_mc", Name: "mc", Type: config.TunnelTCP, LocalAddr: "127.0.0.1:25565", Enabled: true, Paused: true},
	}
	pushed := []control.TunnelSpec{
		{ID: "tnl_mc", Name: "mc", Type: config.TunnelTCP, PublicPort: 25565},
		{ID: "tnl_new", Name: "new", Type: config.TunnelTCP, PublicPort: 25570},
	}
	out := mergeTunnels(existing, pushed)
	byID := map[string]config.Tunnel{}
	for _, tn := range out {
		byID[tn.ID] = tn
	}
	if !byID["tnl_mc"].Paused {
		t.Error("a push cleared the local pause bit")
	}
	if byID["tnl_new"].Paused {
		t.Error("a brand-new pushed tunnel must not be born paused")
	}
}

// SetPause flips exactly the named bit, reports an unknown tunnel, and the
// wire form carries the complete set. (pause)
func TestSetPauseAndLocalPauseState(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.Tunnels = []config.Tunnel{
		{ID: "tnl_a", Name: "a", Enabled: true},
		{ID: "tnl_b", Name: "b", Enabled: true},
	}
	a := &Agent{cfg: cfg, logger: slog.Default()}

	if err := a.SetPause("tnl_a", true, false); err != nil {
		t.Fatalf("SetPause(tnl_a): %v", err)
	}
	if err := a.SetPause("tnl_missing", true, false); err == nil {
		t.Fatal("SetPause on an unknown tunnel must error")
	}
	ps := a.localPauseState()
	if len(ps.TunnelIDs) != 1 || ps.TunnelIDs[0] != "tnl_a" || ps.AgentWide {
		t.Fatalf("localPauseState = %+v, want tnl_a only", ps)
	}

	if err := a.SetPause("", true, false); err != nil {
		t.Fatalf("agent-wide SetPause: %v", err)
	}
	if ps := a.localPauseState(); !ps.AgentWide {
		t.Fatal("agent-wide bit must ride the wire form")
	}
	if !a.agentPaused() {
		t.Fatal("agent-wide bit must be readable for enforcement")
	}
	// Resuming the agent leaves the tunnel bit standing — independent bits.
	if err := a.SetPause("", false, false); err != nil {
		t.Fatal(err)
	}
	if ps := a.localPauseState(); ps.AgentWide || len(ps.TunnelIDs) != 1 {
		t.Fatalf("after agent resume the tunnel bit must stand: %+v", ps)
	}
}

// The persister sees every SetPause with the complete bits — the app owns the
// config file, the agent never writes that disk bare. (pause)
func TestSetPausePersistsThroughPersister(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.Tunnels = []config.Tunnel{{ID: "tnl_a", Name: "a", Enabled: true}}
	a := &Agent{cfg: cfg, logger: slog.Default()}

	var gotWide bool
	var gotTunnels []config.Tunnel
	a.SetPausePersister(func(agentWide bool, tunnels []config.Tunnel) error {
		gotWide, gotTunnels = agentWide, tunnels
		return nil
	})
	if err := a.SetPause("tnl_a", true, false); err != nil {
		t.Fatal(err)
	}
	if gotWide || len(gotTunnels) != 1 || !gotTunnels[0].Paused {
		t.Fatalf("persister saw agentWide=%v tunnels=%+v", gotWide, gotTunnels)
	}
}

// The gateway's reported set answers the UI's "who paused this" — and a nil
// set (link down, or a gateway that never spoke) covers nothing. (pause)
func TestPausedByGateway(t *testing.T) {
	a := &Agent{cfg: &config.Config{}, logger: slog.Default()}
	if a.PausedByGateway("tnl_a") || a.PausedByGateway("") {
		t.Fatal("no reported set must mean nothing paused")
	}
	a.pausedByGateway.Store(newGwPause([]string{"tnl_a"}, false))
	if !a.PausedByGateway("tnl_a") || a.PausedByGateway("tnl_b") || a.PausedByGateway("") {
		t.Fatal("tunnel bit must cover exactly its tunnel")
	}
	a.pausedByGateway.Store(newGwPause(nil, true))
	if !a.PausedByGateway("tnl_b") || !a.PausedByGateway("") {
		t.Fatal("the all bit must cover everything")
	}
}
