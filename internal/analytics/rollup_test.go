package analytics

import (
	"testing"
	"time"
)

// insertBucket writes one rrd bucket with flat OHLC (open=high=low=close) for
// every rate and gauge, so tests can assert sums/maxima directly. Pass -1 for
// a gauge to mark it unknown.
func insertBucket(t *testing.T, d *DB, tier int, tms, inb, outb int64, inRate, outRate, conns, rtt, players, loss float64) {
	t.Helper()
	if _, err := d.sql.Exec(`INSERT INTO rrd
		(tier, t, inb, outb, io, ih, il, ic, oo, oh, ol, oc, co, ch, cl, cc, ro, rh, rl, rc, po, ph, pl, pc, lo, lh, ll, lc)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tier, tms, inb, outb,
		inRate, inRate, inRate, inRate, outRate, outRate, outRate, outRate,
		conns, conns, conns, conns, rtt, rtt, rtt, rtt,
		players, players, players, players, loss, loss, loss, loss); err != nil {
		t.Fatalf("insert bucket: %v", err)
	}
}

// rollupBase is an hour-aligned "current hour" a few hours back, safely inside
// retention.
func rollupBase() (base, now int64) {
	h := time.Now().UnixMilli() / hourMillis
	base = h * hourMillis    // current hour start
	now = base + 20*minuteMs // 20 min into the current hour
	return base, now
}

func TestRollupHourly(t *testing.T) {
	d := openTest(t, t.TempDir())
	base, now := rollupBase()
	hA := base - 2*hourMillis
	hB := base - hourMillis

	// Hour A: two 15 s buckets.
	insertBucket(t, d, 2, hA, 100, 200, 10, 20, 6, 30, 5, 1)
	insertBucket(t, d, 2, hA+15_000, 100, 200, 8, 16, 4, 34, 3, 3)
	// Hour B: one bucket, players unknown.
	insertBucket(t, d, 2, hB, 50, 60, 5, 6, 2, 40, -1, 2)

	// Sessions: two in A (steve+alex), two in B (steve + anon).
	seed := func(id int64, tstart int64, uuid any) {
		if _, err := d.sql.Exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, player_uuid)
			VALUES (?, 't1', '203.0.113.9', ?, ?)`, id, tstart, uuid); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}
	seed(1, hA+1000, steveUUID)
	seed(2, hA+2000, alexUUID)
	seed(3, hB+1000, steveUUID)
	seed(4, hB+2000, nil)

	d.runRollup(time.UnixMilli(now))

	var r struct {
		bin, bout          int64
		pin, pout          float64
		peakPl, avgPl      float64
		rtt, loss          float64
		sessions, uniquePl int
	}
	row := d.sql.QueryRow(`SELECT bytes_in, bytes_out, peak_in_bps, peak_out_bps,
		peak_players, avg_players, rtt_avg, loss_avg, sessions, unique_players
		FROM rollup_hourly WHERE hour_ms = ?`, hA)
	if err := row.Scan(&r.bin, &r.bout, &r.pin, &r.pout, &r.peakPl, &r.avgPl, &r.rtt, &r.loss, &r.sessions, &r.uniquePl); err != nil {
		t.Fatalf("scan A: %v", err)
	}
	if r.bin != 200 || r.bout != 400 {
		t.Errorf("A bytes = %d/%d, want 200/400", r.bin, r.bout)
	}
	if r.pin != 10 || r.pout != 20 {
		t.Errorf("A peak bps = %v/%v, want 10/20", r.pin, r.pout)
	}
	if r.peakPl != 5 || !approx(r.avgPl, 4) {
		t.Errorf("A players peak/avg = %v/%v, want 5/4", r.peakPl, r.avgPl)
	}
	if !approx(r.rtt, 32) || !approx(r.loss, 2) {
		t.Errorf("A rtt/loss = %v/%v, want 32/2", r.rtt, r.loss)
	}
	if r.sessions != 2 || r.uniquePl != 2 {
		t.Errorf("A sessions/unique = %d/%d, want 2/2", r.sessions, r.uniquePl)
	}

	// Hour B: players unknown → -1; anon session not counted as a unique player.
	row = d.sql.QueryRow(`SELECT peak_players, avg_players, sessions, unique_players
		FROM rollup_hourly WHERE hour_ms = ?`, hB)
	if err := row.Scan(&r.peakPl, &r.avgPl, &r.sessions, &r.uniquePl); err != nil {
		t.Fatalf("scan B: %v", err)
	}
	if r.peakPl != -1 || r.avgPl != -1 {
		t.Errorf("B players = %v/%v, want -1/-1", r.peakPl, r.avgPl)
	}
	if r.sessions != 2 || r.uniquePl != 1 {
		t.Errorf("B sessions/unique = %d/%d, want 2/1", r.sessions, r.uniquePl)
	}

	// Daily must equal the sum/max of its hourly rows (self-consistent, no
	// dependence on the day boundary this test happened to land on).
	assertDailyConsistent(t, d)
}

func assertDailyConsistent(t *testing.T, d *DB) {
	t.Helper()
	rows, err := d.sql.Query(`SELECT d.day_ms, d.bytes_in, d.sessions,
		COALESCE((SELECT SUM(bytes_in) FROM rollup_hourly h WHERE h.hour_ms/? *? = d.day_ms), 0),
		COALESCE((SELECT SUM(sessions) FROM rollup_hourly h WHERE h.hour_ms/? *? = d.day_ms), 0)
		FROM rollup_daily d`, dayMillis, dayMillis, dayMillis, dayMillis)
	if err != nil {
		t.Fatalf("daily consistency query: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var day, dbin, dsess, hbin, hsess int64
		if err := rows.Scan(&day, &dbin, &dsess, &hbin, &hsess); err != nil {
			t.Fatalf("scan daily: %v", err)
		}
		if dbin != hbin || dsess != hsess {
			t.Errorf("day %d: bytes %d vs hourly %d, sessions %d vs %d", day, dbin, hbin, dsess, hsess)
		}
		n++
	}
	if n == 0 {
		t.Error("no daily rows produced")
	}
}

// TestRollupPreservesLappedHour finalises an hour, then simulates its rrd
// buckets scrolling out of the tier-2 window and re-rolls: the stored value
// must survive rather than being recomputed from the now-missing buckets.
func TestRollupPreservesLappedHour(t *testing.T) {
	d := openTest(t, t.TempDir())
	base, now := rollupBase()
	hA := base - 2*hourMillis
	hB := base - hourMillis

	insertBucket(t, d, 2, hA, 100, 200, 10, 20, 6, 30, 5, 1)
	insertBucket(t, d, 2, hB, 50, 60, 5, 6, 2, 40, 3, 2)
	d.runRollup(time.UnixMilli(now))

	var binBefore int64
	if err := d.sql.QueryRow(`SELECT bytes_in FROM rollup_hourly WHERE hour_ms = ?`, hA).Scan(&binBefore); err != nil {
		t.Fatalf("scan before: %v", err)
	}
	if binBefore != 100 {
		t.Fatalf("A bytes_in before = %d, want 100", binBefore)
	}

	// Hour A laps out of tier-2; only hour B (and later) remain.
	if _, err := d.sql.Exec(`DELETE FROM rrd WHERE t < ?`, hB); err != nil {
		t.Fatalf("lap: %v", err)
	}
	d.runRollup(time.UnixMilli(now))

	var binAfter int64
	if err := d.sql.QueryRow(`SELECT bytes_in FROM rollup_hourly WHERE hour_ms = ?`, hA).Scan(&binAfter); err != nil {
		t.Fatalf("scan after: %v", err)
	}
	if binAfter != 100 {
		t.Errorf("A bytes_in after lap = %d, want preserved 100", binAfter)
	}
}

// TestRollupPeaksMonotonic checks that an all-time record survives after the
// bucket that set it scrolls out of the window and only smaller values remain.
func TestRollupPeaksMonotonic(t *testing.T) {
	d := openTest(t, t.TempDir())
	base, now := rollupBase()
	hA := base - 2*hourMillis
	hB := base - hourMillis

	insertBucket(t, d, 2, hA, 100, 200, 10, 20, 9, 30, 5, 1) // record: in 10, conns 9, players 5
	insertBucket(t, d, 2, hB, 50, 60, 4, 6, 2, 40, 3, 2)
	d.runRollup(time.UnixMilli(now))

	peak := func(key string) (float64, int64) {
		var v float64
		var at int64
		if err := d.sql.QueryRow(`SELECT value, at_ms FROM peaks WHERE key = ?`, key).Scan(&v, &at); err != nil {
			t.Fatalf("peak %s: %v", key, err)
		}
		return v, at
	}
	if v, at := peak("in_bps"); v != 10 || at != hA {
		t.Errorf("in_bps peak = %v@%d, want 10@%d", v, at, hA)
	}
	if v, _ := peak("players"); v != 5 {
		t.Errorf("players peak = %v, want 5", v)
	}

	// The record bucket laps out; only the smaller hour B remains.
	if _, err := d.sql.Exec(`DELETE FROM rrd WHERE t < ?`, hB); err != nil {
		t.Fatalf("lap: %v", err)
	}
	d.runRollup(time.UnixMilli(now))
	if v, _ := peak("in_bps"); v != 10 {
		t.Errorf("in_bps peak after lap = %v, want preserved 10", v)
	}
	if v, _ := peak("players"); v != 5 {
		t.Errorf("players peak after lap = %v, want preserved 5", v)
	}
}
