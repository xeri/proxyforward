package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const avatarTestUUID = "069a79f4-44e9-4726-a5be-fca90e38aaf5"

var (
	faceRed   = color.NRGBA{0xff, 0x00, 0x00, 0xff}
	hatGreen  = color.NRGBA{0x00, 0xff, 0x00, 0xff}
	fallBlue  = color.NRGBA{0x00, 0x00, 0xff, 0xff}
	crafGold  = color.NRGBA{0xff, 0xaa, 0x00, 0xff}
	uniformly = color.NRGBA{0x80, 0x80, 0x80, 0xff}
)

// testSkin builds a 64×64 skin: red face, and a hat layer whose left half is
// transparent and right half green — so the composed head is red|green.
func testSkin(uniformHat bool) []byte {
	skin := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 8; y < 16; y++ {
		for x := 8; x < 16; x++ {
			skin.SetNRGBA(x, y, faceRed)
		}
		for x := 40; x < 48; x++ {
			switch {
			case uniformHat:
				skin.SetNRGBA(x, y, uniformly)
			case x >= 44:
				skin.SetNRGBA(x, y, hatGreen)
			}
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, skin)
	return buf.Bytes()
}

func solidPNG(size int, c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// avatarStub fakes the session server, the skin host, and both fallbacks,
// with per-endpoint request counters and failure switches.
type avatarStub struct {
	srv *httptest.Server

	profileFail atomic.Bool
	mcHeadsFail atomic.Bool
	crafFail    atomic.Bool

	// profileHold, when set, stalls profile responses until closed —
	// simulating a wedged session server.
	profileHold chan struct{}

	profileReqs atomic.Int32
	skinReqs    atomic.Int32
	mcHeadsReqs atomic.Int32
	crafReqs    atomic.Int32
}

func newAvatarStub(t *testing.T) *avatarStub {
	t.Helper()
	s := &avatarStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/profile/", func(w http.ResponseWriter, r *http.Request) {
		s.profileReqs.Add(1)
		if s.profileHold != nil {
			select {
			case <-s.profileHold:
			case <-r.Context().Done():
				return
			}
		}
		if s.profileFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		tex, _ := json.Marshal(map[string]any{
			"textures": map[string]any{"SKIN": map[string]string{"url": s.srv.URL + "/skin.png"}},
		})
		json.NewEncoder(w).Encode(map[string]any{
			"id": strings.TrimPrefix(r.URL.Path, "/profile/"),
			"properties": []map[string]string{
				{"name": "textures", "value": base64.StdEncoding.EncodeToString(tex)},
			},
		})
	})
	mux.HandleFunc("/skin.png", func(w http.ResponseWriter, _ *http.Request) {
		s.skinReqs.Add(1)
		w.Write(testSkin(false))
	})
	mux.HandleFunc("/mcheads/", func(w http.ResponseWriter, _ *http.Request) {
		s.mcHeadsReqs.Add(1)
		if s.mcHeadsFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(solidPNG(8, fallBlue))
	})
	mux.HandleFunc("/crafatar/", func(w http.ResponseWriter, _ *http.Request) {
		s.crafReqs.Add(1)
		if s.crafFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(solidPNG(8, crafGold))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func newTestAvatarCache(t *testing.T, stub *avatarStub) *avatarCache {
	t.Helper()
	c := newAvatarCache(filepath.Join(t.TempDir(), "avatars"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.profileURL = stub.srv.URL + "/profile/"
	c.mcHeadsURL = stub.srv.URL + "/mcheads/"
	c.crafatarURL = stub.srv.URL + "/crafatar/"
	c.skinHost = "127.0.0.1"
	return c
}

// fetch runs one handler request and returns status, body, and cache header.
func fetch(t *testing.T, c *avatarCache, path string) (int, []byte, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.Bytes(), rec.Header().Get("Cache-Control")
}

// waitAvatar polls path until the served head is long-lived (the background
// warm landed a real/stable render) and returns that response. The very first
// fetch of a cold head is expected to be an instant no-cache placeholder.
func waitAvatar(t *testing.T, c *avatarCache, path string) (int, []byte, string) {
	t.Helper()
	code, body, cache := fetch(t, c, path)
	if strings.Contains(cache, "86400") {
		return code, body, cache
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(15 * time.Millisecond)
		if code, body, cache = fetch(t, c, path); strings.Contains(cache, "86400") {
			return code, body, cache
		}
	}
	t.Fatalf("%s: warm never landed a long-lived head", path)
	return 0, nil, ""
}

// waitMiss polls until the id's .miss marker exists (a background warm
// exhausted the fallback chain).
func waitMiss(t *testing.T, c *avatarCache, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(c.missPath(id)); err == nil {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("%s: miss marker never appeared", id)
}

func decodeSquare(t *testing.T, b []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("served bytes are not a png: %v", err)
	}
	return toNRGBA8(img)
}

func TestComposeHead(t *testing.T) {
	skin, err := png.Decode(bytes.NewReader(testSkin(false)))
	if err != nil {
		t.Fatal(err)
	}
	head := composeHead(skin)
	if got := head.NRGBAAt(1, 3); got != faceRed {
		t.Fatalf("face pixel = %v, want red", got)
	}
	if got := head.NRGBAAt(6, 3); got != hatGreen {
		t.Fatalf("hat pixel = %v, want green overlay", got)
	}
	for i := 3; i < len(head.Pix); i += 4 {
		if head.Pix[i] != 0xff {
			t.Fatal("composed head has transparent pixels")
		}
	}

	// A uniform hat region is the legacy "no hat" fill: skipped entirely.
	skin, _ = png.Decode(bytes.NewReader(testSkin(true)))
	head = composeHead(skin)
	if got := head.NRGBAAt(6, 3); got != faceRed {
		t.Fatalf("uniform hat leaked over the face: %v", got)
	}
}

func TestPlaceholderFace(t *testing.T) {
	a := placeholderFace("offline:someone")
	b := placeholderFace("offline:someone")
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("placeholder is not deterministic")
	}
	if a.Bounds().Dx() != 8 || a.Bounds().Dy() != 8 {
		t.Fatalf("placeholder bounds = %v", a.Bounds())
	}
	// Steve and Alex variants both occur across ids.
	variants := map[string]bool{}
	for _, id := range []string{"offline:a", "offline:b", "offline:c", "offline:d"} {
		variants[string(placeholderFace(id).Pix[:4])] = true
	}
	if len(variants) < 2 {
		t.Fatal("only one placeholder variant across ids")
	}
}

func TestAvatarMojangComposeAndCache(t *testing.T) {
	stub := newAvatarStub(t)
	c := newTestAvatarCache(t, stub)

	// A cold head answers instantly with a no-cache placeholder while the
	// warm runs — the wall must never wait on the network.
	code0, body0, cache0 := fetch(t, c, "/pf/avatar/"+avatarTestUUID+".png?size=32")
	if code0 != http.StatusOK || !strings.Contains(cache0, "no-cache") {
		t.Fatalf("cold fetch: code=%d cache=%q, want instant no-cache placeholder", code0, cache0)
	}
	if decodeSquare(t, body0).Bounds().Dx() != 32 {
		t.Fatal("placeholder wrong size")
	}

	code, body, cache := waitAvatar(t, c, "/pf/avatar/"+avatarTestUUID+".png?size=32")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(cache, "max-age=86400") {
		t.Fatalf("composed head not long-lived: %q", cache)
	}
	img := decodeSquare(t, body)
	if img.Bounds().Dx() != 32 {
		t.Fatalf("size = %d, want 32", img.Bounds().Dx())
	}
	// Left half face red, right half hat green, scaled ×4.
	if img.NRGBAAt(4, 12) != faceRed || img.NRGBAAt(28, 12) != hatGreen {
		t.Fatalf("pixels = %v / %v", img.NRGBAAt(4, 12), img.NRGBAAt(28, 12))
	}
	// Master + size render are on disk.
	for _, size := range []int{8, 32} {
		if _, err := os.Stat(c.path(avatarTestUUID, size)); err != nil {
			t.Fatalf("cache file for size %d missing: %v", size, err)
		}
	}

	// A different size must reuse the master: no second profile fetch.
	code, body, _ = fetch(t, c, "/pf/avatar/"+avatarTestUUID+".png?size=64")
	if code != http.StatusOK || decodeSquare(t, body).Bounds().Dx() != 64 {
		t.Fatal("second size failed")
	}
	if n := stub.profileReqs.Load(); n != 1 {
		t.Fatalf("profile fetches = %d, want 1 (master should be reused)", n)
	}
	// And a repeat of the first size is a pure disk hit.
	skins := stub.skinReqs.Load()
	fetch(t, c, "/pf/avatar/"+avatarTestUUID+".png?size=32")
	if stub.skinReqs.Load() != skins {
		t.Fatal("disk hit still touched the network")
	}
}

func TestAvatarFallbackOrdering(t *testing.T) {
	stub := newAvatarStub(t)
	c := newTestAvatarCache(t, stub)
	stub.profileFail.Store(true)

	// Mojang down → mc-heads face served and cached as the master.
	id1 := "11111111-1111-4111-8111-111111111111"
	_, body, _ := waitAvatar(t, c, "/pf/avatar/"+id1+".png?size=16")
	if got := decodeSquare(t, body).NRGBAAt(8, 8); got != fallBlue {
		t.Fatalf("mc-heads fallback pixel = %v, want blue", got)
	}
	if stub.profileReqs.Load() != 1 || stub.mcHeadsReqs.Load() != 1 || stub.crafReqs.Load() != 0 {
		t.Fatalf("fallback order broke: profile=%d mcheads=%d craf=%d",
			stub.profileReqs.Load(), stub.mcHeadsReqs.Load(), stub.crafReqs.Load())
	}

	// mc-heads down too → crafatar.
	stub.mcHeadsFail.Store(true)
	id2 := "22222222-2222-4222-8222-222222222222"
	_, body, _ = waitAvatar(t, c, "/pf/avatar/"+id2+".png?size=16")
	if got := decodeSquare(t, body).NRGBAAt(8, 8); got != crafGold {
		t.Fatalf("crafatar fallback pixel = %v, want gold", got)
	}

	// Everything down → placeholder, no-cache, .miss marker, and the next
	// request must not touch the network at all.
	stub.crafFail.Store(true)
	id3 := "33333333-3333-4333-8333-333333333333"
	code, _, cache := fetch(t, c, "/pf/avatar/"+id3+".png?size=16")
	if code != http.StatusOK || strings.Contains(cache, "86400") {
		t.Fatalf("placeholder response: code=%d cache=%q", code, cache)
	}
	waitMiss(t, c, id3)
	if _, err := os.Stat(c.path(id3, 16)); err == nil {
		t.Fatal("failure placeholder was cached as a real head")
	}
	before := stub.profileReqs.Load() + stub.mcHeadsReqs.Load() + stub.crafReqs.Load()
	fetch(t, c, "/pf/avatar/"+id3+".png?size=16")
	if after := stub.profileReqs.Load() + stub.mcHeadsReqs.Load() + stub.crafReqs.Load(); after != before {
		t.Fatalf("miss marker did not stop refetching: %d -> %d requests", before, after)
	}
}

func TestAvatarSingleflight(t *testing.T) {
	stub := newAvatarStub(t)
	c := newTestAvatarCache(t, stub)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		size := 16 + (i%4)*16
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
				"/pf/avatar/"+avatarTestUUID+".png?size="+strconv.Itoa(size), nil))
		}()
	}
	wg.Wait()
	// The concurrent burst answered with placeholders; the warms it kicked
	// collapse onto one master build. Wait for it, then count.
	waitAvatar(t, c, "/pf/avatar/"+avatarTestUUID+".png?size=16")
	if n := stub.profileReqs.Load(); n != 1 {
		t.Fatalf("concurrent wall render made %d profile fetches, want 1", n)
	}
}

// TestAvatarFirstPaintInstant (A14): with a wedged upstream, the first
// request must answer in milliseconds with a no-cache placeholder; once the
// upstream recovers and a warm lands, the same URL serves the real head
// long-lived.
func TestAvatarFirstPaintInstant(t *testing.T) {
	stub := newAvatarStub(t)
	c := newTestAvatarCache(t, stub)
	stall := make(chan struct{})
	stub.profileHold = stall

	start := time.Now()
	code, body, cache := fetch(t, c, "/pf/avatar/"+avatarTestUUID+".png?size=64")
	elapsed := time.Since(start)
	if code != http.StatusOK || !strings.Contains(cache, "no-cache") {
		t.Fatalf("first paint: code=%d cache=%q", code, cache)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("first paint took %v behind a wedged upstream, want instant", elapsed)
	}
	if decodeSquare(t, body).Bounds().Dx() != 64 {
		t.Fatal("placeholder wrong size")
	}

	close(stall)
	_, body, cache = waitAvatar(t, c, "/pf/avatar/"+avatarTestUUID+".png?size=64")
	if !strings.Contains(cache, "86400") {
		t.Fatalf("real head not long-lived: %q", cache)
	}
	if got := decodeSquare(t, body).NRGBAAt(8, 8); got != faceRed {
		t.Fatalf("real head pixel = %v, want composed red face", got)
	}
}

func TestAvatarOfflinePlaceholder(t *testing.T) {
	stub := newAvatarStub(t)
	c := newTestAvatarCache(t, stub)

	code, body, cache := fetch(t, c, "/pf/avatar/offline:steve_alt.png?size=48")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if decodeSquare(t, body).Bounds().Dx() != 48 {
		t.Fatal("offline placeholder wrong size")
	}
	if !strings.Contains(cache, "86400") {
		t.Fatalf("offline placeholder should be long-lived: %q", cache)
	}
	if _, err := os.Stat(c.path("offline:steve_alt", 48)); err != nil {
		t.Fatalf("offline head not cached: %v", err)
	}
	if n := stub.profileReqs.Load() + stub.mcHeadsReqs.Load() + stub.crafReqs.Load(); n != 0 {
		t.Fatalf("offline id hit the network %d times", n)
	}
}

func TestAvatarHandlerValidation(t *testing.T) {
	stub := newAvatarStub(t)
	c := newTestAvatarCache(t, stub)

	for _, path := range []string{
		"/pf/avatar/../../secrets.png",
		"/pf/avatar/NotAUuid.png",
		"/pf/avatar/" + avatarTestUUID, // no .png
		"/other/path",
		"/pf/avatar/offline:UPPER.png", // offline names are lowercased
	} {
		if code, _, _ := fetch(t, c, path); code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", path, code)
		}
	}

	// Sizes clamp into 16..128; garbage falls back to 64.
	for q, want := range map[string]int{"size=999": 128, "size=1": 16, "size=banana": 64, "": 64} {
		sep := "?"
		_, body, _ := fetch(t, c, "/pf/avatar/offline:bob.png"+sep+q)
		if got := decodeSquare(t, body).Bounds().Dx(); got != want {
			t.Fatalf("%q: size = %d, want %d", q, got, want)
		}
	}
}

func TestAvatarEviction(t *testing.T) {
	stub := newAvatarStub(t)
	c := newTestAvatarCache(t, stub)
	c.maxFiles = 5
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	for i := 0; i < 10; i++ {
		p := filepath.Join(c.dir, strconv.Itoa(i+1)+"-64.png")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Older files have older mtimes; 1.png is the oldest.
		mt := now.Add(-time.Duration(10-i) * time.Hour)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	// An expired miss marker is swept regardless of the caps.
	miss := filepath.Join(c.dir, "gone.miss")
	os.WriteFile(miss, nil, 0o600)
	old := now.Add(-2 * missTTL)
	os.Chtimes(miss, old, old)

	c.evict()

	entries, err := os.ReadDir(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, e := range entries {
		kept = append(kept, e.Name())
	}
	if len(kept) != 5 {
		t.Fatalf("kept %d files (%v), want 5", len(kept), kept)
	}
	// The newest five survived.
	for _, name := range []string{"6-64.png", "7-64.png", "8-64.png", "9-64.png", "10-64.png"} {
		if _, err := os.Stat(filepath.Join(c.dir, name)); err != nil {
			t.Fatalf("newest file %s was evicted", name)
		}
	}
	if _, err := os.Stat(miss); err == nil {
		t.Fatal("expired miss marker survived eviction")
	}
	_ = stub
}
