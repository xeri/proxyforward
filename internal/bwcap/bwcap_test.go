package bwcap_test

import (
	"context"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"proxyforward/internal/bwcap"
	"proxyforward/internal/relay"
)

func TestNormalizeScope(t *testing.T) {
	cases := map[string]bwcap.Scope{
		"":               bwcap.ScopeCombined, // empty -> combined
		"combined":       bwcap.ScopeCombined,
		"per-direction":  bwcap.ScopePerDirection,
		"per-connection": bwcap.ScopePerConnection,
		"sideways":       bwcap.ScopeCombined, // unknown -> combined (fail-safe)
		"COMBINED":       bwcap.ScopeCombined, // case-sensitive; unknown -> combined
	}
	for in, want := range cases {
		if got := bwcap.NormalizeScope(in); got != want {
			t.Errorf("NormalizeScope(%q) = %q, want %q", in, got, want)
		}
	}
}

// The unit is decimal Mbps (Mbps*125000 bytes/sec) and the burst is relay.BufSize
// — the single source of truth both this package and the e2e assertion rely on.
func TestLimiterUnitAndBurst(t *testing.T) {
	set := bwcap.BuildSet(5, "combined")
	in, _ := bwcap.Resolve(set)
	lim, ok := in.(*rate.Limiter)
	if !ok {
		t.Fatalf("Resolve returned %T, want *rate.Limiter", in)
	}
	if lim.Limit() != rate.Limit(5*125_000) {
		t.Errorf("limit = %v, want %v (5 Mbps decimal)", lim.Limit(), rate.Limit(5*125_000))
	}
	if lim.Burst() != relay.BufSize {
		t.Errorf("burst = %d, want %d (relay.BufSize)", lim.Burst(), relay.BufSize)
	}
}

func TestResolveSharingByScope(t *testing.T) {
	// Combined: both directions share one instance.
	ci, co := bwcap.Resolve(bwcap.BuildSet(5, "combined"))
	if ci == nil || ci != co {
		t.Errorf("combined must share one limiter both ways (in=%v out=%v)", ci, co)
	}

	// Per-direction: two distinct non-nil instances.
	di, do := bwcap.Resolve(bwcap.BuildSet(5, "per-direction"))
	if di == nil || do == nil || di == do {
		t.Errorf("per-direction must give two distinct limiters (in=%v out=%v)", di, do)
	}

	// Per-connection: shared within a connection, fresh across connections.
	pset := bwcap.BuildSet(5, "per-connection")
	p1a, p1b := bwcap.Resolve(pset)
	p2a, _ := bwcap.Resolve(pset)
	if p1a == nil || p1a != p1b {
		t.Error("per-connection must share one limiter within a connection")
	}
	if p1a == p2a {
		t.Error("per-connection must mint a fresh limiter per connection")
	}

	// Uncapped: nil pair (the relay fast path).
	ui, uo := bwcap.Resolve(bwcap.BuildSet(0, "combined"))
	if ui != nil || uo != nil {
		t.Errorf("uncapped must resolve to (nil, nil), got (%v, %v)", ui, uo)
	}
	// A nil set is also the fast path.
	if ni, no := bwcap.Resolve(nil); ni != nil || no != nil {
		t.Errorf("nil set must resolve to (nil, nil), got (%v, %v)", ni, no)
	}
}

func TestReconcileInPlaceVsRebuild(t *testing.T) {
	// nil cur -> build a fresh set.
	s1, sw1 := bwcap.Reconcile(nil, 5, "combined")
	if !sw1 || s1 == nil || s1.Rate() != 5 {
		t.Fatalf("nil cur must build a new capped set: swapped=%v set=%v", sw1, s1)
	}

	// Rate-only change, same scope: applied in place, no swap, same set.
	s2, sw2 := bwcap.Reconcile(s1, 10, "combined")
	if sw2 || s2 != s1 || s2.Rate() != 10 {
		t.Fatalf("rate-only change must update in place: swapped=%v same=%v rate=%d", sw2, s2 == s1, s2.Rate())
	}
	if in, _ := bwcap.Resolve(s2); in.(*rate.Limiter).Limit() != rate.Limit(10*125_000) {
		t.Errorf("rate-only change did not update the shared limiter to 10 Mbps")
	}

	// Scope change: rebuild to a new set.
	s3, sw3 := bwcap.Reconcile(s2, 10, "per-direction")
	if !sw3 || s3 == s2 {
		t.Errorf("scope change must swap to a new set")
	}

	// Capped -> uncapped: rebuild (can't SetLimit a nil limiter into existence).
	s4, sw4 := bwcap.Reconcile(s3, 0, "per-direction")
	if !sw4 {
		t.Error("capped->uncapped flip must swap")
	}
	if in, out := bwcap.Resolve(s4); in != nil || out != nil {
		t.Error("uncapped set must resolve to nil pair")
	}
}

func TestRegistrySharesAndReleases(t *testing.T) {
	r := bwcap.NewRegistry()

	// Same tunnel, combined scope: connections share one bucket.
	a1, _ := r.Resolve("t1", 5, "combined")
	a2, _ := r.Resolve("t1", 5, "combined")
	if a1 == nil || a1 != a2 {
		t.Error("combined tunnel must share one bucket across connections")
	}

	// A distinct tunnel gets its own bucket.
	b1, _ := r.Resolve("t2", 5, "combined")
	if b1 == a1 {
		t.Error("distinct tunnels must not share a bucket")
	}

	// Rate-only change applies in place to the shared bucket.
	a3, _ := r.Resolve("t1", 10, "combined")
	if a3 != a1 {
		t.Error("rate-only change must keep the shared bucket")
	}
	if a3.(*rate.Limiter).Limit() != rate.Limit(10*125_000) {
		t.Error("rate-only change not applied to the shared bucket")
	}

	// Release drops it; the next resolve builds fresh.
	r.Release("t1")
	a4, _ := r.Resolve("t1", 10, "combined")
	if a4 == a1 {
		t.Error("after Release a fresh bucket must be built")
	}
}

// TestThrottleEnforcesRate proves the constructed limiter actually delays a
// transfer to the configured rate (not just that construction looks right).
func TestThrottleEnforcesRate(t *testing.T) {
	const mbps = 5 // 625000 B/s, burst = relay.BufSize
	in, _ := bwcap.Resolve(bwcap.BuildSet(mbps, "combined"))
	ctx := context.Background()

	// Drive ~250000 bytes beyond the initial burst; at 625000 B/s that is ~0.4s.
	total := relay.BufSize + 250_000
	start := time.Now()
	for sent := 0; sent < total; {
		n := 50_000
		if total-sent < n {
			n = total - sent
		}
		if err := in.WaitN(ctx, n); err != nil {
			t.Fatal(err)
		}
		sent += n
	}
	elapsed := time.Since(start)
	if elapsed < 250*time.Millisecond {
		t.Errorf("transfer finished in %s: rate not enforced", elapsed)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("transfer took %s: throttled well below the configured rate", elapsed)
	}
}
