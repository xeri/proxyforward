package app

// The release check — the only outbound call proxyforward makes about itself,
// and the only one that ships turned off.
//
// The privacy charter says nothing leaves this machine by default, and that
// sentence has to stay literally true, so this asks nothing until someone turns
// it on. When they do, the request is a plain conditional GET to GitHub's
// releases API carrying a fixed User-Agent and no query string: identical from
// every install, so the bytes on the wire distinguish nobody. What GitHub can
// still see is the TLS connection itself — this machine's IP address, at most
// once a day — exactly as it already can for the creator avatar (credits.go),
// and the Settings hint says so rather than implying otherwise.
//
// Everything else is credits.go's shape, for the same reasons: ETag and
// Last-Modified so a repeat check transfers nothing, a disk cache so a restart
// is not a fresh request, a slow TTL, and a context deadline so a hung endpoint
// cannot park a binding. Nothing is ever downloaded or run — the whole result is
// a version string and a link a person clicks.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"proxyforward/internal/version"
)

const (
	// releasesAPI is the published-releases endpoint; drafts and prereleases are
	// excluded by GitHub itself, so this never surfaces an unfinished tag. The
	// release workflow builds a tag into a *draft* release, which means a tag
	// existing is not yet an update — /releases/latest is what knows that.
	releasesAPI = "https://api.github.com/repos/xeri/proxyforward/releases/latest"
	// releasesPage is where the user is sent. Deliberately the human page and
	// not an asset URL: nothing here downloads anything.
	releasesPage = "https://github.com/xeri/proxyforward/releases/latest"

	// updateUserAgent is constant across every install, on purpose. GitHub
	// requires a User-Agent; a version or machine name in it would make each
	// install's requests distinguishable, which is the one thing this must not
	// do.
	updateUserAgent = "proxyforward"

	updateTTL       = 24 * time.Hour
	updateFetchDur  = 8 * time.Second
	updateBodyLimit = 1 << 20 // 1 MiB: a release JSON is a few KiB
)

// UpdateInfo is the About panel's whole view of the release channel. It is
// returned even when the check is off, so the caller never has to pair it with a
// separate config read to know whether it means anything.
type UpdateInfo struct {
	// Enabled echoes the setting. False means no request was made and every
	// field below except Current is empty — not that the check failed.
	Enabled bool `json:"enabled"`
	// Current is this build's version, which is worth returning even when the
	// check is off: it is the half of the comparison we always know.
	Current string `json:"current"`
	// Latest is the newest published tag, or "" when nothing was asked. Never
	// guessed and never filled in from Current.
	Latest string `json:"latest"`
	// Newer is the only field that should move any pixels. It is false whenever
	// either version failed to parse: a build labelled with something this code
	// cannot read might be ahead of the release, and an update prompt drawn from
	// a version we could not compare is a guess wearing a fact's clothes.
	Newer bool `json:"newer"`
	// Comparable says the two versions could actually be ranked, which is what
	// keeps Newer:false from meaning two different things. Without it a build
	// stamped "handbuilt" would render as "up to date" — the one wrong answer
	// this whole feature exists to avoid. False means: show the latest tag as a
	// fact and pass no verdict.
	Comparable bool `json:"comparable"`
	// URL is the release page a person clicks. Empty unless Latest is set.
	URL string `json:"url"`
	// CheckedMs is when the answer was obtained, which for a cached one is the
	// original fetch. It is the difference between "up to date" and "up to date
	// as of a week ago on a laptop that has been offline since".
	CheckedMs int64 `json:"checkedMs"`
}

// updateCache is what survives a restart: the validators that make the next
// request free, plus the answer itself so a cold start inside the TTL needs no
// request at all.
type updateCache struct {
	ETag         string `json:"etag"`
	LastModified string `json:"lastModified"`
	FetchedMs    int64  `json:"fetchedMs"`
	Tag          string `json:"tag"`
}

func (a *App) updateCachePath() string {
	return filepath.Join(a.configDir, "cache", "update.json")
}

// SetUpdateCheck turns the release check on or off and persists it. Narrow and
// engine-independent like SetTheme, so the toggle cannot fail on unrelated
// validation — and turning it off is a promise, so the cached answer goes with
// it rather than lingering as a stale claim about a channel we stopped watching.
func (a *App) SetUpdateCheck(on bool) error {
	a.mu.Lock()
	a.cfg.UI.UpdateCheck = on
	err := a.cfg.Save(a.configPath)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if !on {
		a.updateMu.Lock()
		_ = os.Remove(a.updateCachePath()) // best effort; a stale file is re-validated anyway
		a.updateMu.Unlock()
	}
	return nil
}

// CheckForUpdate answers what the About panel needs to draw.
//
// Off is not an error and not a failure state: it returns immediately, having
// touched no socket, with Enabled false. On, and inside the TTL, it answers from
// disk — so opening Settings repeatedly is not a request per visit. Only a stale
// or missing cache reaches the network, and a failure there is returned as an
// error rather than papered over with a stale "up to date", because the two
// answers differ and only one of them is true.
func (a *App) CheckForUpdate() (*UpdateInfo, error) {
	a.mu.Lock()
	enabled := a.cfg.UI.UpdateCheck
	a.mu.Unlock()

	cur := version.Version
	if !enabled {
		return &UpdateInfo{Enabled: false, Current: cur}, nil
	}

	a.updateMu.Lock()
	defer a.updateMu.Unlock()

	cached, _ := a.readUpdateCache()
	if cached != nil && cached.Tag != "" && time.Since(time.UnixMilli(cached.FetchedMs)) < updateTTL {
		return updateInfoFor(cur, cached), nil
	}

	fresh, err := a.fetchLatestRelease(cached)
	if err != nil {
		return nil, err
	}
	return updateInfoFor(cur, fresh), nil
}

// updateInfoFor assembles the answer from a known-good cache entry. Newer is
// computed here rather than stored, so a build stamped with a new version
// re-decides against the same cached tag instead of trusting a verdict that was
// reached about a different binary.
func updateInfoFor(current string, c *updateCache) *UpdateInfo {
	newer, ok := versionIsNewer(c.Tag, current)
	url := releasesPage
	if c.Tag == "" {
		url = "" // nothing published: there is no page worth sending anyone to
	}
	return &UpdateInfo{
		Enabled:    true,
		Current:    current,
		Latest:     c.Tag,
		Newer:      ok && newer,
		Comparable: ok,
		URL:        url,
		CheckedMs:  c.FetchedMs,
	}
}

// fetchLatestRelease does the conditional GET and writes the cache. A 304 keeps
// the tag we hold and only moves the timestamp forward, so the next check waits
// another full TTL for zero bytes of body.
func (a *App) fetchLatestRelease(prev *updateCache) (*updateCache, error) {
	ctx, cancel := context.WithTimeout(a.ctx, updateFetchDur)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	if prev != nil {
		if prev.ETag != "" {
			req.Header.Set("If-None-Match", prev.ETag)
		}
		if prev.LastModified != "" {
			req.Header.Set("If-Modified-Since", prev.LastModified)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the release list: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	now := time.Now().UnixMilli()

	if resp.StatusCode == http.StatusNotModified && prev != nil && prev.Tag != "" {
		m := *prev
		m.FetchedMs = now
		_ = a.writeUpdateCache(&m) // best-effort timestamp refresh
		return &m, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		// No published release yet — a real answer, not a fault. Cached like any
		// other so a repo without releases is not asked again every visit.
		m := &updateCache{FetchedMs: now, Tag: ""}
		_ = a.writeUpdateCache(m)
		return m, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the release list answered %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, updateBodyLimit))
	if err != nil {
		return nil, err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("the release list is not the JSON this build expects: %w", err)
	}
	m := &updateCache{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedMs:    now,
		Tag:          strings.TrimSpace(rel.TagName),
	}
	_ = a.writeUpdateCache(m)
	return m, nil
}

func (a *App) readUpdateCache() (*updateCache, error) {
	data, err := os.ReadFile(a.updateCachePath())
	if err != nil {
		return nil, err
	}
	var c updateCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (a *App) writeUpdateCache(c *updateCache) error {
	if err := os.MkdirAll(filepath.Dir(a.updateCachePath()), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(a.updateCachePath(), data, 0o600)
}

// versionIsNewer reports whether latest is ahead of current, and whether the
// question could be answered at all. The second return is not decoration: an
// unparseable version on either side must produce no claim, because a build this
// code cannot read is as likely to be ahead of the release as behind it.
//
// The comparison is the numeric triple, then one pre-release rule: 1.2.0 is newer
// than 1.2.0-dev, because a pre-release precedes the release it is heading for.
// Two pre-releases at the same numbers are left uncompared — ordering "-rc.2"
// against "-dev.20260729" is a whole grammar, and the only consequence of
// declining is that a developer build does not nag about another developer build.
func versionIsNewer(latest, current string) (bool, bool) {
	lNum, lPre, lOK := parseVersion(latest)
	cNum, cPre, cOK := parseVersion(current)
	if !lOK || !cOK {
		return false, false
	}
	for i := range lNum {
		if lNum[i] != cNum[i] {
			return lNum[i] > cNum[i], true
		}
	}
	if cPre && !lPre {
		return true, true
	}
	return false, true
}

// parseVersion splits "v1.2.3-dev+sha" into its numeric triple and whether a
// pre-release suffix was present. Missing components read as 0, so "v1.2" is
// 1.2.0 — GitHub tags are written by hand and a two-part one is not a mystery.
// Anything with a non-numeric component is refused outright rather than
// salvaged: half-read is how a version comparison produces a confident lie.
func parseVersion(s string) (nums [3]int, pre bool, ok bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		pre = s[i] == '-' // build metadata ("+sha") is not a pre-release
		s = s[:i]
	}
	if s == "" {
		return nums, false, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return nums, false, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nums, false, false
		}
		nums[i] = n
	}
	return nums, pre, true
}
