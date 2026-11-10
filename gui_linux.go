//go:build linux

package main

import "errors"

// errNoGUI answers a bare launch on Linux with what to run instead.
var errNoGUI = errors.New("proxyforward has no GUI on linux; run it headless: " +
	"`proxyforward gateway`, or `proxyforward pair <pairing-code>` then `proxyforward agent` (see --help)")

// runGUI on Linux refuses: Linux runs headless only. The GUI (Wails over
// WebKitGTK) needed per-driver workarounds just to paint there, and leaving it
// out keeps cgo, gtk and webkit out of the linux build, so a plain `go build`
// yields one static binary with no frontend to embed. A pxf:// link passed as
// the sole argument lands here too; the message points it at `pair`.
func runGUI(string) error {
	return errNoGUI
}
