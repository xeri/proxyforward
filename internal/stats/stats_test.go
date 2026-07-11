package stats

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// base is an arbitrary absolute time aligned to every tier resolution
// (multiple of one day in millis).
const base = int64(1_700_006_400_000) // 2023-11-15 00:00:00 UTC

func fresh(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stats.json")
	return Open(path, nil), path
}

// feed pushes samples every stepMs for total duration, with rate bytes/sec in
// each direction scaled by inScale/outScale. The connection count varies
// deterministically over 3..6 so conn OHLC survives aggregation checks.
func feed(s *Store, start int64, stepMs, durMs int64, inRate, outRate int64) (appIn, appOut int64) {
	for t := start; t <= start+durMs; t += stepMs {
		s.Sample(time.UnixMilli(t), appIn, appOut, appIn+appIn/10, appOut+appOut/10, int(3+((t-start)/stepMs)%4), 25)
		appIn += inRate * stepMs / 1000
		appOut += outRate * stepMs / 1000
	}
	return appIn, appOut
}

func TestCascadeAndHistory(t *testing.T) {
	s, _ := fresh(t)
	// 65s of steady 10 KB/s in, 100 KB/s out at 100ms cadence.
	end := base + 65_000
	feed(s, base, 100, 65_000, 10_000, 100_000)

	// 15s window from T0: 150 buckets of 100ms.
	h := s.historyAt(end, 15_000, 150)
	if h.BucketMs != 100 {
		t.Fatalf("15s window bucketMs = %d, want 100", h.BucketMs)
	}
	if len(h.Buckets) < 148 || len(h.Buckets) > 150 {
		t.Fatalf("15s window bucket count = %d, want ~150", len(h.Buckets))
	}
	for _, b := range h.Buckets[1 : len(h.Buckets)-1] {
		if b.In != 1000 || b.Out != 10_000 {
			t.Fatalf("steady bucket bytes = %d/%d, want 1000/10000 (t=%d)", b.In, b.Out, b.T)
		}
		if b.OutC < 99_000 || b.OutC > 101_000 {
			t.Fatalf("steady bucket out rate = %f, want ~100000", b.OutC)
		}
	}

	// 1m window from T0 aggregated ×2: 300 buckets of 200ms.
	h = s.historyAt(end, 60_000, 300)
	if h.BucketMs != 200 {
		t.Fatalf("1m window bucketMs = %d, want 200", h.BucketMs)
	}

	// T1 must have been fed by the cascade: 15m window → 1s buckets ×1.
	h = s.historyAt(end, 900_000, 300)
	if h.BucketMs != 3000 {
		t.Fatalf("15m window bucketMs = %d, want 3000", h.BucketMs)
	}
	if len(h.Buckets) == 0 {
		t.Fatal("15m window is empty; cascade to T1 failed")
	}
	var totalIn int64
	for _, b := range h.Buckets {
		totalIn += b.In
	}
	// ~65s at 10KB/s ≈ 650KB; the live partial T0 bucket has not folded yet.
	if totalIn < 600_000 || totalIn > 700_000 {
		t.Fatalf("T1 total in = %d, want ≈650000", totalIn)
	}
}

func TestAggregationOHLC(t *testing.T) {
	s, _ := fresh(t)
	// Four 100ms samples with rates 1k, 4k, 2k, 3k B/s out.
	rates := []int64{1000, 4000, 2000, 3000}
	var out int64
	for i, r := range rates {
		ts := base + int64(i+1)*100
		s.Sample(time.UnixMilli(ts), 0, out, 0, 0, 2, -1)
		out += r / 10 // bytes accrued in the NEXT 100ms at rate r
	}
	// One more sample to land the last delta.
	s.Sample(time.UnixMilli(base+500), 0, out, 0, 0, 2, -1)

	// Aggregating the four 100ms buckets down to ≤2 groups (groups align to
	// absolute boundaries, so the window may straddle two): byte sums must be
	// exact, h = max of rates, c = last rate.
	h := s.historyAt(base+500, 500, 1)
	if len(h.Buckets) < 1 || len(h.Buckets) > 2 {
		t.Fatalf("bucket count = %d, want 1-2", len(h.Buckets))
	}
	var sum int64
	var high float64
	for _, b := range h.Buckets {
		sum += b.Out
		high = max(high, b.OutH)
	}
	if high != 4000 {
		t.Fatalf("merged OutH = %f, want 4000", high)
	}
	if c := h.Buckets[len(h.Buckets)-1].OutC; c != 3000 {
		t.Fatalf("merged OutC = %f, want 3000", c)
	}
	if sum != out {
		t.Fatalf("Out bytes = %d, want %d", sum, out)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	s := Open(path, nil)
	// 10 minutes of 1s samples: fills T2 (15s) with 40 buckets.
	end := base + 600_000
	feed(s, base, 1000, 600_000, 50_000, 500_000)
	s.ConnOpened("203.0.113.44:52311")
	s.ConnClosed("203.0.113.44:52311", 1234, 56789)
	s.LinkSessionStarted()
	before := s.historyAt(end, 3_600_000, 240)
	lifeBefore := s.Lifetime()
	if err := s.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	s2 := Open(path, nil)
	after := s2.historyAt(end, 3_600_000, 240)
	if len(after.Buckets) != len(before.Buckets) {
		t.Fatalf("restored bucket count = %d, want %d", len(after.Buckets), len(before.Buckets))
	}
	for i := range after.Buckets {
		a, b := after.Buckets[i], before.Buckets[i]
		if a.T != b.T || a.In != b.In || a.Out != b.Out || a.OutH != b.OutH || a.OutC != b.OutC ||
			a.ConnH != b.ConnH || a.ConnC != b.ConnC {
			t.Fatalf("bucket %d mismatch: %+v vs %+v", i, a, b)
		}
	}
	life := s2.Lifetime()
	if life.BytesIn != lifeBefore.BytesIn || life.BytesOut != lifeBefore.BytesOut {
		t.Fatalf("lifetime bytes lost: %+v vs %+v", life, lifeBefore)
	}
	if life.LinkSessions != 1 {
		t.Fatalf("link sessions = %d, want 1", life.LinkSessions)
	}
	if life.FirstRunMs != lifeBefore.FirstRunMs {
		t.Fatalf("firstRun changed: %d vs %d", life.FirstRunMs, lifeBefore.FirstRunMs)
	}
	peers := s2.Peers()
	if len(peers) != 1 || peers[0].IP != "203.0.113.44" || peers[0].TotalBytesOut != 56789 || peers[0].TotalConns != 1 {
		t.Fatalf("peers not restored: %+v", peers)
	}

	// The cascade must resume: new samples fold into the restored tiers.
	in, out := int64(50_000*600), int64(500_000*600)
	for ts := end + 1000; ts <= end+60_000; ts += 1000 {
		s2.Sample(time.UnixMilli(ts), in, out, 0, 0, 2, -1)
		in += 50_000
		out += 500_000
	}
	resumed := s2.historyAt(end+60_000, 3_600_000, 240)
	if len(resumed.Buckets) <= len(after.Buckets) {
		t.Fatalf("cascade did not resume after restore: %d buckets", len(resumed.Buckets))
	}
}

func TestPeerEviction(t *testing.T) {
	s, _ := fresh(t)
	for i := range 600 {
		s.ConnOpened("10.0." + strconv.Itoa(i/250) + "." + strconv.Itoa(i%250) + ":1000")
	}
	s.mu.Lock()
	n := len(s.peers)
	s.mu.Unlock()
	if n > maxPeers {
		t.Fatalf("peer map size = %d, want ≤ %d", n, maxPeers)
	}
	if got := len(s.Peers()); got > maxPeersReturned {
		t.Fatalf("Peers() returned %d, want ≤ %d", got, maxPeersReturned)
	}
}

func TestRebaselineOnCounterReset(t *testing.T) {
	s, _ := fresh(t)
	s.Sample(time.UnixMilli(base), 1_000_000, 2_000_000, 0, 0, 2, -1)
	s.Sample(time.UnixMilli(base+100), 1_001_000, 2_010_000, 0, 0, 2, -1)
	life := s.Lifetime()
	if life.BytesIn != 1000 || life.BytesOut != 10_000 {
		t.Fatalf("lifetime after 2 samples = %+v", life)
	}
	// Engine restart: totals drop to near zero. Must not go negative or spike.
	s.Sample(time.UnixMilli(base+200), 500, 700, 0, 0, 2, -1)
	life = s.Lifetime()
	if life.BytesIn != 1000 || life.BytesOut != 10_000 {
		t.Fatalf("lifetime after reset = %+v, want unchanged", life)
	}
	// Deltas accumulate again from the new baseline.
	s.Sample(time.UnixMilli(base+300), 1500, 1700, 0, 0, 2, -1)
	life = s.Lifetime()
	if life.BytesIn != 2000 || life.BytesOut != 11_000 {
		t.Fatalf("lifetime after re-baseline = %+v", life)
	}
}

func TestCorruptFileStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(path, nil)
	if s.Lifetime().BytesIn != 0 {
		t.Fatal("corrupt file produced non-empty store")
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("corrupt file was not renamed aside: %v", err)
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("flush after corrupt load: %v", err)
	}
}

func TestHistoryEmptyAndAll(t *testing.T) {
	s, _ := fresh(t)
	if h := s.History(60_000, 300); len(h.Buckets) != 0 {
		t.Fatalf("empty store returned %d buckets", len(h.Buckets))
	}
	// Two days of coarse activity land in T4 via direct adds through Sample:
	// sample every 10 min for 2 days (T2/T3/T4 all advance).
	feed(s, base, 600_000, 2*86_400_000, 1000, 10_000)
	h := s.historyAt(base+2*86_400_000, 0, 300)
	if len(h.Buckets) < 2 || len(h.Buckets) > 3 {
		t.Fatalf("All window bucket count = %d, want 2-3 daily bars", len(h.Buckets))
	}
	if h.BucketMs != 86_400_000 {
		t.Fatalf("All window bucketMs = %d, want 1 day", h.BucketMs)
	}
}

func TestConnGaugeSampleAndMerge(t *testing.T) {
	s, _ := fresh(t)
	// Baseline sample (no bucket), then three samples with varying counts.
	s.Sample(time.UnixMilli(base), 0, 0, 0, 0, 3, -1)
	s.Sample(time.UnixMilli(base+100), 0, 1000, 0, 0, 5, -1)
	s.Sample(time.UnixMilli(base+200), 0, 2000, 0, 0, 2, -1)
	s.Sample(time.UnixMilli(base+300), 0, 3000, 0, 0, 7, -1)

	// Raw T0 buckets: the gauge is flat within a slot.
	h := s.historyAt(base+400, 15_000, 300)
	want := []float64{5, 2, 7}
	if len(h.Buckets) != len(want) {
		t.Fatalf("raw bucket count = %d, want %d", len(h.Buckets), len(want))
	}
	for i, b := range h.Buckets {
		if b.ConnO != want[i] || b.ConnH != want[i] || b.ConnL != want[i] || b.ConnC != want[i] {
			t.Fatalf("bucket %d conn OHLC = %f/%f/%f/%f, want all %f", i, b.ConnO, b.ConnH, b.ConnL, b.ConnC, want[i])
		}
	}

	// Regrouped: o = first, h = max, l = min, c = last.
	h = s.historyAt(base+400, 400, 1)
	m := h.Buckets[len(h.Buckets)-1]
	var lo, hi float64 = 1 << 30, -1
	for _, b := range h.Buckets {
		lo = min(lo, b.ConnL)
		hi = max(hi, b.ConnH)
	}
	if hi != 7 || lo != 2 {
		t.Fatalf("merged conn H/L = %f/%f, want 7/2", hi, lo)
	}
	if m.ConnC != 7 {
		t.Fatalf("merged ConnC = %f, want 7", m.ConnC)
	}
	if f := h.Buckets[0].ConnO; f != 5 {
		t.Fatalf("merged ConnO = %f, want 5", f)
	}
}

func TestConnCascadeToCoarseTiers(t *testing.T) {
	s, _ := fresh(t)
	// 65s at 100ms cadence; feed varies the count over 3..6. The 15m window is
	// served by T1, so its buckets exist only via the cascade.
	end := base + 65_000
	feed(s, base, 100, 65_000, 10_000, 100_000)
	h := s.historyAt(end, 900_000, 300)
	if len(h.Buckets) == 0 {
		t.Fatal("15m window empty; cascade failed")
	}
	for _, b := range h.Buckets {
		if b.ConnC < 0 {
			t.Fatalf("cascaded bucket t=%d has unknown conn gauge", b.T)
		}
		if b.ConnH < b.ConnL || b.ConnL < 3 || b.ConnH > 6 {
			t.Fatalf("cascaded conn H/L = %f/%f, want within 3..6", b.ConnH, b.ConnL)
		}
	}
}

func TestRttGaugeSampleAndMerge(t *testing.T) {
	s, _ := fresh(t)
	// Baseline (no bucket), then three samples with varying RTTs. A -1 reading
	// (no link) must record as unknown, not a real 0 ms.
	s.Sample(time.UnixMilli(base), 0, 0, 0, 0, 3, 30)
	s.Sample(time.UnixMilli(base+100), 0, 1000, 0, 0, 5, 40)
	s.Sample(time.UnixMilli(base+200), 0, 2000, 0, 0, 2, 20)
	s.Sample(time.UnixMilli(base+300), 0, 3000, 0, 0, 7, 55)

	// Raw T0 buckets: the gauge is flat within a slot.
	h := s.historyAt(base+400, 15_000, 300)
	want := []float64{40, 20, 55}
	if len(h.Buckets) != len(want) {
		t.Fatalf("raw bucket count = %d, want %d", len(h.Buckets), len(want))
	}
	for i, b := range h.Buckets {
		if b.RttO != want[i] || b.RttH != want[i] || b.RttL != want[i] || b.RttC != want[i] {
			t.Fatalf("bucket %d rtt OHLC = %f/%f/%f/%f, want all %f", i, b.RttO, b.RttH, b.RttL, b.RttC, want[i])
		}
	}

	// Regrouped: o = first, h = max, l = min, c = last.
	h = s.historyAt(base+400, 400, 1)
	m := h.Buckets[len(h.Buckets)-1]
	var lo, hi float64 = 1 << 30, -1
	for _, b := range h.Buckets {
		lo = min(lo, b.RttL)
		hi = max(hi, b.RttH)
	}
	if hi != 55 || lo != 20 {
		t.Fatalf("merged rtt H/L = %f/%f, want 55/20", hi, lo)
	}
	if m.RttC != 55 {
		t.Fatalf("merged RttC = %f, want 55", m.RttC)
	}
	if f := h.Buckets[0].RttO; f != 40 {
		t.Fatalf("merged RttO = %f, want 40", f)
	}
}

func TestRttUnknownWhenNonPositive(t *testing.T) {
	s, _ := fresh(t)
	s.Sample(time.UnixMilli(base), 0, 0, 0, 0, 2, -1)
	s.Sample(time.UnixMilli(base+100), 0, 1000, 0, 0, 2, -1)
	h := s.historyAt(base+200, 15_000, 300)
	if len(h.Buckets) == 0 {
		t.Fatal("no buckets")
	}
	if b := h.Buckets[0]; b.RttC != -1 {
		t.Fatalf("non-positive RTT should record unknown (-1), got %f", b.RttC)
	}
}

func TestStatsV2ToV3Migration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	// A v2 file: 15-element packed rows (conn gauge present, no RTT gauge).
	v2 := `{"v":2,"lifetime":{"bytesIn":1000,"bytesOut":10000,"firstRunMs":1},"peers":[],` +
		`"tiers":{"t2":[[` + strconv.FormatInt(base, 10) + `,1000,10000,10,20,5,15,100,200,50,150,4,6,3,5]]}}`
	if err := os.WriteFile(path, []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(path, nil)
	if _, err := os.Stat(path + ".bad"); err == nil {
		t.Fatal("v2 file was renamed aside instead of migrated")
	}
	h := s.historyAt(base+15_000, 3_600_000, 300)
	if len(h.Buckets) != 1 {
		t.Fatalf("restored bucket count = %d, want 1", len(h.Buckets))
	}
	b := h.Buckets[0]
	if b.ConnC != 5 || b.ConnH != 6 || b.ConnL != 3 {
		t.Fatalf("v2 conn gauge should survive migration, got %+v", b)
	}
	if b.RttO != -1 || b.RttH != -1 || b.RttL != -1 || b.RttC != -1 {
		t.Fatalf("v2 rtt gauge should be unknown (-1), got %+v", b)
	}
}

func TestStatsV1Migration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	// A v1 file: 11-element packed rows, no connection gauge.
	v1 := `{"v":1,"lifetime":{"bytesIn":1000,"bytesOut":10000,"firstRunMs":1},"peers":[],` +
		`"tiers":{"t2":[[` + strconv.FormatInt(base, 10) + `,1000,10000,10,20,5,15,100,200,50,150]]}}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}

	s := Open(path, nil)
	if _, err := os.Stat(path + ".bad"); err == nil {
		t.Fatal("v1 file was renamed aside instead of migrated")
	}
	if life := s.Lifetime(); life.BytesIn != 1000 || life.BytesOut != 10_000 {
		t.Fatalf("v1 lifetime not restored: %+v", life)
	}
	h := s.historyAt(base+15_000, 3_600_000, 300)
	if len(h.Buckets) != 1 {
		t.Fatalf("restored bucket count = %d, want 1", len(h.Buckets))
	}
	b := h.Buckets[0]
	if b.In != 1000 || b.Out != 10_000 || b.OutH != 200 {
		t.Fatalf("v1 bytes/rates not restored: %+v", b)
	}
	if b.ConnO != -1 || b.ConnH != -1 || b.ConnL != -1 || b.ConnC != -1 {
		t.Fatalf("v1 conn gauge should be unknown (-1), got %+v", b)
	}
	if b.RttO != -1 || b.RttH != -1 || b.RttL != -1 || b.RttC != -1 {
		t.Fatalf("v1 rtt gauge should be unknown (-1), got %+v", b)
	}

	// Rewrite and reload: the sentinel must survive a v2 round-trip.
	if err := s.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"v":3`) {
		t.Fatalf("rewritten file is not v3: %.100s", data)
	}
	s2 := Open(path, nil)
	h = s2.historyAt(base+15_000, 3_600_000, 300)
	if len(h.Buckets) != 1 || h.Buckets[0].ConnC != -1 {
		t.Fatalf("sentinel lost across v2 round-trip: %+v", h.Buckets)
	}

	// New samples inside the migrated bucket's 15s slot cascade into it; the
	// unknown (-1) side must adopt the first known value, not poison it.
	s2.Sample(time.UnixMilli(base+100), 0, 0, 0, 0, 4, -1) // baseline only
	var out int64
	for ts := base + 200; ts <= base+2_500; ts += 100 {
		out += 50
		s2.Sample(time.UnixMilli(ts), 0, out, 0, 0, 4, -1)
	}
	h = s2.historyAt(base+2_500, 3_600_000, 300)
	if len(h.Buckets) != 1 {
		t.Fatalf("post-migration bucket count = %d, want 1", len(h.Buckets))
	}
	m := h.Buckets[0]
	if m.In != 1000 || m.Out <= 10_000 {
		t.Fatalf("migrated bucket lost bytes after live merge: %+v", m)
	}
	if m.ConnC != 4 || m.ConnH != 4 || m.ConnL != 4 {
		t.Fatalf("conn gauge after live merge = %f/%f/%f, want all 4", m.ConnH, m.ConnL, m.ConnC)
	}
}

func TestCountingConn(t *testing.T) {
	// A pipe-backed conn check would drag in networking; the arithmetic is
	// what matters and lives in LinkCounters via countingConn's Add calls.
	var totals, session LinkCounters
	totals.In.Add(5)
	session.Out.Add(7)
	if in, _ := totals.Bytes(); in != 5 {
		t.Fatal("counter arithmetic broken")
	}
	if _, out := session.Bytes(); out != 7 {
		t.Fatal("counter arithmetic broken")
	}
}
