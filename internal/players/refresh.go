// Name-change tracking. Mojang removed the public name-history endpoint in
// September 2022 (it now returns 410), so the only way to notice a rename is
// to re-fetch the canonical profile of players we actually see connecting.
// A known player observed live with a profile check older than profileTTL
// gets one GET to the session server, through the same token bucket as bulk
// lookups — the session server enforces a tight per-IP limit (~200 req/10min).
package players

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maybeRefresh enqueues a profile re-fetch when the last check is older than
// profileTTL. It never blocks the Run goroutine: the fetch itself happens on
// the refresh worker, and a full queue drops the request (the next sighting
// retries). ctx is unused now that the network hop moved off this goroutine;
// kept so call sites read naturally against apply's signature.
func (r *Resolver) maybeRefresh(_ context.Context, uuid string) {
	if !r.enabled || strings.HasPrefix(uuid, "offline:") {
		return
	}
	now := r.now().UnixMilli()
	ttl := r.profileTTL.Milliseconds()
	if t, ok := r.checked(uuid); ok && now-t < ttl {
		return
	}
	checked, err := r.db.ProfileCheckedMs(uuid)
	if err != nil {
		r.logger.Debug("players: profile-check read failed", "uuid", uuid, "err", err)
		return
	}
	if now-checked < ttl {
		r.setChecked(uuid, checked)
		return
	}
	select {
	case r.refreshQ <- uuid:
	default: // queue full — drop; best-effort
	}
}

// refreshLoop drains profile re-fetch requests until ctx is cancelled. Runs
// on its own goroutine (joined by Run) so a slow session-server call stalls
// only rename detection, never bulk resolution.
func (r *Resolver) refreshLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case uuid := <-r.refreshQ:
			now := r.now().UnixMilli()
			// Repeat sightings can queue duplicates; re-check the memo once
			// the earlier fetch has landed.
			if t, ok := r.checked(uuid); ok && now-t < r.profileTTL.Milliseconds() {
				continue
			}
			r.refreshProfile(ctx, uuid, now)
		}
	}
}

// profileResp is the session-server profile body (properties ignored).
type profileResp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// refreshProfile fetches the canonical profile and lands the (possibly
// renamed) name. A 204/404 (profile gone) still stamps the check so a dead
// UUID isn't re-fetched every sighting; rate-limit and transport errors stamp
// nothing so a later sighting retries.
func (r *Resolver) refreshProfile(ctx context.Context, uuid string, nowMs int64) {
	if err := r.limiter.wait(ctx); err != nil {
		return // ctx cancelled
	}
	url := r.profileURL + strings.ReplaceAll(uuid, "-", "")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	resp, err := r.http.Do(req)
	if err != nil {
		r.logger.Debug("players: profile fetch failed", "uuid", uuid, "err", err)
		return
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		var p profileResp
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil || p.Name == "" {
			r.logger.Debug("players: profile decode failed", "uuid", uuid, "err", err)
			return
		}
		r.db.ApplyProfileCheck(uuid, p.Name, nowMs)
		r.setChecked(uuid, nowMs)
	case resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound:
		io.Copy(io.Discard, resp.Body)
		r.db.ApplyProfileCheck(uuid, "", nowMs)
		r.setChecked(uuid, nowMs)
	default:
		io.Copy(io.Discard, resp.Body)
		r.logger.Debug("players: profile fetch rejected", "uuid", uuid,
			"err", fmt.Errorf("status %d", resp.StatusCode))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			r.backoff(ctx)
		}
	}
}
