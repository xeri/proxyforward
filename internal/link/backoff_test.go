package link

import (
	"testing"
	"time"
)

// fakeClock is the Backoff `now` seam: a hand-advanced clock, so the fast-tier
// window can be crossed without sleeping through it.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestBackoff(c *fakeClock) *Backoff {
	return &Backoff{Base: time.Second, Max: 30 * time.Second, StableAfter: time.Minute, now: c.now}
}

// nextFast reports the delay and whether it came from the fast tier. Full jitter
// makes the two tiers' ranges overlap, so which tier ran is read from the grant
// counter, never inferred from a sampled duration.
func nextFast(b *Backoff) (time.Duration, bool) {
	grant, spent := b.fastGrantedAt, b.fastSpent
	d := b.Next()
	// A fresh grant always spends its first attempt on the fast tier; within one
	// grant, the tier ran iff the spend counter moved.
	return d, !b.fastGrantedAt.Equal(grant) || b.fastSpent > spent
}

// TestBackoffFastTierDelays: the first FastRetries attempts of an outage retry at
// roughly FastDelay — a gateway restart should cost the player a fraction of a
// second, not a second — and the tier then hands over to the exponential curve at
// exactly the ceiling that curve would have started from.
func TestBackoffFastTierDelays(t *testing.T) {
	c := &fakeClock{t: time.Unix(1, 0)}
	b := newTestBackoff(c)

	lo, hi := FastDelay/2, FastDelay/2+FastDelay
	for i := 0; i < FastRetries; i++ {
		d, fast := nextFast(b)
		if !fast {
			t.Fatalf("attempt %d took the slow tier; the first %d are meant to be fast", i, FastRetries)
		}
		if d < lo || d >= hi {
			t.Fatalf("fast attempt %d: delay %v outside [%v, %v)", i, d, lo, hi)
		}
	}
	// Handover: the fast tier prepends attempts, it does not consume any of the
	// curve, so the first slow delay still sits under the Base ceiling.
	for i, wantCeil := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		d, fast := nextFast(b)
		if fast {
			t.Fatalf("slow attempt %d came from the fast tier; it should be spent", i)
		}
		if d < 1 || d > wantCeil {
			t.Fatalf("slow attempt %d: delay %v outside (0, %v]", i, d, wantCeil)
		}
	}
}

// TestBackoffFastWindowRationsTheTier: the fast tier is granted once per
// FastWindow, not once per Reset. A flapping NIC fires a network-change notice
// every few seconds, and without the ration each one would buy three more ~200 ms
// reconnects — an agent hammering the gateway exactly when the network is least
// able to carry it.
func TestBackoffFastWindowRationsTheTier(t *testing.T) {
	c := &fakeClock{t: time.Unix(1, 0)}
	b := newTestBackoff(c)
	for i := 0; i < FastRetries; i++ {
		b.Next()
	}

	// Still inside the window: Reset restores the sequence but not the tier.
	c.add(FastWindow / 2)
	b.Reset()
	if _, fast := nextFast(b); fast {
		t.Fatal("Reset inside the window re-granted the fast tier; it must be rationed")
	}

	// Past the window (a laptop waking from sleep, not a flap): granted again.
	c.add(FastWindow)
	b.Reset()
	d, fast := nextFast(b)
	if !fast {
		t.Fatal("Reset after the window did not re-grant the fast tier")
	}
	if lo, hi := FastDelay/2, FastDelay/2+FastDelay; d < lo || d >= hi {
		t.Fatalf("re-granted fast delay %v outside [%v, %v)", d, lo, hi)
	}
}

// TestBackoffStableSessionRestoresFastTier: a session that lived long enough to
// count as stable resets the sequence, and — the window having passed while it ran
// — the next outage gets the fast tier again.
func TestBackoffStableSessionRestoresFastTier(t *testing.T) {
	c := &fakeClock{t: time.Unix(1, 0)}
	b := newTestBackoff(c)
	for i := 0; i < FastRetries+2; i++ {
		b.Next()
	}
	c.add(2 * FastWindow)
	b.ConnectionEnded(5 * time.Minute)
	if _, fast := nextFast(b); !fast {
		t.Fatal("after a stable session the first retry should be fast")
	}
}

// TestBackoffShortFlapDoesNotResetSequence: a connection that died before
// StableAfter leaves the sequence where it was, so a backend that accepts and
// immediately drops still climbs the curve instead of looping on the fast tier.
func TestBackoffShortFlapDoesNotResetSequence(t *testing.T) {
	c := &fakeClock{t: time.Unix(1, 0)}
	b := newTestBackoff(c)
	for i := 0; i < FastRetries; i++ {
		b.Next()
	}
	c.add(2 * FastWindow) // even a fresh window must not help: the sequence stands
	b.ConnectionEnded(2 * time.Second)
	if _, fast := nextFast(b); fast {
		t.Fatal("a pre-stable flap took the fast tier; only a reset may restore it")
	}
}

// TestBackoffZeroValueIsUsable: agent.Run builds a bare &Backoff{}, so the
// defaults must produce the documented curve with no fields set — including the
// overflow guard, well past the shift cap.
func TestBackoffZeroValueIsUsable(t *testing.T) {
	b := &Backoff{}
	for i := 0; i < FastRetries; i++ {
		if d, fast := nextFast(b); !fast || d < FastDelay/2 || d >= FastDelay/2+FastDelay {
			t.Fatalf("zero-value fast attempt %d: %v (fast=%v)", i, d, fast)
		}
	}
	for i := 0; i < 40; i++ {
		if d := b.Next(); d < 1 || d > 60*time.Second {
			t.Fatalf("zero-value slow attempt %d: %v outside (0, 60s]", i, d)
		}
	}
}
