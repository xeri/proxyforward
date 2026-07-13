package analytics

import (
	"testing"
	"time"
)

func sessionUUID(t *testing.T, d *DB, id int64) string {
	t.Helper()
	var got string
	if err := d.sql.QueryRow(`SELECT COALESCE(player_uuid, '') FROM sessions WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("session %d uuid: %v", id, err)
	}
	return got
}

func hourlyUniques(t *testing.T, d *DB, hourMs int64) int {
	t.Helper()
	var n int
	if err := d.sql.QueryRow(`SELECT unique_players FROM rollup_hourly WHERE hour_ms = ?`, hourMs).Scan(&n); err != nil {
		t.Fatalf("hourly uniques: %v", err)
	}
	return n
}

// TestBackfillCaseInsensitive: a session whose sniffed name casing differs
// from the canonical name must still be attributed.
func TestBackfillCaseInsensitive(t *testing.T) {
	d := openTest(t, t.TempDir())
	now := time.Now().UnixMilli()
	if _, err := d.sql.Exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, player_name)
		VALUES (1, 't1', '203.0.113.9', ?, 'steve')`, now-minuteMs); err != nil {
		t.Fatal(err)
	}
	d.ApplyIdentity(Identity{UUID: steveUUID, Name: "Steve", NameLower: "steve", SeenMs: now})
	d.Barrier()
	if got := sessionUUID(t, d, 1); got != steveUUID {
		t.Fatalf("backfilled uuid = %q, want %s", got, steveUUID)
	}
}

// TestBackfillTimeFloor: sessions older than the backfill window belong to a
// possible prior owner of the name and must stay unattributed.
func TestBackfillTimeFloor(t *testing.T) {
	d := openTest(t, t.TempDir())
	now := time.Now().UnixMilli()
	old := now - backfillWindow.Milliseconds() - hourMs
	recent := now - minuteMs
	if _, err := d.sql.Exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, player_name) VALUES
		(1, 't1', '203.0.113.9', ?, 'Steve'),
		(2, 't1', '203.0.113.9', ?, 'Steve')`, old, recent); err != nil {
		t.Fatal(err)
	}
	d.ApplyIdentity(Identity{UUID: steveUUID, Name: "Steve", NameLower: "steve", SeenMs: now})
	d.Barrier()
	if got := sessionUUID(t, d, 1); got != "" {
		t.Fatalf("ancient session claimed by new owner: uuid = %q", got)
	}
	if got := sessionUUID(t, d, 2); got != steveUUID {
		t.Fatalf("recent session not backfilled: uuid = %q", got)
	}
}

// TestBackfillRerollsUniquePlayers: late attribution must correct the frozen
// unique-player counts in the hourly and daily rollups.
func TestBackfillRerollsUniquePlayers(t *testing.T) {
	d := openTest(t, t.TempDir())
	base, now := rollupBase()
	if _, err := d.sql.Exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, player_name) VALUES
		(1, 't1', '203.0.113.9', ?, 'Steve'),
		(2, 't1', '203.0.113.9', ?, 'Steve')`, base+1000, base+2000); err != nil {
		t.Fatal(err)
	}
	d.runRollup(time.UnixMilli(now))
	if n := hourlyUniques(t, d, base); n != 0 {
		t.Fatalf("uniques before identity = %d, want 0 (unattributed)", n)
	}

	d.ApplyIdentity(Identity{UUID: steveUUID, Name: "Steve", NameLower: "steve", SeenMs: now})
	d.Barrier()
	if n := hourlyUniques(t, d, base); n != 1 {
		t.Fatalf("hourly uniques after backfill = %d, want 1", n)
	}
	var daily int
	if err := d.sql.QueryRow(`SELECT unique_players FROM rollup_daily WHERE day_ms = ?`,
		base/dayMillis*dayMillis).Scan(&daily); err != nil || daily != 1 {
		t.Fatalf("daily uniques after backfill = %d (err=%v), want 1", daily, err)
	}
}

// TestReconcileRerollsUniquePlayers: when an offline identity is reconciled
// onto its real UUID, hours where both appeared must drop to one unique.
func TestReconcileRerollsUniquePlayers(t *testing.T) {
	d := openTest(t, t.TempDir())
	base, now := rollupBase()
	off := OfflineUUID("zed")
	if _, err := d.sql.Exec(`INSERT INTO players (uuid, name, offline, first_seen, last_seen)
		VALUES (?, 'Zed', 1, ?, ?)`, off, base, base); err != nil {
		t.Fatal(err)
	}
	if _, err := d.sql.Exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms, player_uuid, player_name) VALUES
		(1, 't1', '203.0.113.9', ?, ?, 'Zed'),
		(2, 't1', '203.0.113.9', ?, ?, 'Zed')`, base+1000, off, base+2000, alexUUID); err != nil {
		t.Fatal(err)
	}
	d.runRollup(time.UnixMilli(now))
	if n := hourlyUniques(t, d, base); n != 2 {
		t.Fatalf("uniques before reconcile = %d, want 2", n)
	}

	d.ApplyIdentity(Identity{UUID: alexUUID, Name: "Zed", NameLower: "zed", SeenMs: now})
	d.Barrier()
	if got := sessionUUID(t, d, 1); got != alexUUID {
		t.Fatalf("offline session not re-keyed: uuid = %q", got)
	}
	if n := hourlyUniques(t, d, base); n != 1 {
		t.Fatalf("hourly uniques after reconcile = %d, want 1", n)
	}
	var offRows int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM players WHERE uuid = ?`, off).Scan(&offRows); err != nil || offRows != 0 {
		t.Fatalf("offline player row survived reconcile (n=%d err=%v)", offRows, err)
	}
}
