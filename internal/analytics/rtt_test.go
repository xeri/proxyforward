package analytics

import (
	"testing"
	"time"

	"proxyforward/internal/conntrack"
)

func TestRecordRTTAggregates(t *testing.T) {
	d := openTest(t, t.TempDir())
	rec := d.NewRecorder(nil)

	reg := conntrack.NewRegistry()
	// onRTT → recorder, mirroring the engine wiring.
	reg.SetHooks(
		func(e *conntrack.Entry) { rec.SessionOpened(e) },
		func(e *conntrack.Entry, in, out int64) { rec.SessionClosed(e, in, out) },
		nil,
		func(e *conntrack.Entry) { rec.RecordRTT(e, e.RTT()) },
	)
	e, closeEntry := reg.Open("", "t1", "mc", "203.0.113.9:5555", "", true)
	d.Barrier()

	// Three samples within one minute: avg 20, min 10, max 30.
	for _, ms := range []float64{10, 20, 30} {
		e.SetRTT(ms)
	}
	closeEntry() // flushes the pending bucket + final aggregate
	d.Barrier()

	var id int64
	if err := d.sql.QueryRow(`SELECT id FROM sessions WHERE tunnel_id = 't1'`).Scan(&id); err != nil {
		t.Fatalf("session id: %v", err)
	}

	// session_rtt should hold one minute row for this session.
	var t0 int64
	var avg, mn, mx float64
	var n int
	if err := d.sql.QueryRow(`SELECT t, avg, mn, mx, n FROM session_rtt WHERE session_id = ?`, id).
		Scan(&t0, &avg, &mn, &mx, &n); err != nil {
		t.Fatalf("session_rtt: %v", err)
	}
	if avg != 20 || mn != 10 || mx != 30 || n != 3 {
		t.Fatalf("bucket = avg %v min %v max %v n %d, want 20/10/30/3", avg, mn, mx, n)
	}
	if t0%60_000 != 0 {
		t.Fatalf("bucket t %d is not minute-aligned", t0)
	}

	// The session's running aggregate mirrors the same values.
	var sAvg, sMin, sMax float64
	var sN int
	if err := d.sql.QueryRow(`SELECT rtt_avg, rtt_min, rtt_max, rtt_n FROM sessions WHERE id = ?`, id).
		Scan(&sAvg, &sMin, &sMax, &sN); err != nil {
		t.Fatalf("sessions.rtt_*: %v", err)
	}
	if sAvg != 20 || sMin != 10 || sMax != 30 || sN != 3 {
		t.Fatalf("session aggregate = %v/%v/%v/%d, want 20/10/30/3", sAvg, sMin, sMax, sN)
	}
}

func TestRecordRTTIgnoresUnknown(t *testing.T) {
	d := openTest(t, t.TempDir())
	rec := d.NewRecorder(nil)
	reg := conntrack.NewRegistry()
	reg.SetHooks(
		func(e *conntrack.Entry) { rec.SessionOpened(e) },
		nil, nil,
		func(e *conntrack.Entry) { rec.RecordRTT(e, e.RTT()) },
	)
	e, _ := reg.Open("", "t1", "mc", "203.0.113.9:5555", "", true)
	d.Barrier()
	e.SetRTT(-1) // unknown — must not record
	d.Barrier()

	var n int
	d.sql.QueryRow(`SELECT COUNT(*) FROM session_rtt`).Scan(&n)
	if n != 0 {
		t.Fatalf("session_rtt rows = %d, want 0 for unknown RTT", n)
	}
}

func TestPlayerLatency(t *testing.T) {
	d := openTest(t, t.TempDir())
	base := time.Now().UnixMilli()
	minute := base - base%60_000
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.sql.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, player_uuid) VALUES
		(1, 't1', '203.0.113.9', ?, ?), (2, 't1', '203.0.113.9', ?, ?)`,
		minute-120_000, steveUUID, minute-120_000, steveUUID)
	// Two sessions, same minute: session 1 avg 20 (n=2), session 2 avg 40 (n=2).
	// Weighted average = (20*2 + 40*2)/4 = 30; min 10, max 50.
	exec(`INSERT INTO session_rtt (session_id, t, avg, mn, mx, n) VALUES
		(1, ?, 20, 10, 30, 2), (2, ?, 40, 25, 50, 2)`, minute, minute)

	pts, err := d.PlayerLatency(steveUUID, 0, base)
	if err != nil {
		t.Fatalf("PlayerLatency: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("points = %d, want 1 (%+v)", len(pts), pts)
	}
	p := pts[0]
	if p.Avg != 30 || p.Min != 10 || p.Max != 50 {
		t.Fatalf("bucket = avg %v min %v max %v, want 30/10/50", p.Avg, p.Min, p.Max)
	}
}
