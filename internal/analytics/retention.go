// Retention: a daily prune run by the writer goroutine. Session-grade rows
// expire after Options.RetentionDays; the geo cache turns over monthly so a
// reassigned IP does not keep a stale country forever. Hourly rollups are
// pruned at 90 days (the daily table carries the long tail); daily rollups
// and peaks are tiny (a row a day) and are kept indefinitely. Expiring events
// leave one synthetic carrier row per (kind, tunnel) at the cutoff so
// all-time uptime keeps its last-known state instead of shifting as
// transitions age out.
package analytics

import (
	"time"
)

const (
	geoCacheTTL = 30 * 24 * time.Hour

	// rollup_hourly horizon; ranges longer than ~a week read rollup_daily,
	// which is never pruned.
	rollupHourlyRetention = 90 * 24 * time.Hour
)

func (d *DB) prune(now time.Time) {
	cutoff := now.AddDate(0, 0, -d.retentionDays).UnixMilli()
	geoCutoff := now.Add(-geoCacheTTL).UnixMilli()
	hourlyCutoff := now.Add(-rollupHourlyRetention).UnixMilli()

	tx, err := d.sql.Begin()
	if err != nil {
		d.logger.Warn("analytics: prune begin failed", "err", err)
		return
	}
	// Only ended sessions expire; a (pathological) still-open row is live
	// state, not history. The orphan sweeps run after the sessions delete so
	// they collect both the rows freed by it and any rows whose parent was
	// lost earlier (e.g. a session-open op dropped under queue pressure).
	steps := []struct {
		name string
		q    string
		args []any
	}{
		{"sessions", `DELETE FROM sessions WHERE started_ms < ? AND ended_ms IS NOT NULL`, []any{cutoff}},
		{"session_traffic orphans", `DELETE FROM session_traffic WHERE NOT EXISTS
			(SELECT 1 FROM sessions WHERE id = session_id)`, nil},
		{"session_rtt orphans", `DELETE FROM session_rtt WHERE NOT EXISTS
			(SELECT 1 FROM sessions WHERE id = session_id)`, nil},
		// Before expiring events, park each (kind, tunnel)'s last pre-cutoff
		// state on a synthetic row at the cutoff: uptime over any window then
		// still knows the state entering it. (Bare `up` rides SQLite's
		// MAX()-row rule in the subquery.)
		{"event carriers", `INSERT INTO events (t, kind, tunnel_id, up)
			SELECT ?, kind, tunnel_id, up FROM
				(SELECT kind, tunnel_id, up, MAX(t) FROM events WHERE t < ? GROUP BY kind, tunnel_id)`,
			[]any{cutoff, cutoff}},
		{"events", `DELETE FROM events WHERE t < ?`, []any{cutoff}},
		{"geo_cache", `DELETE FROM geo_cache WHERE resolved_ms < ?`, []any{geoCutoff}},
		{"rollup_hourly", `DELETE FROM rollup_hourly WHERE hour_ms < ?`, []any{hourlyCutoff}},
	}
	for _, s := range steps {
		if _, err := tx.Exec(s.q, s.args...); err != nil {
			tx.Rollback()
			d.logger.Warn("analytics: prune failed", "table", s.name, "err", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		d.logger.Warn("analytics: prune commit failed", "err", err)
		return
	}
	// Hand freed pages back a few at a time; outside the transaction by
	// SQLite's rules.
	if _, err := d.sql.Exec("PRAGMA incremental_vacuum(256)"); err != nil {
		d.logger.Debug("analytics: incremental vacuum failed", "err", err)
	}
}
