package gateway

import (
	"slices"
	"sort"
)

// Conflict resolution: the gateway prefers to keep an agent online and *report* a
// clash rather than fail it. Two clashes are handled here — a public port already
// in use (auto-reassign to a policy-valid alternative) and a same-agentID contest
// from two IPs (a suspected cloned key) — and both are recorded as events the GUI
// surfaces as resolvable cards. Detection lives here; the one-click fixes are the
// GUI's. Everything in this file is touched only from the actor goroutine (the
// event ring) or is pure (reassignCandidates), so nothing here locks.

// Event kinds recorded in the gateway event ring. Stable strings — the GUI event
// log and any test match on them.
const (
	// EventPortReassigned: a requested public port was in use, so the tunnel was
	// bound to a free alternative instead of failing. The card offers to reclaim it.
	EventPortReassigned = "port-reassigned"
	// EventCloneSuspected: the same agentID was rapidly contested from two IPs,
	// which a derived (pubkey-bound) identity should make impossible unless the key
	// was copied. The card nudges the user to re-enroll for a distinct identity.
	EventCloneSuspected = "clone-suspected"
	// EventPortReclaimed: an operator gave a contested public port to one enrolled
	// agent, bumping the enrolled incumbent that held it to an ephemeral port. Unlike
	// the two above it is a completed action, not an open conflict — it lands in the
	// event log for the record, never as a resolvable card.
	EventPortReclaimed = "port-reclaimed"
	// EventProposalDeclined: a gateway-authored config proposal did not apply — the
	// agent declined it (Message carries its reason) or the config moved while the
	// decision was pending. A completed outcome like port-reclaimed: event log only.
	EventProposalDeclined = "proposal-declined"
	// EventAgentEnrolled: a new machine joined the fleet through an invite. A
	// completed outcome: event log only, and the roster shows the newcomer.
	EventAgentEnrolled = "agent-enrolled"
	// EventTunnelPaused / EventTunnelResumed: an arbitration notice — either side
	// paused (or resumed) a tunnel or a whole agent, immediately and without
	// consent (pause edits no tunnel definition). Message names who acted.
	EventTunnelPaused  = "tunnel-paused"
	EventTunnelResumed = "tunnel-resumed"
)

// GatewayEvent is one notable, user-facing thing the gateway did or noticed —
// an auto-fix or a detected conflict. It is polled incrementally by the engine
// (since a cursor) and rendered in the GUI event log. Fields beyond the message
// are structured so a card can act on them without parsing prose.
type GatewayEvent struct {
	Seq           uint64 `json:"seq"`
	TimeMs        int64  `json:"timeMs"`
	Kind          string `json:"kind"`
	AgentID       string `json:"agentId,omitempty"`
	TunnelID      string `json:"tunnelId,omitempty"`
	Message       string `json:"message"`
	RequestedPort int    `json:"requestedPort,omitempty"`
	ActualPort    int    `json:"actualPort,omitempty"`
}

// eventRing is a bounded, monotonically-sequenced buffer of GatewayEvents. It is
// not safe for concurrent use — the actor owns one and mutates it only on its
// goroutine; reads funnel through the actor via do(). The since-cursor mirrors the
// engine log's LogsSince so the GUI can poll incrementally.
type eventRing struct {
	cap     int
	nextSeq uint64
	events  []GatewayEvent
}

func newEventRing(capacity int) *eventRing {
	return &eventRing{cap: capacity}
}

// push assigns the next seq + records ev, dropping the oldest event once the ring
// is full. The caller sets Kind and any structured fields (and TimeMs); push owns
// Seq so cursors stay monotonic across drops.
func (r *eventRing) push(ev GatewayEvent) {
	r.nextSeq++
	ev.Seq = r.nextSeq
	r.events = append(r.events, ev)
	if len(r.events) > r.cap {
		// Drop the oldest, keeping the tail. Re-slice into a fresh backing array so
		// the dropped head can be GC'd rather than pinned by the underlying array.
		r.events = append([]GatewayEvent(nil), r.events[len(r.events)-r.cap:]...)
	}
}

// since returns every retained event with Seq > cursor, oldest first. A cursor at
// or beyond the newest seq returns nothing; a zero cursor returns all retained.
func (r *eventRing) since(cursor uint64) []GatewayEvent {
	var out []GatewayEvent
	for _, e := range r.events {
		if e.Seq > cursor {
			out = append(out, e)
		}
	}
	return out
}

// allowedPorts returns every public port policy permits, ascending — the single
// source for "which ports may this agent have?", shared by the ephemeral-request
// path and the reassignment fallback.
//
// The result [0] means *unconstrained*: 0 is not a port, it is "ask the OS for
// anything free", so it is only ever policy-valid when nothing constrains the
// choice. A constrained gateway gets concrete ports and never 0 — mixing the two
// would hand out the whole ephemeral range next to the set the operator wrote
// down, which is the same as having written nothing. An empty result is the
// honest answer when a scope and an allowlist do not meet: this agent may have
// no port at all.
func allowedPorts(scope Scope, allowlist []int) []int {
	if len(scope.Ports) == 0 && len(allowlist) == 0 {
		return []int{0}
	}
	// The scope narrows first (it is the per-agent grant); the allowlist filters
	// (it is the gateway-wide ceiling). Either alone constrains.
	universe := allowlist
	if len(scope.Ports) > 0 {
		universe = scope.Ports
	}
	seen := make(map[int]struct{}, len(universe))
	var out []int
	for _, p := range universe {
		if p <= 0 {
			continue // 0 in a scope is not "any port"; it is not a port
		}
		if _, dup := seen[p]; dup {
			continue
		}
		if !scope.AllowsPort(p) || !portInAllowlist(p, allowlist) {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// reassignCandidates returns the ordered public ports to try when `requested` (a
// specific, already-validated port) is found in use: every policy-valid port bar
// the requested one. An empty result means no reassignment is possible (every
// allowed alternative is taken, or the request was already ephemeral) and the
// caller hard-fails as before.
func reassignCandidates(requested int, scope Scope, allowlist []int) []int {
	if requested == 0 {
		return nil // an ephemeral request never conflicts, so it never reassigns
	}
	out := slices.DeleteFunc(allowedPorts(scope, allowlist), func(p int) bool {
		return p == requested
	})
	if len(out) == 0 {
		return nil // "nothing is possible", not "an empty list of possibilities"
	}
	return out
}

// portInAllowlist reports whether p is permitted by the gateway allowlist. An
// empty allowlist permits everything (mirrors validateSpec).
func portInAllowlist(p int, allowlist []int) bool {
	if len(allowlist) == 0 {
		return true
	}
	for _, a := range allowlist {
		if a == p {
			return true
		}
	}
	return false
}
