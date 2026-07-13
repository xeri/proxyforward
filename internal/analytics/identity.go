// Player-identity storage: the name→UUID cache, the players/name/IP history
// tables, and the session backfill. The resolver (internal/players) owns the
// network side and rate limiting; it calls these methods to read the cache
// and land results. Writes ride the async batched writer.
package analytics

import (
	"database/sql"
	"strings"
	"time"
)

// backfillWindow bounds how far back a resolved identity claims the name's
// unattributed sessions. Without it, a reused name would re-attribute another
// player's ancient sessions to the new owner.
const backfillWindow = 6 * time.Hour

// CacheEntry is a name→UUID cache row. UUID "" is a confirmed miss (an
// offline-mode or cracked name Mojang has no record of).
type CacheEntry struct {
	UUID       string
	ResolvedMs int64
	Found      bool
}

// CacheGet reads the uuid_cache for a lowercased name. This is a synchronous
// read; call it only from the resolver's own goroutine, never the data path.
func (d *DB) CacheGet(nameLower string) (CacheEntry, error) {
	var e CacheEntry
	err := d.read.QueryRow(`SELECT uuid, resolved_ms FROM uuid_cache WHERE name_lower = ?`, nameLower).
		Scan(&e.UUID, &e.ResolvedMs)
	if err == sql.ErrNoRows {
		return CacheEntry{}, nil
	}
	if err != nil {
		return CacheEntry{}, err
	}
	e.Found = true
	return e, nil
}

// Identity is one resolved observation the resolver hands back for storage.
// UUID is the dashed-lowercase Mojang id, or "offline:<name-lower>" for an
// unresolved (offline-mode / cracked) player.
type Identity struct {
	UUID      string
	Name      string
	NameLower string
	IP        string
	Protocol  int32
	Offline   bool
	SeenMs    int64
	// ProfileCheckedMs is non-zero when this identity came straight from a
	// live Mojang lookup, restarting the rename-check clock.
	ProfileCheckedMs int64
}

// OfflineUUID is the synthetic key for an unresolved player.
func OfflineUUID(nameLower string) string { return "offline:" + strings.ToLower(nameLower) }

// ApplyIdentity lands one resolved observation in a single transaction:
// refresh the name cache, upsert the player and its name/IP history, and
// backfill any of the player's sessions that lack a UUID. When a name that
// previously resolved offline now has a real UUID, the offline row's history
// is reconciled onto the real player.
func (d *DB) ApplyIdentity(id Identity) {
	d.Enqueue("identity", func(tx *sql.Tx) error {
		// Cache the resolution (positive or negative) for TTL checks.
		cacheUUID := id.UUID
		if id.Offline {
			cacheUUID = "" // negative cache entry
		}
		if _, err := tx.Exec(`INSERT INTO uuid_cache (name_lower, uuid, resolved_ms)
			VALUES (?, ?, ?)
			ON CONFLICT(name_lower) DO UPDATE SET uuid = excluded.uuid, resolved_ms = excluded.resolved_ms`,
			id.NameLower, cacheUUID, id.SeenMs); err != nil {
			return err
		}

		// Reconcile a prior offline identity for this name onto the real UUID.
		if !id.Offline {
			off := OfflineUUID(id.NameLower)
			if off != id.UUID {
				lo, hi, err := reconcileOffline(tx, off, id.UUID)
				if err != nil {
					return err
				}
				// Merging two identities changes historical unique-player
				// counts; re-roll the buckets the re-keyed sessions touch.
				if lo.Valid {
					if err := d.rerollSessionRollups(tx, lo.Int64, hi.Int64, id.SeenMs); err != nil {
						return err
					}
				}
			}
		}

		// Upsert the player identity.
		if _, err := tx.Exec(`INSERT INTO players (uuid, name, offline, first_seen, last_seen, profile_checked_ms)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(uuid) DO UPDATE SET
				name = excluded.name,
				last_seen = MAX(players.last_seen, excluded.last_seen),
				first_seen = MIN(players.first_seen, excluded.first_seen),
				profile_checked_ms = MAX(players.profile_checked_ms, excluded.profile_checked_ms)`,
			id.UUID, id.Name, boolInt(id.Offline), id.SeenMs, id.SeenMs, id.ProfileCheckedMs); err != nil {
			return err
		}

		// Name history (locally observed — "names seen on this proxy").
		if _, err := tx.Exec(`INSERT INTO player_names (uuid, name, first_seen, last_seen)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(uuid, name) DO UPDATE SET
				last_seen = MAX(player_names.last_seen, excluded.last_seen),
				first_seen = MIN(player_names.first_seen, excluded.first_seen)`,
			id.UUID, id.Name, id.SeenMs, id.SeenMs); err != nil {
			return err
		}

		// IP history.
		if id.IP != "" {
			if _, err := tx.Exec(`INSERT INTO player_ips (uuid, ip, first_seen, last_seen, sessions)
				VALUES (?, ?, ?, ?, 1)
				ON CONFLICT(uuid, ip) DO UPDATE SET
					last_seen = MAX(player_ips.last_seen, excluded.last_seen),
					first_seen = MIN(player_ips.first_seen, excluded.first_seen),
					sessions = player_ips.sessions + 1`,
				id.UUID, id.IP, id.SeenMs, id.SeenMs); err != nil {
				return err
			}
			// The player wears the country of their latest address (the geo
			// cache was populated when the session opened).
			if _, err := tx.Exec(`UPDATE players SET last_cc =
					COALESCE((SELECT cc FROM geo_cache WHERE ip = ? AND cc != ''), players.last_cc)
				WHERE uuid = ?`, id.IP, id.UUID); err != nil {
				return err
			}
		}

		// Backfill the player's not-yet-attributed sessions. COLLATE NOCASE
		// matches the sniffed casing against the canonical name; the time
		// floor keeps a reused name from claiming a prior owner's history.
		// (Rides the sessions_backfill partial index.)
		floor := id.SeenMs - backfillWindow.Milliseconds()
		var lo, hi sql.NullInt64
		if err := tx.QueryRow(`SELECT MIN(started_ms), MAX(started_ms) FROM sessions
			WHERE player_name = ? COLLATE NOCASE AND (player_uuid IS NULL OR player_uuid = '')
			AND started_ms >= ?`, id.Name, floor).Scan(&lo, &hi); err != nil {
			return err
		}
		if !lo.Valid {
			return nil // nothing to backfill
		}
		if _, err := tx.Exec(`UPDATE sessions SET player_uuid = ?
			WHERE player_name = ? COLLATE NOCASE AND (player_uuid IS NULL OR player_uuid = '')
			AND started_ms >= ?`, id.UUID, id.Name, floor); err != nil {
			return err
		}
		// Late attribution changes unique-player counts already rolled up.
		return d.rerollSessionRollups(tx, lo.Int64, hi.Int64, id.SeenMs)
	})
}

// ProfileCheckedMs reads when the player's canonical profile was last
// re-fetched (0 when never, or when the player is unknown). Synchronous read;
// resolver goroutine only.
func (d *DB) ProfileCheckedMs(uuid string) (int64, error) {
	var ms int64
	err := d.read.QueryRow(`SELECT profile_checked_ms FROM players WHERE uuid = ?`, uuid).Scan(&ms)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return ms, err
}

// ApplyProfileCheck lands the result of a session-server profile fetch:
// stamps the check time and, when name is non-empty, records the canonical
// (possibly renamed) name — updating the player, its local name history, and
// the name→UUID cache so the new name resolves without another lookup.
func (d *DB) ApplyProfileCheck(uuid, name string, checkedMs int64) {
	d.Enqueue("profile-check", func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE players SET profile_checked_ms = ? WHERE uuid = ?`,
			checkedMs, uuid); err != nil {
			return err
		}
		if name == "" {
			return nil
		}
		if _, err := tx.Exec(`UPDATE players SET name = ? WHERE uuid = ?`, name, uuid); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO player_names (uuid, name, first_seen, last_seen)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(uuid, name) DO UPDATE SET
				last_seen = MAX(player_names.last_seen, excluded.last_seen),
				first_seen = MIN(player_names.first_seen, excluded.first_seen)`,
			uuid, name, checkedMs, checkedMs); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO uuid_cache (name_lower, uuid, resolved_ms)
			VALUES (?, ?, ?)
			ON CONFLICT(name_lower) DO UPDATE SET uuid = excluded.uuid, resolved_ms = excluded.resolved_ms`,
			strings.ToLower(name), uuid, checkedMs)
		return err
	})
}

// reconcileOffline migrates a superseded offline player's history and sessions
// onto its now-known real UUID, then deletes the offline row. Each statement
// takes (realUUID, offlineUUID) so placeholder order is unambiguous. The
// returned range spans the re-keyed sessions (invalid when none moved) so the
// caller can re-roll the touched rollup buckets.
func reconcileOffline(tx *sql.Tx, offlineUUID, realUUID string) (lo, hi sql.NullInt64, err error) {
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM players WHERE uuid = ?`, offlineUUID).Scan(&exists); err == sql.ErrNoRows {
		return lo, hi, nil // nothing to reconcile
	} else if err != nil {
		return lo, hi, err
	}
	if err := tx.QueryRow(`SELECT MIN(started_ms), MAX(started_ms) FROM sessions
		WHERE player_uuid = ?`, offlineUUID).Scan(&lo, &hi); err != nil {
		return lo, hi, err
	}
	stmts := []struct {
		q         string
		real, off bool // which args this statement takes, in order
	}{
		{`UPDATE sessions SET player_uuid = ? WHERE player_uuid = ?`, true, true},
		{`INSERT INTO player_names (uuid, name, first_seen, last_seen)
			SELECT ?, name, first_seen, last_seen FROM player_names WHERE uuid = ?
			ON CONFLICT(uuid, name) DO UPDATE SET
				first_seen = MIN(player_names.first_seen, excluded.first_seen),
				last_seen = MAX(player_names.last_seen, excluded.last_seen)`, true, true},
		{`DELETE FROM player_names WHERE uuid = ?`, false, true},
		{`INSERT INTO player_ips (uuid, ip, first_seen, last_seen, sessions)
			SELECT ?, ip, first_seen, last_seen, sessions FROM player_ips WHERE uuid = ?
			ON CONFLICT(uuid, ip) DO UPDATE SET
				first_seen = MIN(player_ips.first_seen, excluded.first_seen),
				last_seen = MAX(player_ips.last_seen, excluded.last_seen),
				sessions = player_ips.sessions + excluded.sessions`, true, true},
		{`DELETE FROM player_ips WHERE uuid = ?`, false, true},
		{`DELETE FROM players WHERE uuid = ?`, false, true},
	}
	for _, s := range stmts {
		var args []any
		if s.real {
			args = append(args, realUUID)
		}
		if s.off {
			args = append(args, offlineUUID)
		}
		if _, err := tx.Exec(s.q, args...); err != nil {
			return lo, hi, err
		}
	}
	return lo, hi, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
