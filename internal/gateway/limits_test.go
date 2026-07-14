package gateway

import (
	"testing"
	"time"
)

func TestAuthLimiterBlocksAfterFailures(t *testing.T) {
	now := time.Now()
	l := newAuthLimiter(3)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if !l.allow("1.2.3.4") {
			t.Fatalf("blocked after %d failures, limit is 3", i)
		}
		l.fail("1.2.3.4")
	}
	if l.allow("1.2.3.4") {
		t.Fatal("allowed after 3 failures")
	}
	// A different IP is unaffected.
	if !l.allow("5.6.7.8") {
		t.Fatal("unrelated IP blocked")
	}
	// The window expiring unblocks.
	now = now.Add(61 * time.Second)
	if !l.allow("1.2.3.4") {
		t.Fatal("still blocked after window expired")
	}
}

func TestAuthLimiterSuccessesDoNotCount(t *testing.T) {
	l := newAuthLimiter(2)
	// allow() alone never consumes budget — only fail() does. A flapping
	// agent that reconnects successfully 100 times stays welcome.
	for range 100 {
		if !l.allow("10.0.0.1") {
			t.Fatal("successful reconnects were rate limited")
		}
	}
}

func TestConnGateCaps(t *testing.T) {
	g := newConnGate(3, 2)

	// Both calls must run (no short-circuit): the point is that two conns from
	// the same IP fit under the per-IP cap of 2.
	first, second := g.admit("a"), g.admit("a")
	if !first || !second {
		t.Fatal("first two conns from a rejected")
	}
	if g.admit("a") {
		t.Fatal("third conn from a admitted past per-IP cap of 2")
	}
	if !g.admit("b") {
		t.Fatal("first conn from b rejected")
	}
	// Global cap (3) now full.
	if g.admit("c") {
		t.Fatal("conn admitted past global cap of 3")
	}
	// Releasing frees both counters.
	g.release("a")
	if !g.admit("c") {
		t.Fatal("slot not freed after release")
	}
	g.release("a")
	g.release("b")
	g.release("c")
	if len(g.perIP) != 0 || g.global != 0 {
		t.Fatalf("counters leak: global=%d perIP=%v", g.global, g.perIP)
	}
}
