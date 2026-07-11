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
		}
	}

	sess := a.curSession.Load()
	if sess == nil {
		a.logger.Info("tunnel config updated; will apply on next connect")
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
