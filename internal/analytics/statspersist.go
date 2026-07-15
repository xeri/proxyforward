// stats.Persister implementation: the bandwidth-history store snapshots
// itself through this seam on the engine's 45 s flush cadence. Writes are
// synchronous (they ride the flush ticker, not the data path) and skip
// buckets the watermark proves unchanged, so a flush touches a handful of
// rows, not the whole ~14k-row history.
package analytics

import (
	"database/sql"
	"fmt"

	"proxyforward/internal/stats"
)

const rrdCols = `agent_id, tier, t, inb, outb,
	io, ih, il, ic, oo, oh, ol, oc,
	co, ch, cl, cc, ro, rh, rl, rc,
	po, ph, pl, pc, lo, lh, ll, lc`

// LoadStats restores the persisted snapshot; (nil, nil) means the database
// holds nothing yet.
func (d *DB) LoadStats() (*stats.SnapshotData, error) {
	snap := &stats.SnapshotData{}
	err := d.read.QueryRow(`SELECT bytes_in, bytes_out, link_bytes_in, link_bytes_out,
			uptime_ms, link_sessions, first_run_ms FROM lifetime WHERE id = 1`).Scan(
		&snap.Lifetime.BytesIn, &snap.Lifetime.BytesOut,
		&snap.Lifetime.LinkBytesIn, &snap.Lifetime.LinkBytesOut,
		&snap.Lifetime.UptimeMs, &snap.Lifetime.LinkSessions, &snap.Lifetime.FirstRunMs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load lifetime: %w", err)
	}

	rows, err := d.read.Query(`SELECT ip, first_seen, last_seen, bytes_in, bytes_out, conns
		FROM peers ORDER BY last_seen DESC`)
	if err != nil {
		return nil, fmt.Errorf("load peers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p stats.PeerStat
		if err := rows.Scan(&p.IP, &p.FirstSeen, &p.LastSeen, &p.TotalBytesIn, &p.TotalBytesOut, &p.TotalConns); err != nil {
			return nil, fmt.Errorf("scan peer: %w", err)
		}
		snap.Peers = append(snap.Peers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load peers: %w", err)
	}

	brows, err := d.read.Query(`SELECT ` + rrdCols + ` FROM rrd ORDER BY agent_id, tier, t`)
	if err != nil {
		return nil, fmt.Errorf("load rrd: %w", err)
	}
	defer brows.Close()
	var cur *stats.TierSnapshot
	for brows.Next() {
		var agentID string
		var tier int
		var b stats.Bucket
		if err := brows.Scan(&agentID, &tier, &b.T, &b.In, &b.Out,
			&b.InO, &b.InH, &b.InL, &b.InC, &b.OutO, &b.OutH, &b.OutL, &b.OutC,
			&b.ConnO, &b.ConnH, &b.ConnL, &b.ConnC, &b.RttO, &b.RttH, &b.RttL, &b.RttC,
			&b.PlayersO, &b.PlayersH, &b.PlayersL, &b.PlayersC,
			&b.LossO, &b.LossH, &b.LossL, &b.LossC); err != nil {
			return nil, fmt.Errorf("scan bucket: %w", err)
		}
		if cur == nil || cur.Tier != tier || cur.AgentID != agentID {
			snap.Tiers = append(snap.Tiers, stats.TierSnapshot{AgentID: agentID, Tier: tier})
			cur = &snap.Tiers[len(snap.Tiers)-1]
		}
		cur.Buckets = append(cur.Buckets, b)
	}
	if err := brows.Err(); err != nil {
		return nil, fmt.Errorf("load rrd: %w", err)
	}
	return snap, nil
}

// SaveStats lands one snapshot in one transaction: lifetime and peers are
// rewritten whole (≤512 rows), rrd rows only from each tier's dirty
// watermark on, with lapped-out rows deleted.
func (d *DB) SaveStats(snap *stats.SnapshotData) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return fmt.Errorf("stats save: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT OR REPLACE INTO lifetime
			(id, bytes_in, bytes_out, link_bytes_in, link_bytes_out, uptime_ms, link_sessions, first_run_ms)
			VALUES (1, ?, ?, ?, ?, ?, ?, ?)`,
		snap.Lifetime.BytesIn, snap.Lifetime.BytesOut,
		snap.Lifetime.LinkBytesIn, snap.Lifetime.LinkBytesOut,
		snap.Lifetime.UptimeMs, snap.Lifetime.LinkSessions, snap.Lifetime.FirstRunMs); err != nil {
		return fmt.Errorf("save lifetime: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM peers`); err != nil {
		return fmt.Errorf("clear peers: %w", err)
	}
	pstmt, err := tx.Prepare(`INSERT INTO peers (ip, first_seen, last_seen, bytes_in, bytes_out, conns)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("save peers: %w", err)
	}
	defer pstmt.Close()
	for _, p := range snap.Peers {
		if _, err := pstmt.Exec(p.IP, p.FirstSeen, p.LastSeen, p.TotalBytesIn, p.TotalBytesOut, p.TotalConns); err != nil {
			return fmt.Errorf("save peer %s: %w", p.IP, err)
		}
	}

	bstmt, err := tx.Prepare(`INSERT OR REPLACE INTO rrd (` + rrdCols + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("save rrd: %w", err)
	}
	defer bstmt.Close()
	// Drop the rrd rows of agent histories evicted since the last save.
	for _, agentID := range snap.DeleteAgents {
		if _, err := tx.Exec(`DELETE FROM rrd WHERE agent_id = ?`, agentID); err != nil {
			return fmt.Errorf("delete agent %q rrd: %w", agentID, err)
		}
	}
	for _, ts := range snap.Tiers {
		if _, err := tx.Exec(`DELETE FROM rrd WHERE agent_id = ? AND tier = ? AND t < ?`, ts.AgentID, ts.Tier, ts.FloorT); err != nil {
			return fmt.Errorf("expire agent %q tier %d: %w", ts.AgentID, ts.Tier, err)
		}
		for _, b := range ts.Buckets {
			if b.T < ts.DirtyFromT {
				continue // unchanged since the last successful save
			}
			if _, err := bstmt.Exec(ts.AgentID, ts.Tier, b.T, b.In, b.Out,
				b.InO, b.InH, b.InL, b.InC, b.OutO, b.OutH, b.OutL, b.OutC,
				b.ConnO, b.ConnH, b.ConnL, b.ConnC, b.RttO, b.RttH, b.RttL, b.RttC,
				b.PlayersO, b.PlayersH, b.PlayersL, b.PlayersC,
				b.LossO, b.LossH, b.LossL, b.LossC); err != nil {
				return fmt.Errorf("save bucket agent=%q tier=%d t=%d: %w", ts.AgentID, ts.Tier, b.T, err)
			}
		}
	}
	return tx.Commit()
}
