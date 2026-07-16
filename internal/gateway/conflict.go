package gateway

import "sort"

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

// reassignCandidates returns the ordered public ports to try when `requested` (a
// specific, already-validated port) is found in use. Each candidate is
// policy-valid — it passes the same scope + allowlist checks validateSpec applied
// to the requested port — and the requested port itself is excluded. When neither
// a scope nor an allowlist constrains the choice, the single candidate 0 means
// "let the OS pick any free ephemeral port"; a constrained gateway must instead
// stay inside its allowed set and never hand out an arbitrary port. An empty
// result means no reassignment is possible (every allowed alternative is taken or
// the request was already ephemeral) and the caller hard-fails as before.
func reassignCandidates(requested int, scope Scope, allowlist []int) []int {
	if requested == 0 {
		return nil // an ephemeral request never conflicts, so it never reassigns
	}
	constrained := len(scope.Ports) > 0 || len(allowlist) > 0
	if !constrained {
		return []int{0} // unconstrained: any ephemeral port is policy-valid
	}
	// Enumerate the constraining set (scope narrows first; the allowlist filters),
	// keep only policy-valid ports other than the requested one, sorted ascending.
	universe := allowlist
	if len(scope.Ports) > 0 {
		universe = scope.Ports
	}
	seen := make(map[int]struct{}, len(universe))
	var out []int
	for _, p := range universe {
		if p == requested || p <= 0 {
			continue
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
