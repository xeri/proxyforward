package analytics

import (
	"fmt"
	"testing"
	"time"
)

const (
	steveUUID = "069a79f4-44e9-4726-a5be-fca90e38aaf5"
	alexUUID  = "853c80ef-3c37-49fd-aa49-938b674adae6"
	minuteMs  = int64(60_000)
	hourMs    = 60 * minuteMs
)

// qBase must stay inside the retention window: the writer goroutine prunes at
// startup, and it may run after seeding — rows dated older than retention
// would be swept mid-test.
var qBase = time.Now().UnixMilli()

// seedQueryFixture loads a small deterministic dataset:
//
//	Steve      2 sessions on t1 (one ended 30 min, one live 10 min), rtt 25
//	Alexandra  1 ended session on t2 (1 h), rtt 50
//	Zed        offline player, no sessions
//	plus one anonymous (no-player) session on t1.
func seedQueryFixture(t *testing.T, d *DB) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.sql.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	exec(`INSERT INTO players (uuid, name, offline, first_seen, last_seen, last_cc) VALUES
		(?, 'Steve', 0, ?, ?, 'NZ'),
		(?, 'Alexandra', 0, ?, ?, 'DE'),
		('offline:zed', 'Zed', 1, ?, ?, '')`,
		steveUUID, qBase-100*hourMs, qBase-1_000,
		alexUUID, qBase-200*hourMs, qBase-5_000,
		qBase-3*hourMs, qBase-2_000)
	exec(`INSERT INTO sessions (id, tunnel_id, tunnel_name, client_ip, started_ms, ended_ms, bytes_in, bytes_out, player_uuid, player_name, cc, rtt_avg, rtt_n) VALUES
		(1, 't1', 'mc', '203.0.113.9',  ?, ?,    1000, 2000, ?, 'Steve', 'NZ', 25, 4),
		(2, 't1', 'mc', '203.0.113.9',  ?, NULL, 500,  500,  ?, 'Steve', 'NZ', 0, NULL),
		(3, 't2', 'web', '198.51.100.7', ?, ?,   10,   20,   ?, 'Alexandra', 'DE', 50, 2),
		(4, 't1', 'mc', '192.0.2.55',   ?, ?,    7,    9,    NULL, NULL, NULL, NULL, NULL)`,
		qBase-hourMs, qBase-30*minuteMs, steveUUID,
		qBase-10*minuteMs, steveUUID,
		qBase-2*hourMs, qBase-hourMs, alexUUID,
		qBase-100_000, qBase-50_000)
	exec(`INSERT INTO player_names (uuid, name, first_seen, last_seen) VALUES
		(?, 'Steve', ?, ?), (?, 'SteveOld', ?, ?)`,
		steveUUID, qBase-hourMs, qBase-1_000,
		steveUUID, qBase-100*hourMs, qBase-50*hourMs)
	exec(`INSERT INTO player_ips (uuid, ip, first_seen, last_seen, sessions) VALUES
		(?, '203.0.113.9', ?, ?, 2)`, steveUUID, qBase-hourMs, qBase-1_000)
	exec(`INSERT INTO geo_cache (ip, cc, resolved_ms) VALUES ('203.0.113.9', 'NZ', ?)`, qBase)
}

func TestPlayersQuery(t *testing.T) {
	d := openTest(t, t.TempDir())
	seedQueryFixture(t, d)

	page, err := d.Players(PlayersQuery{}, map[string]bool{steveUUID: true}, qBase)
	if err != nil {
		t.Fatalf("Players: %v", err)
	}
	if page.Total != 3 || len(page.Players) != 3 {
		t.Fatalf("total=%d rows=%d, want 3/3", page.Total, len(page.Players))
	}
	// Default sort is most recently seen first.
	if got := names(page); got != "Steve,Zed,Alexandra" {
		t.Fatalf("recent order = %s", got)
	}
	steve := page.Players[0]
	if !steve.Online || steve.Offline || steve.LastCC != "NZ" {
		t.Fatalf("steve flags = %+v", steve)
	}
	// Aggregates: 2 sessions, 30 min ended + 10 min live, byte sums, and the
	// sample-less live session excluded from the sample-weighted average.
	if steve.Sessions != 2 || steve.PlayMs != 40*minuteMs {
		t.Fatalf("steve sessions=%d playMs=%d, want 2/%d", steve.Sessions, steve.PlayMs, 40*minuteMs)
	}
	if steve.BytesIn != 1500 || steve.BytesOut != 2500 || steve.RttMs != 25 {
		t.Fatalf("steve totals = %+v", steve)
	}
	zed := page.Players[1]
	if !zed.Offline || zed.Sessions != 0 || zed.PlayMs != 0 {
		t.Fatalf("zed card = %+v", zed)
	}

	// Sorts.
	for _, tc := range []struct{ sort, want string }{
		{"name", "Alexandra,Steve,Zed"},
		{"playtime", "Alexandra,Steve,Zed"}, // 60 min > 40 min > none
		{"sessions", "Steve,Alexandra,Zed"},
		{"data", "Steve,Alexandra,Zed"}, // 4000 B > 30 B > none
	} {
		p, err := d.Players(PlayersQuery{Sort: tc.sort}, nil, qBase)
		if err != nil || names(p) != tc.want {
			t.Fatalf("sort %s = %s (err=%v), want %s", tc.sort, names(p), err, tc.want)
		}
	}

	// Search and tunnel filters.
	if p, _ := d.Players(PlayersQuery{Search: "ste"}, nil, qBase); names(p) != "Steve" || p.Total != 1 {
		t.Fatalf("search = %s total=%d", names(p), p.Total)
	}
	if p, _ := d.Players(PlayersQuery{TunnelID: "t2"}, nil, qBase); names(p) != "Alexandra" {
		t.Fatalf("tunnel filter = %s", names(p))
	}

	// Paging: the count reflects the filter, not the page.
	p1, _ := d.Players(PlayersQuery{Limit: 2}, nil, qBase)
	p2, _ := d.Players(PlayersQuery{Limit: 2, Offset: 2}, nil, qBase)
	if p1.Total != 3 || len(p1.Players) != 2 || len(p2.Players) != 1 {
		t.Fatalf("paging: total=%d page1=%d page2=%d", p1.Total, len(p1.Players), len(p2.Players))
	}
}

// TestPlayersTunnelScopeAndOnlineFirst covers the tunnel-scoped aggregate
// join (a filtered wall shows tunnel-scoped figures, not global), the
// sample-weighted RTT aggregate, and online-first ordering via the name key.
func TestPlayersTunnelScopeAndOnlineFirst(t *testing.T) {
	d := openTest(t, t.TempDir())
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.sql.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO players (uuid, name, first_seen, last_seen) VALUES
		('u1', 'Anna', ?, ?), ('u2', 'Bert', ?, ?)`,
		qBase-hourMs, qBase, qBase-hourMs, qBase-1000)
	// Anna: two lean sessions on tunnel A, one fat one on tunnel B.
	exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, ended_ms, bytes_in, bytes_out, player_uuid, rtt_avg, rtt_n) VALUES
		(1, 'A', 'ip', ?, ?, 100, 100, 'u1', 20, 2),
		(2, 'A', 'ip', ?, ?, 100, 100, 'u1', 40, 6),
		(3, 'B', 'ip', ?, ?, 9000, 9000, 'u1', 100, 100),
		(4, 'A', 'ip', ?, ?, 50, 50, 'u2', NULL, NULL)`,
		qBase-hourMs, qBase-30*minuteMs,
		qBase-hourMs, qBase-30*minuteMs,
		qBase-hourMs, qBase-30*minuteMs,
		qBase-hourMs, qBase-30*minuteMs)

	page, err := d.Players(PlayersQuery{TunnelID: "A"}, nil, qBase)
	if err != nil {
		t.Fatalf("Players: %v", err)
	}
	byName := map[string]PlayerCard{}
	for _, c := range page.Players {
		byName[c.Name] = c
	}
	anna := byName["Anna"]
	// Tunnel-scoped: 2 sessions, 200 bytes, weighted RTT (20·2+40·6)/8 = 35.
	if anna.Sessions != 2 || anna.BytesIn != 200 || !approx(anna.RttMs, 35) {
		t.Errorf("anna on A = sessions %d bytes %d rtt %v, want 2/200/35", anna.Sessions, anna.BytesIn, anna.RttMs)
	}
	if bert := byName["Bert"]; bert.Sessions != 1 || bert.RttMs != -1 {
		t.Errorf("bert on A = sessions %d rtt %v, want 1/-1 (no samples)", bert.Sessions, bert.RttMs)
	}
	// Unfiltered stays global.
	global, _ := d.Players(PlayersQuery{}, nil, qBase)
	for _, c := range global.Players {
		if c.Name == "Anna" && (c.Sessions != 3 || c.BytesIn != 9200) {
			t.Errorf("anna global = sessions %d bytes %d, want 3/9200", c.Sessions, c.BytesIn)
		}
	}

	// Online-first beats the chosen sort, keyed by name for UUID-less conns.
	sorted, err := d.Players(PlayersQuery{Sort: "name"}, map[string]bool{"name:bert": true}, qBase)
	if err != nil {
		t.Fatalf("Players online-first: %v", err)
	}
	if got := names(sorted); got != "Bert,Anna" {
		t.Errorf("online-first order = %s, want Bert,Anna", got)
	}
	if !sorted.Players[0].Online {
		t.Error("bert not flagged online via name key")
	}
}

// TestCountryFilters covers the cc filter on both list queries.
func TestCountryFilters(t *testing.T) {
	d := openTest(t, t.TempDir())
	seedQueryFixture(t, d)

	if p, err := d.Sessions(SessionsQuery{CC: "NZ"}, qBase); err != nil || p.Total != 2 {
		t.Fatalf("sessions cc=NZ total = %d (err=%v), want 2", p.Total, err)
	}
	if p, err := d.Players(PlayersQuery{CC: "DE"}, nil, qBase); err != nil || names(p) != "Alexandra" {
		t.Fatalf("players cc=DE = %s (err=%v), want Alexandra", names(p), err)
	}
	// Combined tunnel+cc must hold on the same session: Alexandra's DE
	// session is on t2, so t1+DE matches nobody.
	if p, err := d.Players(PlayersQuery{TunnelID: "t1", CC: "DE"}, nil, qBase); err != nil || p.Total != 0 {
		t.Fatalf("players t1+DE total = %d (err=%v), want 0", p.Total, err)
	}
}

func names(p PlayersPage) string {
	s := ""
	for i, c := range p.Players {
		if i > 0 {
			s += ","
		}
		s += c.Name
	}
	return s
}

func TestPlayerDetailQuery(t *testing.T) {
	d := openTest(t, t.TempDir())
	seedQueryFixture(t, d)

	det, err := d.PlayerDetail(steveUUID, nil, qBase)
	if err != nil || det == nil {
		t.Fatalf("PlayerDetail: det=%v err=%v", det, err)
	}
	if det.Card.Name != "Steve" || det.Card.Sessions != 2 {
		t.Fatalf("card = %+v", det.Card)
	}
	if len(det.Names) != 2 || det.Names[0].Name != "Steve" || det.Names[1].Name != "SteveOld" {
		t.Fatalf("names = %+v", det.Names)
	}
	if len(det.IPs) != 1 || det.IPs[0].CC != "NZ" || det.IPs[0].Sessions != 2 {
		t.Fatalf("ips = %+v", det.IPs)
	}
	if len(det.Recent) != 2 || det.Recent[0].ID != 2 || det.Recent[1].ID != 1 {
		t.Fatalf("recent = %+v", det.Recent)
	}
	if det.Recent[0].EndedMs != 0 { // live session renders as 0
		t.Fatalf("live session EndedMs = %d", det.Recent[0].EndedMs)
	}

	unknown, err := d.PlayerDetail("no-such-uuid", nil, qBase)
	if err != nil || unknown != nil {
		t.Fatalf("unknown player: det=%v err=%v", unknown, err)
	}
}

func TestSessionsQuery(t *testing.T) {
	d := openTest(t, t.TempDir())
	seedQueryFixture(t, d)

	all, err := d.Sessions(SessionsQuery{}, qBase)
	if err != nil || all.Total != 4 {
		t.Fatalf("all sessions total=%d err=%v", all.Total, err)
	}
	// Newest first: 4 (100 s ago), 2 (10 min), 1 (1 h), 3 (2 h).
	for i, want := range []int64{4, 2, 1, 3} {
		if all.Sessions[i].ID != want {
			t.Fatalf("order[%d] = %d, want %d", i, all.Sessions[i].ID, want)
		}
	}
	if all.Sessions[0].PlayerName != "" || all.Sessions[1].PlayerName != "Steve" {
		t.Fatalf("player names = %+v", all.Sessions[:2])
	}

	if p, _ := d.Sessions(SessionsQuery{PlayerUUID: alexUUID}, qBase); p.Total != 1 || p.Sessions[0].ID != 3 {
		t.Fatalf("player filter = %+v", p)
	}
	if p, _ := d.Sessions(SessionsQuery{TunnelID: "t1"}, qBase); p.Total != 3 {
		t.Fatalf("tunnel filter total = %d, want 3", p.Total)
	}
	if p, _ := d.Sessions(SessionsQuery{SinceMs: qBase - 11*minuteMs}, qBase); p.Total != 2 {
		t.Fatalf("since filter total = %d, want 2", p.Total)
	}
	// Paging keeps the filtered total.
	p, _ := d.Sessions(SessionsQuery{TunnelID: "t1", Limit: 1, Offset: 1}, qBase)
	if p.Total != 3 || len(p.Sessions) != 1 || p.Sessions[0].ID != 2 {
		t.Fatalf("paged = %+v", p)
	}
}

func TestPlayerHistoryBuckets(t *testing.T) {
	d := openTest(t, t.TempDir())
	seedQueryFixture(t, d)

	// 480 samples at 15 s cadence over the 2 h before qBase on Steve's first
	// session, 1 KiB each way — more raw points than the cap allows.
	tx, err := d.sql.Begin()
	if err != nil {
		t.Fatal(err)
	}
	const n = 480
	start := qBase - 2*hourMs
	for i := range n {
		if _, err := tx.Exec(`INSERT INTO session_traffic (session_id, t, inb, outb) VALUES (1, ?, 1024, 1024)`,
			start+int64(i)*15_000); err != nil {
			t.Fatal(err)
		}
	}
	// A sample on Alexandra's session must not leak into Steve's series.
	if _, err := tx.Exec(`INSERT INTO session_traffic (session_id, t, inb, outb) VALUES (3, ?, 9999, 9999)`,
		qBase-hourMs); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	pts, err := d.PlayerHistory(steveUUID, 2*hourMs, qBase)
	if err != nil {
		t.Fatalf("PlayerHistory: %v", err)
	}
	if len(pts) == 0 || len(pts) > MaxHistoryPoints {
		t.Fatalf("points = %d, want 1..%d", len(pts), MaxHistoryPoints)
	}
	var in, out int64
	for _, p := range pts {
		in += p.In
		out += p.Out
	}
	if in != n*1024 || out != n*1024 {
		t.Fatalf("bucketed sums = %d/%d, want %d each (no loss, no leakage)", in, out, n*1024)
	}

	// A window that excludes everything yields an empty (non-nil) series.
	empty, err := d.PlayerHistory(steveUUID, minuteMs, qBase+3*hourMs)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty window = %v (err=%v)", empty, err)
	}

	fixed := bucketTraffic([]TrafficPoint{{T: 0, In: 1}, {T: 14_999, In: 2}, {T: 15_000, In: 4}}, 30_000, 30_000)
	if len(fixed) != 2 || fixed[0].In != 3 || fixed[1].In != 4 {
		t.Fatalf("bucketTraffic = %+v", fixed)
	}
}

func TestPlayersPageClamp(t *testing.T) {
	d := openTest(t, t.TempDir())
	tx, err := d.sql.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range MaxPlayersPage + 20 {
		if _, err := tx.Exec(`INSERT INTO players (uuid, name, first_seen, last_seen) VALUES (?, ?, ?, ?)`,
			fmt.Sprintf("uuid-%03d", i), fmt.Sprintf("p%03d", i), qBase, qBase); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := d.Players(PlayersQuery{Limit: 10_000}, nil, qBase)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != MaxPlayersPage+20 || len(page.Players) != MaxPlayersPage {
		t.Fatalf("clamp: total=%d rows=%d, want %d/%d", page.Total, len(page.Players), MaxPlayersPage+20, MaxPlayersPage)
	}
}

func TestSessionTimeline(t *testing.T) {
	d := openTest(t, t.TempDir())
	seedQueryFixture(t, d)
	base := qBase - hourMs
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.sql.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// Session 1: three 15 s traffic samples and two per-minute RTT rows.
	exec(`INSERT INTO session_traffic (session_id, t, inb, outb) VALUES
		(1, ?, 100, 900), (1, ?, 200, 1800), (1, ?, 50, 400)`,
		base, base+15_000, base+30_000)
	exec(`INSERT INTO session_rtt (session_id, t, avg, mn, mx, n) VALUES
		(1, ?, 25, 20, 30, 6), (1, ?, 35, 28, 50, 6)`,
		base, base+60_000)

	tl, err := d.SessionTimeline(1, qBase)
	if err != nil {
		t.Fatalf("SessionTimeline: %v", err)
	}
	if len(tl.Traffic) != 3 {
		t.Fatalf("traffic points = %d, want 3", len(tl.Traffic))
	}
	var in, out int64
	for _, p := range tl.Traffic {
		in += p.In
		out += p.Out
	}
	if in != 350 || out != 3100 {
		t.Fatalf("traffic sum in=%d out=%d, want 350/3100", in, out)
	}
	if len(tl.Rtt) != 2 {
		t.Fatalf("rtt points = %d, want 2", len(tl.Rtt))
	}
	if tl.Rtt[0].Avg != 25 || tl.Rtt[1].Max != 50 {
		t.Fatalf("rtt = %+v", tl.Rtt)
	}

	// A session with no samples returns empty (non-nil) slices.
	empty, err := d.SessionTimeline(999, qBase)
	if err != nil {
		t.Fatalf("SessionTimeline empty: %v", err)
	}
	if empty.Traffic == nil || empty.Rtt == nil || len(empty.Traffic) != 0 || len(empty.Rtt) != 0 {
		t.Fatalf("empty timeline = %+v", empty)
	}
}
