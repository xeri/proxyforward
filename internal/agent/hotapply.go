package agent

import (
	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// ApplyTunnels replaces the agent's tunnel set at runtime (config
// hot-apply). The new set is desired state: on a CapTunnelSync session it is
// sent whole and the gateway's listener actor reconciles it against the live
// listeners; on a legacy session removed/disabled tunnels are unregistered
// and added/changed ones (re-)registered per frame. With no live session it
// only swaps the config; the next session registers the new set anyway.
//
// The caller owns validation (config.Validate) and persistence (Save); this
// only makes the running engine match.
func (a *Agent) ApplyTunnels(tunnels []config.Tunnel) {
	a.cfgMu.Lock()
	old := a.cfg.Agent.Tunnels
	a.cfg.Agent.Tunnels = tunnels
	a.cfgMu.Unlock()

	oldEnabled := enabledByID(old)
	newEnabled := enabledByID(tunnels)

	// Forget state for tunnels that are gone (or disabled).
	for id := range oldEnabled {
		if _, ok := newEnabled[id]; !ok {
			a.publicPorts.Delete(id)
			a.localUp.Delete(id)
			a.bwLimiters.Release(id)
		}
	}

	sess := a.curSession.Load()
	if sess == nil {
		a.logger.Info("tunnel config updated; will apply on next connect")
		return
	}

	if sess.Has(control.CapGatewayConfig) {
		// Gateway-authoritative: a local edit is a proposal the gateway adopts,
		// bumps, and pushes back (applyPushedConfig then reconciles the resolved
		// set). Deterministic — the gateway, not this write, is the source of truth.
		a.logger.Info("proposing tunnel edit to gateway (hot-apply)", "enabled", len(newEnabled))
		if err := sess.proposeConfig(tunnels); err != nil {
			a.logger.Warn("hot-apply propose failed; gateway will re-push on reconnect", "err", err)
		}
		return
	}

	if sess.Has(control.CapTunnelSync) {
		// Desired-state path: one full-set frame; the gateway reconciles.
		// Idempotency lives there — unchanged specs keep their listeners and
		// live connections, so no agent-side diffing is needed.
		a.logger.Info("syncing tunnel set (hot-apply)", "enabled", len(newEnabled))
		if err := sess.syncTunnels(tunnels); err != nil {
			a.logger.Warn("hot-apply sync failed; session will re-sync on reconnect", "err", err)
		}
		return
	}

	for id, t := range oldEnabled {
		if _, ok := newEnabled[id]; ok {
			continue
		}
		a.logger.Info("unregistering tunnel (hot-apply)", "tunnel", t.Name)
		if err := sess.write(control.TypeUnregister, control.Unregister{TunnelID: id}); err != nil {
			a.logger.Warn("hot-apply unregister failed; session will re-sync on reconnect", "tunnel", t.Name, "err", err)
			return
		}
	}
	for id, t := range newEnabled {
		oldT, existed := oldEnabled[id]
		if existed && !tunnelChanged(oldT, t) {
			continue
		}
		// The confirmed port is stale until register_ok arrives.
		a.publicPorts.Delete(id)
		a.logger.Info("registering tunnel (hot-apply)", "tunnel", t.Name, "public_port", t.PublicPort)
		err := sess.write(control.TypeRegister, control.Register{Tunnel: specFromTunnel(t)})
		if err != nil {
			a.logger.Warn("hot-apply register failed; session will re-sync on reconnect", "tunnel", t.Name, "err", err)
			return
		}
	}
}

// applyPushedConfig replaces the agent's enabled tunnel set with the gateway's
// authoritative one (CapGatewayConfig). The wire spec carries only gateway-relevant
// fields, so agent-local fields (LocalAddr, ProxyProtocolV2) are kept by ID and
// disabled local drafts are left untouched. publicPorts is refreshed from the resolved
// ports and state for removed tunnels is forgotten. Disk persistence is delegated to
// the optional configPersister; with none set the running set is updated in memory and
// the gateway re-pushes on the next reconnect.
func (a *Agent) applyPushedConfig(specs []control.TunnelSpec, generation uint64) {
	a.cfgMu.Lock()
	old := a.cfg.Agent.Tunnels
	merged := mergeTunnels(old, specs)
	a.cfg.Agent.Tunnels = merged
	a.cfgMu.Unlock()
	a.configGen.Store(generation)

	pushed := make(map[string]bool, len(specs))
	for _, spec := range specs {
		pushed[spec.ID] = true
		if spec.PublicPort != 0 {
			a.publicPorts.Store(spec.ID, spec.PublicPort)
		}
	}
	// Forget state for tunnels that were enabled before but the push dropped.
	for _, t := range old {
		if t.Enabled && !pushed[t.ID] {
			a.publicPorts.Delete(t.ID)
			a.localUp.Delete(t.ID)
			a.bwLimiters.Release(t.ID)
		}
	}
	a.logger.Info("applied gateway-authoritative config", "generation", generation, "tunnels", len(specs))
	if p := a.configPersister.Load(); p != nil {
		if err := (*p)(append([]config.Tunnel(nil), merged...), generation); err != nil {
			a.logger.Warn("persisting gateway config failed; gateway will re-push on reconnect", "err", err)
		}
	}
}

// mergeTunnels applies a gateway-authoritative spec set to the agent's tunnels: each
// pushed spec upserts by ID, keeping the agent-local fields the wire never carries
// (LocalAddr, ProxyProtocolV2); enabled tunnels the push omits are dropped; disabled
// local drafts are preserved. enabledSpecs(mergeTunnels(old, specs)) round-trips to
// specs (for the hashed fields), so the agent's next hello hash matches the gateway's.
func mergeTunnels(existing []config.Tunnel, specs []control.TunnelSpec) []config.Tunnel {
	byID := make(map[string]config.Tunnel, len(existing))
	for _, t := range existing {
		byID[t.ID] = t
	}
	pushed := make(map[string]bool, len(specs))
	out := make([]config.Tunnel, 0, len(specs)+len(existing))
	for _, spec := range specs {
		pushed[spec.ID] = true
		t := byID[spec.ID] // keeps LocalAddr/Options for a known id; zero for a new one
		t.ID = spec.ID
		t.Name = spec.Name
		t.Type = spec.Type
		t.PublicPort = spec.PublicPort
		t.Enabled = true
		t.Options.OfflineMOTD = spec.OfflineMOTD
		t.Options.MinecraftAware = spec.MinecraftAware
		t.Options.BandwidthLimitMbps = spec.BandwidthLimitMbps
		t.Options.BandwidthLimitScope = spec.BandwidthLimitScope
		out = append(out, t)
	}
	// Disabled local drafts survive — the authoritative set governs only enabled tunnels.
	for _, t := range existing {
		if !t.Enabled && !pushed[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

func enabledByID(tunnels []config.Tunnel) map[string]config.Tunnel {
	m := make(map[string]config.Tunnel)
	for _, t := range tunnels {
		if t.Enabled {
			m[t.ID] = t
		}
	}
	return m
}

// tunnelChanged reports whether a live tunnel needs re-registering.
// TunnelOptions is all scalars, so struct equality is exact.
func tunnelChanged(a, b config.Tunnel) bool {
	return a.Name != b.Name ||
		a.Type != b.Type ||
		a.LocalAddr != b.LocalAddr ||
		a.PublicPort != b.PublicPort ||
		a.Options != b.Options
}
