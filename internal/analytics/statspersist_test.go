package analytics

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"proxyforward/internal/stats"
)

// base matches the stats package's test anchor: aligned to every tier
// resolution.
const base = int64(1_700_006_400_000)

// TestStatsRoundTripThroughSQLite drives a real stats.Store through the real
// SQLite persister twice, exercising restore, cascade resume, and the dirty
// watermark against actual SQL semantics. Samples use recent wall-clock
// times because Store.History windows off time.Now().
func TestStatsRoundTripThroughSQLite(t *testing.T) {
	dir := t.TempDir()
	d := openTest(t, dir)

	s := stats.Open(d, nil)
	// Ten minutes of 1 s samples ending two minutes ago.
	mid := time.Now().Add(-2 * time.Minute).UnixMilli()
	start := mid - 600_000
	var in, out int64
	for ts := start; ts <= mid; ts += 1000 {
		s.Sample(time.UnixMilli(ts), in, out, 0, 0, 3, -1, 25, -1)
		in += 50_000
		out += 500_000
	}
	s.ConnOpened("203.0.113.44:52311")
	s.ConnClosed("203.0.113.44:52311", 1234, 56789)
	s.LinkSessionStarted()
	before := s.History(3_600_000, 240)
	if len(before.Buckets) == 0 {
		t.Fatal("no buckets sampled")
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Restore into a second store from the same database.
	s2 := stats.Open(d, nil)
	after := s2.History(3_600_000, 240)
	if len(after.Buckets) != len(before.Buckets) {
		t.Fatalf("restored %d buckets, want %d", len(after.Buckets), len(before.Buckets))
	}
	for i := range after.Buckets {
		a, b := after.Buckets[i], before.Buckets[i]
		if a.T != b.T || a.In != b.In || a.Out != b.Out || a.OutC != b.OutC || a.ConnC != b.ConnC || a.RttC != b.RttC {
			t.Fatalf("bucket %d mismatch after SQLite round trip:\n  got  %+v\n  want %+v", i, a, b)
		}
	}
	life := s2.Lifetime()
	if life.LinkSessions != 1 || life.BytesOut == 0 {
		t.Fatalf("lifetime not restored: %+v", life)
	}
	peers := s2.Peers()
	if len(peers) != 1 || peers[0].IP != "203.0.113.44" || peers[0].TotalBytesOut != 56789 {
		t.Fatalf("peers not restored: %+v", peers)
	}

	// Continue sampling up to now on the restored store and flush again: the
	// incremental (watermarked) save must still produce a complete database.
	for ts := mid + 1000; ts <= mid+120_000; ts += 1000 {
		s2.Sample(time.UnixMilli(ts), in, out, 0, 0, 3, -1, 25, -1)
		in += 50_000
		out += 500_000
	}
	if err := s2.Flush(); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	live := s2.History(3_600_000, 240)
	s3 := stats.Open(d, nil)
	final := s3.History(3_600_000, 240)
	if len(final.Buckets) != len(live.Buckets) {
		t.Fatalf("incremental save lost buckets: restored %d, live had %d", len(final.Buckets), len(live.Buckets))
	}
}

// TestAgentHistoryRoundTripThroughSQLite: two agents' bandwidth histories
// persist and restore with their agent_id, distinct from each other and from
// the gateway-wide (”) series.
func TestAgentHistoryRoundTripThroughSQLite(t *testing.T) {
	dir := t.TempDir()
	d := openTest(t, dir)
	s := stats.Open(d, nil)

	mid := time.Now().Add(-2 * time.Minute).UnixMilli()
	start := mid - 600_000
	var gIn, gOut, aIn, aOut, bIn, bOut int64
	for ts := start; ts <= mid; ts += 1000 {
		now := time.UnixMilli(ts)
		s.Sample(now, gIn, gOut, 0, 0, 3, -1, 25, -1) // gateway-wide series
		s.SampleAgent("agentA", now, aIn, aOut, 2, -1, 25, -1)
		s.SampleAgent("agentB", now, bIn, bOut, 1, -1, 40, -1)
		gIn += 60_000
		gOut += 600_000
		aIn += 40_000 // agent A carries twice agent B's rate
		aOut += 400_000
		bIn += 20_000
		bOut += 200_000
	}
	beforeA := s.AgentHistory("agentA", 3_600_000, 240)
	beforeB := s.AgentHistory("agentB", 3_600_000, 240)
	if len(beforeA.Buckets) == 0 || len(beforeB.Buckets) == 0 {
		t.Fatal("no per-agent buckets sampled")
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Restore into a second store from the same database.
	s2 := stats.Open(d, nil)
	afterA := s2.AgentHistory("agentA", 3_600_000, 240)
	afterB := s2.AgentHistory("agentB", 3_600_000, 240)
	if len(afterA.Buckets) != len(beforeA.Buckets) {
		t.Fatalf("agentA restored %d buckets, want %d", len(afterA.Buckets), len(beforeA.Buckets))
	}
	var sumA, sumB int64
	for _, bk := range afterA.Buckets {
		sumA += bk.In
	}
	for _, bk := range afterB.Buckets {
		sumB += bk.In
	}
	if sumA == 0 || sumB == 0 || sumA <= sumB {
		t.Fatalf("per-agent bytes commingled or wrong: A=%d B=%d (want A>B>0)", sumA, sumB)
	}

	// The rrd table carries at least three distinct series: '', agentA, agentB.
	var nSeries int
	if err := d.read.QueryRow(`SELECT COUNT(DISTINCT agent_id) FROM rrd`).Scan(&nSeries); err != nil {
		t.Fatalf("count rrd series: %v", err)
	}
	if nSeries < 3 {
		t.Fatalf("rrd has %d distinct agent_id series, want ≥ 3", nSeries)
	}
}

func legacyV2JSON() string {
	return `{"v":2,"lifetime":{"bytesIn":1000,"bytesOut":10000,"linkSessions":4,"firstRunMs":1},` +
		`"peers":[{"ip":"198.51.100.7","firstSeen":5,"lastSeen":9,"totalBytesIn":11,"totalBytesOut":22,"totalConns":3}],` +
		`"tiers":{"t2":[[` + strconv.FormatInt(base, 10) + `,1000,10000,10,20,5,15,100,200,50,150,4,6,3,5]]}}`
}

func TestImportLegacyStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")
	if err := os.WriteFile(path, []byte(legacyV2JSON()), 0o600); err != nil {
		t.Fatal(err)
	}
	d := openTest(t, dir)
	d.ImportLegacyStats(dir)

	if _, err := os.Stat(path + ".imported"); err != nil {
		t.Fatalf("imported file not renamed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("original stats.json still present after import")
	}

	// Assert on the stored snapshot directly: the fixture's bucket is years
	// old, so time-windowed history views would exclude it.
	snap, err := d.LoadStats()
	if err != nil || snap == nil {
		t.Fatalf("imported snapshot unreadable: %v", err)
	}
	if snap.Lifetime.BytesIn != 1000 || snap.Lifetime.BytesOut != 10_000 || snap.Lifetime.LinkSessions != 4 {
		t.Fatalf("imported lifetime wrong: %+v", snap.Lifetime)
	}
	if len(snap.Peers) != 1 || snap.Peers[0].IP != "198.51.100.7" || snap.Peers[0].TotalConns != 3 {
		t.Fatalf("imported peers wrong: %+v", snap.Peers)
	}
	if len(snap.Tiers) != 1 || snap.Tiers[0].Tier != 2 || len(snap.Tiers[0].Buckets) != 1 {
		t.Fatalf("imported tiers wrong: %+v", snap.Tiers)
	}
	if b := snap.Tiers[0].Buckets[0]; b.T != base || b.ConnC != 5 || b.RttC != -1 || b.PlayersC != -1 || b.LossC != -1 {
		t.Fatalf("imported gauge sentinels wrong: %+v", b)
	}
}

func TestImportSkipsWhenDataExists(t *testing.T) {
	dir := t.TempDir()
	d := openTest(t, dir)

	// The database already holds a snapshot (e.g. this machine migrated long
	// ago and someone restored an ancient stats.json backup).
	s := stats.Open(d, nil)
	s.LinkSessionStarted()
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "stats.json")
	if err := os.WriteFile(path, []byte(legacyV2JSON()), 0o600); err != nil {
		t.Fatal(err)
	}
	d.ImportLegacyStats(dir)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stats.json should be untouched when the db has data: %v", err)
	}
	s2 := stats.Open(d, nil)
	if life := s2.Lifetime(); life.BytesIn == 1000 {
		t.Fatal("legacy data imported over existing database")
	}
}

func TestImportCorruptRenamesBad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := openTest(t, dir)
	d.ImportLegacyStats(dir)
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("corrupt stats.json not renamed aside: %v", err)
	}
	if snap, err := d.LoadStats(); err != nil || snap != nil {
		t.Fatalf("corrupt import should leave db empty (snap=%v, err=%v)", snap, err)
	}
}
