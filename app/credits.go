package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Creator credit — surfaced in the Settings "About" panel and the exe metadata.
const (
	CreatorName = "xeri"
	CreatorURL  = "https://github.com/xeri"

	avatarURL      = "https://github.com/xeri.png?size=128"
	avatarTTL      = 7 * 24 * time.Hour // re-check GitHub at most weekly
	avatarFetchDur = 6 * time.Second
)

// avatarMeta records what we need for a conditional refresh: the validators
// GitHub returned and when we last fetched, so we only re-download when the
// weekly TTL has elapsed AND the image actually changed.
type avatarMeta struct {
	ETag         string `json:"etag"`
	LastModified string `json:"lastModified"`
	FetchedMs    int64  `json:"fetchedMs"`
	ContentType  string `json:"contentType"`
}

func (a *App) avatarCacheDir() string { return filepath.Join(a.configDir, "cache") }
func (a *App) avatarImgPath() string  { return filepath.Join(a.avatarCacheDir(), "xeri-avatar.img") }
func (a *App) avatarMetaPath() string { return filepath.Join(a.avatarCacheDir(), "xeri-avatar.meta.json") }

// CreatorInfo returns the static creator credit for the About panel.
func (a *App) CreatorInfo() map[string]string {
	return map[string]string{"name": CreatorName, "url": CreatorURL}
}

// OpenCreatorURL opens the creator's GitHub page in the user's browser.
func (a *App) OpenCreatorURL() {
	runtime.BrowserOpenURL(a.ctx, CreatorURL)
}

// CreatorAvatar returns the creator's GitHub avatar as a data: URL, backed by a
// weekly on-disk cache. Fresh cache is served without any network call; a stale
// or missing cache triggers a conditional fetch (304 keeps the bytes, 200
// replaces them). On network failure a stale cache is still returned; only a
// cold miss surfaces an error so the GUI can fall back to a placeholder.
func (a *App) CreatorAvatar() (string, error) {
	a.avatarMu.Lock()
	defer a.avatarMu.Unlock()

	meta, _ := a.readAvatarMeta()
	img, imgErr := os.ReadFile(a.avatarImgPath())
	fresh := imgErr == nil && len(img) > 0 && meta != nil &&
		time.Since(time.UnixMilli(meta.FetchedMs)) < avatarTTL

	if fresh {
		return dataURL(meta.ContentType, img), nil
	}

	newImg, newMeta, err := a.fetchAvatar(meta)
	if err != nil {
		// Serve a stale copy if we have one; otherwise report the failure.
		if len(img) > 0 && meta != nil {
			return dataURL(meta.ContentType, img), nil
		}
		return "", err
	}
	return dataURL(newMeta.ContentType, newImg), nil
}

// fetchAvatar does the conditional GET and, on success, updates the cache. On a
// 304 it returns the existing bytes (and refreshes the timestamp) so the next
// check waits another full TTL.
func (a *App) fetchAvatar(prev *avatarMeta) ([]byte, *avatarMeta, error) {
	ctx, cancel := context.WithTimeout(a.ctx, avatarFetchDur)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, avatarURL, nil)
	if err != nil {
		return nil, nil, err
	}
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
		return nil, nil, err
	}
	defer resp.Body.Close()

	now := time.Now().UnixMilli()

	if resp.StatusCode == http.StatusNotModified && prev != nil {
		img, err := os.ReadFile(a.avatarImgPath())
		if err != nil {
			return nil, nil, fmt.Errorf("avatar 304 but cache unreadable: %w", err)
		}
		m := *prev
		m.FetchedMs = now
		a.writeAvatarMeta(&m) // best-effort timestamp refresh
		return img, &m, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("avatar fetch: unexpected status %d", resp.StatusCode)
	}

	img, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MiB cap
	if err != nil {
		return nil, nil, err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" || !strings.HasPrefix(ct, "image/") {
		ct = "image/png"
	}
	m := &avatarMeta{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedMs:    now,
		ContentType:  ct,
	}
	if err := os.MkdirAll(a.avatarCacheDir(), 0o700); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(a.avatarImgPath(), img, 0o600); err != nil {
		return nil, nil, err
	}
	a.writeAvatarMeta(m)
	return img, m, nil
}

func (a *App) readAvatarMeta() (*avatarMeta, error) {
	data, err := os.ReadFile(a.avatarMetaPath())
	if err != nil {
		return nil, err
	}
	var m avatarMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (a *App) writeAvatarMeta(m *avatarMeta) error {
	if err := os.MkdirAll(a.avatarCacheDir(), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(a.avatarMetaPath(), data, 0o600)
}

func dataURL(contentType string, img []byte) string {
	if contentType == "" {
		contentType = "image/png"
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(img)
}
