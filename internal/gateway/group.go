package gateway

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
