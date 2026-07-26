package link

import (
	"math/rand/v2"
	"time"
)

// The fast-retry tier. The overwhelming majority of disconnects are a gateway
// restart or a momentary blip, and a player is sitting at the "connection lost"
// screen while the agent waits — so the first FastRetries attempts of an outage
// retry at roughly FastDelay instead of starting on the exponential curve.
//
// FastWindow is what keeps that from being a weapon: the tier is granted once per
// window, not once per Reset. A flapping NIC fires a network-change notice (and
// thus a Reset) every few seconds, and without the ration each one would buy three
// more ~200 ms reconnects — an agent hammering the gateway exactly when the network
// is least able to carry it. Values: docs/agent/architecture.md "The numbers".
const (
	FastRetries = 3
	FastDelay   = 200 * time.Millisecond
	FastWindow  = 60 * time.Second
)

// Backoff produces reconnect delays: a short fast tier, then exponential with
// full jitter, capped, and reset once a connection has proven stable. Full jitter
// avoids thundering-herd reconnects when a gateway restart drops many agents.
//
// The zero value is usable — agent.Run builds one — and every field below gets a
// default on first use.
type Backoff struct {
	Base time.Duration // first retry ceiling after the fast tier (default 1s)
	Max  time.Duration // ceiling for all retries (default 60s)
	// StableAfter: a session that lived at least this long resets the
	// sequence (default 60s).
	StableAfter time.Duration

	// now is the clock, injected only by tests so the fast-tier window can be
	// crossed without sleeping through it. nil = time.Now.
	now func() time.Time

	attempt int
	// fastGrantedAt is when the current fast-tier grant began (zero = never
	// granted) and fastSpent how many of its FastRetries attempts are used.
	// Neither is cleared by Reset or ConnectionEnded — spending is tracked
	// separately from attempt precisely so a reset cannot refill the tier;
	// rationing it across resets is the point of the window.
	fastGrantedAt time.Time
	fastSpent     int
}

func (b *Backoff) defaults() {
	if b.Base <= 0 {
		b.Base = time.Second
	}
	if b.Max <= 0 {
		b.Max = 60 * time.Second
	}
	if b.StableAfter <= 0 {
		b.StableAfter = 60 * time.Second
	}
	if b.now == nil {
		b.now = time.Now
	}
}

// Next returns the delay to sleep before the next attempt. It is the only method
// that advances the sequence.
func (b *Backoff) Next() time.Duration {
	b.defaults()
	// Two gates, and both must hold. `attempt` says this is the START of an
	// outage — a sequence already climbing the curve never drops back to 200 ms,
	// or a long outage would refresh the window and oscillate forever. The grant
	// says the tier has not been spent recently: it covers FastRetries attempts
	// and expires FastWindow after it began, so a sequence that Resets inside a
	// spent grant goes straight to the curve.
	if b.attempt < FastRetries {
		now := b.now()
		if b.fastGrantedAt.IsZero() || now.Sub(b.fastGrantedAt) >= FastWindow {
			b.fastGrantedAt, b.fastSpent = now, 0
		}
		if b.fastSpent < FastRetries {
			b.fastSpent++
			b.attempt++
			// Jittered around FastDelay, never zero: [FastDelay/2, 3·FastDelay/2).
			return FastDelay/2 + time.Duration(rand.Int64N(int64(FastDelay)))
		}
	}
	// Exponential from where the fast tier hands over, so the first slow delay
	// keeps the Base ceiling the sequence would have started from.
	shift := b.attempt - FastRetries
	if shift < 0 {
		shift = 0 // the tier was rationed away; start where it would have handed over
	}
	ceil := b.Base << shift
	if ceil > b.Max || ceil <= 0 { // <=0 guards shift overflow
		ceil = b.Max
	}
	if b.attempt < 30 {
		b.attempt++
	}
	return time.Duration(rand.Int64N(int64(ceil)) + 1)
}

// ConnectionEnded reports how long the session lasted so a stable connection
// resets the sequence.
func (b *Backoff) ConnectionEnded(lifetime time.Duration) {
	b.defaults()
	if lifetime >= b.StableAfter {
		b.attempt = 0
	}
}

// Reset forces the sequence back to the start (e.g. a network-change
// notification suggests connectivity is back right now). Whether that also buys
// the fast tier is the window's call, not Reset's.
func (b *Backoff) Reset() { b.attempt = 0 }
