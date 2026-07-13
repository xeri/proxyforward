package analytics

import (
	"fmt"
	"testing"
	"time"
)

func TestSummary(t *testing.T) {
	d := openTest(t, t.TempDir())
	base, now := rollupBase()
	hA := base - 2*hourMillis
	hB := base - hourMillis

	insertBucket(t, d, 2, hA, 100, 200, 10, 20, 6, 30, 5, 1)
	insertBucket(t, d, 2, hB, 50, 60, 4, 6, 2, 40, 3, 3)
	mk := func(id, tstart int64, uuid any) {
		if _, err := d.sql.Exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, player_uuid)
			VALUES (?, 't1', '203.0.113.9', ?, ?)`, id, tstart, uuid); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mk(1, hA+1000, steveUUID)
	mk(2, hA+2000, alexUUID)
	mk(3, hB+1000, steveUUID)

	// Link up the whole window, engine covering it.
	d.recordEventAt(hA-minuteMs, EventEngine, "", true)
	d.recordEventAt(hA-minuteMs, EventLink, "", true)
	d.Barrier()

	d.runRollup(time.UnixMilli(now))

	s, err := d.Summary(base-3*hourMillis, now)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if s.BytesIn != 150 || s.BytesOut != 260 {
		t.Errorf("bytes = %d/%d, want 150/260", s.BytesIn, s.BytesOut)
	}
	if s.Sessions != 3 || s.UniquePlayers != 2 {
		t.Errorf("sessions/unique = %d/%d, want 3/2", s.Sessions, s.UniquePlayers)
	}
	if s.PeakInBps != 10 || s.PeakOutBps != 20 {
		t.Errorf("peak bps = %v/%v, want 10/20", s.PeakInBps, s.PeakOutBps)
	}
	if s.PeakPlayers != 5 || s.PeakPlayersAt != hA {
		t.Errorf("peak players = %v@%d, want 5@%d", s.PeakPlayers, s.PeakPlayersAt, hA)
	}
	if !approx(s.AvgRttMs, 35) || !approx(s.AvgLossPct, 2) {
		t.Errorf("avg rtt/loss = %v/%v, want 35/2", s.AvgRttMs, s.AvgLossPct)
	}
	if !approx(s.LinkUptimePct, 100) {
		t.Errorf("link uptime = %v, want 100", s.LinkUptimePct)
	}
	// All-time records populated from the peaks table.
	if s.RecInBps != 10 || s.RecPlayers != 5 {
		t.Errorf("records in/players = %v/%v, want 10/5", s.RecInBps, s.RecPlayers)
	}
}

func TestSummaryEmpty(t *testing.T) {
	d := openTest(t, t.TempDir())
	_, now := rollupBase()
	s, err := d.Summary(0, now)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if s.Sessions != 0 || s.BytesIn != 0 {
		t.Errorf("empty summary not zero: %+v", s)
	}
	if s.PeakPlayers != -1 || s.AvgRttMs != -1 || s.LinkUptimePct != -1 {
		t.Errorf("empty gauges should be -1: players=%v rtt=%v uptime=%v", s.PeakPlayers, s.AvgRttMs, s.LinkUptimePct)
	}
}

func TestTunnelUptime(t *testing.T) {
	d := openTest(t, t.TempDir())
	_, now := rollupBase()
	start := now - 4*hourMillis

	// Engine up for the whole window.
	d.recordEventAt(start, EventEngine, "", true)
	// Link: up at start, one 1-hour outage in the middle → ~75%.
	d.recordEventAt(start, EventLink, "", true)
	d.recordEventAt(start+hourMillis, EventLink, "", false)
	d.recordEventAt(start+2*hourMillis, EventLink, "", true)
	// Tunnel t1: up the whole window → 100%.
	d.recordEventAt(start, EventTunnelLocal, "t1", true)
	d.Barrier()

	rep, err := d.TunnelUptime(start, now)
	if err != nil {
		t.Fatalf("TunnelUptime: %v", err)
	}
	if !approx(rep.Link.UptimePct, 75) {
		t.Errorf("link uptime = %v, want 75", rep.Link.UptimePct)
	}
	if len(rep.Link.Events) != 3 {
		t.Errorf("link events = %d, want 3", len(rep.Link.Events))
	}
	if len(rep.Tunnels) != 1 || rep.Tunnels[0].TunnelID != "t1" {
		t.Fatalf("tunnels = %+v, want one t1", rep.Tunnels)
	}
	if !approx(rep.Tunnels[0].UptimePct, 100) {
		t.Errorf("t1 uptime = %v, want 100", rep.Tunnels[0].UptimePct)
	}
}

// TestTunnelUptimeClamped: with more tunnels than MaxUptimeTunnels, the
// report keeps the most recently active ones so the reply stays inside the
// IPC frame.
func TestTunnelUptimeClamped(t *testing.T) {
	d := openTest(t, t.TempDir())
	_, now := rollupBase()
	start := now - 40*hourMillis
	// 30 tunnels, each with a distinct last-activity hour; t29 is the most
	// recent, t0 the stalest.
	for i := range 30 {
		d.recordEventAt(start+int64(i)*hourMillis, EventTunnelLocal, fmt.Sprintf("t%d", i), true)
	}
	d.Barrier()

	rep, err := d.TunnelUptime(start, now)
	if err != nil {
		t.Fatalf("TunnelUptime: %v", err)
	}
	if len(rep.Tunnels) != MaxUptimeTunnels {
		t.Fatalf("tunnels = %d, want clamped %d", len(rep.Tunnels), MaxUptimeTunnels)
	}
	got := map[string]bool{}
	for _, tu := range rep.Tunnels {
		got[tu.TunnelID] = true
	}
	if !got["t29"] || got["t0"] {
		t.Fatalf("clamp kept the wrong tunnels: has t29=%v t0=%v", got["t29"], got["t0"])
	}
}

// TestSummaryRangeRouting seeds hourly and daily rollups with deliberately
// different figures: short ranges must read hourly, ranges past ~a week (and
// all-time) must read daily, and session counts must share the bucket-floored
// left edge.
func TestSummaryRangeRouting(t *testing.T) {
	d := openTest(t, t.TempDir())
	// The writer's startup rollup re-derives daily rows from hourly ones; a
	// barrier orders it before the divergent seeds below.
	d.Barrier()
	_, now := rollupBase()

	hour := (now - 2*hourMillis) / hourMillis * hourMillis
	day := now / dayMillis * dayMillis
	if _, err := d.sql.Exec(`INSERT INTO rollup_hourly (hour_ms, bytes_in, bytes_out) VALUES (?, 100, 10)`, hour); err != nil {
		t.Fatal(err)
	}
	if _, err := d.sql.Exec(`INSERT INTO rollup_daily (day_ms, bytes_in, bytes_out) VALUES (?, 999, 99)`, day); err != nil {
		t.Fatal(err)
	}

	short, err := d.Summary(now-24*hourMillis, now)
	if err != nil {
		t.Fatalf("24h summary: %v", err)
	}
	if short.BytesIn != 100 || short.BytesOut != 10 {
		t.Errorf("24h bytes = %d/%d, want hourly 100/10", short.BytesIn, short.BytesOut)
	}
	long, err := d.Summary(now-30*dayMillis, now)
	if err != nil {
		t.Fatalf("30d summary: %v", err)
	}
	if long.BytesIn != 999 || long.BytesOut != 99 {
		t.Errorf("30d bytes = %d/%d, want daily 999/99", long.BytesIn, long.BytesOut)
	}
	all, err := d.Summary(0, now)
	if err != nil {
		t.Fatalf("all-time summary: %v", err)
	}
	if all.BytesIn != 999 {
		t.Errorf("all-time bytes = %d, want daily 999", all.BytesIn)
	}

	// Boundary sessions: one at exactly the floored day bucket counts, one a
	// millisecond before it does not.
	sinceMs := now - 30*dayMillis
	bucket := sinceMs / dayMillis * dayMillis
	mk := func(id, tstart int64) {
		if _, err := d.sql.Exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms)
			VALUES (?, 't1', '203.0.113.9', ?)`, id, tstart); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mk(1, bucket)
	mk(2, bucket-1)
	s, err := d.Summary(sinceMs, now)
	if err != nil {
		t.Fatalf("boundary summary: %v", err)
	}
	if s.Sessions != 1 {
		t.Errorf("boundary sessions = %d, want 1 (>= floored bucket only)", s.Sessions)
	}
}

// TestPeakMatrixHalfHourZone pins the midpoint bucketing: in a +05:30 zone,
// the UTC hour starting 03:00 (midpoint 03:30 → 09:00 local) must land in
// local hour 9, not the start-time's hour 8.
func TestPeakMatrixHalfHourZone(t *testing.T) {
	d := openTest(t, t.TempDir())
	_, now := rollupBase()
	loc := time.FixedZone("IST", 5*3600+1800)

	h := now / hourMillis * hourMillis
	for time.UnixMilli(h).UTC().Hour() != 3 {
		h -= hourMillis
	}
	if _, err := d.sql.Exec(`INSERT INTO rollup_hourly (hour_ms, avg_players, peak_players)
		VALUES (?, 4, 7)`, h); err != nil {
		t.Fatalf("seed hourly: %v", err)
	}
	m, err := d.PeakMatrix(4, now, loc)
	if err != nil {
		t.Fatalf("PeakMatrix: %v", err)
	}
	dow := int(time.UnixMilli(h + hourMillis/2).In(loc).Weekday())
	if c := m.Cells[dow][9]; !approx(c.Avg, 4) || !approx(c.Max, 7) {
		t.Errorf("local hour 9 cell = %+v, want avg 4 max 7", c)
	}
	if c := m.Cells[dow][8]; c.Avg != -1 || c.Max != -1 {
		t.Errorf("local hour 8 cell = %+v, want empty (start-time bucketing regression)", c)
	}
}

func TestPeakMatrixWeeksClamp(t *testing.T) {
	d := openTest(t, t.TempDir())
	_, now := rollupBase()
	// A row 13 weeks back must be outside even the maximum clamped window.
	h := now/hourMillis*hourMillis - 13*7*dayMillis
	if _, err := d.sql.Exec(`INSERT INTO rollup_hourly (hour_ms, avg_players, peak_players)
		VALUES (?, 4, 7)`, h); err != nil {
		t.Fatalf("seed hourly: %v", err)
	}
	m, err := d.PeakMatrix(999, now, time.UTC)
	if err != nil {
		t.Fatalf("PeakMatrix: %v", err)
	}
	for i := range m.Cells {
		for j := range m.Cells[i] {
			if m.Cells[i][j].Avg != -1 || m.Cells[i][j].Max != -1 {
				t.Fatalf("cell [%d][%d] = %+v populated — weeks clamp (12) not applied", i, j, m.Cells[i][j])
			}
		}
	}
}

func TestPeakMatrix(t *testing.T) {
	d := openTest(t, t.TempDir())
	_, now := rollupBase()
	// One hourly row with known players at a known local hour.
	h := now/hourMillis*hourMillis - 24*hourMillis
	if _, err := d.sql.Exec(`INSERT INTO rollup_hourly (hour_ms, avg_players, peak_players)
		VALUES (?, 4, 7)`, h); err != nil {
		t.Fatalf("seed hourly: %v", err)
	}
	m, err := d.PeakMatrix(4, now, time.Local)
	if err != nil {
		t.Fatalf("PeakMatrix: %v", err)
	}
	// The bucketing key is the hour's midpoint; on whole-hour zones like
	// Local here it lands in the same local hour as the start.
	lt := time.UnixMilli(h).Local()
	c := m.Cells[int(lt.Weekday())][lt.Hour()]
	if !approx(c.Avg, 4) || !approx(c.Max, 7) {
		t.Errorf("cell = %+v, want avg 4 max 7", c)
	}
	// An untouched cell stays unknown.
	other := m.Cells[(int(lt.Weekday())+3)%7][(lt.Hour()+5)%24]
	if other.Avg != -1 || other.Max != -1 {
		t.Errorf("untouched cell = %+v, want -1/-1", other)
	}
}
