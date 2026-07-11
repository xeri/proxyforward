// Package stats is the engine's bandwidth-history and lifetime-statistics
// store: RRD-style multi-resolution ring buffers fed by a periodic sampler,
// plus per-peer aggregates and lifetime counters persisted to a single JSON
// file. It deliberately imports nothing from the rest of the project so any
// layer (agent, gateway, engine, ipc, app) can depend on it.
//
// Direction semantics are conntrack's: In = client → server bytes,
// Out = server → client bytes. The UI maps Out to "download" at the chart
// layer; this package stays neutral.
package stats

import (
	"io"
	"log/slog"
	"net"
	"sort"
	"sync"
	"time"
)

// Bucket is one time slot of one tier: exact byte sums plus OHLC of the
// transfer rate (bytes/sec) observed within the slot. Coarse tiers build
// their OHLC from the finer tier's per-bucket average rates, so candles at
// long ranges reflect sustained rates rather than 100 ms spikes.
type Bucket struct {
	T   int64 `json:"t"`   // unix millis of bucket start
	In  int64 `json:"in"`  // bytes, client → server
	Out int64 `json:"out"` // bytes, server → client

	InO  float64 `json:"io"` // rate OHLC, bytes/sec
	InH  float64 `json:"ih"`
	InL  float64 `json:"il"`
	InC  float64 `json:"ic"`
	OutO float64 `json:"oo"`
	OutH float64 `json:"oh"`
	OutL float64 `json:"ol"`
	OutC float64 `json:"oc"`

	// Conn* is the OHLC of the live proxied-connection count (a gauge, not a
	// rate) observed within the slot. -1 on all four means unknown: the bucket
	// was recorded by a version that predates connection sampling. ConnC < 0
	// is the canonical unknown check.
	ConnO float64 `json:"co"`
	ConnH float64 `json:"ch"`
	ConnL float64 `json:"cl"`
	ConnC float64 `json:"cc"`
}

// HistoryResult is one rendered window: buckets oldest-first, the last one
// possibly still in progress.
type HistoryResult struct {
	WindowMs int64    `json:"windowMs"`
	BucketMs int64    `json:"bucketMs"`
	Buckets  []Bucket `json:"buckets"`
}

// PeerStat is the lifetime record of one client IP.
type PeerStat struct {
	IP            string `json:"ip"`
	FirstSeen     int64  `json:"firstSeen"` // unix millis
	LastSeen      int64  `json:"lastSeen"`
	TotalBytesIn  int64  `json:"totalBytesIn"`
	TotalBytesOut int64  `json:"totalBytesOut"`
	TotalConns    int64  `json:"totalConns"`
}

// Lifetime aggregates survive restarts.
type Lifetime struct {
	BytesIn      int64 `json:"bytesIn"` // proxied bytes (conntrack semantics)
	BytesOut     int64 `json:"bytesOut"`
	LinkBytesIn  int64 `json:"linkBytesIn"` // raw control-link bytes
	LinkBytesOut int64 `json:"linkBytesOut"`
	UptimeMs     int64 `json:"uptimeMs"` // cumulative engine uptime
	LinkSessions int64 `json:"linkSessions"`
	FirstRunMs   int64 `json:"firstRunMs"`
}

const (
	// maxHistoryBuckets bounds any History response so it always fits an IPC
	// frame (control.MaxFrame is 64 KiB; 300 buckets ≈ 40 KB of JSON).
	maxHistoryBuckets = 300
	// maxPeersReturned bounds Peers() for the same reason.
	maxPeersReturned = 300
	// maxPeers caps the peer map; beyond it the oldest lastSeen is evicted.
	maxPeers = 512
)

// tierSpecs define the resolution ladder. Retention of each tier covers the
// UI windows it serves with headroom: T0 (100ms×1200=2min) → 15s/1m,
// T1 (1s×1800=30min) → 15m, T2 (15s×7200=30h) → 1h/6h/24h,
// T3 (10min×5760=40d) → 7d/30d, T4 (1d×1100≈3y) → All.
var tierSpecs = []struct {
	resMs   int64
	slots   int
	persist bool
}{
	{100, 1200, false},
	{1_000, 1800, false},
	{15_000, 7200, true},
	{600_000, 5760, true},
	{86_400_000, 1100, true},
}

// ring is one fixed-size tier. Slot for absolute bucket index i is
// buf[i mod len]; a slot is valid only when its stored T matches the T its
// position implies, so laps and restart gaps need no explicit invalidation.
type ring struct {
	resMs    int64
	buf      []Bucket
	cur      int64 // absolute index of the newest bucket; -1 when empty
	curEmpty bool  // newest bucket exists but has no data merged yet
}

func newRing(resMs int64, slots int) *ring {
	return &ring{resMs: resMs, buf: make([]Bucket, slots), cur: -1}
}

func (r *ring) pos(i int64) int { return int(i % int64(len(r.buf))) }

// start opens a fresh current bucket at absolute index idx.
func (r *ring) start(idx int64) {
	r.cur = idx
	r.buf[r.pos(idx)] = Bucket{T: idx * r.resMs}
	r.curEmpty = true
}

// mergeCur folds one point (a raw sample or a completed finer bucket) into
// the current bucket: bytes sum exactly, rate OHLC combines o=first, c=last,
// h=max, l=min.
func (r *ring) mergeCur(b Bucket) {
	dst := &r.buf[r.pos(r.cur)]
	dst.In += b.In
	dst.Out += b.Out
	if r.curEmpty {
		dst.InO, dst.InH, dst.InL, dst.InC = b.InO, b.InH, b.InL, b.InC
		dst.OutO, dst.OutH, dst.OutL, dst.OutC = b.OutO, b.OutH, b.OutL, b.OutC
		dst.ConnO, dst.ConnH, dst.ConnL, dst.ConnC = b.ConnO, b.ConnH, b.ConnL, b.ConnC
		r.curEmpty = false
		return
	}
	dst.InC = b.InC
	dst.InH = max(dst.InH, b.InH)
	dst.InL = min(dst.InL, b.InL)
	dst.OutC = b.OutC
	dst.OutH = max(dst.OutH, b.OutH)
	dst.OutL = min(dst.OutL, b.OutL)
	mergeConn(dst, b)
}

// mergeConn folds b's connection gauge into dst, skipping unknown (-1)
// sides so pre-upgrade buckets never poison a merge.
func mergeConn(dst *Bucket, b Bucket) {
	if b.ConnC < 0 {
		return // source unknown: keep dst as-is (known or unknown)
	}
	if dst.ConnC < 0 {
		dst.ConnO, dst.ConnH, dst.ConnL, dst.ConnC = b.ConnO, b.ConnH, b.ConnL, b.ConnC
		return
	}
	dst.ConnC = b.ConnC
	dst.ConnH = max(dst.ConnH, b.ConnH)
	dst.ConnL = min(dst.ConnL, b.ConnL)
}

// valid reports whether absolute index i holds real data (not a stale lap or
// an unwritten slot).
func (r *ring) valid(i int64) bool {
	if r.cur < 0 || i > r.cur || i <= r.cur-int64(len(r.buf)) {
		return false
	}
	return r.buf[r.pos(i)].T == i*r.resMs
}

// Store owns the tier rings, peer records, and lifetime counters. All methods
// are safe for concurrent use; a single mutex suffices at these rates.
type Store struct {
	mu     sync.Mutex
	path   string
	logger *slog.Logger
	tiers  []*ring
	peers  map[string]*PeerStat
	life   Lifetime

	// Sampler baselines: totals are monotonic per engine run; the first
	// sample only records them, a negative delta re-baselines.
	baselined                                      bool
	lastT                                          int64
	lastAppIn, lastAppOut, lastLinkIn, lastLinkOut int64

	// upMark is when uptime was last folded into life.UptimeMs.
	upMark time.Time

	// flushMu serializes the write phase of Flush without holding mu.
	flushMu sync.Mutex
}

// Open loads the store from path, or starts fresh. A corrupt or unreadable
// file is renamed aside (.bad) and never blocks engine start.
func Open(path string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Store{
		path:   path,
		logger: logger,
		peers:  make(map[string]*PeerStat),
	}
	for _, ts := range tierSpecs {
		s.tiers = append(s.tiers, newRing(ts.resMs, ts.slots))
	}
	s.load()
	if s.life.FirstRunMs == 0 {
		s.life.FirstRunMs = time.Now().UnixMilli()
	}
	s.upMark = time.Now()
	return s
}

// Sample ingests one reading of the monotonic byte totals. Call it on a
// steady cadence (~10 Hz); the instantaneous rate is the delta over the
// actual elapsed time, so jitter and system sleeps do not fabricate spikes.
func (s *Store) Sample(now time.Time, appIn, appOut, linkIn, linkOut int64, conns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := now.UnixMilli()
	if !s.baselined {
		s.baselined = true
		s.lastT, s.lastAppIn, s.lastAppOut, s.lastLinkIn, s.lastLinkOut = t, appIn, appOut, linkIn, linkOut
		return
	}
	dt := t - s.lastT
	if dt <= 0 {
		return
	}
	dIn := monotonicDelta(appIn, s.lastAppIn)
	dOut := monotonicDelta(appOut, s.lastAppOut)
	dLinkIn := monotonicDelta(linkIn, s.lastLinkIn)
	dLinkOut := monotonicDelta(linkOut, s.lastLinkOut)
	s.lastT, s.lastAppIn, s.lastAppOut, s.lastLinkIn, s.lastLinkOut = t, appIn, appOut, linkIn, linkOut

	s.life.BytesIn += dIn
	s.life.BytesOut += dOut
	s.life.LinkBytesIn += dLinkIn
	s.life.LinkBytesOut += dLinkOut

	inRate := float64(dIn) * 1000 / float64(dt)
	outRate := float64(dOut) * 1000 / float64(dt)
	c := float64(conns)
	s.add(0, Bucket{
		T: t, In: dIn, Out: dOut,
		InO: inRate, InH: inRate, InL: inRate, InC: inRate,
		OutO: outRate, OutH: outRate, OutL: outRate, OutC: outRate,
		ConnO: c, ConnH: c, ConnL: c, ConnC: c,
	})
}

// monotonicDelta treats a shrinking total (engine restart, counter reset) as
// a re-baseline rather than negative traffic.
func monotonicDelta(cur, prev int64) int64 {
	if d := cur - prev; d > 0 {
		return d
	}
	return 0
}

// add folds a point into tier level; when the tier's current bucket
// completes, the completed bucket cascades one level up. mu must be held.
func (s *Store) add(level int, b Bucket) {
	r := s.tiers[level]
	idx := b.T / r.resMs
	switch {
	case r.cur < 0:
		r.start(idx)
	case idx > r.cur:
		completed := r.buf[r.pos(r.cur)]
		r.start(idx)
		if level+1 < len(s.tiers) {
			s.add(level+1, completed)
		}
	case idx < r.cur:
		return // clock went backward; drop rather than corrupt the ring
	}
	r.mergeCur(b)
}

// History returns up to maxBuckets buckets covering the trailing windowMs
// (0 = everything the store has, from the daily tier). The newest bucket may
// still be in progress.
func (s *Store) History(windowMs int64, maxBuckets int) HistoryResult {
	return s.historyAt(time.Now().UnixMilli(), windowMs, maxBuckets)
}

func (s *Store) historyAt(nowMs, windowMs int64, maxBuckets int) HistoryResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxBuckets <= 0 || maxBuckets > maxHistoryBuckets {
		maxBuckets = maxHistoryBuckets
	}

	var (
		r                *ring
		startIdx, endIdx int64
	)
	if windowMs <= 0 {
		// All time: serve the daily tier from its oldest data.
		r = s.tiers[len(s.tiers)-1]
		if r.cur < 0 {
			return HistoryResult{Buckets: []Bucket{}}
		}
		oldest := r.cur
		for i := r.cur - int64(len(r.buf)) + 1; i < r.cur; i++ {
			if i >= 0 && r.valid(i) {
				oldest = i
				break
			}
		}
		startIdx, endIdx = oldest, r.cur
		windowMs = max(nowMs-oldest*r.resMs, r.resMs)
	} else {
		for _, t := range s.tiers {
			if t.resMs*int64(len(t.buf)) >= windowMs {
				r = t
				break
			}
		}
		if r == nil {
			r = s.tiers[len(s.tiers)-1]
		}
		n := (windowMs + r.resMs - 1) / r.resMs
		endIdx = nowMs / r.resMs
		startIdx = endIdx - n + 1
	}
	k := (endIdx - startIdx + 1 + int64(maxBuckets) - 1) / int64(maxBuckets)

	out := []Bucket{}
	groupIdx := int64(-1)
	first := true
	for i := startIdx; i <= endIdx; i++ {
		if !r.valid(i) {
			continue
		}
		b := r.buf[r.pos(i)]
		grp := i / k
		if first || grp != groupIdx {
			b.T = grp * k * r.resMs
			out = append(out, b)
			groupIdx = grp
			first = false
			continue
		}
		dst := &out[len(out)-1]
		dst.In += b.In
		dst.Out += b.Out
		dst.InC = b.InC
		dst.InH = max(dst.InH, b.InH)
		dst.InL = min(dst.InL, b.InL)
		dst.OutC = b.OutC
		dst.OutH = max(dst.OutH, b.OutH)
		dst.OutL = min(dst.OutL, b.OutL)
		mergeConn(dst, b)
	}
	return HistoryResult{WindowMs: windowMs, BucketMs: k * r.resMs, Buckets: out}
}

// ConnOpened records a client connection to addr (IP:port or bare IP).
func (s *Store) ConnOpened(addr string) {
	ip := peerIP(addr)
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.peers[ip]
	if p == nil {
		s.evictPeerLocked()
		p = &PeerStat{IP: ip, FirstSeen: now}
		s.peers[ip] = p
	}
	p.LastSeen = now
	p.TotalConns++
}

// ConnClosed folds a finished connection's bytes into its peer record.
func (s *Store) ConnClosed(addr string, bytesIn, bytesOut int64) {
	ip := peerIP(addr)
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.peers[ip]
	if p == nil {
		// Evicted while the connection was open; recreate rather than lose it.
		s.evictPeerLocked()
		p = &PeerStat{IP: ip, FirstSeen: now, TotalConns: 1}
		s.peers[ip] = p
	}
	p.LastSeen = now
	p.TotalBytesIn += bytesIn
	p.TotalBytesOut += bytesOut
}

// evictPeerLocked makes room for one new peer when the map is full.
func (s *Store) evictPeerLocked() {
	if len(s.peers) < maxPeers {
		return
	}
	var victim string
	oldest := int64(1<<63 - 1)
	for ip, p := range s.peers {
		if p.LastSeen < oldest {
			oldest, victim = p.LastSeen, ip
		}
	}
	delete(s.peers, victim)
}

// Peers lists peer records, most recently seen first, capped for IPC frames.
func (s *Store) Peers() []PeerStat {
	s.mu.Lock()
	out := make([]PeerStat, 0, len(s.peers))
	for _, p := range s.peers {
		out = append(out, *p)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].IP < out[j].IP
	})
	if len(out) > maxPeersReturned {
		out = out[:maxPeersReturned]
	}
	return out
}

// LinkSessionStarted bumps the lifetime control-link session counter.
func (s *Store) LinkSessionStarted() {
	s.mu.Lock()
	s.life.LinkSessions++
	s.mu.Unlock()
}

// Lifetime returns the persisted aggregates plus live-accruing uptime.
func (s *Store) Lifetime() Lifetime {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.life
	l.UptimeMs += time.Since(s.upMark).Milliseconds()
	return l
}

// peerIP strips the port so a client is one peer across reconnects.
func peerIP(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
