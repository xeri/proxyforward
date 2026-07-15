// The Recorder turns live connection events into session history rows. It is
// driven by the conntrack hooks (open/close/player) plus a periodic live
// sample from the engine. All writes go through the DB's async batched writer
// so the splice goroutines never block on SQLite.
package analytics

import (
	"database/sql"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"proxyforward/internal/conntrack"
)

// Recorder records one engine's connection history. Session ids are assigned
// locally (not via SQLite autoincrement) so session_traffic / session_rtt can
// reference them without a synchronous round trip; the counter is seeded past
// any existing rows so ids stay unique across restarts.
type Recorder struct {
	db     *DB
	nextID atomic.Int64
	live   sync.Map // conntrack Entry.ID (uint64) -> *liveSession

	// geo enriches new sessions with country/network data; nil = disabled.
	// Fixed at construction (it is read from the writer goroutine unlocked).
	geo GeoResolver
}

type liveSession struct {
	id              int64
	lastIn, lastOut int64 // last sampled cumulative bytes, for deltas

	// RTT accumulation. rttMu guards these because RecordRTT (the RTT source
	// goroutine) and SessionClosed (the splice goroutine) both flush them.
	// bucket* accumulate the current minute; all* is the session-lifetime
	// running aggregate written to sessions.rtt_*.
	rttMu        sync.Mutex
	bucketMinute int64 // minute-truncated ms of the open bucket; 0 = none
	bucketSum    float64
	bucketMin    float64
	bucketMax    float64
	bucketN      int
	allSum       float64
	allMin       float64
	allMax       float64
	allN         int
}

// NewRecorder seeds the session-id counter from the database's high-water
// mark. g enriches new sessions with geo data (nil = disabled) and must be
// ready before traffic flows — it is fixed here so the writer goroutine can
// read it unlocked. Returns nil if d is nil (persistence unavailable) so
// callers can no-op cleanly.
func (d *DB) NewRecorder(g GeoResolver) *Recorder {
	if d == nil {
		return nil
	}
	r := &Recorder{db: d, geo: g}
	var maxID int64
	d.read.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM sessions`).Scan(&maxID)
	r.nextID.Store(maxID)
	return r
}

// SessionOpened inserts a session row for a newly registered connection.
func (r *Recorder) SessionOpened(e *conntrack.Entry) {
	if r == nil {
		return
	}
	id := r.nextID.Add(1)
	r.live.Store(e.ID, &liveSession{id: id})
	ip, port := splitAddr(e.ClientAddr)
	agentID, connKey, tunnelID, tunnelName := e.AgentID, e.ConnKey, e.TunnelID, e.TunnelName
	started := e.StartedAt.UnixMilli()
	r.db.Enqueue("session-open", func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO sessions
			(id, agent_id, conn_key, tunnel_id, tunnel_name, client_ip, client_port, started_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, agentID, connKey, tunnelID, tunnelName, ip, port, started); err != nil {
			return err
		}
		return r.stampGeo(tx, id, ip, started)
	})
}

// PlayerSeen records the sniffed name and protocol on the session. The
// authoritative UUID and player-table upsert are the identity service's job
// (Phase 3); here we persist what the login handshake directly told us.
func (r *Recorder) PlayerSeen(e *conntrack.Entry) {
	if r == nil {
		return
	}
	v, ok := r.live.Load(e.ID)
	if !ok {
		return
	}
	p := e.Player()
	if p == nil {
		return
	}
	id := v.(*liveSession).id
	name, proto := p.Name, p.Protocol
	r.db.Enqueue("session-player", func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sessions SET player_name = ?, protocol = ? WHERE id = ?`,
			name, proto, id)
		return err
	})
}

// SessionClosed stamps the end time and final byte totals.
func (r *Recorder) SessionClosed(e *conntrack.Entry, bytesIn, bytesOut int64) {
	if r == nil {
		return
	}
	v, ok := r.live.LoadAndDelete(e.ID)
	if !ok {
		return
	}
	ls := v.(*liveSession)
	// Flush any partial RTT minute so the session's final aggregate lands.
	ls.rttMu.Lock()
	if ls.bucketN > 0 {
		r.flushRTTBucketLocked(ls)
	}
	ls.rttMu.Unlock()
	id := ls.id
	ended := time.Now().UnixMilli()
	r.db.Enqueue("session-close", func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sessions SET ended_ms = ?, bytes_in = ?, bytes_out = ? WHERE id = ?`,
			ended, bytesIn, bytesOut, id)
		return err
	})
}

// RecordRTT accumulates one round-trip measurement (milliseconds) for a live
// connection into per-minute session_rtt rows and the session's running
// rtt_avg/min/max. Negative (unknown) samples and untracked connections are
// ignored. Called from the RTT source goroutine — the gateway's sampler or the
// agent's control loop.
func (r *Recorder) RecordRTT(e *conntrack.Entry, rttMs float64) {
	if r == nil || rttMs < 0 {
		return
	}
	v, ok := r.live.Load(e.ID)
	if !ok {
		return
	}
	ls := v.(*liveSession)
	now := time.Now().UnixMilli()
	minute := now - now%60_000

	ls.rttMu.Lock()
	defer ls.rttMu.Unlock()
	if ls.bucketMinute != minute {
		if ls.bucketN > 0 {
			r.flushRTTBucketLocked(ls) // completed minute → session_rtt + aggregate
		}
		ls.bucketMinute = minute
		ls.bucketSum, ls.bucketN = 0, 0
		ls.bucketMin, ls.bucketMax = rttMs, rttMs
	}
	ls.bucketSum += rttMs
	ls.bucketN++
	if rttMs < ls.bucketMin {
		ls.bucketMin = rttMs
	}
	if rttMs > ls.bucketMax {
		ls.bucketMax = rttMs
	}
	if ls.allN == 0 {
		ls.allMin, ls.allMax = rttMs, rttMs
	}
	ls.allSum += rttMs
	ls.allN++
	if rttMs < ls.allMin {
		ls.allMin = rttMs
	}
	if rttMs > ls.allMax {
		ls.allMax = rttMs
	}
}

// flushRTTBucketLocked enqueues the open minute bucket as a session_rtt row and
// refreshes the session's running rtt_* aggregate. Caller holds ls.rttMu and
// has ensured ls.bucketN > 0.
func (r *Recorder) flushRTTBucketLocked(ls *liveSession) {
	id := ls.id
	t := ls.bucketMinute
	avg := ls.bucketSum / float64(ls.bucketN)
	mn, mx, n := ls.bucketMin, ls.bucketMax, ls.bucketN
	allAvg := ls.allSum / float64(ls.allN)
	allMin, allMax, allN := ls.allMin, ls.allMax, ls.allN
	r.db.Enqueue("session-rtt", func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO session_rtt (session_id, t, avg, mn, mx, n)
			VALUES (?, ?, ?, ?, ?, ?)`, id, t, avg, mn, mx, n); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE sessions SET rtt_avg = ?, rtt_min = ?, rtt_max = ?, rtt_n = ? WHERE id = ?`,
			allAvg, allMin, allMax, allN, id)
		return err
	})
}

// SampleLive appends per-connection traffic deltas for the session-replay
// timeline. Called on the sampler goroutine at ~15 s cadence with the current
// live snapshot; idle connections (no delta) are skipped so the table stays
// sparse.
func (r *Recorder) SampleLive(snaps []conntrack.Snapshot) {
	if r == nil {
		return
	}
	now := time.Now().UnixMilli()
	for _, s := range snaps {
		v, ok := r.live.Load(s.ID)
		if !ok {
			continue
		}
		ls := v.(*liveSession)
		din := s.BytesIn - ls.lastIn
		dout := s.BytesOut - ls.lastOut
		ls.lastIn, ls.lastOut = s.BytesIn, s.BytesOut
		if din < 0 {
			din = 0
		}
		if dout < 0 {
			dout = 0
		}
		if din == 0 && dout == 0 {
			continue
		}
		id := ls.id
		r.db.Enqueue("session-traffic", func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT OR REPLACE INTO session_traffic (session_id, t, inb, outb)
				VALUES (?, ?, ?, ?)`, id, now, din, dout)
			return err
		})
	}
}

// splitAddr splits "ip:port" into its parts; a bare or malformed address
// keeps the whole string as the ip with port 0.
func splitAddr(addr string) (ip string, port int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	p, _ := strconv.Atoi(portStr)
	return host, p
}
