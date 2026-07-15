// Package bwcap turns a tunnel's (rate Mbps, scope) bandwidth cap into the rate
// limiters the relay splice enforces. It is the single home for the unit
// (decimal megabits/sec, one token = one byte) and for scope→sharing semantics.
//
// Ownership: a LimiterSet's scope and limiter pointers are fixed at BuildSet; a
// scope or capped/uncapped change rebuilds the set rather than mutating it, so a
// consumer holding an old *LimiterSet always sees a consistent snapshot. Only
// the applied rate changes in place — via the limiters' own concurrent-safe
// SetLimit plus an atomic mbps — so a rate-only config reload is picked up by
// in-flight combined/per-direction connections without a rebuild. Every field a
// lock-free reader (Resolve) touches is therefore either immutable or atomic.
package bwcap

import (
	"sync"
	"sync/atomic"

	"golang.org/x/time/rate"

	"proxyforward/internal/relay"
)

// Scope selects what a tunnel's cap applies to.
type Scope string

const (
	// ScopeCombined shares one bucket across both directions and all of the
	// tunnel's connections.
	ScopeCombined Scope = "combined"
	// ScopePerDirection caps inbound and outbound independently, each summed
	// across the tunnel's connections.
	ScopePerDirection Scope = "per-direction"
	// ScopePerConnection gives every connection its own bucket.
	ScopePerConnection Scope = "per-connection"
)

// bytesPerMbps converts decimal megabits/sec to bytes/sec (one rate token = one
// byte). This constant is the single source of truth for the unit.
const bytesPerMbps = 125_000

// NormalizeScope maps a wire/config string to a Scope, failing safe to
// ScopeCombined for empty or unrecognized values so a bad value throttles the
// tunnel rather than erroring it. (Config load rejects unknown scopes; the wire
// side normalizes whatever arrives.)
func NormalizeScope(s string) Scope {
	switch Scope(s) {
	case ScopePerDirection:
		return ScopePerDirection
	case ScopePerConnection:
		return ScopePerConnection
	default:
		return ScopeCombined
	}
}

func newLimiter(mbps int) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(mbps*bytesPerMbps), relay.BufSize)
}

// LimiterSet holds the shared limiters for one tunnel's cap. Built once at bind,
// swapped on a scope/capped change, updated in place on a rate-only change. See
// the package doc for the concurrency model.
type LimiterSet struct {
	scope Scope        // immutable after BuildSet
	mbps  atomic.Int64 // current applied rate; 0 = uncapped
	// combined serves ScopeCombined (both directions share it); in/out serve
	// ScopePerDirection. All nil for ScopePerConnection (minted per connection)
	// and for an uncapped set. The pointers are immutable; only the limiters'
	// internal rate changes, via SetLimit.
	combined *rate.Limiter
	in       *rate.Limiter
	out      *rate.Limiter
}

// BuildSet constructs the shared limiters for a (mbps, scope) cap. mbps <= 0 is
// uncapped (all limiters nil, Resolve returns nil pair).
func BuildSet(mbps int, scope string) *LimiterSet {
	s := NormalizeScope(scope)
	set := &LimiterSet{scope: s}
	set.mbps.Store(int64(mbps))
	if mbps <= 0 {
		return set
	}
	switch s {
	case ScopePerDirection:
		set.in = newLimiter(mbps)
		set.out = newLimiter(mbps)
	case ScopePerConnection:
		// No shared limiters; Resolve mints a fresh one per connection.
	default: // ScopeCombined
		set.combined = newLimiter(mbps)
	}
	return set
}

// Rate reports the currently applied cap in Mbps (0 = uncapped).
func (s *LimiterSet) Rate() int { return int(s.mbps.Load()) }

// setRate applies a rate-only change (same scope, still capped) to the shared
// limiters in place, so in-flight combined/per-direction connections pick up the
// new rate immediately. Per-connection has no shared limiters, so only the
// stored rate updates — for connections opened afterward.
func (s *LimiterSet) setRate(mbps int) {
	lim := rate.Limit(mbps * bytesPerMbps)
	if s.combined != nil {
		s.combined.SetLimit(lim)
	}
	if s.in != nil {
		s.in.SetLimit(lim)
	}
	if s.out != nil {
		s.out.SetLimit(lim)
	}
	s.mbps.Store(int64(mbps))
}

// Resolve returns the (inbound, outbound) limiters for one connection, called
// once per connection. ScopeCombined shares one instance both ways;
// ScopePerDirection returns the two shared instances; ScopePerConnection mints a
// fresh combined limiter so each connection gets its own bucket. An uncapped or
// nil set returns (nil, nil) — the relay fast path.
func Resolve(set *LimiterSet) (inbound, outbound relay.Limiter) {
	if set == nil || set.Rate() <= 0 {
		return nil, nil
	}
	switch set.scope {
	case ScopePerDirection:
		return asLimiter(set.in), asLimiter(set.out)
	case ScopePerConnection:
		l := newLimiter(set.Rate())
		return l, l
	default: // ScopeCombined
		return asLimiter(set.combined), asLimiter(set.combined)
	}
}

// Reconcile makes a live set match a new (mbps, scope) cap. A same-scope,
// still-capped, rate-only change is applied in place and returns (cur, false)
// — keep the existing set, in-flight connections stay valid. Anything else (nil
// cur, scope change, or a capped/uncapped flip) builds a fresh set and returns
// (newSet, true) — the caller must install it.
func Reconcile(cur *LimiterSet, mbps int, scope string) (set *LimiterSet, swapped bool) {
	s := NormalizeScope(scope)
	if cur == nil || cur.scope != s || (cur.Rate() <= 0) != (mbps <= 0) {
		return BuildSet(mbps, scope), true
	}
	if mbps > 0 && cur.Rate() != mbps {
		cur.setRate(mbps)
	}
	return cur, false
}

// asLimiter converts a possibly-nil *rate.Limiter to a relay.Limiter, returning
// a true nil interface (not a non-nil interface wrapping a nil pointer, which
// would make relay's nil check pass and then panic in WaitN).
func asLimiter(l *rate.Limiter) relay.Limiter {
	if l == nil {
		return nil
	}
	return l
}

// Registry holds each tunnel's shared LimiterSet on the agent, keyed by tunnel
// ID. An agent process serves only its own tunnels, so tunnel IDs are unique
// here — unlike the gateway, which multiplexes agents and keys buckets by
// (agentID, tunnelID) via the per-agent publicListener. Safe for concurrent use.
type Registry struct {
	mu   sync.Mutex
	sets map[string]*LimiterSet
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{sets: make(map[string]*LimiterSet)}
}

// Resolve returns the (inbound, outbound) limiters for a connection on tunID,
// building the shared set on first use and reconciling it to the current cap
// (rate-only change in place, scope/flip rebuilds). Uncapped returns (nil, nil).
func (r *Registry) Resolve(tunID string, mbps int, scope string) (inbound, outbound relay.Limiter) {
	r.mu.Lock()
	set, _ := Reconcile(r.sets[tunID], mbps, scope)
	r.sets[tunID] = set
	r.mu.Unlock()
	return Resolve(set)
}

// Release drops a tunnel's shared set (call when the tunnel is removed).
func (r *Registry) Release(tunID string) {
	r.mu.Lock()
	delete(r.sets, tunID)
	r.mu.Unlock()
}
