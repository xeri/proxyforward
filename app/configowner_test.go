package app

import (
	"testing"

	"proxyforward/internal/config"
)

func agentCfg() *config.Config {
	cfg := config.Default()
	cfg.Role = config.RoleAgent
	cfg.Agent.GatewayHost = "gw.example.com"
	cfg.Agent.CertFingerprint = "sha256:" + "ab"
	cfg.Agent.AgentID = "agt_original"
	cfg.Agent.Tunnels = []config.Tunnel{{
		ID: "tnl_mc", Name: "Minecraft", Type: config.TunnelTCP,
		LocalAddr: "127.0.0.1:25565", PublicPort: 25565, Enabled: true,
	}}
	return cfg
}

// TestEngineDoesNotShareTheAppsConfig (D7) pins the ownership rule a data race
// came out of: a *config.Config has exactly one owner.
//
// The app used to hand its own document to engine.New, so the same struct had two
// writers under two different locks — the app's a.mu and the agent's cfgMu.
// SaveSettings' whole-struct write (`*a.cfg = cfg`) then raced enabledTunnelByID's
// scan of Agent.Tunnels, which runs on the data path for every player that
// connects. A torn slice header there is an out-of-range index, not a stale read.
//
// This asserts the separation directly: an app-side edit must be invisible to the
// running engine until something carries it across on purpose (ApplyTunnels, a
// persister, or a restart).
func TestEngineDoesNotShareTheAppsConfig(t *testing.T) {
	a := newTestAppDir(t, agentCfg())
	a.mu.Lock()
	a.startEngineLocked()
	eng := a.eng
	a.mu.Unlock()
	if eng == nil || eng.Agent == nil {
		t.Fatal("engine did not start with an agent role")
	}
	t.Cleanup(func() { a.engMu.Lock(); defer a.engMu.Unlock(); _ = a.stopEngine() })

	// The app mutates its own document the way SaveSettings and SaveTunnels do,
	// including an in-place element write (the shape a shallow copy would leak).
	a.mu.Lock()
	a.cfg.Agent.Tunnels[0].Name = "renamed by the app"
	a.cfg.Agent.Tunnels[0].LocalAddr = "127.0.0.1:1"
	a.cfg.Agent.Tunnels = append(a.cfg.Agent.Tunnels, config.Tunnel{
		ID: "tnl_added", Name: "added", Type: config.TunnelTCP,
		LocalAddr: "127.0.0.1:2", PublicPort: 25566, Enabled: true,
	})
	a.cfg.Agent.AgentID = "agt_renamed"
	a.mu.Unlock()

	got := eng.Agent.Tunnels()
	if len(got) != 1 {
		t.Fatalf("engine sees %d tunnels, want the 1 it was built with — the app's append reached it", len(got))
	}
	if got[0].Name != "Minecraft" || got[0].LocalAddr != "127.0.0.1:25565" {
		t.Fatalf("engine sees %+v — the app's in-place edit reached it through a shared backing array", got[0])
	}
	if id := eng.Agent.AgentID(); id != "agt_original" {
		t.Fatalf("engine sees agent id %q, want the one it was built with", id)
	}
}

// TestGetConfigHandsOutACopy (D7): the binding layer marshals GetConfig's result
// after the method has released a.mu, so returning the live document would
// serialize a struct the app keeps mutating — a read under no lock at all.
func TestGetConfigHandsOutACopy(t *testing.T) {
	a := newTestAppDir(t, agentCfg())
	got := a.GetConfig()
	if got == a.cfg {
		t.Fatal("GetConfig returned the live document; the binding marshals it outside the lock")
	}
	got.Agent.Tunnels[0].Name = "mutated by a caller"
	if a.cfg.Agent.Tunnels[0].Name != "Minecraft" {
		t.Fatal("mutating the returned config reached the app's own — the copy shares a backing array")
	}
}

// TestBadPushedConfigDoesNotWedgeSaving (D7): Config.Save validates, so a
// persister that mutated the live document first and saved second left the
// invalid set behind when the save was refused — and every later save of
// anything, a theme or the stopped flag, failed the same validation from then on.
//
// A gateway can produce exactly that input: push_config auto-applies and its
// specs carry no LocalAddr, so a spec for a tunnel this agent has never held
// merges into one with nowhere to dial, which config.Validate rejects. One such
// push must not stop the config file being written ever again.
func TestBadPushedConfigDoesNotWedgeSaving(t *testing.T) {
	a := newTestAppDir(t, agentCfg())
	if err := a.SetTheme("light"); err != nil {
		t.Fatalf("baseline save failed: %v", err)
	}

	// What mergeTunnels produces for a pushed spec naming an unknown tunnel.
	bad := []config.Tunnel{{ID: "tnl_invented", Name: "invented", Type: config.TunnelTCP, Enabled: true}}
	if err := a.persistPushedConfig(bad, 7); err == nil {
		t.Fatal("a tunnel with no local address was accepted; config.Validate rejects it")
	}

	// The live document must be untouched, and unrelated writes must still land.
	a.mu.Lock()
	kept := append([]config.Tunnel(nil), a.cfg.Agent.Tunnels...)
	a.mu.Unlock()
	if len(kept) != 1 || kept[0].ID != "tnl_mc" {
		t.Fatalf("the refused set reached the live config: %+v", kept)
	}
	if err := a.SetTheme("dark"); err != nil {
		t.Fatalf("a later save failed after the refused push — the config is wedged: %v", err)
	}
	reloaded, err := config.Load(a.configPath)
	if err != nil {
		t.Fatalf("config no longer loads: %v", err)
	}
	if reloaded.UI.Theme != "dark" || len(reloaded.Agent.Tunnels) != 1 {
		t.Fatalf("on-disk config wrong after the refused push: theme=%q tunnels=%d",
			reloaded.UI.Theme, len(reloaded.Agent.Tunnels))
	}
}
