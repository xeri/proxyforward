//go:build !linux || bindings

package main

import (
	"context"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Window geometry, in logical (DPI-scaled) pixels — the units Wails sizes
// windows and reports screens in.
const (
	// windowWidth/Height is the design size the window opens at when it fits.
	windowWidth, windowHeight = 1360, 820
	// windowMinWidth/Height is the floor the layout still works at: the UI has
	// a narrow step below 1150 px (tokens.css --ui-scale) and every screen
	// scrolls. It must stay below common small work areas — a 1080p display
	// at 150% is 1280×720 logical, ~1280×672 above the taskbar — or the OS
	// cannot shrink the window to fit and it overflows the screen.
	windowMinWidth, windowMinHeight = 1024, 640
	// screenMarginW/H is what fitting leaves around the window. Wails reports
	// the whole monitor, not its work area, so the vertical margin covers a
	// bottom taskbar/panel (48 px on Windows 11): centred, the window then
	// ends exactly above it with its title bar — the frameless window's only
	// drag handle and caption buttons — on-screen.
	screenMarginW, screenMarginH = 32, 96
)

// fitWindowSize picks the startup window size for a screen of the given
// logical size. ok=false means the design size already fits (leave it);
// maximise=true means even the floor would not fit with margins, so the
// window should take the work area instead.
func fitWindowSize(screenW, screenH int) (w, h int, maximise, ok bool) {
	if screenW <= 0 || screenH <= 0 {
		return 0, 0, false, false // unknown screen: keep the design size
	}
	w = min(windowWidth, screenW-screenMarginW)
	h = min(windowHeight, screenH-screenMarginH)
	if w == windowWidth && h == windowHeight {
		return 0, 0, false, false
	}
	if w < windowMinWidth || h < windowMinHeight {
		return 0, 0, true, true
	}
	return w, h, false, true
}

// fitWindowToScreen shrinks the window to the monitor it opened on when the
// design size does not fit. Without it, a screen smaller than the window
// (e.g. 1080p at 150%) gets a window overflowing every edge, with the title
// bar — and with it every way to move or restore the window — off-screen.
// Runs from OnStartup, before the page has loaded.
func fitWindowToScreen(ctx context.Context) {
	screens, err := wailsruntime.ScreenGetAll(ctx)
	if err != nil || len(screens) == 0 {
		return
	}
	scr := screens[0]
	for _, s := range screens {
		if s.IsCurrent {
			scr = s
			break
		}
		if s.IsPrimary {
			scr = s
		}
	}
	w, h, maximise, ok := fitWindowSize(scr.Size.Width, scr.Size.Height)
	switch {
	case !ok:
	case maximise:
		wailsruntime.WindowMaximise(ctx)
	default:
		wailsruntime.WindowSetSize(ctx, w, h)
		wailsruntime.WindowCenter(ctx)
	}
}
