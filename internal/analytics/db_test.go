package analytics

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T, dir string) *DB {
	t.Helper()
	d, err := Open(dir, Options{}, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenMigrateReopen(t *testing.T) {
	dir := t.TempDir()
	d := openTest(t, dir)

	var v int
	if err := d.sql.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("schema version = %d (err=%v), want %d", v, err, len(migrations))
	}
	// A few load-bearing tables must exist.
	for _, table := range []string{"rrd", "lifetime", "sessions", "players", "rollup_hourly", "events"} {
		var n int
		if err := d.sql.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table') AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing (n=%d, err=%v)", table, n, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen: no re-migration, same version.
	d2 := openTest(t, dir)
	if err := d2.sql.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("reopened schema version = %d (err=%v)", v, err)
	}
}

func TestOpenRecoversFromGarbageFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, dbFile)
	if err := os.WriteFile(path, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := openTest(t, dir)
	// The garbage moved aside; a working database took its place.
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("garbage db was not renamed aside: %v", err)
	}
	var v int
	if err := d.sql.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("replacement db not migrated (v=%d, err=%v)", v, err)
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	dir := t.TempDir()
	d := openTest(t, dir)
	if _, err := d.sql.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	d.Close()

	// A plain reopen must refuse (downgrade protection)…
	if _, err := openSQLite(filepath.Join(dir, dbFile)); err == nil {
		t.Fatal("newer schema was not refused")
	}
	// …while Open falls back to renaming it aside and starting fresh.
	d2 := openTest(t, dir)
	var v int
	if err := d2.sql.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("fresh db after refusal not migrated (v=%d, err=%v)", v, err)
	}
	if _, err := os.Stat(filepath.Join(dir, dbFile) + ".bad"); err != nil {
		t.Fatalf("newer db was not preserved aside: %v", err)
	}
}

func TestWriterBatchAndBarrier(t *testing.T) {
	d := openTest(t, t.TempDir())
	for i := range 10 {
		d.Enqueue("test-insert", func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO events (t, kind, tunnel_id, up) VALUES (?, 'link', '', ?)`, int64(i), i%2)
			return err
		})
	}
	d.Barrier()
	var n int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil || n != 10 {
		t.Fatalf("events after barrier = %d (err=%v), want 10", n, err)
	}

	// Barrier after Close must not hang.
	d.Close()
	doneC := make(chan struct{})
	go func() { d.Barrier(); close(doneC) }()
	select {
	case <-doneC:
	case <-time.After(2 * time.Second):
		t.Fatal("Barrier hung after Close")
	}
}

// TestEnqueueDropOldest wedges the writer inside a transaction, floods the
// queue past capacity, and checks the drop-oldest contract: the new op always
// lands, exactly K ops are dropped, and a barrier is never lost.
func TestEnqueueDropOldest(t *testing.T) {
	d := openTest(t, t.TempDir())
	d.Barrier() // let the startup prune/rollup finish first

	entered := make(chan struct{})
	release := make(chan struct{})
	d.Enqueue("blocker", func(tx *sql.Tx) error {
		close(entered)
		<-release
		return nil
	})
	<-entered // writer is now blocked inside the blocker's transaction

	const K = 50
	for range writeQueueCap + K {
		d.Enqueue("flood", func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO events (t, kind, tunnel_id, up) VALUES (1, 'link', '', 1)`)
			return err
		})
	}
	if n := d.dropped.Load(); n != K {
		t.Fatalf("dropped = %d, want exactly %d", n, K)
	}

	// A concurrent Barrier parks behind the full queue and must return only
	// after every earlier op commits.
	barrierDone := make(chan struct{})
	go func() { d.Barrier(); close(barrierDone) }()
	select {
	case <-barrierDone:
		t.Fatal("Barrier returned while the writer was still blocked")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-barrierDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Barrier did not return after the writer resumed")
	}
	var n int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'link'`).Scan(&n); err != nil || n != writeQueueCap {
		t.Fatalf("landed flood ops = %d (err=%v), want %d", n, err, writeQueueCap)
	}
}

// TestPruneEventCarriersAndOrphans drives the retention upgrades with an
// injected clock: windowed uptime identical before/after prune, all-time
// uptime seeded by the synthetic carrier, orphaned side rows swept, hourly
// rollups pruned at their horizon while daily rollups are kept.
func TestPruneEventCarriersAndOrphans(t *testing.T) {
	d := openTest(t, t.TempDir())
	d.Barrier() // startup prune must not race the seeds

	now := time.Now()
	nowMs := now.UnixMilli()
	cutoff := now.AddDate(0, 0, -d.retentionDays).UnixMilli()
	const day = int64(24 * 60 * 60 * 1000)
	old := cutoff - 2*day
	rec1 := cutoff + 1*day
	rec2 := cutoff + 2*day

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.sql.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// Engine up since `old`; link up since `old` with one in-window outage.
	mustExec(`INSERT INTO events (t, kind, tunnel_id, up) VALUES
		(?, 'engine', '', 1), (?, 'link', '', 1), (?, 'link', '', 0), (?, 'link', '', 1)`,
		old, old, rec1, rec2)
	// Orphaned side rows whose parent session never landed.
	mustExec(`INSERT INTO session_traffic (session_id, t, inb, outb) VALUES (777, ?, 1, 1)`, nowMs)
	mustExec(`INSERT INTO session_rtt (session_id, t, avg, mn, mx, n) VALUES (777, ?, 1, 1, 1, 1)`, nowMs)
	// One hourly rollup beyond the 90 d horizon, one fresh; one ancient daily.
	mustExec(`INSERT INTO rollup_hourly (hour_ms, bytes_in) VALUES (?, 1), (?, 2)`,
		nowMs-rollupHourlyRetention.Milliseconds()-day, nowMs-day)
	mustExec(`INSERT INTO rollup_daily (day_ms, bytes_in) VALUES (?, 3)`, nowMs-400*day)

	before, err := d.TunnelUptime(cutoff, nowMs)
	if err != nil {
		t.Fatalf("TunnelUptime before: %v", err)
	}

	d.prune(now)

	after, err := d.TunnelUptime(cutoff, nowMs)
	if err != nil {
		t.Fatalf("TunnelUptime after: %v", err)
	}
	if !approx(before.Link.UptimePct, after.Link.UptimePct) {
		t.Errorf("windowed uptime shifted across prune: %v -> %v", before.Link.UptimePct, after.Link.UptimePct)
	}
	// All-time: the carrier keeps the pre-cutoff state known, so the window
	// [cutoff, now] is fully known with one (rec2-rec1) outage.
	all, err := d.TunnelUptime(0, nowMs)
	if err != nil {
		t.Fatalf("TunnelUptime all-time: %v", err)
	}
	want := float64(nowMs-cutoff-(rec2-rec1)) / float64(nowMs-cutoff) * 100
	if !approx(all.Link.UptimePct, want) {
		t.Errorf("all-time uptime after prune = %v, want %v", all.Link.UptimePct, want)
	}

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := d.sql.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM session_traffic WHERE session_id = 777`); n != 0 {
		t.Errorf("orphaned session_traffic rows = %d, want 0", n)
	}
	if n := count(`SELECT COUNT(*) FROM session_rtt WHERE session_id = 777`); n != 0 {
		t.Errorf("orphaned session_rtt rows = %d, want 0", n)
	}
	if n := count(`SELECT COUNT(*) FROM rollup_hourly`); n != 1 {
		t.Errorf("hourly rollups = %d, want 1 (expired row pruned)", n)
	}
	if n := count(`SELECT COUNT(*) FROM rollup_daily`); n != 1 {
		t.Errorf("daily rollups = %d, want 1 (kept forever)", n)
	}
}

func TestPrune(t *testing.T) {
	d := openTest(t, t.TempDir())
	// The writer runs a prune at startup; wait for it so seeded rows are not
	// swept by that instead of the call under test.
	d.Barrier()
	now := time.Now()
	old := now.AddDate(0, 0, -(d.retentionDays + 5)).UnixMilli()
	fresh := now.Add(-time.Hour).UnixMilli()

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.sql.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// One expired ended session (with traffic), one expired-but-live session,
	// one fresh session.
	mustExec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, ended_ms) VALUES (1, 't', '1.1.1.1', ?, ?)`, old, old+1000)
	mustExec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms) VALUES (2, 't', '1.1.1.1', ?)`, old)
	mustExec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, ended_ms) VALUES (3, 't', '1.1.1.1', ?, ?)`, fresh, fresh+1000)
	mustExec(`INSERT INTO session_traffic (session_id, t, inb, outb) VALUES (1, ?, 1, 1), (3, ?, 1, 1)`, old, fresh)
	mustExec(`INSERT INTO events (t, kind, up) VALUES (?, 'link', 1), (?, 'link', 0)`, old, fresh)
	mustExec(`INSERT INTO geo_cache (ip, resolved_ms) VALUES ('1.1.1.1', ?), ('2.2.2.2', ?)`,
		now.Add(-40*24*time.Hour).UnixMilli(), fresh)

	d.prune(now)

	counts := map[string]int{}
	for _, q := range []struct{ name, query string }{
		{"sessions", `SELECT COUNT(*) FROM sessions`},
		{"traffic", `SELECT COUNT(*) FROM session_traffic`},
		{"events", `SELECT COUNT(*) FROM events`},
		{"geo", `SELECT COUNT(*) FROM geo_cache`},
	} {
		var n int
		if err := d.sql.QueryRow(q.query).Scan(&n); err != nil {
			t.Fatalf("%s count: %v", q.name, err)
		}
		counts[q.name] = n
	}
	// Session 1 expired, 2 survives (still open), 3 survives (fresh).
	if counts["sessions"] != 2 {
		t.Fatalf("sessions after prune = %d, want 2", counts["sessions"])
	}
	if counts["traffic"] != 1 {
		t.Fatalf("session_traffic after prune = %d, want 1", counts["traffic"])
	}
	// The expired up-event is replaced by a synthetic carrier at the cutoff
	// (preserving the last-known state); the fresh down-event survives.
	if counts["events"] != 2 {
		t.Fatalf("events after prune = %d, want 2 (carrier + fresh)", counts["events"])
	}
	cutoff := now.AddDate(0, 0, -d.retentionDays).UnixMilli()
	var carrierUp int
	if err := d.sql.QueryRow(`SELECT up FROM events WHERE t = ? AND kind = 'link'`, cutoff).Scan(&carrierUp); err != nil || carrierUp != 1 {
		t.Fatalf("carrier event at cutoff: up=%d err=%v, want up=1", carrierUp, err)
	}
	if counts["geo"] != 1 {
		t.Fatalf("geo_cache after prune = %d, want 1", counts["geo"])
	}
}
