//go:build !linux

package main

import "testing"

// TestFitWindowSize pins the startup fit against real screens (logical px). An
// oversized window on a small screen overflowed every edge with the title bar
// off-screen, so the window could neither be moved nor restored.
func TestFitWindowSize(t *testing.T) {
	cases := []struct {
		name           string
		sw, sh         int
		w, h           int
		maximise, fits bool
	}{
		{"1080p at 100% keeps the design size", 1920, 1080, 0, 0, false, false},
		{"1440p at 125% keeps the design size", 2048, 1152, 0, 0, false, false},
		{"1080p at 125% loses height to the taskbar", 1536, 864, 1360, 768, false, true},
		{"1366x768 laptop shrinks both ways", 1366, 768, 1334, 672, false, true},
		{"1080p at 150% is below the floor: maximise", 1280, 720, 0, 0, true, true},
		{"unknown screen keeps the design size", 0, 0, 0, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, h, maximise, ok := fitWindowSize(tc.sw, tc.sh)
			if ok != tc.fits || maximise != tc.maximise || w != tc.w || h != tc.h {
				t.Fatalf("fitWindowSize(%d, %d) = (%d, %d, maximise=%v, ok=%v), want (%d, %d, maximise=%v, ok=%v)",
					tc.sw, tc.sh, w, h, maximise, ok, tc.w, tc.h, tc.maximise, tc.fits)
			}
			if ok && !maximise && (w > tc.sw || h > tc.sh || w < windowMinWidth || h < windowMinHeight) {
				t.Fatalf("fitted %dx%d escapes the screen %dx%d or the layout floor", w, h, tc.sw, tc.sh)
			}
		})
	}
}
