// Dashboard aggregates: the range summary tiles, the day-of-week × hour-of-day
// peak-hours matrix, and per-tunnel uptime. Bandwidth/gauge figures come from
// the rollup tables; session and player counts come from the sessions table
// (full retention, always current); uptime is reconstructed from the events
// journal. All are read-only and bounded well under the IPC frame.
package analytics

import (
	"database/sql"
	"time"
)

// MaxUptimeEvents caps the transition list returned per tunnel so the reply
// fits the IPC frame; the uptime percentage is computed over every event, not
// just the returned slice.
const MaxUptimeEvents = 100

// Summary is the analytics dashboard's stat-tile payload for one range. Gauge
// figures use -1 for "no data in range". The Lifetime* fields are filled by
// the engine from the live stats store, not by this query.
type Summary struct {
	RangeMs       int64   `json:"rangeMs"`
	BytesIn       int64   `json:"bytesIn"`
	BytesOut      int64   `json:"bytesOut"`
	Sessions      int     `json:"sessions"`
	UniquePlayers int     `json:"uniquePlayers"`
	PeakPlayers   float64 `json:"peakPlayers"`
	PeakPlayersAt int64   `json:"peakPlayersAt"`
	PeakInBps     float64 `json:"peakInBps"`
	PeakInAt      int64   `json:"peakInAt"`
	PeakOutBps    float64 `json:"peakOutBps"`
	PeakOutAt     int64   `json:"peakOutAt"`
	AvgRttMs      float64 `json:"avgRttMs"`
	AvgLossPct    float64 `json:"avgLossPct"`
	LinkUptimePct float64 `json:"linkUptimePct"`

	// All-time records (peaks table), independent of the selected range.
	RecInBps     float64 `json:"recInBps"`
	RecInAt      int64   `json:"recInAt"`
	RecOutBps    float64 `json:"recOutBps"`
	RecOutAt     int64   `json:"recOutAt"`
	RecPlayers   float64 `json:"recPlayers"`
	RecPlayersAt int64   `json:"recPlayersAt"`
	RecConns     float64 `json:"recConns"`
	RecConnsAt   int64   `json:"recConnsAt"`

	// Lifetime passthrough, filled by the engine from the live stats store.
	LifetimeBytesIn  int64 `json:"lifetimeBytesIn"`
	LifetimeBytesOut int64 `json:"lifetimeBytesOut"`
	LifetimeUptimeMs int64 `json:"lifetimeUptimeMs"`
	LinkSessions     int64 `json:"linkSessions"`
}

// Summary computes the range tiles. sinceMs 0 means all time; nowMs is the
// right edge (injected for tests).
func (d *DB) Summary(sinceMs, nowMs int64) (Summary, error) {
	s := Summary{
		PeakPlayers: -1, PeakInBps: 0, PeakOutBps: 0,
		AvgRttMs: -1, AvgLossPct: -1, LinkUptimePct: -1,
		RecPlayers: -1, RecConns: -1,
	}
	// Ranges longer than ~a week (30d, all-time) read the daily rollups —
	// hourly rows only live rollupHourlyRetention; short ranges read hourly.
	// Session and player counts share the same bucket-floored left edge as
	// the rollup reads so every tile in the row covers one window (the
	// boundary bucket whole).
	table, timeCol, bucketMs := "rollup_hourly", "hour_ms", hourMillis
	if sinceMs <= 0 || nowMs-sinceMs > 8*dayMillis {
		table, timeCol, bucketMs = "rollup_daily", "day_ms", dayMillis
	}
	sinceBucket := sinceMs / bucketMs * bucketMs

	if err := d.read.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT player_uuid)
		FROM sessions WHERE started_ms >= ?`, sinceBucket).Scan(&s.Sessions, &s.UniquePlayers); err != nil {
		return s, err
	}

	// Bandwidth/gauge tiles are gateway-wide: read the global rollup series
	// (agent_id '') so per-agent rows never double-count into the totals.
	if err := d.read.QueryRow(`SELECT
			COALESCE(SUM(bytes_in), 0), COALESCE(SUM(bytes_out), 0),
			COALESCE(AVG(CASE WHEN rtt_avg >= 0 THEN rtt_avg END), -1),
			COALESCE(AVG(CASE WHEN loss_avg >= 0 THEN loss_avg END), -1)
		FROM `+table+` WHERE `+timeCol+` >= ? AND agent_id = ''`, sinceBucket).
		Scan(&s.BytesIn, &s.BytesOut, &s.AvgRttMs, &s.AvgLossPct); err != nil {
		return s, err
	}

	// Range peaks with the bucket they occurred in (global series only).
	peakAt := func(col, guard string) (float64, int64, error) {
		var v float64
		var at int64
		err := d.read.QueryRow(`SELECT `+col+`, `+timeCol+` FROM `+table+`
			WHERE `+timeCol+` >= ? AND agent_id = '' `+guard+` ORDER BY `+col+` DESC, `+timeCol+` ASC LIMIT 1`, sinceBucket).Scan(&v, &at)
		if err == sql.ErrNoRows {
			return 0, 0, nil
		}
		return v, at, err
	}
	var err error
	if s.PeakInBps, s.PeakInAt, err = peakAt("peak_in_bps", ""); err != nil {
		return s, err
	}
	if s.PeakOutBps, s.PeakOutAt, err = peakAt("peak_out_bps", ""); err != nil {
		return s, err
	}
	// Players stays at the -1 sentinel unless a real reading was found; a
	// found row always carries a non-zero bucket key.
	if v, at, e := peakAt("peak_players", "AND peak_players >= 0"); e != nil {
		return s, e
	} else if at != 0 {
		s.PeakPlayers, s.PeakPlayersAt = v, at
	}

	if err := d.loadRecords(&s); err != nil {
		return s, err
	}

	events, err := d.loadEvents(EventLink, "")
	if err != nil {
		return s, err
	}
	engineEvents, err := d.loadEvents(EventEngine, "")
	if err != nil {
		return s, err
	}
	s.LinkUptimePct = computeUptimePct(events, engineCoverage(engineEvents, nowMs), sinceMs, nowMs)
	return s, nil
}

// loadRecords fills the all-time record fields from the peaks table.
func (d *DB) loadRecords(s *Summary) error {
	rows, err := d.read.Query(`SELECT key, value, at_ms FROM peaks`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var v float64
		var at int64
		if err := rows.Scan(&key, &v, &at); err != nil {
			return err
		}
		switch key {
		case "in_bps":
			s.RecInBps, s.RecInAt = v, at
		case "out_bps":
			s.RecOutBps, s.RecOutAt = v, at
		case "players":
			s.RecPlayers, s.RecPlayersAt = v, at
		case "conns":
			s.RecConns, s.RecConnsAt = v, at
		}
	}
	return rows.Err()
}

// PeakCell is one day-of-week × hour-of-day bucket of the peak-hours heatmap.
type PeakCell struct {
	Avg float64 `json:"avg"` // mean of hourly avg-players; -1 = no data
	Max float64 `json:"max"` // peak players observed; -1 = no data
}

// PeakMatrix is the [weekday][hour] grid; weekday 0 = Sunday, hours 0–23, in
// the engine's local time zone.
type PeakMatrix struct {
	Cells [7][24]PeakCell `json:"cells"`
}

// PeakMatrix aggregates the trailing `weeks` weeks (clamped to [1,12]) of
// hourly player rollups into the day-of-week × hour-of-day grid, bucketed in
// loc (the viewer's zone; the engine passes time.Local).
func (d *DB) PeakMatrix(weeks int, nowMs int64, loc *time.Location) (PeakMatrix, error) {
	if weeks <= 0 {
		weeks = 8
	}
	if weeks > 12 {
		weeks = 12
	}
	if loc == nil {
		loc = time.UTC
	}
	var m PeakMatrix
	for i := range m.Cells {
		for j := range m.Cells[i] {
			m.Cells[i][j] = PeakCell{Avg: -1, Max: -1}
		}
	}
	since := nowMs - int64(weeks)*7*dayMillis
	rows, err := d.read.Query(`SELECT hour_ms, avg_players, peak_players
		FROM rollup_hourly WHERE hour_ms >= ? AND agent_id = ''`, since)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	var sum [7][24]float64
	var n [7][24]int
	for rows.Next() {
		var hourStart int64
		var avg, peak float64
		if err := rows.Scan(&hourStart, &avg, &peak); err != nil {
			return m, err
		}
		if avg < 0 && peak < 0 {
			continue // no player data this hour
		}
		// Bucket by the UTC hour's midpoint so zones on :30/:45 offsets land
		// in the local hour that holds most of the data, not the one before.
		lt := time.UnixMilli(hourStart + hourMillis/2).In(loc)
		dow := int(lt.Weekday()) // 0 = Sunday
		hod := lt.Hour()
		c := &m.Cells[dow][hod]
		if avg >= 0 {
			sum[dow][hod] += avg
			n[dow][hod]++
		}
		if peak >= 0 && peak > c.Max {
			c.Max = peak
		}
	}
	if err := rows.Err(); err != nil {
		return m, err
	}
	for i := range m.Cells {
		for j := range m.Cells[i] {
			if n[i][j] > 0 {
				m.Cells[i][j].Avg = sum[i][j] / float64(n[i][j])
			}
		}
	}
	return m, nil
}

// UptimeSpan is one state transition on the uptime timeline.
type UptimeSpan struct {
	T  int64 `json:"t"`
	Up bool  `json:"up"`
}

// TunnelUptime is one row of the uptime report: the up-fraction over the
// window and the transitions that produced it. Name is filled by the engine.
type TunnelUptime struct {
	TunnelID  string       `json:"tunnelId"`
	Name      string       `json:"name"`
	UptimePct float64      `json:"uptimePct"` // -1 = no known state in window
	Events    []UptimeSpan `json:"events"`
}

// UptimeReport is the control link plus every tunnel's local-target uptime.
type UptimeReport struct {
	Link    TunnelUptime   `json:"link"`
	Tunnels []TunnelUptime `json:"tunnels"`
}

// TunnelUptime builds the uptime report over [sinceMs, nowMs] (sinceMs 0 = all
// time). Tunnel names are left blank for the engine to fill from live config.
func (d *DB) TunnelUptime(sinceMs, nowMs int64) (UptimeReport, error) {
	var rep UptimeReport
	engineEvents, err := d.loadEvents(EventEngine, "")
	if err != nil {
		return rep, err
	}
	cover := engineCoverage(engineEvents, nowMs)

	linkEvents, err := d.loadEvents(EventLink, "")
	if err != nil {
		return rep, err
	}
	rep.Link = TunnelUptime{
		UptimePct: computeUptimePct(linkEvents, cover, sinceMs, nowMs),
		Events:    windowSpans(linkEvents, sinceMs, nowMs),
	}

	ids, err := d.tunnelEventIDs()
	if err != nil {
		return rep, err
	}
	for _, id := range ids {
		ev, err := d.loadEvents(EventTunnelLocal, id)
		if err != nil {
			return rep, err
		}
		rep.Tunnels = append(rep.Tunnels, TunnelUptime{
			TunnelID:  id,
			UptimePct: computeUptimePct(ev, cover, sinceMs, nowMs),
			Events:    windowSpans(ev, sinceMs, nowMs),
		})
	}
	return rep, nil
}

// MaxUptimeTunnels caps how many tunnels the uptime report carries so the
// reply stays well inside the 64 KiB IPC frame (16 × MaxUptimeEvents spans
// ≈ 51 KiB worst case). The most recently active tunnels win.
const MaxUptimeTunnels = 16

// tunnelEventIDs lists the tunnel ids that have any local-health event,
// most recently active first, clamped to MaxUptimeTunnels.
func (d *DB) tunnelEventIDs() ([]string, error) {
	rows, err := d.read.Query(`SELECT tunnel_id FROM events
		WHERE kind = ? GROUP BY tunnel_id ORDER BY MAX(t) DESC LIMIT ?`,
		EventTunnelLocal, MaxUptimeTunnels)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type uptimeEvent struct {
	t  int64
	up bool
}

// loadEvents reads one kind/tunnel's transitions in chronological order.
func (d *DB) loadEvents(kind, tunnelID string) ([]uptimeEvent, error) {
	rows, err := d.read.Query(`SELECT t, up FROM events
		WHERE kind = ? AND tunnel_id = ? ORDER BY t ASC`, kind, tunnelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uptimeEvent
	for rows.Next() {
		var t int64
		var up int
		if err := rows.Scan(&t, &up); err != nil {
			return nil, err
		}
		out = append(out, uptimeEvent{t: t, up: up != 0})
	}
	return out, rows.Err()
}

// engineCoverage turns EventEngine transitions into the [start,end] intervals
// during which the engine was running. A trailing unmatched 'up' extends to
// now (the current run); a graceful 'down' closes its interval, so a gap
// before the next 'up' is left uncovered — the uptime query treats uncovered
// time as unknown rather than down. A crash (an 'up' with no 'down' before the
// next 'up') keeps its run covered to the next 'down'/now, so its gap is
// attributed to the last known state (documented limitation). When there are
// no engine events at all, coverage is the whole window (uptime is never
// penalised for lifecycle we never tracked).
func engineCoverage(events []uptimeEvent, nowMs int64) [][2]int64 {
	var out [][2]int64
	open := int64(-1)
	for _, e := range events {
		if e.up {
			if open < 0 {
				open = e.t
			}
		} else if open >= 0 {
			out = append(out, [2]int64{open, e.t})
			open = -1
		}
	}
	if open >= 0 {
		out = append(out, [2]int64{open, nowMs})
	}
	return out
}

// computeUptimePct returns the up-fraction (0–100) of known, engine-covered
// time within [since, now], or -1 when no state is known there.
func computeUptimePct(events []uptimeEvent, engineCover [][2]int64, since, now int64) float64 {
	if since <= 0 {
		since = 0
	}
	if now <= since {
		return -1
	}
	cover := engineCover
	if len(cover) == 0 {
		cover = [][2]int64{{since, now}}
	}
	var up, known int64
	for _, iv := range cover {
		a, b := max(iv[0], since), min(iv[1], now)
		u, k := upKnown(events, a, b)
		up += u
		known += k
	}
	if known == 0 {
		return -1
	}
	return float64(up) / float64(known) * 100
}

// upKnown accumulates up-time and known-time within [a,b] from the transition
// list: the state at any instant is the up-value of the last event at or
// before it; before the first event the state is unknown (contributes to
// neither total).
func upKnown(events []uptimeEvent, a, b int64) (up, known int64) {
	if a >= b {
		return 0, 0
	}
	state, ok := stateAt(events, a)
	cursor := a
	for _, e := range events {
		if e.t <= a || e.t >= b {
			continue
		}
		seg := e.t - cursor
		if ok {
			known += seg
			if state {
				up += seg
			}
		}
		cursor = e.t
		state, ok = e.up, true
	}
	seg := b - cursor
	if ok {
		known += seg
		if state {
			up += seg
		}
	}
	return up, known
}

// stateAt reports the state at t: the up-value of the latest event at or
// before t, ok=false when no event precedes t. events must be sorted ascending.
func stateAt(events []uptimeEvent, t int64) (up, ok bool) {
	for _, e := range events {
		if e.t <= t {
			up, ok = e.up, true
			continue
		}
		break
	}
	return up, ok
}

// windowSpans returns the transitions visible in [since, now]: a leading span
// at `since` carrying the state entering the window (when known), then each
// in-window transition, capped to the most recent MaxUptimeEvents.
func windowSpans(events []uptimeEvent, since, now int64) []UptimeSpan {
	spans := []UptimeSpan{}
	if since > 0 {
		if st, ok := stateAt(events, since); ok {
			spans = append(spans, UptimeSpan{T: since, Up: st})
		}
	}
	for _, e := range events {
		if (since <= 0 || e.t > since) && e.t <= now {
			spans = append(spans, UptimeSpan{T: e.t, Up: e.up})
		}
	}
	if len(spans) > MaxUptimeEvents {
		spans = spans[len(spans)-MaxUptimeEvents:]
	}
	return spans
}
