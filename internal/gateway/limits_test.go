package gateway

import (
	"fmt"
	"testing"
	"time"
)

// TestSourceNetIsTheUnitOfAbuse pins the reduction every limit below depends on.
func TestSourceNetIsTheUnitOfAbuse(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3.4":               "1.2.3.4", // v4 costs money: account per address
		"::ffff:1.2.3.4":        "1.2.3.4", // v4-mapped is still that v4 address
		"2001:db8::1":           "2001:db8::/64",
		"2001:db8:0:0:ffff::99": "2001:db8::/64", // same subscriber, free address
		"2001:db8:0:1::1":       "2001:db8:0:1::/64",
		"fe80::1%eth0":          "fe80::/64",             // the zone is not part of the network
		`\\.\pipe\proxyforward`: `\\.\pipe\proxyforward`, // not an address: account whole
	} {
		if got := sourceNet(in); got != want {
			t.Errorf("sourceNet(%q) = %q, want %q", in, got, want)
		}
	}
}

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

// TestAuthLimiterIsBoundedUnderSourceRotation is the DoS this structure exists to
// stop. allow() is the first thing handleControlConn does, so the auth table's
// mutex is on every honest agent's admission path. The old table triggered its
// cleanup on size (>4096 keys) but deleted only entries that had *fully expired* —
// so a flood that keeps its entries fresh frees nothing, grows without bound, and
// re-scans every key on every insert with that mutex held. Time is frozen here
// because that is the attacker's choice, not a contrivance: nothing expires.
func TestAuthLimiterIsBoundedUnderSourceRotation(t *testing.T) {
	now := time.Now()
	l := newAuthLimiter(10)
	l.now = func() time.Time { return now }

	for i := range 200_000 {
		l.fail(fmt.Sprintf("2001:db8:%x:%x::1", i>>16, i&0xffff))
	}
	if got := len(l.at); got > authFailNets {
		t.Errorf("the auth table holds %d networks; the cap is %d, and every honest "+
			"admission waits behind a scan of it", got, authFailNets)
	}
	if l.lru.Len() != len(l.at) {
		t.Errorf("index and LRU disagree (%d vs %d): eviction leaks", len(l.at), l.lru.Len())
	}
}

// TestAuthLimiterAccountsIPv6PerPrefix: keyed per address, the failure budget is
// 2^64 wide and free, because the smallest block a subscriber is handed is a /64.
func TestAuthLimiterAccountsIPv6PerPrefix(t *testing.T) {
	now := time.Now()
	l := newAuthLimiter(3)
	l.now = func() time.Time { return now }

	for i := range 3 {
		addr := fmt.Sprintf("2001:db8::%x", i)
		if !l.allow(addr) {
			t.Fatalf("blocked after %d failures from the /64, limit is 3", i)
		}
		l.fail(addr)
	}
	if l.allow("2001:db8::dead:beef") {
		t.Fatal("a fresh address in an exhausted /64 was allowed: rotating the low bits is free, so the limit is not one")
	}
	if got := len(l.at); got != 1 {
		t.Errorf("one /64 cost %d table entries, want 1", got)
	}
	// A different /64 is a different subscriber and keeps its own budget.
	if !l.allow("2001:db8:0:1::1") {
		t.Fatal("an unrelated /64 was blocked")
	}
}

// TestAuthLimiterBlockOutlastsATick guards the trap in a refilling bucket:
// budget accrues continuously, so admitting on a *fraction* of an attempt would
// end every block the nanosecond it started — a rate limiter that never limits,
// and one that still passes every test above.
func TestAuthLimiterBlockOutlastsATick(t *testing.T) {
	now := time.Now()
	l := newAuthLimiter(6) // spent, this refills one attempt per 10s
	l.now = func() time.Time { return now }

	for range 6 {
		l.fail("198.51.100.7")
	}
	if l.allow("198.51.100.7") {
		t.Fatal("not blocked after spending the whole burst")
	}
	now = now.Add(time.Second)
	if l.allow("198.51.100.7") {
		t.Fatal("the block lasted one second: partial budget must not buy a whole attempt")
	}
	now = now.Add(9 * time.Second)
	if !l.allow("198.51.100.7") {
		t.Fatal("still blocked once a full attempt's budget refilled; the sustained rate is the limit, not zero")
	}
}

// TestAuthLimiterBanIsBoundedByTheWindow: a burst must not buy a ban
// proportional to itself. Auth failures are attributed to a source network, and
// a network is shared — CGNAT, a school, a VPN exit. The bystander must not
// serve out the loudest tenant's sentence.
func TestAuthLimiterBanIsBoundedByTheWindow(t *testing.T) {
	now := time.Now()
	l := newAuthLimiter(10)
	l.now = func() time.Time { return now }

	for range 100_000 {
		l.fail("203.0.113.9")
	}
	if l.allow("203.0.113.9") {
		t.Fatal("not blocked after 100k failures")
	}
	now = now.Add(time.Minute)
	if !l.allow("203.0.113.9") {
		t.Fatal("a 100k burst outlasted the window: a NAT'd bystander is locked out for what someone else did")
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
	if len(g.perNet) != 0 || g.global != 0 {
		t.Fatalf("counters leak: global=%d perNet=%v", g.global, g.perNet)
	}
}

// TestConnGateCapsIPv6PerPrefix: the per-IP cap is the only thing standing
// between one client and the whole global cap. Keyed per address it stands
// between nothing — an IPv6 client re-sources from its own /64 and collects a
// fresh 32-conn budget per address, for free, until the gateway is full.
func TestConnGateCapsIPv6PerPrefix(t *testing.T) {
	g := newConnGate(100, 2)

	first, second := g.admit("2001:db8::1"), g.admit("2001:db8::2")
	if !first || !second {
		t.Fatal("the /64's first two conns were rejected")
	}
	if g.admit("2001:db8::3") {
		t.Fatal("a third address in the same /64 brought its own budget: one subscriber can take the global cap")
	}
	if !g.admit("2001:db8:0:1::1") {
		t.Fatal("an unrelated /64 was refused")
	}

	// release must reduce the address exactly as admit did, or slots leak until
	// the /64 is permanently full.
	g.release("2001:db8::1")
	if !g.admit("2001:db8::3") {
		t.Fatal("releasing one address did not free its /64's slot: admit and release disagree on the key")
	}

	g.release("2001:db8::2")
	g.release("2001:db8::3")
	g.release("2001:db8:0:1::1")
	if len(g.perNet) != 0 || g.global != 0 {
		t.Fatalf("counters leak: global=%d perNet=%v", g.global, g.perNet)
	}
}
