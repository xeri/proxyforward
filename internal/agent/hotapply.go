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
//
// It must not be called on a config-consent session — there an edit needs the
// gateway's approval before it changes anything, so the caller routes to
// ProposeTunnels instead (App.SaveTunnels does). Called anyway, it refuses rather
// than apply behind the operator's back.
func (a *Agent) ApplyTunnels(tunnels []config.Tunnel) {
	if a.ConsentActive() {
		a.logger.Warn("refusing to hot-apply on a config-consent session; edits must be proposed")
		return
	}

	old := a.swapLocal(tunnels)
	oldEnabled := enabledByID(old)
	newEnabled := enabledByID(tunnels)

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

// swapLocal replaces the running tunnel set and forgets the state of tunnels that
// are gone (or newly disabled), touching nothing on the gateway. It returns the
// previous set. Shared by the hot-apply path and the local-only edits consent lets
// through unproposed.
func (a *Agent) swapLocal(tunnels []config.Tunnel) (old []config.Tunnel) {
	a.cfgMu.Lock()
	old = a.cfg.Agent.Tunnels
	a.cfg.Agent.Tunnels = tunnels
	a.cfgMu.Unlock()

	newEnabled := enabledByID(tunnels)
	for id := range enabledByID(old) {
		if _, ok := newEnabled[id]; !ok {
			a.publicPorts.Delete(id)
			a.localUp.Delete(id)
			a.bwLimiters.Release(id)
		}
	}
	return old
}

// overlayDraft lays an authored draft over the current tunnels: a draft entry wins
// (it is what the user just authored and the gateway approved), unknown ones are
// appended, and everything else carries through. It exists because the wire spec
// carries only the gateway's half of a tunnel, so a set coming back from the gateway
// cannot restore agent-local fields for a tunnel this agent has not adopted yet.
func overlayDraft(existing, draft []config.Tunnel) []config.Tunnel {
	if len(draft) == 0 {
		return existing
	}
	out := append([]config.Tunnel(nil), existing...)
	at := make(map[string]int, len(out))
	for i, t := range out {
		at[t.ID] = i
	}
	for _, d := range draft {
		if i, ok := at[d.ID]; ok {
			out[i] = d
			continue
		}
		at[d.ID] = len(out)
		out = append(out, d)
	}
	return out
}

// applyPushedConfig replaces the agent's enabled tunnel set with the gateway's
// authoritative one (CapGatewayConfig). The wire spec carries only gateway-relevant
// fields, so agent-local fields (LocalAddr, ProxyProtocolV2) are kept by ID and
// disabled local drafts are left untouched. publicPorts is refreshed from the resolved
// ports and state for removed tunnels is forgotten. Disk persistence is delegated to
// the optional configPersister; with none set the running set is updated in memory and
// the gateway re-pushes on the next reconnect.
func (a *Agent) applyPushedConfig(specs []control.TunnelSpec, generation uint64) {
	// A set this agent proposed comes back naming tunnels the local config may not
	// hold yet, and the wire never carried their LocalAddr/PP2 — so overlay the
	// authored draft still awaiting its decision, or an approved new tunnel would
	// go live on the gateway with nowhere to dial locally. Those fields were never
	// the gateway's to approve; the specs still win for everything it does own.
	draft := a.pendingDraft()

	a.cfgMu.Lock()
	old := a.cfg.Agent.Tunnels
	merged := mergeTunnels(overlayDraft(old, draft), specs)
	a.cfg.Agent.Tunnels = merged
	a.cfgMu.Unlock()
	a.configGen.Store(generation)

	// The ports this agent was holding before the push, so a gateway-initiated move can
	// be told apart from this agent's own edit echoing back.
	wasPort := make(map[string]int, len(old))
	for _, t := range old {
		if t.Enabled {
			wasPort[t.ID] = t.PublicPort
		}
	}
	pushed := make(map[string]bool, len(specs))
	for _, spec := range specs {
		pushed[spec.ID] = true
		if spec.PublicPort != 0 {
			a.publicPorts.Store(spec.ID, spec.PublicPort)
		}
		// A concrete port that moved under us: an operator reclaimed it for another
		// agent, or the gateway auto-reassigned off a busy one. Either way this agent
		// asked for the old port and no longer has it, so say so — a diff-and-flag, no
		// new wire. An agent-authored edit never trips this (ApplyTunnels writes the new
		// set locally before proposing, so the gateway's echo matches), and an ephemeral
		// (0) request resolving to a concrete port is the gateway doing what was asked.
		if was, held := wasPort[spec.ID]; held && was != 0 && spec.PublicPort != 0 && was != spec.PublicPort {
			a.logger.Warn("public port reassigned by the gateway",
				"tunnel", spec.Name, "was", was, "now", spec.PublicPort)
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
	// A pushed id this agent has never held becomes a tunnel with no LocalAddr —
	// the wire has no field to carry one. That is the intended landing for a
	// gateway-authored tunnel (the operator fills the address in), but until they
	// do it holds a public port that dials nowhere, so it is named at the moment
	// it appears rather than left to be inferred from a player's failed join.
	for _, t := range merged {
		if t.Enabled && t.LocalAddr == "" {
			a.logger.Warn("pushed tunnel has no local address; it will hold its public port without serving until one is set",
				"tunnel", t.Name, "tunnel_id", t.ID)
		}
	}
	if p := a.configPersister.Load(); p != nil {
		if err := (*p)(append([]config.Tunnel(nil), merged...), generation); err != nil {
			a.logger.Warn("persisting gateway config failed; gateway will re-push on reconnect", "err", err)
		}
	}
}

// mergeTunnels applies a gateway-authoritative spec set to the agent's tunnels: each
// pushed spec upserts by ID, keeping the agent-local fields the wire never carries
// (LocalAddr, ProxyProtocolV2); enabled tunnels the push omits are dropped; disabled
// local drafts are preserved, and a push can never enable one. enabledSpecs(
// mergeTunnels(old, specs)) round-trips to specs (for the hashed fields), so the
// agent's next hello hash matches the gateway's.
//
// Enabling is the one thing the gateway has no say over. A disabled draft is the
// user's "do not expose this", it keeps a LocalAddr naming a real local service, and
// the gateway only ever learns the *enabled* set — so a spec for a disabled id is
// either a mistake or a guess (ids are name slugs: tnl_rcon, tnl_ssh). The gateway is
// routinely someone else's machine, and push_config auto-applies without consent by
// design, so this is the boundary that keeps a push from serving a switched-off
// service to the internet.
func mergeTunnels(existing []config.Tunnel, specs []control.TunnelSpec) []config.Tunnel {
	byID := make(map[string]config.Tunnel, len(existing))
	for _, t := range existing {
		byID[t.ID] = t
	}
	out := make([]config.Tunnel, 0, len(specs)+len(existing))
	for _, spec := range specs {
		known, isKnown := byID[spec.ID]
		if isKnown && !known.Enabled {
			continue // a disabled draft; carried through untouched below
		}
		t := known // keeps LocalAddr/Options for a known id; zero for a new one
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
	// Every disabled draft survives exactly as authored — the loop above never
	// emits one, so this cannot duplicate.
	for _, t := range existing {
		if !t.Enabled {
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
