package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"proxyforward/internal/analytics"
	"proxyforward/internal/engine"
	"proxyforward/internal/geo"
	"proxyforward/internal/ipc"
)

// analyticsCall runs one analytics op in whichever mode this GUI is in and
// unmarshals the result into out. Both modes go through the same JSON
// envelope so each binding is one small typed wrapper.
//
// Concurrency: callers must NOT hold a.mu. Engine-mode ops run lock-free
// against the store's read pool; attached-mode ops serialize on anMu over a
// dedicated pipe connection — either way a slow analytics query can never
// park the 2 Hz status tick behind a.mu. A daemon that answered with an
// error (ipc.OpError) is transient; only transport-level failures latch
// analyticsUnsupported.
func (a *App) analyticsCall(op string, req any, out any) error {
	var body json.RawMessage
	if req != nil {
		b, err := json.Marshal(req)
		if err != nil {
			return err
		}
		body = b
	}

	a.mu.Lock()
	mode, eng, latched := a.mode, a.eng, a.analyticsUnsupported
	a.mu.Unlock()

	var raw json.RawMessage
	switch mode {
	case ModeEngine:
		// The op layer reports store-unavailable itself; ops that don't need
		// the store (geo_status) still answer when the DB failed to open.
		if eng == nil {
			return errAnalyticsUnavailable
		}
		r, err := eng.AnalyticsOp(op, body)
		if err != nil {
			return err
		}
		raw = r
	case ModeAttached:
		if latched {
			return errAnalyticsUnavailable
		}
		r, err := a.attachedAnalytics(op, body)
		if err != nil {
			return err
		}
		raw = r
	default:
		return errAnalyticsUnavailable
	}
	if len(raw) == 0 || out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// attachedAnalytics runs one op over the dedicated analytics pipe, dialing
// it lazily. Dial failure means "unavailable right now" (the daemon may be
// restarting) — never a latch. A served OpError passes through untouched
// (the protocol works; the query failed). A transport failure drops the
// connection and latches unsupported.
func (a *App) attachedAnalytics(op string, body json.RawMessage) (json.RawMessage, error) {
	a.anMu.Lock()
	defer a.anMu.Unlock()

	a.mu.Lock()
	c := a.anClient
	a.mu.Unlock()
	if c == nil {
		c2, err := ipc.Dial(time.Second)
		if err != nil {
			return nil, errAnalyticsUnavailable
		}
		a.mu.Lock()
		if a.mode != ModeAttached {
			// Raced a mode change (engine start / shutdown); the reset owns
			// the state — don't resurrect the pipe behind its back.
			a.mu.Unlock()
			c2.Close()
			return nil, errAnalyticsUnavailable
		}
		a.anClient = c2
		a.mu.Unlock()
		c = c2
	}

	raw, err := c.Analytics(op, body)
	if err == nil {
		return raw, nil
	}
	var opErr *ipc.OpError
	if errors.As(err, &opErr) {
		return nil, err // daemon answered: transient, keep the pipe, no latch
	}
	// Transport/timeout/unknown-type: this pipe (or the daemon's protocol)
	// is broken. Drop the connection; latch only while still attached — a
	// concurrent reset owns the state otherwise.
	c.Close()
	a.mu.Lock()
	if a.anClient == c {
		a.anClient = nil
	}
	if a.mode == ModeAttached {
		a.logger.Warn("daemon does not serve analytics (older version?)", "op", op, "err", err)
		a.analyticsUnsupported = true
	}
	a.mu.Unlock()
	return nil, errAnalyticsUnavailable
}

var errAnalyticsUnavailable = fmt.Errorf("analytics unavailable")

// Players returns one page of the player wall. On any error (no store,
// detached, old daemon) it returns an empty page so the UI shows an empty
// state rather than surfacing an exception.
func (a *App) Players(q analytics.PlayersQuery) analytics.PlayersPage {
	var page analytics.PlayersPage
	if err := a.analyticsCall(engine.OpPlayers, q, &page); err != nil {
		return analytics.PlayersPage{Players: []analytics.PlayerCard{}}
	}
	if page.Players == nil {
		page.Players = []analytics.PlayerCard{}
	}
	return page
}

// PlayerDetail returns the full view for one player UUID (empty when unknown).
func (a *App) PlayerDetail(uuid string) analytics.PlayerDetail {
	var det analytics.PlayerDetail
	req := struct {
		UUID string `json:"uuid"`
	}{UUID: uuid}
	if err := a.analyticsCall(engine.OpPlayerDetail, req, &det); err != nil {
		return analytics.PlayerDetail{}
	}
	return det
}

// PlayerHistory returns the player's traffic over the trailing windowMs
// (0 = all), bucketed for charting.
func (a *App) PlayerHistory(uuid string, windowMs int64) []analytics.TrafficPoint {
	var pts []analytics.TrafficPoint
	req := struct {
		UUID     string `json:"uuid"`
		WindowMs int64  `json:"windowMs"`
	}{UUID: uuid, WindowMs: windowMs}
	if err := a.analyticsCall(engine.OpPlayerHistory, req, &pts); err != nil || pts == nil {
		return []analytics.TrafficPoint{}
	}
	return pts
}

// PlayerLatency returns the player's round-trip latency over the trailing
// windowMs (0 = all), bucketed for charting.
func (a *App) PlayerLatency(uuid string, windowMs int64) []analytics.LatencyPoint {
	var pts []analytics.LatencyPoint
	req := struct {
		UUID     string `json:"uuid"`
		WindowMs int64  `json:"windowMs"`
	}{UUID: uuid, WindowMs: windowMs}
	if err := a.analyticsCall(engine.OpPlayerLatency, req, &pts); err != nil || pts == nil {
		return []analytics.LatencyPoint{}
	}
	return pts
}

// Sessions returns one page of connection history.
func (a *App) Sessions(q analytics.SessionsQuery) analytics.SessionsPage {
	var page analytics.SessionsPage
	if err := a.analyticsCall(engine.OpSessions, q, &page); err != nil {
		return analytics.SessionsPage{Sessions: []analytics.SessionMeta{}}
	}
	if page.Sessions == nil {
		page.Sessions = []analytics.SessionMeta{}
	}
	return page
}

// SessionTimeline returns one connection's replay — bucketed traffic and RTT
// over the session's life. Empty on any error (no store, detached, old daemon).
func (a *App) SessionTimeline(id int64) analytics.SessionTimeline {
	req := struct {
		ID int64 `json:"id"`
	}{ID: id}
	var tl analytics.SessionTimeline
	if err := a.analyticsCall(engine.OpSessionTimeline, req, &tl); err != nil {
		return analytics.SessionTimeline{Traffic: []analytics.TrafficPoint{}, Rtt: []analytics.LatencyPoint{}}
	}
	if tl.Traffic == nil {
		tl.Traffic = []analytics.TrafficPoint{}
	}
	if tl.Rtt == nil {
		tl.Rtt = []analytics.LatencyPoint{}
	}
	return tl
}

// Summary returns the analytics dashboard tiles for the trailing rangeMs
// (0 = all time). Zero-value on any error (no store, detached, old daemon).
func (a *App) Summary(rangeMs int64) analytics.Summary {
	req := struct {
		RangeMs int64 `json:"rangeMs"`
	}{RangeMs: rangeMs}
	var sum analytics.Summary
	if err := a.analyticsCall(engine.OpSummary, req, &sum); err != nil {
		return analytics.Summary{}
	}
	return sum
}

// PeakMatrix returns the day-of-week × hour-of-day player heatmap over the
// trailing `weeks` weeks (0 = default).
func (a *App) PeakMatrix(weeks int) analytics.PeakMatrix {
	req := struct {
		Weeks int `json:"weeks"`
	}{Weeks: weeks}
	var m analytics.PeakMatrix
	if err := a.analyticsCall(engine.OpPeakMatrix, req, &m); err != nil {
		return analytics.PeakMatrix{}
	}
	return m
}

// TunnelUptime returns per-tunnel and control-link uptime over the trailing
// windowMs (0 = all time).
func (a *App) TunnelUptime(windowMs int64) analytics.UptimeReport {
	req := struct {
		WindowMs int64 `json:"windowMs"`
	}{WindowMs: windowMs}
	var rep analytics.UptimeReport
	if err := a.analyticsCall(engine.OpTunnelUptime, req, &rep); err != nil {
		return analytics.UptimeReport{Tunnels: []analytics.TunnelUptime{}}
	}
	if rep.Tunnels == nil {
		rep.Tunnels = []analytics.TunnelUptime{}
	}
	return rep
}

// GeoStatus reports which GeoLite2 databases are loaded, for the Settings
// badge. Zero-value (nothing loaded) on any error.
func (a *App) GeoStatus() geo.Status {
	var st geo.Status
	if err := a.analyticsCall(engine.OpGeoStatus, nil, &st); err != nil {
		return geo.Status{}
	}
	return st
}

// GeoSnapshot aggregates sessions by country over the trailing rangeMs
// (0 = all time), busiest countries first.
func (a *App) GeoSnapshot(rangeMs int64) []analytics.CountryAgg {
	req := struct {
		RangeMs int64 `json:"rangeMs"`
	}{RangeMs: rangeMs}
	var rows []analytics.CountryAgg
	if err := a.analyticsCall(engine.OpGeoSnapshot, req, &rows); err != nil || rows == nil {
		return []analytics.CountryAgg{}
	}
	return rows
}

// BrowseMMDB opens a file picker for a MaxMind .mmdb database and returns the
// chosen path ("" when cancelled).
func (a *App) BrowseMMDB(title string) (string, error) {
	return wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:   title,
		Filters: []wailsruntime.FileFilter{{DisplayName: "MaxMind database (*.mmdb)", Pattern: "*.mmdb"}},
	})
}
