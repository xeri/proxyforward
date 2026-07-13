// Rollups and all-time peaks. A writer-goroutine job folds the fine-grained
// rrd tier-2 buckets (15 s) and the sessions table into hourly and daily
// aggregates, and refreshes the all-time record table. It runs on start
// (catch-up) and on a short ticker; every write is idempotent
// (INSERT OR REPLACE by time key) so re-running a partial hour only sharpens
// it.
//
// Hour/day keys are UTC bucket starts (the frontend renders them in local
// time). A fully-lapped hour is never re-rolled — the rrd-derived pass only
// emits hours still present in the tier-2 window, so a finalized value is
// preserved once its buckets scroll out. The single hour at the trailing edge
// of the window (partially lapped) is excluded via firstFullHour so it can
// never be under-counted.
package analytics

import (
	"database/sql"
	"time"
)

const (
	rollupEvery = 5 * time.Minute
	hourMillis      = int64(time.Hour / time.Millisecond)
	dayMillis       = int64(24 * time.Hour / time.Millisecond)
)

// runRollup refreshes the hourly/daily rollups and the peaks table up to now.
// Runs on the writer goroutine (single connection), so it shares SQLite with
// the batched writes and the stats flush by the busy_timeout, exactly like
// prune.
func (d *DB) runRollup(now time.Time) {
	nowMs := now.UnixMilli()
	curHour := nowMs / hourMillis * hourMillis
	curHourEnd := curHour + hourMillis

	// Roll from the first hour still fully present in tier-2 (the trailing
	// partial hour is excluded); a fresh/empty rrd falls back to the current
	// hour so live activity still lands.
	var minT sql.NullInt64
	if err := d.sql.QueryRow(`SELECT MIN(t) FROM rrd WHERE tier = 2`).Scan(&minT); err != nil {
		d.logger.Warn("analytics: rollup min scan failed", "err", err)
		return
	}
	rollFrom := curHour
	if minT.Valid {
		firstFull := (minT.Int64 + hourMillis - 1) / hourMillis * hourMillis
		if firstFull < rollFrom {
			rollFrom = firstFull
		}
	}
	dayFrom := rollFrom / dayMillis * dayMillis
	dayEnd := (curHour/dayMillis + 1) * dayMillis

	tx, err := d.sql.Begin()
	if err != nil {
		d.logger.Warn("analytics: rollup begin failed", "err", err)
		return
	}
	defer tx.Rollback()

	steps := []struct {
		name string
		q    string
		args []any
	}{
		// Hourly bandwidth/gauge aggregates from tier-2 rrd.
		{"rollup_hourly rrd", `
			INSERT INTO rollup_hourly (hour_ms, bytes_in, bytes_out, peak_in_bps, peak_out_bps,
				peak_players, avg_players, rtt_avg, loss_avg)
			SELECT t / ? * ? AS h,
				COALESCE(SUM(inb), 0), COALESCE(SUM(outb), 0),
				COALESCE(MAX(ih), 0), COALESCE(MAX(oh), 0),
				COALESCE(MAX(CASE WHEN pc >= 0 THEN ph END), -1),
				COALESCE(AVG(CASE WHEN pc >= 0 THEN pc END), -1),
				COALESCE(AVG(CASE WHEN rc >= 0 THEN rc END), -1),
				COALESCE(AVG(CASE WHEN lc >= 0 THEN lc END), -1)
			FROM rrd WHERE tier = 2 AND t >= ? AND t < ?
			GROUP BY h
			ON CONFLICT(hour_ms) DO UPDATE SET
				bytes_in = excluded.bytes_in, bytes_out = excluded.bytes_out,
				peak_in_bps = excluded.peak_in_bps, peak_out_bps = excluded.peak_out_bps,
				peak_players = excluded.peak_players, avg_players = excluded.avg_players,
				rtt_avg = excluded.rtt_avg, loss_avg = excluded.loss_avg`,
			[]any{hourMillis, hourMillis, rollFrom, curHourEnd}},
		// Hourly session counts from the sessions table.
		{"rollup_hourly sessions", `
			INSERT INTO rollup_hourly (hour_ms, sessions, unique_players)
			SELECT started_ms / ? * ? AS h, COUNT(*), COUNT(DISTINCT player_uuid)
			FROM sessions WHERE started_ms >= ? AND started_ms < ?
			GROUP BY h
			ON CONFLICT(hour_ms) DO UPDATE SET
				sessions = excluded.sessions, unique_players = excluded.unique_players`,
			[]any{hourMillis, hourMillis, rollFrom, curHourEnd}},
		// Daily aggregates rolled up from the hourly rows (whole days recomputed).
		{"rollup_daily hourly", `
			INSERT INTO rollup_daily (day_ms, bytes_in, bytes_out, peak_in_bps, peak_out_bps,
				peak_players, avg_players, rtt_avg, loss_avg)
			SELECT hour_ms / ? * ? AS d,
				SUM(bytes_in), SUM(bytes_out), MAX(peak_in_bps), MAX(peak_out_bps),
				COALESCE(MAX(CASE WHEN peak_players >= 0 THEN peak_players END), -1),
				COALESCE(AVG(CASE WHEN avg_players >= 0 THEN avg_players END), -1),
				COALESCE(AVG(CASE WHEN rtt_avg >= 0 THEN rtt_avg END), -1),
				COALESCE(AVG(CASE WHEN loss_avg >= 0 THEN loss_avg END), -1)
			FROM rollup_hourly WHERE hour_ms >= ? AND hour_ms < ?
			GROUP BY d
			ON CONFLICT(day_ms) DO UPDATE SET
				bytes_in = excluded.bytes_in, bytes_out = excluded.bytes_out,
				peak_in_bps = excluded.peak_in_bps, peak_out_bps = excluded.peak_out_bps,
				peak_players = excluded.peak_players, avg_players = excluded.avg_players,
				rtt_avg = excluded.rtt_avg, loss_avg = excluded.loss_avg`,
			[]any{dayMillis, dayMillis, dayFrom, dayEnd}},
		{"rollup_daily sessions", `
			INSERT INTO rollup_daily (day_ms, sessions, unique_players)
			SELECT started_ms / ? * ? AS d, COUNT(*), COUNT(DISTINCT player_uuid)
			FROM sessions WHERE started_ms >= ? AND started_ms < ?
			GROUP BY d
			ON CONFLICT(day_ms) DO UPDATE SET
				sessions = excluded.sessions, unique_players = excluded.unique_players`,
			[]any{dayMillis, dayMillis, dayFrom, dayEnd}},
	}
	for _, s := range steps {
		if _, err := tx.Exec(s.q, s.args...); err != nil {
			d.logger.Warn("analytics: rollup step failed", "step", s.name, "err", err)
			return
		}
	}
	if err := rollupPeaks(tx); err != nil {
		d.logger.Warn("analytics: rollup peaks failed", "err", err)
		return
	}
	if err := tx.Commit(); err != nil {
		d.logger.Warn("analytics: rollup commit failed", "err", err)
	}
}

// rerollSessionRollups re-runs the sessions legs of the hourly and daily
// rollups for the buckets touching [fromMs, toMs]. Late identity resolution
// (session backfill, offline reconciliation) changes historical
// unique-player counts; this corrects the frozen aggregates. Buckets older
// than the session retention window are left alone — their source rows are
// gone, so a re-roll would erase real history.
func (d *DB) rerollSessionRollups(tx *sql.Tx, fromMs, toMs, nowMs int64) error {
	floor := nowMs - int64(d.retentionDays)*dayMillis
	if fromMs < floor {
		fromMs = floor
	}
	if toMs < fromMs {
		return nil
	}
	steps := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO rollup_hourly (hour_ms, sessions, unique_players)
			SELECT started_ms / ? * ? AS h, COUNT(*), COUNT(DISTINCT player_uuid)
			FROM sessions WHERE started_ms >= ? AND started_ms < ?
			GROUP BY h
			ON CONFLICT(hour_ms) DO UPDATE SET
				sessions = excluded.sessions, unique_players = excluded.unique_players`,
			[]any{hourMillis, hourMillis, fromMs / hourMillis * hourMillis, toMs/hourMillis*hourMillis + hourMillis}},
		{`INSERT INTO rollup_daily (day_ms, sessions, unique_players)
			SELECT started_ms / ? * ? AS d, COUNT(*), COUNT(DISTINCT player_uuid)
			FROM sessions WHERE started_ms >= ? AND started_ms < ?
			GROUP BY d
			ON CONFLICT(day_ms) DO UPDATE SET
				sessions = excluded.sessions, unique_players = excluded.unique_players`,
			[]any{dayMillis, dayMillis, fromMs / dayMillis * dayMillis, toMs/dayMillis*dayMillis + dayMillis}},
	}
	for _, s := range steps {
		if _, err := tx.Exec(s.q, s.args...); err != nil {
			return err
		}
	}
	return nil
}

// peakSpec maps an all-time record key to the tier-2 column carrying its high
// and the guard that marks a real reading (gauges use -1 for unknown).
type peakSpec struct {
	key   string
	col   string
	guard string // extra WHERE, "" for none
}

var peakSpecs = []peakSpec{
	{"in_bps", "ih", ""},
	{"out_bps", "oh", ""},
	{"conns", "ch", "AND cc >= 0"},
	{"players", "ph", "AND pc >= 0"},
}

// rollupPeaks refreshes the all-time record table from the tier-2 window. Each
// key keeps its record only if the window's best beats the stored value, so a
// record older than the window survives untouched.
func rollupPeaks(tx *sql.Tx) error {
	for _, ps := range peakSpecs {
		var t int64
		var v float64
		err := tx.QueryRow(`SELECT t, `+ps.col+` FROM rrd
			WHERE tier = 2 `+ps.guard+`
			ORDER BY `+ps.col+` DESC, t ASC LIMIT 1`).Scan(&t, &v)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO peaks (key, value, at_ms) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, at_ms = excluded.at_ms
			WHERE excluded.value > peaks.value`, ps.key, v, t); err != nil {
			return err
		}
	}
	return nil
}
