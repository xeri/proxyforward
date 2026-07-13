// Geo enrichment storage: the per-IP cache and the country aggregates behind
// the world heatmap and latency-by-country views. The resolver itself lives
// in internal/geo; this file owns what lands in SQLite.
package analytics

import (
	"database/sql"

	"proxyforward/internal/geo"
)

// GeoResolver is the lookup seam the recorder uses; *geo.Resolver implements
// it, tests substitute a map. Cache freshness rides geoCacheTTL
// (retention.go), the same horizon the pruner enforces.
type GeoResolver interface {
	LookupIP(ip string) (geo.Info, bool)
}

// stampGeo enriches one session row inside the writer transaction: cache hit
// within TTL wins, else one in-memory lookup lands in both the cache and the
// row. A miss stamps nothing.
func (r *Recorder) stampGeo(tx *sql.Tx, sessionID int64, ip string, nowMs int64) error {
	if r.geo == nil || ip == "" {
		return nil
	}
	var (
		cc, country, asOrg string
		asn                int64
		resolvedMs         int64
	)
	err := tx.QueryRow(`SELECT cc, country, asn, as_org, resolved_ms FROM geo_cache WHERE ip = ?`, ip).
		Scan(&cc, &country, &asn, &asOrg, &resolvedMs)
	fresh := err == nil && nowMs-resolvedMs < geoCacheTTL.Milliseconds()
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if !fresh {
		info, ok := r.geo.LookupIP(ip)
		if !ok {
			return nil
		}
		cc, country, asn, asOrg = info.CC, info.Country, int64(info.ASN), info.ASOrg
		if _, err := tx.Exec(`INSERT INTO geo_cache (ip, cc, country, asn, as_org, resolved_ms)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(ip) DO UPDATE SET cc = excluded.cc, country = excluded.country,
				asn = excluded.asn, as_org = excluded.as_org, resolved_ms = excluded.resolved_ms`,
			ip, cc, country, asn, asOrg, nowMs); err != nil {
			return err
		}
	}
	if cc == "" && asn == 0 {
		return nil
	}
	_, err = tx.Exec(`UPDATE sessions SET cc = ?, asn = ?, as_org = ? WHERE id = ?`,
		cc, asn, asOrg, sessionID)
	return err
}

// CountryAgg is one row of the geo snapshot: everything the heatmap and the
// latency-by-country list need for one country.
type CountryAgg struct {
	CC       string  `json:"cc"`
	Country  string  `json:"country"`
	Players  int     `json:"players"`
	Sessions int     `json:"sessions"`
	BytesIn  int64   `json:"bytesIn"`
	BytesOut int64   `json:"bytesOut"`
	RttAvg   float64 `json:"rttAvg"` // 0 when no RTT recorded
}

// MaxCountryRows keeps the reply inside the 64 KiB IPC frame (≈250 countries
// exist; the clamp is a guarantee, not a truncation in practice).
const MaxCountryRows = 250

// GeoSnapshot aggregates sessions by country over the trailing window
// (sinceMs 0 = all time), busiest first.
func (d *DB) GeoSnapshot(sinceMs int64) ([]CountryAgg, error) {
	rows, err := d.read.Query(`
		SELECT s.cc,
			COALESCE((SELECT g.country FROM geo_cache g WHERE g.cc = s.cc AND g.country != '' LIMIT 1), ''),
			COUNT(DISTINCT s.player_uuid),
			COUNT(*),
			COALESCE(SUM(s.bytes_in), 0), COALESCE(SUM(s.bytes_out), 0),
			COALESCE(AVG(NULLIF(s.rtt_avg, 0)), 0)
		FROM sessions s
		WHERE s.cc IS NOT NULL AND s.cc != '' AND s.started_ms >= ?
		GROUP BY s.cc
		ORDER BY COUNT(*) DESC
		LIMIT ?`, sinceMs, MaxCountryRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CountryAgg
	for rows.Next() {
		var a CountryAgg
		if err := rows.Scan(&a.CC, &a.Country, &a.Players, &a.Sessions, &a.BytesIn, &a.BytesOut, &a.RttAvg); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
