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

	// Rtt* is the OHLC of the control-link round-trip time in milliseconds (a
	// gauge, not a rate) observed within the slot. Both roles heartbeat the
	// link and sample it (readings clamp to ≥1ms so they never collapse into
	// the sentinel). -1 on all four means unknown: the bucket predates RTT
	// sampling, or the link was down. RttC < 0 is the canonical unknown check.
	RttO float64 `json:"ro"`
	RttH float64 `json:"rh"`
	RttL float64 `json:"rl"`
	RttC float64 `json:"rc"`

	// Players* is the OHLC of the identified-player count (a gauge). -1 on
	// all four means unknown: the bucket predates player sampling, or no
	// tunnel is Minecraft-aware. PlayersC < 0 is the canonical unknown check.
	PlayersO float64 `json:"po"`
	PlayersH float64 `json:"ph"`
	PlayersL float64 `json:"pl"`
	PlayersC float64 `json:"pc"`

	// Loss* is the OHLC of control-link packet loss in percent (a gauge).
	// -1 on all four means unknown; 0 is a real "no loss" reading.
	LossO float64 `json:"lo"`
	LossH float64 `json:"lh"`
	LossL float64 `json:"ll"`
	LossC float64 `json:"lc"`
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
		dst.RttO, dst.RttH, dst.RttL, dst.RttC = b.RttO, b.RttH, b.RttL, b.RttC
		dst.PlayersO, dst.PlayersH, dst.PlayersL, dst.PlayersC = b.PlayersO, b.PlayersH, b.PlayersL, b.PlayersC
		dst.LossO, dst.LossH, dst.LossL, dst.LossC = b.LossO, b.LossH, b.LossL, b.LossC
		r.curEmpty = false
		return
	}
	dst.InC = b.InC
	dst.InH = max(dst.InH, b.InH)
	dst.InL = min(dst.InL, b.InL)
	dst.OutC = b.OutC
	dst.OutH = max(dst.OutH, b.OutH)
	dst.OutL = min(dst.OutL, b.OutL)
	mergeGauges(dst, b)
}

// mergeGauge folds one gauge OHLC into another, skipping unknown (-1) sides
// so pre-upgrade or unmeasured buckets never poison a merge.
func mergeGauge(dstO, dstH, dstL, dstC *float64, bO, bH, bL, bC float64) {
	if bC < 0 {
		return // source unknown: keep dst as-is (known or unknown)
	}
	if *dstC < 0 {
		*dstO, *dstH, *dstL, *dstC = bO, bH, bL, bC
		return
	}
	*dstC = bC
	*dstH = max(*dstH, bH)
	*dstL = min(*dstL, bL)
}

// mergeGauges folds every gauge family (conn, rtt, players, loss) of b into
// dst.
func mergeGauges(dst *Bucket, b Bucket) {
	mergeGauge(&dst.ConnO, &dst.ConnH, &dst.ConnL, &dst.ConnC, b.ConnO, b.ConnH, b.ConnL, b.ConnC)
	mergeGauge(&dst.RttO, &dst.RttH, &dst.RttL, &dst.RttC, b.RttO, b.RttH, b.RttL, b.RttC)
	mergeGauge(&dst.PlayersO, &dst.PlayersH, &dst.PlayersL, &dst.PlayersC, b.PlayersO, b.PlayersH, b.PlayersL, b.PlayersC)
	mergeGauge(&dst.LossO, &dst.LossH, &dst.LossL, &dst.LossC, b.LossO, b.LossH, b.LossL, b.LossC)
}

// valid reports whether absolute index i holds real data (not a stale lap or
// an unwritten slot).
func (r *ring) valid(i int64) bool {
	if r.cur < 0 || i > r.cur || i <= r.cur-int64(len(r.buf)) {
		return false
	}
	return r.buf[r.pos(i)].T == i*r.resMs
}

// history is one bandwidth-history ring ladder: the tier rings plus the
// per-tier dirty watermark and this series' sampler baseline. The Store owns
// one global history (the gateway-wide series) and one per connected agent.
// Every method assumes the Store mutex is held — the ladder carries no lock of
// its own.
type history struct {
	tiers []*ring

	// lastCurT tracks, per persisted tier, the current bucket's start time at
	// the last successful save: only buckets at or after it can have changed
	// since, so the next save skips everything older.
	lastCurT map[int]int64

	// Sampler baseline for this series' app-byte totals: monotonic per engine
	// run; the first sample only records it, a negative delta re-baselines.
	baselined       bool
	lastT           int64
	lastIn, lastOut int64

	// touched is the unix-millis of the last sample, for LRU eviction of the
	// per-agent histories.
	touched int64
}

func newHistory() *history {
	h := &history{lastCurT: make(map[int]int64)}
	for _, ts := range tierSpecs {
		h.tiers = append(h.tiers, newRing(ts.resMs, ts.slots))
	}
	return h
}

// sample folds one reading of the monotonic app-byte totals into tier 0 (which
// cascades up) and returns the byte deltas plus whether a real delta was
// produced. advanced is false on the very first sample (baseline only) and on a
// non-advancing clock, so the caller can gate lifetime accounting on it. Gauges
// record unknown when unmeasured: players < 0, rttMs <= 0, lossPct < 0.
func (h *history) sample(now time.Time, appIn, appOut int64, conns, players int, rttMs, lossPct float64) (dIn, dOut int64, advanced bool) {
	t := now.UnixMilli()
	h.touched = t
	if !h.baselined {
		h.baselined = true
		h.lastT, h.lastIn, h.lastOut = t, appIn, appOut
		return 0, 0, false
	}
	dt := t - h.lastT
	if dt <= 0 {
		return 0, 0, false
	}
	dIn = monotonicDelta(appIn, h.lastIn)
	dOut = monotonicDelta(appOut, h.lastOut)
	h.lastT, h.lastIn, h.lastOut = t, appIn, appOut

	inRate := float64(dIn) * 1000 / float64(dt)
	outRate := float64(dOut) * 1000 / float64(dt)
	c := float64(conns)
	rtt := -1.0
	if rttMs > 0 {
		rtt = rttMs
	}
	ply := -1.0
	if players >= 0 {
		ply = float64(players)
	}
	loss := -1.0
	if lossPct >= 0 {
		loss = lossPct
	}
	h.add(0, Bucket{
		T: t, In: dIn, Out: dOut,
		InO: inRate, InH: inRate, InL: inRate, InC: inRate,
		OutO: outRate, OutH: outRate, OutL: outRate, OutC: outRate,
		ConnO: c, ConnH: c, ConnL: c, ConnC: c,
		RttO: rtt, RttH: rtt, RttL: rtt, RttC: rtt,
		PlayersO: ply, PlayersH: ply, PlayersL: ply, PlayersC: ply,
		LossO: loss, LossH: loss, LossL: loss, LossC: loss,
	})
	return dIn, dOut, true
}

// maxAgentHistories bounds the per-agent bandwidth histories a gateway keeps in
// memory and on disk. The "several friends" topology needs a handful; the cap
// is a safety valve against a gateway that has cycled through many agent ids.
const maxAgentHistories = 32

// Store owns the tier histories, peer records, and lifetime counters. All
// methods are safe for concurrent use; a single mutex suffices at these rates.
type Store struct {
	mu      sync.Mutex
	persist Persister // nil = memory-only (persistence unavailable)
	logger  *slog.Logger

	// global is the gateway-wide bandwidth history (agent_id "" on disk);
	// agents holds one history per connected agent, LRU-capped.
	global *history
	agents map[string]*history

	// evicted names agent histories dropped from `agents` since the last save
	// so SaveStats can delete their persisted rrd rows.
	evicted map[string]struct{}

	peers map[string]*PeerStat
	life  Lifetime

	// Link-byte baseline for the lifetime counter (the tier histories keep
	// their own app-byte baselines). Totals are monotonic per engine run.
	linkBaselined           bool
	lastLinkIn, lastLinkOut int64

	// upMark is when uptime was last folded into life.UptimeMs.
	upMark time.Time

	// flushMu serializes the write phase of Flush without holding mu.
	flushMu sync.Mutex
}

// Open restores the store from p, or starts fresh. p may be nil (persistence
// unavailable): the store then runs memory-only. A load failure never blocks
// engine start.
func Open(p Persister, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Store{
		persist: p,
		logger:  logger,
		global:  newHistory(),
		agents:  make(map[string]*history),
		evicted: make(map[string]struct{}),
		peers:   make(map[string]*PeerStat),
	}
	s.load()
	if s.life.FirstRunMs == 0 {
		s.life.FirstRunMs = time.Now().UnixMilli()
	}
	s.upMark = time.Now()
	return s
}

// load restores state from the persister into the freshly-constructed store.
func (s *Store) load() {
	if s.persist == nil {
		return
	}
	snap, err := s.persist.LoadStats()
	if err != nil {
		s.logger.Warn("stats: restore failed — starting fresh", "err", err)
		return
	}
	if snap == nil {
		return
	}
	s.life = snap.Lifetime
	for _, p := range snap.Peers {
		if len(s.peers) >= maxPeers {
			break
		}
		pc := p
		s.peers[p.IP] = &pc
	}
	for _, ts := range snap.Tiers {
		h := s.historyForLoad(ts.AgentID)
		if h == nil || ts.Tier < 0 || ts.Tier >= len(h.tiers) {
			continue
		}
		restoreTier(h.tiers[ts.Tier], ts.Buckets)
	}
}

// historyFor returns the history for an agent id ("" = global), or nil if an
// agent is not tracked. Callers hold s.mu.
func (s *Store) historyFor(agentID string) *history {
	if agentID == "" {
		return s.global
	}
	return s.agents[agentID]
}

// historyForLoad returns the history to restore into, creating the per-agent
// one on demand (persisted agents are already bounded by the save-time cap).
func (s *Store) historyForLoad(agentID string) *history {
	if agentID == "" {
		return s.global
	}
	h := s.agents[agentID]
	if h == nil {
		h = newHistory()
		s.agents[agentID] = h
	}
	return h
}

// evictAgentIfFullLocked drops the least-recently-sampled agent history when the
// map is at capacity, marking it for rrd deletion on the next save.
func (s *Store) evictAgentIfFullLocked() {
	if len(s.agents) < maxAgentHistories {
		return
	}
	var victim string
	oldest := int64(1<<63 - 1)
	for id, h := range s.agents {
		if h.touched < oldest {
			oldest, victim = h.touched, id
		}
	}
	if victim != "" {
		delete(s.agents, victim)
		s.evicted[victim] = struct{}{}
	}
}

// restoreTier places persisted buckets back into a ring. Only buckets within
// one ring-length of the newest survive; older ones would collide with newer
// slots. The newest becomes the ring's current so the cascade resumes where
// the previous run stopped.
func restoreTier(r *ring, buckets []Bucket) {
	var newest int64 = -1
	for _, b := range buckets {
		if b.T <= 0 || b.T%r.resMs != 0 {
			continue // misaligned entry: drop it, keep the rest
		}
		if idx := b.T / r.resMs; idx > newest {
			newest = idx
		}
	}
	if newest < 0 {
		return
	}
	floor := newest - int64(len(r.buf)) + 1
	for _, b := range buckets {
		if b.T <= 0 || b.T%r.resMs != 0 {
			continue
		}
		idx := b.T / r.resMs
		if idx < floor {
			continue
		}
		r.buf[r.pos(idx)] = b
	}
	r.cur = newest
	r.curEmpty = false
}

// snapshotLocked builds the persistable image; mu must be held. Only valid
// buckets are included (idle servers stay sparse), oldest first.
func (s *Store) snapshotLocked() *SnapshotData {
	snap := &SnapshotData{
		Lifetime: s.life,
		Peers:    make([]PeerStat, 0, len(s.peers)),
	}
	for _, p := range s.peers {
		snap.Peers = append(snap.Peers, *p)
	}
	s.appendTiersLocked(snap, "", s.global)
	for id, h := range s.agents {
		s.appendTiersLocked(snap, id, h)
	}
	for id := range s.evicted {
		snap.DeleteAgents = append(snap.DeleteAgents, id)
	}
	return snap
}

// appendTiersLocked adds one history's persisted tiers to snap under agentID.
func (s *Store) appendTiersLocked(snap *SnapshotData, agentID string, h *history) {
	for ti, spec := range tierSpecs {
		if !spec.persist {
			continue
		}
		r := h.tiers[ti]
		if r.cur < 0 {
			continue
		}
		ts := TierSnapshot{AgentID: agentID, Tier: ti, ResMs: r.resMs, DirtyFromT: h.lastCurT[ti]}
		floorIdx := r.cur - int64(len(r.buf)) + 1
		ts.FloorT = max(floorIdx*r.resMs, 0)
		for i := floorIdx; i <= r.cur; i++ {
			if i >= 0 && r.valid(i) {
				ts.Buckets = append(ts.Buckets, r.buf[r.pos(i)])
			}
		}
		snap.Tiers = append(snap.Tiers, ts)
	}
}

// Flush saves the store through the persister and folds accrued uptime into
// the lifetime counter. Memory-only stores just fold uptime.
func (s *Store) Flush() error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.mu.Lock()
	now := time.Now()
	s.life.UptimeMs += now.Sub(s.upMark).Milliseconds()
	s.upMark = now
	var snap *SnapshotData
	if s.persist != nil {
		snap = s.snapshotLocked()
	}
	s.mu.Unlock()

	if s.persist == nil {
		return nil
	}
	if err := s.persist.SaveStats(snap); err != nil {
		return err
	}
	// Advance the dirty watermarks only after the save landed; a failed save
	// keeps everything dirty for the next attempt.
	s.mu.Lock()
	for _, ts := range snap.Tiers {
		if n := len(ts.Buckets); n > 0 {
			if h := s.historyFor(ts.AgentID); h != nil {
				h.lastCurT[ts.Tier] = ts.Buckets[n-1].T
			}
		}
	}
	// The evicted agents' rrd rows are gone now; stop re-requesting their
	// deletion (a returning agent re-creates a fresh history).
	for _, id := range snap.DeleteAgents {
		delete(s.evicted, id)
	}
	s.mu.Unlock()
	return nil
}

// Sample ingests one reading of the monotonic byte totals. Call it on a
// steady cadence (~10 Hz); the instantaneous rate is the delta over the
// actual elapsed time, so jitter and system sleeps do not fabricate spikes.
// Gauges record unknown when unmeasured: players < 0, rttMs <= 0 (no link or
// a role that does not measure it), lossPct < 0. A zero players/loss reading
// is real data.
func (s *Store) Sample(now time.Time, appIn, appOut, linkIn, linkOut int64, conns, players int, rttMs, lossPct float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The global history owns the app-byte baseline and the tier cascade; the
	// link bytes feed only the lifetime counter and keep their own baseline.
	dIn, dOut, advanced := s.global.sample(now, appIn, appOut, conns, players, rttMs, lossPct)
	if !s.linkBaselined {
		s.linkBaselined = true
		s.lastLinkIn, s.lastLinkOut = linkIn, linkOut
	}
	if !advanced {
		return
	}
	s.life.BytesIn += dIn
	s.life.BytesOut += dOut
	s.life.LinkBytesIn += monotonicDelta(linkIn, s.lastLinkIn)
	s.life.LinkBytesOut += monotonicDelta(linkOut, s.lastLinkOut)
	s.lastLinkIn, s.lastLinkOut = linkIn, linkOut
}

// SampleAgent folds one reading of an agent's proxied byte totals and gauges
// into that agent's bandwidth history (created on demand, LRU-capped). agentID
// "" is a no-op — the gateway-wide series is Sample's job. Lifetime/link
// counters are global and are not touched here.
func (s *Store) SampleAgent(agentID string, now time.Time, appIn, appOut int64, conns, players int, rttMs, lossPct float64) {
	if agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.agents[agentID]
	if h == nil {
		s.evictAgentIfFullLocked()
		h = newHistory()
		s.agents[agentID] = h
		delete(s.evicted, agentID) // re-created before its rows were deleted
	}
	h.sample(now, appIn, appOut, conns, players, rttMs, lossPct)
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
func (h *history) add(level int, b Bucket) {
	r := h.tiers[level]
	idx := b.T / r.resMs
	switch {
	case r.cur < 0:
		r.start(idx)
	case idx > r.cur:
		completed := r.buf[r.pos(r.cur)]
		r.start(idx)
		if level+1 < len(h.tiers) {
			h.add(level+1, completed)
		}
	case idx < r.cur:
		return // clock went backward; drop rather than corrupt the ring
	}
	r.mergeCur(b)
}

// History returns up to maxBuckets buckets covering the trailing windowMs
// (0 = everything the store has, from the daily tier). The newest bucket may
// still be in progress. This is the gateway-wide series.
func (s *Store) History(windowMs int64, maxBuckets int) HistoryResult {
	return s.historyAt(time.Now().UnixMilli(), windowMs, maxBuckets)
}

// historyAt is History on the global series with an injected clock (tests).
func (s *Store) historyAt(nowMs, windowMs int64, maxBuckets int) HistoryResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.global.historyAt(nowMs, windowMs, maxBuckets)
}

// AgentHistory is History scoped to one agent's series; an unknown agent (never
// sampled, or evicted from the LRU) yields an empty result.
func (s *Store) AgentHistory(agentID string, windowMs int64, maxBuckets int) HistoryResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.agents[agentID]
	if h == nil {
		return HistoryResult{Buckets: []Bucket{}}
	}
	return h.historyAt(time.Now().UnixMilli(), windowMs, maxBuckets)
}

// historyAt walks one history's rings; the caller holds s.mu.
func (h *history) historyAt(nowMs, windowMs int64, maxBuckets int) HistoryResult {
	if maxBuckets <= 0 || maxBuckets > maxHistoryBuckets {
		maxBuckets = maxHistoryBuckets
	}

	var (
		r                *ring
		startIdx, endIdx int64
	)
	if windowMs <= 0 {
		// All time: serve the daily tier from its oldest data.
		r = h.tiers[len(h.tiers)-1]
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
		for _, t := range h.tiers {
			if t.resMs*int64(len(t.buf)) >= windowMs {
				r = t
				break
			}
		}
		if r == nil {
			r = h.tiers[len(h.tiers)-1]
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
		mergeGauges(dst, b)
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
