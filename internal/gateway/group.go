package gateway

import (
	"net"
	"sort"
	"sync"
	"sync/atomic"
)

// Tunnel groups: N interchangeable member backends behind one shared public port,
// for redundancy (standby failover) or load spreading (spread, least-connections).
// A group is a first-class, operator-owned gateway object (agentstore.go), and
// grouping is orchestration over the *existing* data plane — the pick chooses which
// member session a player is routed to, and relay.Splice carries them unchanged.
//
// Ownership: GroupSpec and the caps below are pure config. The live groupListener
// and its members (actor.go) are owned by the actor goroutine; only the immutable
// snapshot the pick reads is off-actor. This file holds the config type, the caps,
// and the pure selection logic — nothing here touches actor state directly.

// GroupSpec is a group as the operator defines it. Members share world state (a
// Velocity/Bungee proxy behind each, or mirrored lobbies) — that precondition is
// the operator's to satisfy; the group treats members as interchangeable. Stored
// in gateway_agents.json alongside agents. Grouping is arbitration, never member
// consent: the gateway decides the port and membership and pushes immediately.
type GroupSpec struct {
	ID          string `json:"id"`         // "grp_" + id
	Name        string `json:"name"`       // display
	PublicPort  int    `json:"publicPort"` // the one shared public port
	Proto       string `json:"proto"`      // "tcp" only this iteration (§11)
	Policy      string `json:"policy"`     // spread | standby
	OfflineMOTD string `json:"offlineMotd,omitempty"`
}

const (
	// PolicySpread distributes new players across eligible members by least live
	// connections, breaking ties round-robin over the sorted member snapshot.
	PolicySpread = "spread"
	// PolicyStandby sends every player to the lowest-ordinal eligible member and
	// fails over to the next only when that one stops being eligible.
	PolicyStandby = "standby"
	// GroupProtoTCP is the only protocol a group binds this iteration. UDP groups
	// are deferred: a UDP port has no offline-responder analogue and the datagram
	// plane's failover semantics are unproven.
	GroupProtoTCP = "tcp"
)

const (
	// MaxGroups / MaxMembersPerGroup bound total group membership at the config
	// layer where the operator creates it (validated in AgentStore.SetGroup).
	// Membership is operator-assigned, not agent-requested, so it is deliberately
	// NOT counted against MaxTunnelsPerAgent — that cap bounds the one resource an
	// agent grows by asking (a listener it allocates), which a group membership is
	// not. Values: docs/agent/architecture.md "The numbers".
	MaxGroups          = 16
	MaxMembersPerGroup = 16
)

// validPolicy reports whether p names a supported group policy.
func validPolicy(p string) bool { return p == PolicySpread || p == PolicyStandby }

// agentTun is a member's identity within a group: (agentID, tunnelID). A group
// member is one specific tunnel of one agent, so an agent may contribute distinct
// tunnels to distinct groups — but only one tunnel per group (SetGroup enforces it).
type agentTun struct {
	AgentID  string
	TunnelID string
}

// groupMember is one live member of a group: the agent session, the tunnel it
// contributes, its join ordinal, and its live-connection load signal. Ownership is
// split by design — sess/tunnelID/joinOrdinal are stamped once on the actor
// goroutine at join and never mutated, so the off-actor pick reads them without a
// lock; liveConns is the one hot-path field, an atomic the pick and handleGroupClient
// both touch.
type groupMember struct {
	sess     *agentSession
	tunnelID string
	// joinOrdinal is monotonic, stamped at join. The standby primary is the eligible
	// member with the lowest ordinal; a rejoin gets a new, higher ordinal, so a
	// recovered ex-primary never reclaims primary (no-failback, by construction).
	joinOrdinal uint64
	// liveConns is the least-connections load signal — everything that opens a
	// connection through this member, Minecraft status pings included (we can't know
	// it's a ping before routing). Hot path: incremented before openFlow, decremented
	// when the splice ends.
	liveConns atomic.Int64
}

// groupListener is one bound shared public port serving a group. Concurrency: the
// actor goroutine owns members/nextOrdinal and rebuilds snap copy-on-write; the
// off-actor accept path reads only snap (an immutable slice) and per-member atomics,
// so the pick never locks. Exactly one accept loop runs on ln, closed and awaited via
// done at DeleteGroup/shutdown (the group ghost-listener guarantee).
type groupListener struct {
	spec GroupSpec
	ln   net.Listener
	done chan struct{} // closed when the accept loop has fully exited

	// snap is the immutable member slice the pick reads, sorted by joinOrdinal.
	// Rebuilt and atomic-swapped by the actor on every membership change.
	snap atomic.Pointer[[]*groupMember]

	// members/nextOrdinal are actor-only (single goroutine, no hot path): O(1)
	// mutate-time lookup by (agentID, tunnelID) and the monotonic ordinal counter.
	members     map[agentTun]*groupMember
	nextOrdinal uint64

	// rr is the spread round-robin cursor for breaking least-connections ties
	// deterministically over the sorted snapshot (an atomic; the pick reads it).
	rr atomic.Uint64

	// conns tracks live client conns (connID → net.Conn) for this group's lifetime,
	// so DeleteGroup can drain in-flight player legs (closing the player side unwinds
	// the data leg under every transport). Populated by handleGroupClient.
	conns sync.Map
}

// rebuildSnap recomputes the immutable member snapshot from members, sorted by
// joinOrdinal, and atomic-swaps it in. Runs on the actor goroutine only.
func (gl *groupListener) rebuildSnap() {
	out := make([]*groupMember, 0, len(gl.members))
	for _, m := range gl.members {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].joinOrdinal < out[j].joinOrdinal })
	gl.snap.Store(&out)
}

// eligible reports whether a member can take a new player right now: it has a live
// session, neither side has paused its tunnel, and the agent has reported the local
// backend UP. The positive-health requirement is a deliberate divergence from the
// solo "unknown ⇒ up" rule: a group has alternatives, so routing to an unverified
// member would let a dead-backend member that keeps reconnecting (liveConns 0, so the
// argmin) become a thundering-herd target. The agent seeds current health on session
// start, so a member is eligible within one probe of joining. Reads only per-member
// atomics and sync.Maps — safe off the actor goroutine.
func eligible(m *groupMember) bool {
	if m.sess.session() == nil {
		return false
	}
	if m.sess.pauseMine.Load().covers(m.tunnelID) || m.sess.pauseTheirs.Load().covers(m.tunnelID) {
		return false
	}
	v, ok := m.sess.health.Load(m.tunnelID)
	up, _ := v.(bool)
	return ok && up
}

// pick selects the member a new player is routed to under the group's policy,
// excluding any already tried this connection (bounded re-pick). It reads the
// immutable, joinOrdinal-sorted snapshot and per-member atomics only — no lock, no
// allocation beyond the small eligible slice — because it runs on the hot accept
// path. Returns nil when no member is eligible.
func pick(policy string, members []*groupMember, exclude map[*groupMember]bool, rr *atomic.Uint64) *groupMember {
	cands := make([]*groupMember, 0, len(members))
	for _, m := range members {
		if exclude[m] || !eligible(m) {
			continue
		}
		cands = append(cands, m)
	}
	return pickFrom(policy, cands, rr)
}

// pickFrom is the pure selection over an already-eligible, joinOrdinal-sorted
// candidate slice — the unit-testable core, no sessions required.
//   - standby: the lowest-ordinal candidate (first in the sorted slice). All players
//     go to it; it changes only when it stops being eligible (failover, no failback).
//   - spread: least live connections, ties broken round-robin over the sorted
//     candidates (deterministic because the snapshot order is stable).
func pickFrom(policy string, cands []*groupMember, rr *atomic.Uint64) *groupMember {
	if len(cands) == 0 {
		return nil
	}
	if policy == PolicyStandby {
		return cands[0]
	}
	minConns := cands[0].liveConns.Load()
	for _, m := range cands[1:] {
		if c := m.liveConns.Load(); c < minConns {
			minConns = c
		}
	}
	var tied []*groupMember // preserves the sorted candidate order
	for _, m := range cands {
		if m.liveConns.Load() == minConns {
			tied = append(tied, m)
		}
	}
	if len(tied) == 1 {
		return tied[0]
	}
	return tied[rr.Add(1)%uint64(len(tied))]
}
