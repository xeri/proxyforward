package app

import (
	"proxyforward/internal/engine"
	"proxyforward/internal/gateway"
)

// Tunnel-group management (gateway role). These bindings drive the GUI group card:
// list groups with live member state, create/edit/remove a group, and tag or untag a
// member's tunnel. Each runs one op over the shared JSON envelope (analyticsCall), so
// it works both in-process and attached to a service daemon, and degrades to an
// empty/error result off the gateway role. Grouping is arbitration — a save, delete,
// or tag applies immediately, with no member consent round-trip.

// ListGroups returns every configured group with its live members (eligibility,
// standby primary, live-conn load). Empty on any error (agent role, detached, old
// daemon) — the group card renders that as its honest unavailable state.
func (a *App) ListGroups() []gateway.GroupStatus {
	var out []gateway.GroupStatus
	if err := a.analyticsCall(engine.OpListGroups, nil, &out); err != nil || out == nil {
		return []gateway.GroupStatus{}
	}
	return out
}

// SaveGroup creates (empty id) or updates a group and binds its shared public port.
// An empty id mints a fresh one; the bound id is returned so the caller can select the
// group it just saved. A returned error is the honest refusal to surface verbatim
// (validation, or a port held by a listener the group may not displace).
func (a *App) SaveGroup(id, name string, publicPort int, policy, offlineMOTD string) (string, error) {
	req := struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		PublicPort  int    `json:"publicPort"`
		Policy      string `json:"policy"`
		OfflineMOTD string `json:"offlineMotd"`
	}{id, name, publicPort, policy, offlineMOTD}
	var resp struct {
		ID string `json:"id"`
	}
	if err := a.analyticsCall(engine.OpSetGroup, req, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// DeleteGroup removes a group: its shared socket closes, in-flight players are
// drained, and members' tunnels revert to solo listeners on their next reconcile.
func (a *App) DeleteGroup(id string) error {
	return a.agentMutate(engine.OpDeleteGroup, struct {
		ID string `json:"id"`
	}{id})
}

// SetTunnelGroup tags (groupId set) or untags (groupId empty) one of an agent's
// tunnels into a group. The tunnel leaves its solo listener and joins the group's
// shared one (or the reverse), applied immediately.
func (a *App) SetTunnelGroup(agentID, tunnelID, groupID string) error {
	return a.agentMutate(engine.OpSetTunnelGroup, struct {
		AgentID  string `json:"agentId"`
		TunnelID string `json:"tunnelId"`
		GroupID  string `json:"groupId"`
	}{agentID, tunnelID, groupID})
}
