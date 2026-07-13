// Legacy persistence: through v3 the store lived in one JSON file
// (stats.json, next to config.toml) with buckets packed as arrays-of-numbers.
// SQLite (internal/analytics) replaced it; this loader remains so an upgrade
// imports the old file once, then renames it aside.
package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

const legacyFileVersion = 3

// packedBucket is [t,in,out,io,ih,il,ic,oo,oh,ol,oc,co,ch,cl,cc,ro,rh,rl,rc].
// Unix-ms timestamps and realistic byte counts sit far below float64's 2^53
// integer ceiling. v1 rows had only the first 11 elements (no connection
// gauge); v2 rows had 15 (no RTT gauge).
type packedBucket [19]float64

func unpack(p packedBucket) Bucket {
	return Bucket{
		T: int64(p[0]), In: int64(p[1]), Out: int64(p[2]),
		InO: p[3], InH: p[4], InL: p[5], InC: p[6],
		OutO: p[7], OutH: p[8], OutL: p[9], OutC: p[10],
		ConnO: p[11], ConnH: p[12], ConnL: p[13], ConnC: p[14],
		RttO: p[15], RttH: p[16], RttL: p[17], RttC: p[18],
		// The JSON format predates these gauges entirely.
		PlayersO: -1, PlayersH: -1, PlayersL: -1, PlayersC: -1,
		LossO: -1, LossH: -1, LossL: -1, LossC: -1,
	}
}

type legacyStatsFile struct {
	V        int                       `json:"v"`
	Lifetime Lifetime                  `json:"lifetime"`
	Peers    []PeerStat                `json:"peers"`
	Tiers    map[string][]packedBucket `json:"tiers"`
}

// legacyTiers maps file keys to tier indices.
var legacyTiers = map[string]int{"t2": 2, "t3": 3, "t4": 4}

// LoadLegacyJSON reads a v1–v3 stats.json into a snapshot ready for a
// Persister. Missing file surfaces as os.ErrNotExist; a corrupt or
// unsupported file is an error — the caller decides whether to rename it
// aside. Misaligned bucket entries are dropped individually, matching the
// old loader's tolerance.
func LoadLegacyJSON(path string) (*SnapshotData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f legacyStatsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.V < 1 || f.V > legacyFileVersion {
		return nil, fmt.Errorf("unsupported stats file version %d", f.V)
	}
	if f.V < 3 {
		// Older rows carry fewer gauges; json zero-filled the missing trailing
		// elements, so stamp them unknown (-1) — 0 would read as a real
		// "0 connections" / "0 ms". v1 lacks conn+rtt (indices 11-18); v2
		// lacks only rtt (indices 15-18).
		connStart := 15 // v2: only RTT is missing
		if f.V == 1 {
			connStart = 11 // v1: both conn and RTT are missing
		}
		for _, rows := range f.Tiers {
			for i := range rows {
				for j := connStart; j < 19; j++ {
					rows[i][j] = -1
				}
			}
		}
	}

	snap := &SnapshotData{Lifetime: f.Lifetime, Peers: f.Peers}
	for key, ti := range legacyTiers {
		rows := f.Tiers[key]
		if len(rows) == 0 {
			continue
		}
		res := tierSpecs[ti].resMs
		var newest int64 = -1
		buckets := make([]Bucket, 0, len(rows))
		for _, p := range rows {
			b := unpack(p)
			if b.T <= 0 || b.T%res != 0 {
				continue // misaligned entry: drop it, keep the rest
			}
			buckets = append(buckets, b)
			newest = max(newest, b.T)
		}
		if newest < 0 {
			continue
		}
		ts := TierSnapshot{
			Tier:   ti,
			ResMs:  res,
			FloorT: max(newest-int64(tierSpecs[ti].slots-1)*res, 0),
		}
		for _, b := range buckets {
			if b.T >= ts.FloorT {
				ts.Buckets = append(ts.Buckets, b)
			}
		}
		sort.Slice(ts.Buckets, func(i, j int) bool { return ts.Buckets[i].T < ts.Buckets[j].T })
		snap.Tiers = append(snap.Tiers, ts)
	}
	sort.Slice(snap.Tiers, func(i, j int) bool { return snap.Tiers[i].Tier < snap.Tiers[j].Tier })
	return snap, nil
}
