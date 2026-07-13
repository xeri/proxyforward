package players

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"proxyforward/internal/analytics"
)

const (
	aliceUUID  = "069a79f4-44e9-4726-a5be-fca90e38aaf5"
	bobUUID    = "853c80ef-3c37-49fd-aa49-938b674adae6"
	renameUUID = "61699b2e-d327-4a01-9f1e-0ea8c3f06bc6"
)

// testClock is a fake time source safe to advance while the Run goroutine
// reads it.
type testClock struct {
	base   time.Time
	offset atomic.Int64
}

func newTestClock() *testClock               { return &testClock{base: time.Now()} }
func (c *testClock) now() time.Time          { return c.base.Add(time.Duration(c.offset.Load())) }
func (c *testClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

// mojangStub fakes both the bulk-lookup and session-server endpoints.
// byName maps lowercased name -> {undashed id, canonical name}; missing names
// are omitted from the bulk response (Mojang's "no such player").
type mojangStub struct {
	byName    map[string]bulkEntry
	byUUID    map[string]bulkEntry // undashed id -> profile
	rejectN   atomic.Int32         // this many bulk requests get a 429 first
	bulkReqs  atomic.Int32
	profReqs  atomic.Int32
	lastBatch atomic.Value // []string

	// profileHold, when set, blocks every profile response until closed —
	// simulating a wedged session server.
	profileHold chan struct{}
}

func (m *mojangStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/bulk", func(w http.ResponseWriter, req *http.Request) {
		m.bulkReqs.Add(1)
		if m.rejectN.Add(-1) >= 0 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		var names []string
		body, _ := io.ReadAll(req.Body)
		json.Unmarshal(body, &names)
		m.lastBatch.Store(names)
		var out []bulkEntry
		for _, n := range names {
			if e, ok := m.byName[strings.ToLower(n)]; ok {
				out = append(out, e)
			}
		}
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/profile/", func(w http.ResponseWriter, req *http.Request) {
		m.profReqs.Add(1)
		if m.profileHold != nil {
			select {
			case <-m.profileHold:
			case <-req.Context().Done():
				return // client gave up (test teardown) — release the server
			}
		}
		id := strings.TrimPrefix(req.URL.Path, "/profile/")
		e, ok := m.byUUID[id]
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": e.ID, "name": e.Name})
	})
	return mux
}

// newTestResolver wires a Resolver to a temp DB and the stub, with timing
// shortened so tests run in milliseconds, and starts Run.
func newTestResolver(t *testing.T, enabled bool, stub *mojangStub, clock *testClock) (*Resolver, *analytics.DB) {
	t.Helper()
	db, err := analytics.Open(t.TempDir(), analytics.Options{}, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	r := New(db, enabled, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.baseURL = srv.URL + "/bulk"
	r.profileURL = srv.URL + "/profile/"
	r.limiter = newLimiter(10000, 100)
	r.batchDelay = 25 * time.Millisecond
	r.backoffMin = 5 * time.Millisecond
	if clock != nil {
		r.now = clock.now
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return r, db
}

// waitFor polls cond (with a write barrier before each check) until it holds.
func waitFor(t *testing.T, db *analytics.DB, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		db.Barrier()
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func playerByUUID(t *testing.T, db *analytics.DB, uuid string) *analytics.PlayerDetail {
	t.Helper()
	det, err := db.PlayerDetail(uuid, nil, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("PlayerDetail(%s): %v", uuid, err)
	}
	return det
}

func seedSession(t *testing.T, db *analytics.DB, id int64, playerName string) {
	t.Helper()
	db.Enqueue("test-seed", func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO sessions (id, tunnel_id, tunnel_name, client_ip, started_ms, player_name)
			VALUES (?, 't1', 'mc', '203.0.113.9', ?, ?)`, id, time.Now().UnixMilli(), playerName)
		return err
	})
	db.Barrier()
}

func TestBulkResolveAndOfflineMiss(t *testing.T) {
	stub := &mojangStub{byName: map[string]bulkEntry{
		"alice_1": {ID: strings.ReplaceAll(aliceUUID, "-", ""), Name: "Alice_1"},
	}}
	r, db := newTestResolver(t, true, stub, nil)
	seedSession(t, db, 1, "Alice_1")

	r.Observe("alice_1", "203.0.113.9", 767) // observed casing differs from canonical
	r.Observe("Cracked_Guy", "203.0.113.10", 767)

	waitFor(t, db, "both players stored", func() bool {
		page, err := db.Players(analytics.PlayersQuery{}, nil, time.Now().UnixMilli())
		return err == nil && page.Total == 2
	})

	det := playerByUUID(t, db, aliceUUID)
	if det == nil || det.Card.Name != "Alice_1" || det.Card.Offline {
		t.Fatalf("alice card = %+v", det)
	}
	// The pre-existing session was backfilled with her UUID.
	sess, err := db.Sessions(analytics.SessionsQuery{PlayerUUID: aliceUUID}, time.Now().UnixMilli())
	if err != nil || sess.Total != 1 {
		t.Fatalf("backfilled sessions = %d (err=%v), want 1", sess.Total, err)
	}
	// The unknown name resolved offline with a negative cache entry.
	off := playerByUUID(t, db, analytics.OfflineUUID("cracked_guy"))
	if off == nil || !off.Card.Offline || off.Card.Name != "Cracked_Guy" {
		t.Fatalf("offline card = %+v", off)
	}
	if e, err := db.CacheGet("cracked_guy"); err != nil || !e.Found || e.UUID != "" {
		t.Fatalf("negative cache entry = %+v (err=%v)", e, err)
	}
	if e, err := db.CacheGet("alice_1"); err != nil || e.UUID != aliceUUID {
		t.Fatalf("positive cache entry = %+v (err=%v)", e, err)
	}
}

func TestBatchCoalesces(t *testing.T) {
	stub := &mojangStub{byName: map[string]bulkEntry{}}
	r, db := newTestResolver(t, true, stub, nil)

	r.Observe("one", "203.0.113.1", 767)
	r.Observe("two", "203.0.113.2", 767)
	r.Observe("three", "203.0.113.3", 767)

	waitFor(t, db, "single coalesced batch", func() bool {
		if stub.bulkReqs.Load() == 0 {
			return false
		}
		names, _ := stub.lastBatch.Load().([]string)
		return len(names) == 3
	})
	// Give any stray extra request a moment to show up, then assert one batch.
	time.Sleep(50 * time.Millisecond)
	if n := stub.bulkReqs.Load(); n != 1 {
		t.Fatalf("bulk requests = %d, want 1", n)
	}
}

func TestRateLimitRetriesOnNextSighting(t *testing.T) {
	stub := &mojangStub{byName: map[string]bulkEntry{
		"alice_1": {ID: strings.ReplaceAll(aliceUUID, "-", ""), Name: "Alice_1"},
	}}
	stub.rejectN.Store(1) // first bulk request gets a 429
	r, db := newTestResolver(t, true, stub, nil)

	r.Observe("alice_1", "203.0.113.9", 767)
	waitFor(t, db, "first (rejected) request", func() bool { return stub.bulkReqs.Load() >= 1 })

	// The rejected round must not negative-cache the name.
	if e, err := db.CacheGet("alice_1"); err != nil || e.Found {
		t.Fatalf("cache entry after 429 = %+v (err=%v), want none", e, err)
	}

	// The next sighting retries and succeeds.
	r.Observe("alice_1", "203.0.113.9", 767)
	waitFor(t, db, "resolution after retry", func() bool {
		e, err := db.CacheGet("alice_1")
		return err == nil && e.UUID == aliceUUID
	})
}

func TestOfflineReconciledOntoRealUUID(t *testing.T) {
	stub := &mojangStub{byName: map[string]bulkEntry{}}
	clock := newTestClock()
	r, db := newTestResolver(t, true, stub, clock)
	seedSession(t, db, 1, "Bob")

	// First sighting: Mojang doesn't know him -> offline identity.
	r.Observe("Bob", "203.0.113.20", 767)
	offUUID := analytics.OfflineUUID("bob")
	waitFor(t, db, "offline identity", func() bool {
		det := playerByUUID(t, db, offUUID)
		return det != nil && det.Card.Offline
	})
	if s, _ := db.Sessions(analytics.SessionsQuery{PlayerUUID: offUUID}, time.Now().UnixMilli()); s.Total != 1 {
		t.Fatalf("offline session backfill = %d, want 1", s.Total)
	}

	// He registers the name; past the negative TTL the next sighting re-checks.
	stub.byName["bob"] = bulkEntry{ID: strings.ReplaceAll(bobUUID, "-", ""), Name: "Bob"}
	clock.advance(25 * time.Hour)
	r.Observe("Bob", "203.0.113.20", 767)

	waitFor(t, db, "offline row reconciled", func() bool {
		det := playerByUUID(t, db, bobUUID)
		return det != nil && !det.Card.Offline
	})
	// Exactly one player remains, and the history moved with him.
	page, err := db.Players(analytics.PlayersQuery{}, nil, time.Now().UnixMilli())
	if err != nil || page.Total != 1 {
		t.Fatalf("players after reconcile = %d (err=%v), want 1", page.Total, err)
	}
	if s, _ := db.Sessions(analytics.SessionsQuery{PlayerUUID: bobUUID}, time.Now().UnixMilli()); s.Total != 1 {
		t.Fatalf("reconciled session count = %d, want 1", s.Total)
	}
	det := playerByUUID(t, db, bobUUID)
	if len(det.Names) != 1 || det.Names[0].Name != "Bob" {
		t.Fatalf("reconciled name history = %+v", det.Names)
	}
	if len(det.IPs) != 1 || det.IPs[0].IP != "203.0.113.20" {
		t.Fatalf("reconciled ip history = %+v", det.IPs)
	}
}

func TestProfileRefreshTracksRename(t *testing.T) {
	undashed := strings.ReplaceAll(renameUUID, "-", "")
	stub := &mojangStub{
		byName: map[string]bulkEntry{"oldname": {ID: undashed, Name: "OldName"}},
		byUUID: map[string]bulkEntry{undashed: {ID: undashed, Name: "OldName"}},
	}
	clock := newTestClock()
	r, db := newTestResolver(t, true, stub, clock)

	// Fresh bulk resolution: no profile refresh should fire.
	r.Observe("OldName", "203.0.113.30", 767)
	waitFor(t, db, "initial resolution", func() bool {
		e, err := db.CacheGet("oldname")
		return err == nil && e.UUID == renameUUID
	})
	if n := stub.profReqs.Load(); n != 0 {
		t.Fatalf("profile requests after fresh resolve = %d, want 0", n)
	}

	// They rename; a sighting a day later (cache still positive) refreshes the
	// profile and picks up the new canonical name.
	stub.byUUID[undashed] = bulkEntry{ID: undashed, Name: "NewName"}
	clock.advance(25 * time.Hour)
	r.Observe("OldName", "203.0.113.30", 767)

	waitFor(t, db, "rename picked up", func() bool {
		det := playerByUUID(t, db, renameUUID)
		return det != nil && det.Card.Name == "NewName"
	})
	det := playerByUUID(t, db, renameUUID)
	if len(det.Names) != 2 {
		t.Fatalf("name history = %+v, want OldName+NewName", det.Names)
	}
	// The new name now resolves from cache without a lookup.
	if e, err := db.CacheGet("newname"); err != nil || e.UUID != renameUUID {
		t.Fatalf("cache for new name = %+v (err=%v)", e, err)
	}
	// Repeat sightings inside the TTL don't re-fetch the profile.
	r.Observe("NewName", "203.0.113.30", 767)
	waitFor(t, db, "repeat sighting applied", func() bool {
		det := playerByUUID(t, db, renameUUID)
		return det != nil && len(det.IPs) == 1
	})
	time.Sleep(50 * time.Millisecond)
	if n := stub.profReqs.Load(); n != 1 {
		t.Fatalf("profile requests = %d, want 1", n)
	}
	if n := stub.bulkReqs.Load(); n != 1 {
		t.Fatalf("bulk requests = %d, want 1", n)
	}
}

// TestSlowProfileFetchDoesNotStallResolution wedges the session server (the
// profile refresh path) and checks that a burst of new sightings still
// resolves on the batch cadence — the refresh must run on its own goroutine,
// never in front of bulk resolution.
func TestSlowProfileFetchDoesNotStallResolution(t *testing.T) {
	undashed := strings.ReplaceAll(renameUUID, "-", "")
	stub := &mojangStub{
		byName: map[string]bulkEntry{"renamer": {ID: undashed, Name: "Renamer"}},
		byUUID: map[string]bulkEntry{undashed: {ID: undashed, Name: "Renamer"}},
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	stub.profileHold = release
	clock := newTestClock()
	r, db := newTestResolver(t, true, stub, clock)

	// Resolve once (fresh — no refresh), then age past the profile TTL so the
	// next sighting queues a refresh that hangs on the stub.
	r.Observe("Renamer", "203.0.113.30", 767)
	waitFor(t, db, "initial resolution", func() bool {
		e, err := db.CacheGet("renamer")
		return err == nil && e.UUID == renameUUID
	})
	clock.advance(25 * time.Hour)
	r.Observe("Renamer", "203.0.113.30", 767)
	waitFor(t, db, "hung profile fetch", func() bool { return stub.profReqs.Load() >= 1 })

	// 25 distinct sightings while the profile fetch hangs: every one must
	// resolve; none may be dropped behind the stalled refresh.
	for i := range 25 {
		name := fmt.Sprintf("player_%02d", i)
		stub.byName[name] = bulkEntry{ID: fmt.Sprintf("%031x", i) + "f", Name: name}
		r.Observe(name, "203.0.113.31", 767)
	}
	waitFor(t, db, "all 25 resolved behind the hung refresh", func() bool {
		page, err := db.Players(analytics.PlayersQuery{}, nil, clock.now().UnixMilli())
		return err == nil && page.Total == 26 // renamer + 25
	})
}

func TestLookupsDisabledResolvesOffline(t *testing.T) {
	stub := &mojangStub{byName: map[string]bulkEntry{
		"alice_1": {ID: strings.ReplaceAll(aliceUUID, "-", ""), Name: "Alice_1"},
	}}
	r, db := newTestResolver(t, false, stub, nil)

	r.Observe("Alice_1", "203.0.113.9", 767)
	waitFor(t, db, "offline identity", func() bool {
		det := playerByUUID(t, db, analytics.OfflineUUID("alice_1"))
		return det != nil && det.Card.Offline
	})
	if n := stub.bulkReqs.Load() + stub.profReqs.Load(); n != 0 {
		t.Fatalf("network requests with lookups disabled = %d, want 0", n)
	}
}

func TestLimiterHonorsContext(t *testing.T) {
	l := newLimiter(0.0001, 1)
	if err := l.wait(context.Background()); err != nil {
		t.Fatalf("first token: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.wait(ctx); err == nil {
		t.Fatal("wait with exhausted bucket did not respect ctx")
	}
}
