package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"proxyforward/internal/analytics"
	"proxyforward/internal/ipc"
)

// Analytics op names. These are the wire contract between the GUI and the
// daemon; keep them stable.
const (
	OpPlayers         = "players"
	OpPlayerDetail    = "player_detail"
	OpPlayerHistory   = "player_history"
	OpPlayerLatency   = "player_latency"
	OpSessions        = "sessions"
	OpSessionTimeline = "session_timeline"
	OpGeoStatus       = "geo_status"
	OpGeoSnapshot     = "geo_snapshot"
	OpSummary         = "summary"
	OpPeakMatrix      = "peak_matrix"
	OpTunnelUptime    = "tunnel_uptime"
)

// analyticsOp runs one query by name against the store, returning the
// JSON-encoded result. It is the single dispatch point for both the in-process
// GUI (engine mode) and attached GUIs (over the IPC envelope), so every op is
// defined exactly once.
func (e *Engine) analyticsOp(op string, body json.RawMessage) (json.RawMessage, error) {
	// geo_status describes the resolver, not the store — it must answer even
	// when the database failed to open (the Settings badge relies on it).
	if op == OpGeoStatus {
		return encodeResult(e.geo.Status(), nil)
	}
	if e.DB == nil {
		return nil, fmt.Errorf("analytics store unavailable")
	}
	now := time.Now().UnixMilli()
	switch op {
	case OpPlayers:
		var q analytics.PlayersQuery
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		page, err := e.DB.Players(q, e.livePlayerKeys(), now)
		return encodeResult(page, err)
	case OpPlayerDetail:
		var q struct {
			UUID string `json:"uuid"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		det, err := e.DB.PlayerDetail(q.UUID, e.livePlayerKeys(), now)
		if err != nil {
			return nil, err
		}
		if det == nil {
			det = &analytics.PlayerDetail{}
		}
		return encodeResult(det, nil)
	case OpPlayerHistory:
		var q struct {
			UUID     string `json:"uuid"`
			WindowMs int64  `json:"windowMs"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		pts, err := e.DB.PlayerHistory(q.UUID, q.WindowMs, now)
		return encodeResult(pts, err)
	case OpPlayerLatency:
		var q struct {
			UUID     string `json:"uuid"`
			WindowMs int64  `json:"windowMs"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		pts, err := e.DB.PlayerLatency(q.UUID, q.WindowMs, now)
		return encodeResult(pts, err)
	case OpSessions:
		var q analytics.SessionsQuery
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		page, err := e.DB.Sessions(q, now)
		return encodeResult(page, err)
	case OpSessionTimeline:
		var q struct {
			ID int64 `json:"id"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		tl, err := e.DB.SessionTimeline(q.ID, now)
		return encodeResult(tl, err)
	case OpGeoSnapshot:
		var q struct {
			RangeMs int64 `json:"rangeMs"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		since := int64(0)
		if q.RangeMs > 0 {
			since = now - q.RangeMs
		}
		rows, err := e.DB.GeoSnapshot(since)
		if rows == nil {
			rows = []analytics.CountryAgg{}
		}
		return encodeResult(rows, err)
	case OpSummary:
		var q struct {
			RangeMs int64 `json:"rangeMs"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		since := int64(0)
		if q.RangeMs > 0 {
			since = now - q.RangeMs
		}
		sum, err := e.DB.Summary(since, now)
		if err != nil {
			return nil, err
		}
		sum.RangeMs = q.RangeMs
		life := e.Stats.Lifetime()
		sum.LifetimeBytesIn, sum.LifetimeBytesOut = life.BytesIn, life.BytesOut
		sum.LifetimeUptimeMs = life.UptimeMs
		sum.LinkSessions = life.LinkSessions
		return encodeResult(sum, nil)
	case OpPeakMatrix:
		var q struct {
			Weeks int `json:"weeks"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		m, err := e.DB.PeakMatrix(q.Weeks, now, time.Local)
		return encodeResult(m, err)
	case OpTunnelUptime:
		var q struct {
			WindowMs int64 `json:"windowMs"`
		}
		if err := decodeBody(body, &q); err != nil {
			return nil, err
		}
		since := int64(0)
		if q.WindowMs > 0 {
			since = now - q.WindowMs
		}
		rep, err := e.DB.TunnelUptime(since, now)
		if err != nil {
			return nil, err
		}
		e.nameTunnelUptime(&rep)
		return encodeResult(rep, nil)
	default:
		return nil, fmt.Errorf("unknown analytics op %q", op)
	}
}

// AnalyticsOp is the engine-mode entry point (the GUI running in-process).
func (e *Engine) AnalyticsOp(op string, body json.RawMessage) (json.RawMessage, error) {
	return e.analyticsOp(op, body)
}

// AnalyticsReady reports whether the analytics store opened — the in-process
// analogue of the attached-mode "unsupported daemon" latch, so the UI can
// show one honest empty state either way.
func (e *Engine) AnalyticsReady() bool { return e.DB != nil }

// analyticsSource adapts analyticsOp to the IPC envelope.
func (e *Engine) analyticsSource(req ipc.AnalyticsReq) ipc.AnalyticsResp {
	out, err := e.analyticsOp(req.Op, req.Body)
	if err != nil {
		return ipc.AnalyticsResp{Err: err.Error()}
	}
	return ipc.AnalyticsResp{Body: out}
}

// nameTunnelUptime fills each row's display name from live config and appends
// configured tunnels that have no events yet as unknown rows, so the report
// lists every tunnel the user has, not only those that have flapped.
func (e *Engine) nameTunnelUptime(rep *analytics.UptimeReport) {
	rep.Link.Name = "Control link"
	names := map[string]string{}
	var order []string
	switch {
	case e.Agent != nil:
		for _, t := range e.Agent.Tunnels() {
			names[t.ID] = t.Name
			order = append(order, t.ID)
		}
	case e.Gateway != nil:
		for _, t := range e.Gateway.Tunnels() {
			names[t.ID] = t.Name
			order = append(order, t.ID)
		}
	}
	seen := map[string]bool{}
	for i := range rep.Tunnels {
		id := rep.Tunnels[i].TunnelID
		seen[id] = true
		if n := names[id]; n != "" {
			rep.Tunnels[i].Name = n
		} else {
			rep.Tunnels[i].Name = id
		}
	}
	for _, id := range order {
		if seen[id] {
			continue
		}
		rep.Tunnels = append(rep.Tunnels, analytics.TunnelUptime{
			TunnelID:  id,
			Name:      names[id],
			UptimePct: -1,
			Events:    []analytics.UptimeSpan{},
		})
	}
}

// livePlayerKeys is the set of currently-connected player identity keys —
// each live conn contributes its handshake UUID and a "name:<lower>" key.
// The name key is what flags offline/cracked players (their stored
// "offline:" UUID never matches a handshake UUID) and pre-1.19 clients (no
// handshake UUID at all) as online.
func (e *Engine) livePlayerKeys() map[string]bool {
	out := map[string]bool{}
	for _, c := range e.conns().Snapshot() {
		if c.PlayerUUID != "" {
			out[c.PlayerUUID] = true
		}
		if c.PlayerName != "" {
			out["name:"+strings.ToLower(c.PlayerName)] = true
		}
	}
	return out
}

func decodeBody(body json.RawMessage, v any) error {
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

func encodeResult(v any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
