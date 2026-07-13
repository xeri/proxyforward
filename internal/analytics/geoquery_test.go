package analytics

import (
	"testing"

	"proxyforward/internal/geo"
)

// fakeGeo is a static lookup table standing in for the MaxMind resolver.
type fakeGeo map[string]geo.Info

func (f fakeGeo) LookupIP(ip string) (geo.Info, bool) {
	info, ok := f[ip]
	return info, ok
}

func TestGeoSnapshot(t *testing.T) {
	d := openTest(t, t.TempDir())
	seedQueryFixture(t, d)

	rows, err := d.GeoSnapshot(0)
	if err != nil {
		t.Fatalf("GeoSnapshot: %v", err)
	}
	// Two countries carry sessions (NZ x2, DE x1); the anonymous session has a
	// NULL cc and is excluded. Busiest first.
	if len(rows) != 2 {
		t.Fatalf("countries = %d, want 2 (%+v)", len(rows), rows)
	}
	nz := rows[0]
	if nz.CC != "NZ" || nz.Sessions != 2 || nz.Players != 1 {
		t.Fatalf("NZ = %+v, want cc=NZ sessions=2 players=1", nz)
	}
	if nz.BytesIn != 1500 || nz.BytesOut != 2500 {
		t.Fatalf("NZ bytes = %d/%d, want 1500/2500", nz.BytesIn, nz.BytesOut)
	}
	// rtt_avg averages only recorded (non-zero) samples: session 2's 0 is skipped.
	if nz.RttAvg != 25 {
		t.Fatalf("NZ rttAvg = %v, want 25", nz.RttAvg)
	}
	de := rows[1]
	if de.CC != "DE" || de.Sessions != 1 || de.Players != 1 || de.RttAvg != 50 {
		t.Fatalf("DE = %+v, want cc=DE sessions=1 players=1 rtt=50", de)
	}
}

func TestGeoSnapshotWindowExcludesOld(t *testing.T) {
	d := openTest(t, t.TempDir())
	seedQueryFixture(t, d)

	// Only sessions started in the last 45 minutes: Steve's live session
	// (10 min) and the anonymous NULL-cc one (excluded); the 1 h and 2 h rows
	// drop out. GeoSnapshot takes an absolute since-timestamp.
	rows, err := d.GeoSnapshot(qBase - 45*minuteMs)
	if err != nil {
		t.Fatalf("GeoSnapshot: %v", err)
	}
	if len(rows) != 1 || rows[0].CC != "NZ" || rows[0].Sessions != 1 {
		t.Fatalf("windowed = %+v, want single NZ/1", rows)
	}
}

func TestStampGeoEnrichesSessionAndCache(t *testing.T) {
	d := openTest(t, t.TempDir())
	rec := d.NewRecorder(fakeGeo{"203.0.113.9": {CC: "NZ", Country: "New Zealand", ASN: 64500, ASOrg: "Example Net"}})

	if _, err := d.sql.Exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms) VALUES (1, 't1', '203.0.113.9', ?)`, qBase); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	tx, err := d.sql.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := rec.stampGeo(tx, 1, "203.0.113.9", qBase); err != nil {
		t.Fatalf("stampGeo: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var cc, asOrg string
	var asn int64
	if err := d.sql.QueryRow(`SELECT cc, asn, as_org FROM sessions WHERE id = 1`).Scan(&cc, &asn, &asOrg); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if cc != "NZ" || asn != 64500 || asOrg != "Example Net" {
		t.Fatalf("session = %s/%d/%s, want NZ/64500/Example Net", cc, asn, asOrg)
	}
	var cachedCountry string
	if err := d.sql.QueryRow(`SELECT country FROM geo_cache WHERE ip = '203.0.113.9'`).Scan(&cachedCountry); err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if cachedCountry != "New Zealand" {
		t.Fatalf("cached country = %q, want New Zealand", cachedCountry)
	}
}

func TestStampGeoMissStampsNothing(t *testing.T) {
	d := openTest(t, t.TempDir())
	rec := d.NewRecorder(fakeGeo{}) // resolver knows nothing

	if _, err := d.sql.Exec(`INSERT INTO sessions (id, tunnel_id, client_ip, started_ms) VALUES (1, 't1', '203.0.113.9', ?)`, qBase); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	tx, _ := d.sql.Begin()
	if err := rec.stampGeo(tx, 1, "203.0.113.9", qBase); err != nil {
		t.Fatalf("stampGeo: %v", err)
	}
	tx.Commit()

	var cc string
	if err := d.sql.QueryRow(`SELECT COALESCE(cc, '') FROM sessions WHERE id = 1`).Scan(&cc); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if cc != "" {
		t.Fatalf("cc = %q, want empty on miss", cc)
	}
	var n int
	d.sql.QueryRow(`SELECT COUNT(*) FROM geo_cache`).Scan(&n)
	if n != 0 {
		t.Fatalf("geo_cache rows = %d, want 0 on miss", n)
	}
}
