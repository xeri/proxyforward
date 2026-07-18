package trayicon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// testBase renders a synthetic 1024×1024 base mark (a filled colored square on
// transparency) so the tests exercise real downscaling without the repo asset.
func testBase(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	for y := 128; y < 896; y++ {
		for x := 128; x < 896; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 40, G: 160, B: 240, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// icoEntries parses the ICONDIR and returns the (width, height) of each entry.
func icoEntries(t *testing.T, ico []byte) [][2]int {
	t.Helper()
	if len(ico) < 6 || binary.LittleEndian.Uint16(ico[2:4]) != 1 {
		t.Fatalf("not an ICO: % x", ico[:6])
	}
	n := int(binary.LittleEndian.Uint16(ico[4:6]))
	var out [][2]int
	for i := 0; i < n; i++ {
		e := ico[6+16*i:]
		out = append(out, [2]int{int(e[0]), int(e[1])})
	}
	return out
}

// xorPixels returns entry 0's raw bottom-up BGRA rows (the 16×16 image).
func xorPixels(t *testing.T, ico []byte) []byte {
	t.Helper()
	off := binary.LittleEndian.Uint32(ico[6+12 : 6+16])
	return ico[off+40 : off+40+16*16*4]
}

func TestRenderProducesTwoEntryICO(t *testing.T) {
	ico, err := Render(testBase(t), StateRunning)
	if err != nil {
		t.Fatal(err)
	}
	got := icoEntries(t, ico)
	want := [][2]int{{16, 16}, {32, 32}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestStatesRenderDistinctIcons(t *testing.T) {
	base := testBase(t)
	seen := map[string]State{}
	for _, st := range []State{StateRunning, StateStopped, StateFatal} {
		ico, err := Render(base, st)
		if err != nil {
			t.Fatal(err)
		}
		key := string(ico)
		if prev, dup := seen[key]; dup {
			t.Fatalf("state %v renders identically to state %v", st, prev)
		}
		seen[key] = st
	}
}

func TestStoppedIsDesaturated(t *testing.T) {
	// The stopped icon must be grey: every visible XOR pixel has R==G==B.
	ico, err := Render(testBase(t), StateStopped)
	if err != nil {
		t.Fatal(err)
	}
	px := xorPixels(t, ico)
	for i := 0; i+4 <= len(px); i += 4 {
		b, g, r, a := px[i], px[i+1], px[i+2], px[i+3]
		if a != 0 && (b != g || g != r) {
			t.Fatalf("pixel %d not grey: bgra=%d,%d,%d,%d", i/4, b, g, r, a)
		}
	}
}

func TestRepoAppIconRenders(t *testing.T) {
	// The runtime base is the checked-in app mark; render failure there means
	// no tray at all (RegisterTray fails soft), so keep the asset renderable.
	b, err := os.ReadFile(filepath.Join("..", "..", "build", "appicon.png"))
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []State{StateRunning, StateStopped, StateFatal} {
		if _, err := Render(b, st); err != nil {
			t.Fatalf("state %v: %v", st, err)
		}
	}
}

func TestFatalCarriesADot(t *testing.T) {
	// The fatal icon must place a strongly red pixel in the bottom-right
	// quadrant. Rows are bottom-up, so low row indices are the image's bottom.
	ico, err := Render(testBase(t), StateFatal)
	if err != nil {
		t.Fatal(err)
	}
	px := xorPixels(t, ico)
	found := false
	for row := 0; row < 8; row++ { // bottom half
		for x := 8; x < 16; x++ { // right half
			i := (row*16 + x) * 4
			b, g, r, a := px[i], px[i+1], px[i+2], px[i+3]
			if a == 255 && r > 180 && g < 120 && b < 120 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no red dot pixel in the bottom-right quadrant")
	}
}
