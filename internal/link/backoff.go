package link

import (
	"math/rand/v2"
	"time"
)

// Backoff produces reconnect delays: exponential with full jitter, capped,
// and reset once a connection has proven stable. Full jitter avoids
// thundering-herd reconnects when a gateway restart drops many agents.
type Backoff struct {
	Base time.Duration // first retry ceiling (default 1s)
	Max  time.Duration // ceiling for all retries (default 60s)
	// StableAfter: a session that lived at least this long resets the
	// sequence (default 60s).
	StableAfter time.Duration

	attempt int
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
}

// Next returns the delay to sleep before the next attempt.
func (b *Backoff) Next() time.Duration {
	b.defaults()
	ceil := b.Base << b.attempt
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

// Reset forces the sequence back to the base delay (e.g. a network-change
// notification suggests connectivity is back right now).
func (b *Backoff) Reset() { b.attempt = 0 }
