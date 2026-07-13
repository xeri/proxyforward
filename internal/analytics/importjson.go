// One-time migration of the pre-SQLite stats.json into the database. Runs at
// engine start, before the stats store opens; a successful import renames the
// file to stats.json.imported so it never runs twice. Failure never blocks
// engine start — a corrupt file is renamed aside, an import error is retried
// next start.
package analytics

import (
	"errors"
	"os"
	"path/filepath"

	"proxyforward/internal/stats"
)

// ImportLegacyStats migrates <configDir>/stats.json into the database if the
// database has never held stats data. The .pfsetup import flow still writes
// stats.json when restoring an old machine's backup, so this also upgrades
// those on the next engine start.
func (d *DB) ImportLegacyStats(configDir string) {
	path := filepath.Join(configDir, "stats.json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return
	}
	var n int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM lifetime`).Scan(&n); err != nil || n > 0 {
		return // the database already has data (or is unreadable) — never import over it
	}
	snap, err := stats.LoadLegacyJSON(path)
	if err != nil {
		d.logger.Warn("analytics: legacy stats.json unreadable — starting fresh", "path", path, "err", err)
		os.Rename(path, path+".bad")
		return
	}
	if err := d.SaveStats(snap); err != nil {
		d.logger.Warn("analytics: importing legacy stats.json failed — will retry next start", "err", err)
		return
	}
	if err := os.Rename(path, path+".imported"); err != nil {
		d.logger.Warn("analytics: could not rename imported stats.json", "path", path, "err", err)
		return
	}
	d.logger.Info("analytics: imported legacy stats.json", "path", path)
}
