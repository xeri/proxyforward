// Avatar pipeline (GUI-owned). The engine resolves identities; the GUI turns
// a player id into a head image: /pf/avatar/<id>.png?size=N served by the
// Wails asset server. The network layer produces one canonical 8×8 master
// face per player — Mojang profile → skin → compose face+hat locally, falling
// back to mc-heads.net, then crafatar.com, then an embedded placeholder — and
// every requested size is a nearest-neighbor upscale of that master, cached
// on disk. Keying the singleflight and the caches on the player id (not
// id+size) is what collapses a whole wall render to at most one profile fetch
// per player, which the session server's tight per-IP rate limit
// (~200 req/10 min) demands.
package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	avatarMinSize = 16
	avatarMaxSize = 128
	// masterSize is the composed face itself; below the public clamp so the
	// "<id>-8.png" master can never collide with a served size.
	masterSize = 8

	// One profile fetch per player per spacing window; failures shorter than
	// a full fallback miss retry after missTTL.
	profileSpacing = 60 * time.Second
	missTTL        = 15 * time.Minute

	avatarFetchTimeout = 5 * time.Second
	maxAvatarBody      = 1 << 20 // skins are ~2-16 KiB; 1 MiB is generous

	// Disk cache bounds, enforced at startup and every 6 h by mtime.
	evictEvery    = 6 * time.Hour
	evictMaxFiles = 4000
	evictMaxBytes = 64 << 20
)

// avatarID accepts a dashed-lowercase Mojang UUID or an offline pseudo-id.
// Anything else is a 404 — ids become cache filenames, so this is also the
// path-traversal guard.
var avatarID = regexp.MustCompile(`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|offline:[a-z0-9_]{1,16})$`)

// avatarCache owns the on-disk head cache and the fetch pipeline.
type avatarCache struct {
	dir    string
	logger *slog.Logger
	http   *http.Client

	// Endpoints and the skin-host allowlist, overridden in tests.
	profileURL  string
	mcHeadsURL  string // + "<id>/<size>"
	crafatarURL string // + "<id>?size=N&overlay"
	skinHost    string

	mu        sync.Mutex
	inflight  map[string]*avatarFlight
	lastTry   map[string]time.Time // per-profile session-server spacing
	tokens    float64              // session-server token bucket (1/s, burst 3)
	tokensAt  time.Time
	lastEvict time.Time

	// warmSem caps concurrent background master builds; a full semaphore
	// skips the warm (that head warms on a later request).
	warmSem chan struct{}

	// Cache bounds, shrunk in tests.
	maxFiles int
	maxBytes int64

	now func() time.Time
}

type avatarFlight struct {
	done      chan struct{}
	img       *image.NRGBA
	cacheable bool
}

func newAvatarCache(dir string, logger *slog.Logger) *avatarCache {
	return &avatarCache{
		dir:         dir,
		logger:      logger,
		http:        &http.Client{Timeout: avatarFetchTimeout},
		profileURL:  "https://sessionserver.mojang.com/session/minecraft/profile/",
		mcHeadsURL:  "https://mc-heads.net/avatar/",
		crafatarURL: "https://crafatar.com/avatars/",
		skinHost:    "textures.minecraft.net",
		inflight:    map[string]*avatarFlight{},
		lastTry:     map[string]time.Time{},
		warmSem:     make(chan struct{}, 3),
		tokens:      3,
		maxFiles:    evictMaxFiles,
		maxBytes:    evictMaxBytes,
		now:         time.Now,
	}
}

// AvatarHandler serves player heads for the frontend. It is mounted as the
// Wails asset-server fallback handler, so it must 404 anything that is not an
// avatar path.
func (a *App) AvatarHandler() http.Handler { return a.avatars }

// ServeHTTP never blocks on the network: anything not servable from disk (or
// pure CPU) answers with the procedural placeholder immediately while a
// background warm builds the real head — a 60-head wall paints instantly and
// fills in as warms land.
func (c *avatarCache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutPrefix(r.URL.Path, "/pf/avatar/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	id, ok = strings.CutSuffix(id, ".png")
	if !ok || !avatarID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	size := clampAvatarSize(r.URL.Query().Get("size"))
	c.maybeEvict()

	// Fast path: this exact render is on disk.
	if b, err := os.ReadFile(c.path(id, size)); err == nil {
		serveAvatar(w, b, true)
		return
	}

	// Synchronous no-network paths: a stable offline placeholder, or a new
	// scale of a master already on disk.
	if strings.HasPrefix(id, "offline:") {
		c.render(w, placeholderFace(id), id, size, true)
		return
	}
	if img := c.readMaster(id); img != nil {
		c.render(w, img, id, size, true)
		return
	}

	// True miss: warm in the background, serve the placeholder now. no-cache
	// so the wall's retry picks up the real head once the warm lands.
	c.warm(id)
	c.render(w, placeholderFace(id), id, size, false)
}

// render encodes and serves one head at the requested size; cacheable renders
// also land on disk.
func (c *avatarCache) render(w http.ResponseWriter, img *image.NRGBA, id string, size int, cacheable bool) {
	b, err := encodePNG(scaleNearest(img, size))
	if err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	if cacheable {
		c.writeFile(c.path(id, size), b)
	}
	serveAvatar(w, b, cacheable)
}

// warm builds the master for id off the request path. The semaphore bounds
// concurrent builds; the singleflight in master collapses duplicate ids; the
// miss marker and token bucket inside the build chain keep their guarantees.
func (c *avatarCache) warm(id string) {
	select {
	case c.warmSem <- struct{}{}:
	default:
		return // enough warms in flight; this head warms on a later request
	}
	go func() {
		defer func() { <-c.warmSem }()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c.master(ctx, id)
	}()
}

func serveAvatar(w http.ResponseWriter, b []byte, longLived bool) {
	w.Header().Set("Content-Type", "image/png")
	if longLived {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	} else {
		// Placeholder while the real head warms (or after a full-chain miss):
		// no-cache keeps the browser re-asking so the swap happens.
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Write(b)
}

func clampAvatarSize(q string) int {
	n, err := strconv.Atoi(q)
	if err != nil {
		return 64
	}
	return min(max(n, avatarMinSize), avatarMaxSize)
}

// path is the cache filename for one rendered size (or the size-8 master).
func (c *avatarCache) path(id string, size int) string {
	return filepath.Join(c.dir, strings.ReplaceAll(id, ":", "_")+"-"+strconv.Itoa(size)+".png")
}

func (c *avatarCache) missPath(id string) string {
	return filepath.Join(c.dir, strings.ReplaceAll(id, ":", "_")+".miss")
}

// master returns the player's 8×8 face and whether it may be cached (a real
// face or a stable offline placeholder — but never a failure placeholder,
// which must not freeze into the size cache). Concurrent callers for the same
// id collapse onto one build.
func (c *avatarCache) master(ctx context.Context, id string) (*image.NRGBA, bool) {
	c.mu.Lock()
	if f, ok := c.inflight[id]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.img, f.cacheable
		case <-ctx.Done():
			return placeholderFace(id), false
		}
	}
	f := &avatarFlight{done: make(chan struct{})}
	c.inflight[id] = f
	c.mu.Unlock()

	f.img, f.cacheable = c.buildMaster(ctx, id)
	close(f.done)

	c.mu.Lock()
	delete(c.inflight, id)
	c.mu.Unlock()
	return f.img, f.cacheable
}

func (c *avatarCache) buildMaster(ctx context.Context, id string) (*image.NRGBA, bool) {
	// Offline/cracked players have no Mojang profile by definition: a stable
	// embedded placeholder, no network.
	if strings.HasPrefix(id, "offline:") {
		return placeholderFace(id), true
	}
	if img := c.readMaster(id); img != nil {
		return img, true
	}
	if fi, err := os.Stat(c.missPath(id)); err == nil && c.now().Sub(fi.ModTime()) < missTTL {
		return placeholderFace(id), false
	}

	img := c.fromMojang(ctx, id)
	if img == nil {
		img = c.fetchFace(ctx, c.mcHeadsURL+id+"/"+strconv.Itoa(masterSize))
	}
	if img == nil {
		img = c.fetchFace(ctx, c.crafatarURL+id+"?size="+strconv.Itoa(masterSize)+"&overlay")
	}
	if img == nil {
		c.writeFile(c.missPath(id), []byte{})
		return placeholderFace(id), false
	}
	os.Remove(c.missPath(id))
	if b, err := encodePNG(img); err == nil {
		c.writeFile(c.path(id, masterSize), b)
	}
	return img, true
}

func (c *avatarCache) readMaster(id string) *image.NRGBA {
	b, err := os.ReadFile(c.path(id, masterSize))
	if err != nil {
		return nil
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	return toNRGBA8(img)
}

// fromMojang is the primary path: session-server profile → textures property
// → skin PNG → locally composed face. Returns nil on any failure or when the
// per-profile spacing / token bucket says not now (the caller falls back).
func (c *avatarCache) fromMojang(ctx context.Context, id string) *image.NRGBA {
	c.mu.Lock()
	if c.now().Sub(c.lastTry[id]) < profileSpacing {
		c.mu.Unlock()
		return nil
	}
	// Bound the spacing memo: entries past their window are dead weight.
	if len(c.lastTry) > 2048 {
		for k, t := range c.lastTry {
			if c.now().Sub(t) >= profileSpacing {
				delete(c.lastTry, k)
			}
		}
	}
	c.lastTry[id] = c.now()
	c.mu.Unlock()
	if err := c.takeToken(ctx); err != nil {
		return nil
	}

	body, err := c.get(ctx, c.profileURL+strings.ReplaceAll(id, "-", ""))
	if err != nil {
		c.logger.Debug("avatars: profile fetch failed", "id", id, "err", err)
		return nil
	}
	skinURL, err := skinURLFromProfile(body)
	if err != nil {
		c.logger.Debug("avatars: profile decode failed", "id", id, "err", err)
		return nil
	}
	if u, err := url.Parse(skinURL); err != nil || u.Hostname() != c.skinHost {
		c.logger.Debug("avatars: unexpected skin host", "id", id, "url", skinURL)
		return nil
	}
	raw, err := c.get(ctx, skinURL)
	if err != nil {
		c.logger.Debug("avatars: skin fetch failed", "id", id, "err", err)
		return nil
	}
	skin, err := png.Decode(bytes.NewReader(raw))
	if err != nil || skin.Bounds().Dx() < 64 || skin.Bounds().Dy() < 32 {
		c.logger.Debug("avatars: bad skin image", "id", id, "err", err)
		return nil
	}
	return composeHead(skin)
}

// fetchFace grabs a pre-rendered 8×8 head from a fallback service.
func (c *avatarCache) fetchFace(ctx context.Context, u string) *image.NRGBA {
	raw, err := c.get(ctx, u)
	if err != nil {
		c.logger.Debug("avatars: fallback fetch failed", "url", u, "err", err)
		return nil
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	out := toNRGBA8(img)
	if out.Bounds().Dx() != masterSize {
		// The service ignored the size hint; sample it down to the master.
		out = resample(out, masterSize)
	}
	return out
}

func (c *avatarCache) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxAvatarBody))
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxAvatarBody))
}

// takeToken enforces ~1 session-server request per second (burst 3) across
// all players, waiting (ctx-bound) for the next token.
func (c *avatarCache) takeToken(ctx context.Context) error {
	for {
		c.mu.Lock()
		now := c.now()
		if !c.tokensAt.IsZero() {
			c.tokens += now.Sub(c.tokensAt).Seconds()
		}
		c.tokensAt = now
		if c.tokens > 3 {
			c.tokens = 3
		}
		if c.tokens >= 1 {
			c.tokens--
			c.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - c.tokens) * float64(time.Second))
		c.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// skinURLFromProfile digs the skin URL out of a session-server profile body
// (the base64 "textures" property).
func skinURLFromProfile(body []byte) (string, error) {
	var profile struct {
		Properties []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		return "", err
	}
	for _, p := range profile.Properties {
		if p.Name != "textures" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(p.Value)
		if err != nil {
			return "", err
		}
		var tex struct {
			Textures struct {
				Skin struct {
					URL string `json:"url"`
				} `json:"SKIN"`
			} `json:"textures"`
		}
		if err := json.Unmarshal(raw, &tex); err != nil {
			return "", err
		}
		if tex.Textures.Skin.URL == "" {
			return "", fmt.Errorf("profile has no skin")
		}
		return tex.Textures.Skin.URL, nil
	}
	return "", fmt.Errorf("profile has no textures property")
}

// composeHead cuts the face (8,8)–(16,16) from a skin and overlays the hat
// layer (40,8)–(48,16). A uniform hat region is the legacy "no hat" fill and
// is skipped. Works for both 64×64 and legacy 64×32 skins (both regions live
// in the top half).
func composeHead(skin image.Image) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, masterSize, masterSize))
	draw.Draw(out, out.Bounds(), skin, image.Pt(8, 8), draw.Src)
	if !uniformRegion(skin, 40, 8) {
		draw.Draw(out, out.Bounds(), skin, image.Pt(40, 8), draw.Over)
	}
	// Heads are opaque: some skins leave face pixels transparent and rely on
	// the hat; force alpha so composited output never shows holes.
	for i := 3; i < len(out.Pix); i += 4 {
		out.Pix[i] = 0xff
	}
	return out
}

func uniformRegion(img image.Image, x0, y0 int) bool {
	first := img.At(x0, y0)
	fr, fg, fb, fa := first.RGBA()
	for y := y0; y < y0+masterSize; y++ {
		for x := x0; x < x0+masterSize; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if r != fr || g != fg || b != fb || a != fa {
				return false
			}
		}
	}
	return true
}

// scaleNearest upscales the 8×8 master with hard pixel edges — the Minecraft
// look; any smoothing would blur it.
func scaleNearest(src *image.NRGBA, size int) *image.NRGBA {
	if src.Bounds().Dx() == size {
		return src
	}
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	for y := 0; y < size; y++ {
		sy := src.Bounds().Min.Y + y*sh/size
		for x := 0; x < size; x++ {
			sx := src.Bounds().Min.X + x*sw/size
			out.SetNRGBA(x, y, src.NRGBAAt(sx, sy))
		}
	}
	return out
}

// resample is scaleNearest for arbitrary source sizes (fallback services may
// ignore the size hint).
func resample(src *image.NRGBA, size int) *image.NRGBA { return scaleNearest(src, size) }

func toNRGBA8(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

func encodePNG(img *image.NRGBA) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeFile lands a cache file atomically (tmp + rename) so a crashed write
// can never serve as a truncated PNG.
func (c *avatarCache) writeFile(path string, b []byte) {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		c.logger.Debug("avatars: mkdir failed", "err", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		c.logger.Debug("avatars: cache write failed", "path", path, "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		c.logger.Debug("avatars: cache rename failed", "path", path, "err", err)
	}
}

// maybeEvict runs the cache sweep on the first request and every evictEvery
// after. Inline (not a goroutine): a directory scan of ≤4000 entries is
// milliseconds, and it keeps the cache free of lifecycle management.
func (c *avatarCache) maybeEvict() {
	c.mu.Lock()
	due := c.now().Sub(c.lastEvict) >= evictEvery
	if due {
		c.lastEvict = c.now()
	}
	c.mu.Unlock()
	if due {
		c.evict()
	}
}

// evict drops expired miss markers and, when the cache exceeds its bounds,
// the oldest files by mtime until it fits.
func (c *avatarCache) evict() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	type f struct {
		path  string
		size  int64
		mtime time.Time
	}
	var files []f
	var total int64
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		path := filepath.Join(c.dir, e.Name())
		if strings.HasSuffix(e.Name(), ".miss") {
			if c.now().Sub(fi.ModTime()) >= missTTL {
				os.Remove(path)
			}
			continue
		}
		files = append(files, f{path, fi.Size(), fi.ModTime()})
		total += fi.Size()
	}
	if len(files) <= c.maxFiles && total <= c.maxBytes {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })
	removed := 0
	for _, x := range files {
		if len(files)-removed <= c.maxFiles && total <= c.maxBytes {
			break
		}
		if os.Remove(x.path) == nil {
			removed++
			total -= x.size
		}
	}
	if removed > 0 {
		c.logger.Debug("avatars: cache evicted", "files", removed)
	}
}

// ---- embedded placeholders ----

// placeholderFace is a procedural Steve- or Alex-style head for players with
// no fetchable skin. The pick follows the id hash, like the vanilla default.
func placeholderFace(id string) *image.NRGBA {
	var h uint32
	for _, b := range []byte(id) {
		h = h*31 + uint32(b)
	}
	grid, pal := steveGrid, stevePalette
	if h&1 == 1 {
		grid, pal = alexGrid, alexPalette
	}
	out := image.NewNRGBA(image.Rect(0, 0, masterSize, masterSize))
	for y, row := range grid {
		for x := 0; x < masterSize; x++ {
			out.SetNRGBA(x, y, pal[row[x]])
		}
	}
	return out
}

// 8×8 pixel grids: h hair, s skin, d skin shadow, w eye white, e iris,
// n nose, m mouth.
var steveGrid = [masterSize]string{
	"hhhhhhhh",
	"hhhhhhhh",
	"hssssssh",
	"ssssssss",
	"swessews",
	"sssnnsss",
	"ssnmmnss",
	"ssssssss",
}

var stevePalette = map[byte]color.NRGBA{
	'h': {0x3b, 0x2d, 0x22, 0xff},
	's': {0xb5, 0x8d, 0x6d, 0xff},
	'd': {0x9c, 0x76, 0x57, 0xff},
	'w': {0xff, 0xff, 0xff, 0xff},
	'e': {0x52, 0x3d, 0x91, 0xff},
	'n': {0x7a, 0x5b, 0x47, 0xff},
	'm': {0x8a, 0x5a, 0x44, 0xff},
}

var alexGrid = [masterSize]string{
	"hhhhhhhh",
	"hhhhhhhh",
	"hssssssh",
	"ssssssss",
	"swessews",
	"sssnnsss",
	"ssdmmdss",
	"ssssssss",
}

var alexPalette = map[byte]color.NRGBA{
	'h': {0xcf, 0x8a, 0x3e, 0xff},
	's': {0xdd, 0xac, 0x89, 0xff},
	'd': {0xc4, 0x92, 0x70, 0xff},
	'w': {0xff, 0xff, 0xff, 0xff},
	'e': {0x4f, 0x7d, 0x38, 0xff},
	'n': {0xb0, 0x83, 0x63, 0xff},
	'm': {0xa8, 0x6f, 0x53, 0xff},
}
