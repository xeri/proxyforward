package app

import (
	"fmt"
	"strings"

	"proxyforward/internal/qr"
)

// QRCode is a pairing code rendered as a QR symbol, in the one shape the GUI
// needs: the module count, and a single SVG path covering every dark module. A
// path rather than a matrix of booleans because the matrix crosses the binding as
// JSON — a version-5 symbol is 1369 of them — and because the frontend would only
// turn it into this anyway. The path's coordinate system is the module grid, so
// the caller sets one viewBox and scales freely.
type QRCode struct {
	// Size is the symbol's width in modules, quiet zone excluded — the caller
	// adds one, because how much margin the code needs is a layout question.
	Size int    `json:"size"`
	Path string `json:"path"`
}

// PairingQR renders a pairing code as a QR symbol so a second machine can read it
// off the screen instead of someone typing thirty characters into it. Pure
// computation with no engine state, so it answers whether or not a run is up.
//
// An error is a real answer here: a code too long for the largest symbol this
// encoder makes gets an honest "no QR for this one" in the UI, not a dense
// unscannable square.
func (a *App) PairingQR(code string) (QRCode, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return QRCode{}, fmt.Errorf("no pairing code to render")
	}
	c, err := qr.Encode([]byte(code))
	if err != nil {
		return QRCode{}, err
	}
	return QRCode{Size: c.Size, Path: svgPath(c)}, nil
}

// svgPath walks each row and emits one subpath per horizontal run of dark
// modules — a rectangle per run rather than per module, which is most of the
// string on a real symbol.
func svgPath(c *qr.Code) string {
	var b strings.Builder
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; {
			if !c.Dark(x, y) {
				x++
				continue
			}
			run := 0
			for x+run < c.Size && c.Dark(x+run, y) {
				run++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x, y, run, run)
			x += run
		}
	}
	return b.String()
}
