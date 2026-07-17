package agent

import (
	"log/slog"
	"testing"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// TestEnabledTunnelByIDIgnoresDisabledTunnels guards the data-plane half of the
// same boundary mergeTunnels guards on the control plane, and it is the half that
// actually matters: handleDataStream picks the local address to dial straight from
// the gateway's open_conn.TunnelID. Fixing only mergeTunnels would have been
// theatre — a gateway that wants a switched-off service never needed to push a
// config for it, it can just name the id on a data stream and be spliced to
// whatever the draft points at (a disabled tnl_rcon is a Minecraft console).
//
// So the lookup itself must refuse anything not enabled. Ids are name slugs
// (link.TunnelID), so the gateway does not need to have been told one to guess it.
func TestEnabledTunnelByIDIgnoresDisabledTunnels(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.Tunnels = []config.Tunnel{
		{ID: "tnl_mc", Name: "mc", LocalAddr: "127.0.0.1:25565", Enabled: true},
		{ID: "tnl_rcon", Name: "rcon", LocalAddr: "127.0.0.1:25575", Enabled: false},
	}
	a := &Agent{cfg: cfg, logger: slog.Default()}

	if got := a.enabledTunnelByID("tnl_mc"); got == nil || got.LocalAddr != "127.0.0.1:25565" {
		t.Errorf("an enabled tunnel must still resolve: %+v", got)
	}
	if got := a.enabledTunnelByID("tnl_rcon"); got != nil {
		t.Errorf("a disabled tunnel resolved to %s — the gateway can dial a service its owner switched off", got.LocalAddr)
	}
	if got := a.enabledTunnelByID("tnl_nope"); got != nil {
		t.Errorf("an unknown tunnel resolved: %+v", got)
	}
}

// TestMergeTunnelsKeepsDisabledDraftsDisabled pins the boundary the gateway's
// authority stops at. Disabling a tunnel is the user's way of saying "do not
// expose this service", and the local draft keeps its LocalAddr so re-enabling it
// is one click. That makes a disabled draft a loaded gun: it names a real local
// service, and nothing but the Enabled flag keeps it off the public internet.
//
// The gateway is not trusted with that flag. A gateway is often someone else's
// machine — the product's whole point is that a friend or a provider hosts it —
// and it only ever learns the *enabled* set (the hello carries a hash of those).
// Tunnel ids are slugs of their names (link.TunnelID: tnl_rcon, tnl_ssh), so a
// hostile gateway can simply guess one and push a spec for it. Before this,
// mergeTunnels answered a pushed spec for a disabled draft by handing back the
// draft — LocalAddr and all — with Enabled forced true, and the agent dialed a
// service its owner had switched off.
func TestMergeTunnelsKeepsDisabledDraftsDisabled(t *testing.T) {
	const rconAddr = "127.0.0.1:25575"
	existing := []config.Tunnel{
		{ID: "tnl_mc", Name: "mc", Type: config.TunnelTCP, LocalAddr: "127.0.0.1:25565", PublicPort: 25565, Enabled: true},
		// The user turned this one off. It still points at a real local service.
		{ID: "tnl_rcon", Name: "rcon", Type: config.TunnelTCP, LocalAddr: rconAddr, Enabled: false},
	}

	// A hostile gateway guesses the id and pushes it as part of the authoritative set.
	pushed := []control.TunnelSpec{
		{ID: "tnl_mc", Name: "mc", Type: config.TunnelTCP, PublicPort: 25565},
		{ID: "tnl_rcon", Name: "rcon", Type: config.TunnelTCP, PublicPort: 25566},
	}

	byID := map[string]config.Tunnel{}
	for _, tun := range mergeTunnels(existing, pushed) {
		byID[tun.ID] = tun
	}

	rcon, ok := byID["tnl_rcon"]
	if !ok {
		t.Fatal("the disabled draft vanished; it must survive a push, just not be enabled by one")
	}
	if rcon.Enabled {
		t.Errorf("a pushed spec re-enabled a disabled draft: the gateway just exposed %s to the internet", rcon.LocalAddr)
	}
	if rcon.LocalAddr != rconAddr {
		t.Errorf("the draft's local address changed to %q; the wire never carries LocalAddr", rcon.LocalAddr)
	}

	// The tunnel the user did enable stays governed by the gateway as before.
	if mc := byID["tnl_mc"]; !mc.Enabled || mc.PublicPort != 25565 || mc.LocalAddr != "127.0.0.1:25565" {
		t.Errorf("an enabled tunnel must still take the gateway's resolved set: %+v", mc)
	}
}

// TestMergeTunnelsAppliesToEnabledAndNewTunnels: the guard above must not cost the
// gateway its actual authority. It still resolves ports for tunnels the user
// enabled, and a tunnel it introduces still lands (harmlessly — the wire carries no
// LocalAddr, so an invented tunnel has nothing to dial until its owner fills one in).
func TestMergeTunnelsAppliesToEnabledAndNewTunnels(t *testing.T) {
	existing := []config.Tunnel{
		{ID: "tnl_mc", Name: "mc", Type: config.TunnelTCP, LocalAddr: "127.0.0.1:25565", PublicPort: 0, Enabled: true},
	}
	pushed := []control.TunnelSpec{
		{ID: "tnl_mc", Name: "mc", Type: config.TunnelTCP, PublicPort: 40001}, // gateway resolved the ephemeral request
		{ID: "tnl_new", Name: "new", Type: config.TunnelTCP, PublicPort: 40002},
	}
	byID := map[string]config.Tunnel{}
	for _, tun := range mergeTunnels(existing, pushed) {
		byID[tun.ID] = tun
	}
	if mc := byID["tnl_mc"]; !mc.Enabled || mc.PublicPort != 40001 {
		t.Errorf("an enabled tunnel must take the gateway's resolved port: %+v", mc)
	}
	if nw, ok := byID["tnl_new"]; !ok || !nw.Enabled || nw.LocalAddr != "" {
		t.Errorf("a gateway-introduced tunnel should land with no local target: %+v ok=%v", nw, ok)
	}
}

// TestMergeTunnelsDropsEnabledTunnelsThePushOmits: the authoritative set is
// desired state, so omission still means "stop serving this".
func TestMergeTunnelsDropsEnabledTunnelsThePushOmits(t *testing.T) {
	existing := []config.Tunnel{
		{ID: "tnl_a", Name: "a", LocalAddr: "127.0.0.1:1", Enabled: true},
		{ID: "tnl_b", Name: "b", LocalAddr: "127.0.0.1:2", Enabled: true},
	}
	pushed := []control.TunnelSpec{{ID: "tnl_a", Name: "a", PublicPort: 1}}
	for _, tun := range mergeTunnels(existing, pushed) {
		if tun.ID == "tnl_b" {
			t.Errorf("an enabled tunnel the push omitted survived: %+v", tun)
		}
	}
}
