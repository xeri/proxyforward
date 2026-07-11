// Persistence: one JSON file (stats.json, next to config.toml) holding the
// lifetime aggregates, peer records, and the coarse tiers (T2/T3/T4). Fine
// tiers are pointless across restarts and are never written. Buckets are
// packed as arrays-of-numbers here only; the API keeps named fields.
package stats

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const fileVersion = 3

// packedBucket is [t,in,out,io,ih,il,ic,oo,oh,ol,oc,co,ch,cl,cc,ro,rh,rl,rc].
// Unix-ms timestamps and realistic byte counts sit far below float64's 2^53
// integer ceiling. v1 rows had only the first 11 elements (no connection
// gauge); v2 rows had 15 (no RTT gauge).
type packedBucket [19]float64

func pack(b Bucket) packedBucket {
	return packedBucket{
		float64(b.T), float64(b.In), float64(b.Out),
		b.InO, b.InH, b.InL, b.InC,
		b.OutO, b.OutH, b.OutL, b.OutC,
		b.ConnO, b.ConnH, b.ConnL, b.ConnC,
		b.RttO, b.RttH, b.RttL, b.RttC,
	}
}

func unpack(p packedBucket) Bucket {
	return Bucket{
		T: int64(p[0]), In: int64(p[1]), Out: int64(p[2]),
		InO: p[3], InH: p[4], InL: p[5], InC: p[6],
		OutO: p[7], OutH: p[8], OutL: p[9], OutC: p[10],
		ConnO: p[11], ConnH: p[12], ConnL: p[13], ConnC: p[14],
		RttO: p[15], RttH: p[16], RttL: p[17], RttC: p[18],
	}
}

type statsFile struct {
	V        int                       `json:"v"`
	Lifetime Lifetime                  `json:"lifetime"`
	Peers    []PeerStat                `json:"peers"`
	Tiers    map[string][]packedBucket `json:"tiers"`
}

// persistedTiers maps file keys to tier indices.
var persistedTiers = map[string]int{"t2": 2, "t3": 3, "t4": 4}

// load restores state from s.path into the freshly-constructed store. Any
// failure other than "no file yet" renames the file aside and starts fresh —
// stats must never block engine start.
func (s *Store) load() {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		var f statsFile
		if jsonErr := json.Unmarshal(data, &f); jsonErr != nil {
			err = jsonErr
		} else if f.V < 1 || f.V > fileVersion {
			err = fmt.Errorf("unsupported stats file version %d", f.V)
		} else {
			if f.V < 3 {
				// Older rows carry fewer gauges; json zero-filled the missing
				// trailing elements, so stamp them unknown (-1) — 0 would read
				// as a real "0 connections" / "0 ms". v1 lacks conn+rtt
				// (indices 11-18); v2 lacks only rtt (indices 15-18).
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
			s.life = f.Lifetime
			for _, p := range f.Peers {
				if len(s.peers) >= maxPeers {
					break
				}
				pc := p
				s.peers[p.IP] = &pc
			}
			for key, idx := range persistedTiers {
				restoreTier(s.tiers[idx], f.Tiers[key])
			}
			return
		}
	}
	s.logger.Warn("stats: unreadable stats file — starting fresh", "path", s.path, "err", err)
	os.Rename(s.path, s.path+".bad") // best-effort; a failure just means we overwrite in place
}

// restoreTier places persisted buckets back into a ring. Only buckets within
// one ring-length of the newest survive; older ones would collide with newer
// slots. The newest becomes the ring's current so the cascade resumes where
// the previous run stopped.
func restoreTier(r *ring, packed []packedBucket) {
	var newest int64 = -1
	buckets := make([]Bucket, 0, len(packed))
	for _, p := range packed {
		b := unpack(p)
		if b.T <= 0 || b.T%r.resMs != 0 {
			continue // misaligned entry: drop it, keep the rest
		}
		buckets = append(buckets, b)
		if idx := b.T / r.resMs; idx > newest {
			newest = idx
		}
	}
	if newest < 0 {
		return
	}
	floor := newest - int64(len(r.buf)) + 1
	for _, b := range buckets {
		idx := b.T / r.resMs
		if idx < floor {
			continue
		}
		r.buf[r.pos(idx)] = b
	}
	r.cur = newest
	r.curEmpty = false
}

// snapshotLocked builds the file image; mu must be held. Only valid buckets
// are written (idle servers stay sparse), oldest first.
func (s *Store) snapshotLocked() statsFile {
	f := statsFile{
		V:        fileVersion,
		Lifetime: s.life,
		Peers:    make([]PeerStat, 0, len(s.peers)),
		Tiers:    make(map[string][]packedBucket, len(persistedTiers)),
	}
	for _, p := range s.peers {
		f.Peers = append(f.Peers, *p)
	}
	for key, ti := range persistedTiers {
		r := s.tiers[ti]
		if r.cur < 0 {
			continue
		}
		var out []packedBucket
		for i := r.cur - int64(len(r.buf)) + 1; i <= r.cur; i++ {
			if i >= 0 && r.valid(i) {
				out = append(out, pack(r.buf[r.pos(i)]))
			}
		}
		f.Tiers[key] = out
	}
	return f
}

// Flush writes the store to disk atomically (temp file + rename, exactly the
// config.Save pattern) and folds accrued uptime into the lifetime counter.
func (s *Store) Flush() error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.mu.Lock()
	now := time.Now()
	s.life.UptimeMs += now.Sub(s.upMark).Milliseconds()
	s.upMark = now
	f := s.snapshotLocked()
	s.mu.Unlock()

	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("stats: marshal: %w", err)
	}
	return atomicWrite(s.path, data)
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".stats-*.json.tmp")
	if err != nil {
		return fmt.Errorf("stats: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("stats: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("stats: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("stats: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		// One retry: AV scanners briefly hold fresh files on Windows.
		time.Sleep(100 * time.Millisecond)
		if err = os.Rename(tmpName, path); err != nil {
			return fmt.Errorf("stats: replace file: %w", err)
		}
	}
	return nil
}
