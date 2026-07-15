// Uptime event journal: link, tunnel-local, and engine-lifecycle transitions.
// Rows are append-only transitions ('up' 1/0); the uptime queries reconstruct
// state segments from them. Writes ride the batched writer like every other
// recorder path so the sampler never blocks on SQLite.
package analytics

import (
	"database/sql"
	"time"
)

// Event kinds. 'link' (tunnel_id "") is the control link between agent and
// gateway; 'tunnel_local' is one tunnel's local target reachability; 'engine'
// (tunnel_id "") brackets one engine run so the uptime queries can treat
// time-while-off as unknown rather than down.
const (
	EventLink        = "link"
	EventTunnelLocal = "tunnel_local"
	EventEngine      = "engine"
)

// RecordEvent journals one up/down transition at the current time. agentID
// owns the link/tunnel_local transition ("" for engine-lifecycle, or a
// single-agent gateway). Nil-safe (persistence unavailable) so callers can fire
// unconditionally.
func (r *Recorder) RecordEvent(kind, agentID, tunnelID string, up bool) {
	if r == nil {
		return
	}
	r.db.recordEventAt(time.Now().UnixMilli(), kind, agentID, tunnelID, up)
}

// recordEventAt is the injectable-clock core; tests call it directly.
func (d *DB) recordEventAt(t int64, kind, agentID, tunnelID string, up bool) {
	upVal := 0
	if up {
		upVal = 1
	}
	d.Enqueue("event", func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO events (t, agent_id, kind, tunnel_id, up) VALUES (?, ?, ?, ?, ?)`,
			t, agentID, kind, tunnelID, upVal)
		return err
	})
}
