package engine

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"proxyforward/internal/analytics"
	"proxyforward/internal/config"
)

// TestStatsLifecycle runs a real gateway engine briefly and verifies the
// stats store samples and lands in the analytics database at shutdown. The
// IPC pipe may be owned by another process on a dev machine; that only fails
// Run's error path, not the store lifecycle this test asserts on.
func TestStatsLifecycle(t *testing.T) {
	dir := t.TempDir()
	// Config validation requires a concrete port; borrow a free one.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := config.Default()
	cfg.Role = config.RoleGateway
	cfg.Gateway.BindAddr = "127.0.0.1"
	cfg.Gateway.ControlPort = port
	cfg.Gateway.Token = "test-token"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	eng, err := New(cfg, dir, filepath.Join(dir, "config.toml"), logger)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()

	// Wait for the precondition the shutdown assertions below actually need,
	// rather than sleeping a magic 1200ms (.claude/rules/go-tests.md: no bare
	// sleeps as assertions). A persisted tier (>=15s) receives its first bucket
	// only when a 1s-tier bucket COMPLETES and cascades up (stats.go add), so
	// the store needs a full second of *sampling* — and a fixed sleep has to
	// cover engine startup too. Under -race startup eats the budget and the old
	// sleep lost the race; it was marginal even without it.
	//
	// A window wider than tier0's 2-minute span selects the 1s tier, so two
	// buckets there means one has completed and cascaded into a persisted tier.
	deadline := time.Now().Add(30 * time.Second)
	for len(eng.History(300_000, 300).Buckets) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("sampler never completed a 1s bucket")
		}
		time.Sleep(20 * time.Millisecond)
	}

	st := eng.Status()
	if st.ProcessStartMs == 0 {
		t.Error("Status.ProcessStartMs is zero")
	}
	if h := eng.History(15_000, 150); len(h.Buckets) == 0 {
		t.Error("sampler produced no history buckets")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("engine did not stop")
	}

	// The final flush must have landed in analytics.db before Run returned:
	// reopen the database and restore the snapshot.
	db, err := analytics.Open(dir, analytics.Options{}, logger)
	if err != nil {
		t.Fatalf("analytics.db not reopenable after shutdown: %v", err)
	}
	defer db.Close()
	snap, err := db.LoadStats()
	if err != nil {
		t.Fatalf("stats not restorable from analytics.db: %v", err)
	}
	if snap == nil {
		t.Fatal("analytics.db holds no stats snapshot after shutdown")
	}
	if snap.Lifetime.FirstRunMs == 0 {
		t.Error("restored lifetime has no first-run stamp")
	}
	var buckets int
	for _, ts := range snap.Tiers {
		buckets += len(ts.Buckets)
	}
	if buckets == 0 {
		t.Error("no persisted-tier buckets landed at shutdown")
	}

	// Phase 8: the analytics summary op must answer over the freshly-closed
	// store, and the run must have bracketed itself with engine up/down events.
	if _, err := db.Summary(0, time.Now().UnixMilli()); err != nil {
		t.Errorf("Summary after shutdown: %v", err)
	}

	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "analytics.db")))
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer raw.Close()
	var ups, downs int
	if err := raw.QueryRow(`SELECT
		COUNT(*) FILTER (WHERE up = 1), COUNT(*) FILTER (WHERE up = 0)
		FROM events WHERE kind = 'engine'`).Scan(&ups, &downs); err != nil {
		t.Fatalf("count engine events: %v", err)
	}
	if ups != 1 || downs != 1 {
		t.Errorf("engine events = %d up / %d down, want 1/1", ups, downs)
	}
}
