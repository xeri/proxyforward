// Read queries behind the analytics API. Aggregates (session counts,
// playtime, bytes) are computed from the sessions table at query time rather
// than denormalized, so they stay correct without careful increment
// bookkeeping. Every list result is clamped to fit the 64 KiB IPC frame.
package analytics

import (
	"database/sql"
	"strings"
	"time"
)

// Result-size clamps (well under the 64 KiB frame once JSON-encoded).
const (
	MaxPlayersPage   = 80
	MaxSessionsPage  = 100
	MaxHistoryPoints = 300
	MaxNameSpans     = 60
	MaxIPSpans       = 60
)

// PlayersQuery selects and pages the player wall.
type PlayersQuery struct {
	Search string `json:"search"`
	Sort   string `json:"sort"` // recent | name | playtime | sessions | data
	// AgentID scopes to one agent ("" = all agents). On a multi-agent gateway
	// two agents may share a TunnelID, so a tunnel-scoped wall should set both;
	// a bare TunnelID aggregates that tunnel across every agent.
	AgentID  string `json:"agentId"`
	TunnelID string `json:"tunnelId"` // "" = all tunnels
	CC       string `json:"cc"`       // "" = all countries
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

// PlayerCard is one wall tile / detail header.
type PlayerCard struct {
	UUID      string  `json:"uuid"`
	Name      string  `json:"name"`
	Offline   bool    `json:"offline"`
	Online    bool    `json:"online"`
	FirstSeen int64   `json:"firstSeen"`
	LastSeen  int64   `json:"lastSeen"`
	Sessions  int     `json:"sessions"`
	PlayMs    int64   `json:"playMs"`
	BytesIn   int64   `json:"bytesIn"`
	BytesOut  int64   `json:"bytesOut"`
	LastCC    string  `json:"lastCc"`
	RttMs     float64 `json:"rttMs"` // sample-weighted avg over the player's sessions; -1 if none
}

// PlayersPage is one page of the wall.
type PlayersPage struct {
	Total   int          `json:"total"`
	Players []PlayerCard `json:"players"`
}

// NameSpan / IPSpan are history rows on the detail view.
type NameSpan struct {
	Name      string `json:"name"`
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
}
type IPSpan struct {
	IP        string `json:"ip"`
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
	Sessions  int    `json:"sessions"`
	CC        string `json:"cc"`
}

// SessionMeta is one connection-history row.
type SessionMeta struct {
	ID         int64   `json:"id"`
	TunnelName string  `json:"tunnelName"`
	ClientIP   string  `json:"clientIp"`
	PlayerName string  `json:"playerName"`
	PlayerUUID string  `json:"playerUuid"` // "" when unidentified; drives the replay head
	StartedMs  int64   `json:"startedMs"`
	EndedMs    int64   `json:"endedMs"` // 0 = still live
	BytesIn    int64   `json:"bytesIn"`
	BytesOut   int64   `json:"bytesOut"`
	CC         string  `json:"cc"`
	RttAvg     float64 `json:"rttAvg"`
}

// PlayerDetail is the full per-player view.
type PlayerDetail struct {
	Card   PlayerCard    `json:"card"`
	Names  []NameSpan    `json:"names"`
	IPs    []IPSpan      `json:"ips"`
	Recent []SessionMeta `json:"recent"`
}

// rttAggSQL is the shared sample-weighted session RTT aggregate: each
// session's rtt_avg weighted by its sample count; -1 when no session in the
// group carries samples.
const rttAggSQL = `COALESCE(SUM(CASE WHEN s.rtt_n > 0 THEN s.rtt_avg * s.rtt_n END) /
			NULLIF(SUM(CASE WHEN s.rtt_n > 0 THEN s.rtt_n END), 0), -1)`

// Players returns one page of the wall. online is the live-connection key set
// — player UUIDs plus "name:<lower>" keys, from the caller's conntrack — used
// to flag currently-connected players and to sort them first.
func (d *DB) Players(q PlayersQuery, online map[string]bool, nowMs int64) (PlayersPage, error) {
	limit := q.Limit
	if limit <= 0 || limit > MaxPlayersPage {
		limit = MaxPlayersPage
	}
	where, args := playersFilter(q)

	var page PlayersPage
	if err := d.read.QueryRow(`SELECT COUNT(*) FROM players p `+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}

	order := "p.last_seen DESC"
	switch q.Sort {
	case "name":
		order = "p.name COLLATE NOCASE ASC"
	case "playtime":
		order = "play_ms DESC"
	case "sessions":
		order = "sessions DESC"
	case "data":
		// Total traffic either direction, tunnel-scoped like the other
		// aggregates (the join carries the tunnel filter).
		order = "(COALESCE(SUM(s.bytes_in), 0) + COALESCE(SUM(s.bytes_out), 0)) DESC"
	}
	// Currently-connected players float to the top whatever the chosen sort.
	prefix, orderArgs := onlineFirst(online)
	order = prefix + order

	// The aggregate join carries the agent+tunnel scope, so a scoped wall shows
	// scoped sessions/playtime/bytes/RTT rather than global figures next to a
	// scoped player list.
	qargs := []any{nowMs, q.AgentID, q.AgentID, q.TunnelID, q.TunnelID}
	qargs = append(qargs, args...)
	qargs = append(qargs, orderArgs...)
	qargs = append(qargs, limit, max(q.Offset, 0))
	rows, err := d.read.Query(`
		SELECT p.uuid, p.name, p.offline, p.first_seen, p.last_seen, p.last_cc,
			COUNT(s.id) AS sessions,
			COALESCE(SUM(COALESCE(s.ended_ms, ?) - s.started_ms), 0) AS play_ms,
			COALESCE(SUM(s.bytes_in), 0), COALESCE(SUM(s.bytes_out), 0),
			`+rttAggSQL+`
		FROM players p LEFT JOIN sessions s ON s.player_uuid = p.uuid
			AND (? = '' OR s.agent_id = ?) AND (? = '' OR s.tunnel_id = ?)
		`+where+`
		GROUP BY p.uuid
		ORDER BY `+order+`
		LIMIT ? OFFSET ?`, qargs...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCard(rows, online)
		if err != nil {
			return page, err
		}
		page.Players = append(page.Players, c)
	}
	return page, rows.Err()
}

// playersFilter builds the shared WHERE clause for the count and page
// queries. Tunnel and country membership share one EXISTS, so when both are
// set they must hold on the same session.
func playersFilter(q PlayersQuery) (string, []any) {
	var conds []string
	var args []any
	if s := strings.TrimSpace(q.Search); s != "" {
		conds = append(conds, "p.name LIKE ? COLLATE NOCASE")
		args = append(args, "%"+s+"%")
	}
	if q.AgentID != "" || q.TunnelID != "" || q.CC != "" {
		member := "s2.player_uuid = p.uuid"
		if q.AgentID != "" {
			member += " AND s2.agent_id = ?"
			args = append(args, q.AgentID)
		}
		if q.TunnelID != "" {
			member += " AND s2.tunnel_id = ?"
			args = append(args, q.TunnelID)
		}
		if q.CC != "" {
			member += " AND s2.cc = ?"
			args = append(args, q.CC)
		}
		conds = append(conds, "EXISTS (SELECT 1 FROM sessions s2 WHERE "+member+")")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// onlineFirst builds the ORDER BY prefix that floats live players to the top:
// membership of the row's UUID or name key in the online set. Each IN list is
// clamped to 200 keys — far beyond any realistic concurrent-player count, and
// exceeding it only costs sort position, never correctness.
func onlineFirst(online map[string]bool) (string, []any) {
	const maxKeys = 200
	var uuids, names []any
	for k, ok := range online {
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "name:") {
			if len(names) < maxKeys {
				names = append(names, k)
			}
		} else if len(uuids) < maxKeys {
			uuids = append(uuids, k)
		}
	}
	var conds []string
	var args []any
	if len(uuids) > 0 {
		conds = append(conds, "p.uuid IN ("+placeholders(len(uuids))+")")
		args = append(args, uuids...)
	}
	if len(names) > 0 {
		conds = append(conds, "'name:'||lower(p.name) IN ("+placeholders(len(names))+")")
		args = append(args, names...)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return "(" + strings.Join(conds, " OR ") + ") DESC, ", args
}

// placeholders renders n comma-separated SQL parameter marks.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

type scannable interface {
	Scan(dest ...any) error
}

func scanCard(row scannable, online map[string]bool) (PlayerCard, error) {
	var c PlayerCard
	var offline int
	if err := row.Scan(&c.UUID, &c.Name, &offline, &c.FirstSeen, &c.LastSeen, &c.LastCC,
		&c.Sessions, &c.PlayMs, &c.BytesIn, &c.BytesOut, &c.RttMs); err != nil {
		return c, err
	}
	c.Offline = offline != 0
	// The name key covers players whose handshake carried no usable UUID:
	// offline/cracked rows (their "offline:" key never matches a live
	// handshake UUID) and pre-1.19 clients.
	c.Online = online[c.UUID] || online["name:"+strings.ToLower(c.Name)]
	return c, nil
}

// PlayerDetail returns the full view for one player UUID. Its aggregates are
// intentionally global (never tunnel-scoped): the dossier is the player's
// whole record, while the wall reflects the active filter.
func (d *DB) PlayerDetail(uuid string, online map[string]bool, nowMs int64) (*PlayerDetail, error) {
	row := d.read.QueryRow(`
		SELECT p.uuid, p.name, p.offline, p.first_seen, p.last_seen, p.last_cc,
			COUNT(s.id) AS sessions,
			COALESCE(SUM(COALESCE(s.ended_ms, ?) - s.started_ms), 0),
			COALESCE(SUM(s.bytes_in), 0), COALESCE(SUM(s.bytes_out), 0),
			`+rttAggSQL+`
		FROM players p LEFT JOIN sessions s ON s.player_uuid = p.uuid
		WHERE p.uuid = ?
		GROUP BY p.uuid`, nowMs, uuid)
	card, err := scanCard(row, online)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	det := &PlayerDetail{Card: card}

	names, err := d.read.Query(`SELECT name, first_seen, last_seen FROM player_names
		WHERE uuid = ? ORDER BY last_seen DESC LIMIT ?`, uuid, MaxNameSpans)
	if err != nil {
		return nil, err
	}
	defer names.Close()
	for names.Next() {
		var n NameSpan
		if err := names.Scan(&n.Name, &n.FirstSeen, &n.LastSeen); err != nil {
			return nil, err
		}
		det.Names = append(det.Names, n)
	}
	names.Close()

	ips, err := d.read.Query(`SELECT pi.ip, pi.first_seen, pi.last_seen, pi.sessions,
			COALESCE((SELECT cc FROM geo_cache g WHERE g.ip = pi.ip), '')
		FROM player_ips pi WHERE pi.uuid = ? ORDER BY pi.last_seen DESC LIMIT ?`, uuid, MaxIPSpans)
	if err != nil {
		return nil, err
	}
	defer ips.Close()
	for ips.Next() {
		var s IPSpan
		if err := ips.Scan(&s.IP, &s.FirstSeen, &s.LastSeen, &s.Sessions, &s.CC); err != nil {
			return nil, err
		}
		det.IPs = append(det.IPs, s)
	}
	ips.Close()

	recent, err := d.Sessions(SessionsQuery{PlayerUUID: uuid, Limit: 25}, nowMs)
	if err != nil {
		return nil, err
	}
	det.Recent = recent.Sessions
	return det, nil
}

// SessionsQuery filters the connection-history table.
type SessionsQuery struct {
	PlayerUUID string `json:"playerUuid"`
	AgentID    string `json:"agentId"` // "" = all agents
	TunnelID   string `json:"tunnelId"`
	CC         string `json:"cc"` // "" = all countries
	SinceMs    int64  `json:"sinceMs"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
}

// SessionsPage is one page of connection history, newest first.
type SessionsPage struct {
	Total    int           `json:"total"`
	Sessions []SessionMeta `json:"sessions"`
}

// Sessions returns connection-history rows, newest first.
func (d *DB) Sessions(q SessionsQuery, nowMs int64) (SessionsPage, error) {
	limit := q.Limit
	if limit <= 0 || limit > MaxSessionsPage {
		limit = MaxSessionsPage
	}
	var conds []string
	var args []any
	if q.PlayerUUID != "" {
		conds = append(conds, "player_uuid = ?")
		args = append(args, q.PlayerUUID)
	}
	if q.AgentID != "" {
		conds = append(conds, "agent_id = ?")
		args = append(args, q.AgentID)
	}
	if q.TunnelID != "" {
		conds = append(conds, "tunnel_id = ?")
		args = append(args, q.TunnelID)
	}
	if q.CC != "" {
		conds = append(conds, "cc = ?")
		args = append(args, q.CC)
	}
	if q.SinceMs > 0 {
		conds = append(conds, "started_ms >= ?")
		args = append(args, q.SinceMs)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	var page SessionsPage
	if err := d.read.QueryRow(`SELECT COUNT(*) FROM sessions `+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := d.read.Query(`
		SELECT id, tunnel_name, client_ip, COALESCE(player_name, ''), COALESCE(player_uuid, ''),
			started_ms, COALESCE(ended_ms, 0), bytes_in, bytes_out, COALESCE(cc, ''), COALESCE(rtt_avg, 0)
		FROM sessions `+where+`
		ORDER BY started_ms DESC
		LIMIT ? OFFSET ?`, append(args, limit, max(q.Offset, 0))...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var s SessionMeta
		if err := rows.Scan(&s.ID, &s.TunnelName, &s.ClientIP, &s.PlayerName, &s.PlayerUUID, &s.StartedMs,
			&s.EndedMs, &s.BytesIn, &s.BytesOut, &s.CC, &s.RttAvg); err != nil {
			return page, err
		}
		page.Sessions = append(page.Sessions, s)
	}
	return page, rows.Err()
}

// PlayerHistory returns the player's traffic bucketed to at most
// MaxHistoryPoints points over the trailing windowMs (0 = all time). Deltas
// come from the session_traffic samples of the player's sessions.
func (d *DB) PlayerHistory(uuid string, windowMs, nowMs int64) ([]TrafficPoint, error) {
	from := int64(0)
	if windowMs > 0 {
		from = nowMs - windowMs
	}
	// Pick a bucket width that keeps the series under the point cap.
	span := nowMs - from
	if from == 0 {
		span = windowMs // 'all' still needs a floor; refined below
	}
	rows, err := d.read.Query(`
		SELECT st.t, st.inb, st.outb
		FROM session_traffic st
		JOIN sessions s ON s.id = st.session_id
		WHERE s.player_uuid = ? AND st.t >= ?
		ORDER BY st.t`, uuid, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []TrafficPoint
	for rows.Next() {
		var p TrafficPoint
		if err := rows.Scan(&p.T, &p.In, &p.Out); err != nil {
			return nil, err
		}
		samples = append(samples, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bucketTraffic(samples, span, nowMs), nil
}

// TrafficPoint is one time bucket of session traffic bytes.
type TrafficPoint struct {
	T   int64 `json:"t"`
	In  int64 `json:"in"`
	Out int64 `json:"out"`
}

// LatencyPoint is one time bucket of round-trip latency in milliseconds.
type LatencyPoint struct {
	T   int64   `json:"t"`
	Avg float64 `json:"avg"`
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// rttSample is one session_rtt row before bucketing; n weights the average.
type rttSample struct {
	t           int64
	avg, mn, mx float64
	n           int
}

// PlayerLatency returns the player's round-trip latency bucketed to at most
// MaxHistoryPoints points over the trailing windowMs (0 = all time), drawn from
// the per-minute session_rtt aggregates of the player's sessions.
func (d *DB) PlayerLatency(uuid string, windowMs, nowMs int64) ([]LatencyPoint, error) {
	from := int64(0)
	if windowMs > 0 {
		from = nowMs - windowMs
	}
	span := nowMs - from
	if from == 0 {
		span = windowMs
	}
	rows, err := d.read.Query(`
		SELECT sr.t, sr.avg, sr.mn, sr.mx, sr.n
		FROM session_rtt sr
		JOIN sessions s ON s.id = sr.session_id
		WHERE s.player_uuid = ? AND sr.t >= ?
		ORDER BY sr.t`, uuid, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []rttSample
	for rows.Next() {
		var s rttSample
		if err := rows.Scan(&s.t, &s.avg, &s.mn, &s.mx, &s.n); err != nil {
			return nil, err
		}
		samples = append(samples, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bucketLatency(samples, span, nowMs), nil
}

// bucketLatency groups per-minute RTT rows into at most MaxHistoryPoints
// buckets: the average is sample-count-weighted, the min/max carry through.
func bucketLatency(samples []rttSample, span, nowMs int64) []LatencyPoint {
	if len(samples) == 0 {
		return []LatencyPoint{}
	}
	if span <= 0 {
		span = nowMs - samples[0].t
	}
	bucketMs := span/int64(MaxHistoryPoints-1) + 1
	if bucketMs < int64(time.Minute/time.Millisecond) {
		bucketMs = int64(time.Minute / time.Millisecond) // session_rtt is per-minute
	}
	out := make([]LatencyPoint, 0, MaxHistoryPoints)
	var (
		curBucket = int64(-1)
		wsum      float64 // Σ avg*n
		wn        int     // Σ n
		mn, mx    float64
		t         int64
	)
	flush := func() {
		if wn == 0 {
			return
		}
		out = append(out, LatencyPoint{T: t, Avg: wsum / float64(wn), Min: mn, Max: mx})
	}
	for _, s := range samples {
		b := s.t / bucketMs
		if b != curBucket {
			flush()
			curBucket = b
			t, wsum, wn, mn, mx = b*bucketMs, 0, 0, s.mn, s.mx
		}
		wsum += s.avg * float64(s.n)
		wn += s.n
		if s.mn < mn {
			mn = s.mn
		}
		if s.mx > mx {
			mx = s.mx
		}
	}
	flush()
	return out
}

// SessionTimeline is one connection's replay: bytes moved and round-trip
// latency across the life of the session, each bucketed to fit the frame.
type SessionTimeline struct {
	Traffic []TrafficPoint `json:"traffic"`
	Rtt     []LatencyPoint `json:"rtt"`
}

// SessionTimeline returns the traffic and RTT samples recorded for one session,
// each bucketed to at most MaxHistoryPoints points (a long session can hold
// more 15 s samples than the frame allows). Empty slices when the session left
// no samples (very short-lived, or a build without per-connection RTT).
func (d *DB) SessionTimeline(sessionID, nowMs int64) (SessionTimeline, error) {
	out := SessionTimeline{Traffic: []TrafficPoint{}, Rtt: []LatencyPoint{}}

	trows, err := d.read.Query(`SELECT t, inb, outb FROM session_traffic
		WHERE session_id = ? ORDER BY t`, sessionID)
	if err != nil {
		return out, err
	}
	defer trows.Close()
	var traffic []TrafficPoint
	for trows.Next() {
		var p TrafficPoint
		if err := trows.Scan(&p.T, &p.In, &p.Out); err != nil {
			return out, err
		}
		traffic = append(traffic, p)
	}
	if err := trows.Err(); err != nil {
		return out, err
	}
	if len(traffic) > 0 {
		out.Traffic = bucketTraffic(traffic, traffic[len(traffic)-1].T-traffic[0].T, nowMs)
	}

	rrows, err := d.read.Query(`SELECT t, avg, mn, mx, n FROM session_rtt
		WHERE session_id = ? ORDER BY t`, sessionID)
	if err != nil {
		return out, err
	}
	defer rrows.Close()
	var samples []rttSample
	for rrows.Next() {
		var s rttSample
		if err := rrows.Scan(&s.t, &s.avg, &s.mn, &s.mx, &s.n); err != nil {
			return out, err
		}
		samples = append(samples, s)
	}
	if err := rrows.Err(); err != nil {
		return out, err
	}
	if len(samples) > 0 {
		out.Rtt = bucketLatency(samples, samples[len(samples)-1].t-samples[0].t, nowMs)
	}
	return out, nil
}

// bucketTraffic groups raw samples into at most MaxHistoryPoints time buckets.
func bucketTraffic(samples []TrafficPoint, span, nowMs int64) []TrafficPoint {
	if len(samples) == 0 {
		return []TrafficPoint{}
	}
	if span <= 0 {
		span = nowMs - samples[0].T
	}
	// Divide by cap-1: unaligned sample times straddle one extra bucket, so a
	// full window would otherwise emit cap+1 points.
	bucketMs := span/int64(MaxHistoryPoints-1) + 1
	if bucketMs < int64(15*time.Second/time.Millisecond) {
		bucketMs = int64(15 * time.Second / time.Millisecond) // sample cadence floor
	}
	out := make([]TrafficPoint, 0, MaxHistoryPoints)
	var cur TrafficPoint
	curBucket := int64(-1)
	for _, s := range samples {
		b := s.T / bucketMs
		if b != curBucket {
			if curBucket >= 0 {
				out = append(out, cur)
			}
			cur = TrafficPoint{T: b * bucketMs}
			curBucket = b
		}
		cur.In += s.In
		cur.Out += s.Out
	}
	out = append(out, cur)
	return out
}
