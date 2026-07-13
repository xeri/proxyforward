// Package players resolves observed Minecraft usernames to Mojang UUIDs and
// keeps the local player/name/IP history current. It owns the network side —
// the modern Microsoft-managed bulk lookup endpoint, a token-bucket limiter,
// TTL caches, and offline-mode fallbacks — and hands results to the analytics
// store to persist. The engine runs one Resolver as a ctx-bound goroutine.
package players

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"proxyforward/internal/analytics"
)

const (
	// The modern, Microsoft-managed bulk lookup: accepts up to 10 usernames,
	// no auth token, Content-Type application/json. The legacy
	// api.mojang.com/profiles/minecraft endpoint is deprecated and throttled.
	bulkLookupURL = "https://api.minecraftservices.com/minecraft/profile/lookup/bulk/byname"
	batchSize     = 10

	// TTLs: a resolved name is stable for a month; a miss (offline/cracked)
	// is re-checked daily in case the name later gets registered.
	positiveTTL = 30 * 24 * time.Hour
	negativeTTL = 24 * time.Hour

	// Batches coalesce over this window before a lookup fires.
	defaultBatchDelay = 2 * time.Second

	// Backoff after a 429/5xx.
	defaultBackoffMin = 30 * time.Second

	// A known player's canonical profile (current name) is re-fetched from the
	// session server at most this often, and only while they are actually seen
	// connecting. This is the only rename signal left — Mojang removed the
	// name-history endpoint in 2022.
	defaultProfileTTL = 24 * time.Hour

	// The Mojang session server; GET <base><undashed-uuid> returns the profile.
	profileLookupURL = "https://sessionserver.mojang.com/session/minecraft/profile/"
)

// observation is one sighting of a player from the login sniffer.
type observation struct {
	name     string
	ip       string
	protocol int32
}

// Resolver turns username sightings into stored identities.
type Resolver struct {
	db         *analytics.DB
	logger     *slog.Logger
	http       *http.Client
	enabled    bool
	baseURL    string
	profileURL string
	limiter    *limiter

	seen chan observation

	// refreshQ carries profile re-fetch requests to their own goroutine so a
	// slow session-server call can never stall bulk resolution. Full queue =
	// drop; the next sighting retries.
	refreshQ chan string

	// checkedAt memoizes the last profile refresh per UUID so repeat
	// sightings don't re-read the players table (and so a just-resolved
	// player isn't immediately re-fetched while the async write is in
	// flight). mu guards it — the Run and refresh goroutines both touch it —
	// and setChecked bounds its size.
	mu        sync.Mutex
	checkedAt map[string]int64

	// Timing knobs, shortened in tests.
	batchDelay time.Duration
	backoffMin time.Duration
	profileTTL time.Duration

	// now is time.Now, overridable in tests.
	now func() time.Time
}

// New builds a resolver. enabled=false makes every sighting resolve offline
// immediately (no network). db may be nil, in which case New returns nil and
// Observe/Run are no-ops.
func New(db *analytics.DB, enabled bool, logger *slog.Logger) *Resolver {
	if db == nil {
		return nil
	}
	return &Resolver{
		db:         db,
		logger:     logger,
		http:       &http.Client{Timeout: 10 * time.Second},
		enabled:    enabled,
		baseURL:    bulkLookupURL,
		profileURL: profileLookupURL,
		limiter:    newLimiter(1, 3), // 1 req/s, burst 3
		seen:       make(chan observation, 256),
		refreshQ:   make(chan string, 64),
		checkedAt:  map[string]int64{},
		batchDelay: defaultBatchDelay,
		backoffMin: defaultBackoffMin,
		profileTTL: defaultProfileTTL,
		now:        time.Now,
	}
}

// Observe records a sighting. Non-blocking and safe from the splice
// goroutine: a full queue drops the sighting (identity is best-effort).
func (r *Resolver) Observe(name, ip string, protocol int32) {
	if r == nil || name == "" {
		return
	}
	select {
	case r.seen <- observation{name: name, ip: ip, protocol: protocol}:
	default:
	}
}

// Run drains sightings and resolves them until ctx is cancelled, joining the
// profile-refresh worker before returning so no goroutine outlives it.
func (r *Resolver) Run(ctx context.Context) {
	if r == nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.refreshLoop(ctx)
	}()
	defer wg.Wait()
	pending := make(map[string]observation) // nameLower -> latest sighting
	var timer *time.Timer
	var timerC <-chan time.Time
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(r.batchDelay)
			timerC = timer.C
		}
	}
	disarm := func() {
		if timer != nil {
			timer.Stop()
			timer, timerC = nil, nil
		}
	}
	for {
		select {
		case <-ctx.Done():
			disarm()
			return
		case o := <-r.seen:
			nameLower := strings.ToLower(o.name)
			if r.handleCached(ctx, nameLower, o) {
				continue
			}
			pending[nameLower] = o
			if len(pending) >= batchSize {
				disarm()
				r.resolveBatch(ctx, pending)
				pending = make(map[string]observation)
			} else {
				arm()
			}
		case <-timerC:
			disarm()
			if len(pending) > 0 {
				r.resolveBatch(ctx, pending)
				pending = make(map[string]observation)
			}
		}
	}
}

// handleCached applies an identity straight from cache (or immediately
// offline when lookups are disabled) and reports whether it handled the
// sighting without needing a network lookup.
func (r *Resolver) handleCached(ctx context.Context, nameLower string, o observation) bool {
	if !r.enabled {
		r.applyOffline(nameLower, o)
		return true
	}
	e, err := r.db.CacheGet(nameLower)
	if err != nil {
		r.logger.Debug("players: cache read failed", "name", nameLower, "err", err)
		return false // fall through to a lookup
	}
	if !e.Found {
		return false
	}
	age := r.now().UnixMilli() - e.ResolvedMs
	if e.UUID != "" {
		if age > int64(positiveTTL/time.Millisecond) {
			return false // stale positive: re-resolve (also catches renames)
		}
		r.apply(ctx, e.UUID, o, false, false)
		return true
	}
	if age > int64(negativeTTL/time.Millisecond) {
		return false // stale negative: re-check in case the name got registered
	}
	r.applyOffline(nameLower, o)
	return true
}

// bulkEntry is one element of the lookup response.
type bulkEntry struct {
	ID   string `json:"id"`   // undashed 32-char hex
	Name string `json:"name"` // canonical casing
}

// resolveBatch looks up all pending names in one request and applies each
// result. On a rate-limit or server error it backs off and re-queues.
func (r *Resolver) resolveBatch(ctx context.Context, pending map[string]observation) {
	// Run flushes pending at batchSize, so the whole map always fits one
	// request.
	names := make([]string, 0, len(pending))
	for _, o := range pending {
		names = append(names, o.name)
	}
	if err := r.limiter.wait(ctx); err != nil {
		return // ctx cancelled
	}
	found, retry, err := r.lookup(ctx, names)
	if err != nil {
		if retry {
			r.backoff(ctx)
		}
		r.logger.Debug("players: bulk lookup failed", "names", len(names), "retry", retry, "err", err)
		// Re-queue by resolving offline-for-now is wrong (would negative-cache
		// a real name); instead just drop this round — the next sighting of the
		// same player retries.
		return
	}
	for lower, o := range pending {
		if canonical, ok := found[lower]; ok {
			// fresh=true: the canonical name just came off the wire, so the
			// profile-refresh clock restarts now.
			r.apply(ctx, canonical.uuid, observation{name: canonical.name, ip: o.ip, protocol: o.protocol}, false, true)
		} else {
			r.applyOffline(lower, o)
		}
	}
}

type resolved struct {
	uuid string
	name string
}

// lookup posts the bulk request. retry is true when the caller should back
// off (429 / 5xx / transport error) rather than treat the names as offline.
func (r *Resolver) lookup(ctx context.Context, names []string) (map[string]resolved, bool, error) {
	body, _ := json.Marshal(names)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		io.Copy(io.Discard, resp.Body)
		return nil, true, fmt.Errorf("players: lookup status %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, false, fmt.Errorf("players: lookup status %d", resp.StatusCode)
	}
	var entries []bulkEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, false, err
	}
	out := make(map[string]resolved, len(entries))
	for _, e := range entries {
		u := dashUUID(e.ID)
		if u == "" {
			continue
		}
		out[strings.ToLower(e.Name)] = resolved{uuid: u, name: e.Name}
	}
	return out, false, nil
}

// apply lands one identity. fresh means the resolution just came from a live
// bulk lookup (so the canonical profile is current); a cache-hit apply is not
// fresh and may trigger a profile refresh to catch renames.
func (r *Resolver) apply(ctx context.Context, uuid string, o observation, offline, fresh bool) {
	now := r.now().UnixMilli()
	id := analytics.Identity{
		UUID:      uuid,
		Name:      o.name,
		NameLower: strings.ToLower(o.name),
		IP:        o.ip,
		Protocol:  o.protocol,
		Offline:   offline,
		SeenMs:    now,
	}
	if fresh && !offline {
		id.ProfileCheckedMs = now
		r.setChecked(uuid, now)
	}
	r.db.ApplyIdentity(id)
	if !offline && !fresh {
		r.maybeRefresh(ctx, uuid)
	}
}

func (r *Resolver) applyOffline(nameLower string, o observation) {
	r.apply(context.Background(), analytics.OfflineUUID(nameLower), o, true, false)
}

func (r *Resolver) backoff(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(r.backoffMin):
	}
}

// checkedAtMax bounds the refresh-time memo; beyond it, entries older than
// profileTTL (useless — a fresh DB read would happen anyway) are swept.
const checkedAtMax = 4096

// checked reads the memoized last profile-refresh time.
func (r *Resolver) checked(uuid string) (int64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.checkedAt[uuid]
	return t, ok
}

// setChecked memoizes a profile-refresh time, sweeping expired entries when
// the map outgrows checkedAtMax.
func (r *Resolver) setChecked(uuid string, t int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.checkedAt) > checkedAtMax {
		cutoff := r.now().UnixMilli() - r.profileTTL.Milliseconds()
		for k, v := range r.checkedAt {
			if v < cutoff {
				delete(r.checkedAt, k)
			}
		}
	}
	r.checkedAt[uuid] = t
}

// dashUUID formats an undashed 32-char hex id as canonical dashed lowercase,
// or "" if it is not 32 hex chars.
func dashUUID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if len(id) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(id); err != nil {
		return ""
	}
	return id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:32]
}
