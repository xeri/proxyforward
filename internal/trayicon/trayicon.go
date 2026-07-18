// Package trayicon renders the system-tray icon for each engine run state.
// Pure image math over the app mark: decode PNG, box-downscale to the classic
// tray sizes, apply the state treatment, encode a two-entry ICO. No
// concurrency; callers render once at startup and cache the bytes.
//
// The ICO uses classic 32-bit BGRA BMP entries, not PNG-compressed ones: the
// systray library loads icons with LoadImage(LR_LOADFROMFILE), which predates
// PNG entries and reads BMP entries on every Windows version.
package trayicon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// State is the tray's summary of the run — the answer to "am I on?" that
// outlives the window. It deliberately mirrors engine.RunState + EngineFatal
// rather than importing them: the mapping is the caller's policy.
type State int

const (
	StateRunning State = iota // full-color mark: the tunnel is up (or draining)
	StateStopped              // dim grey mark: deliberately off
	StateFatal                // grey mark + red dot: the run died and needs you
)

// sizes are the classic tray raster sizes; Explorer picks per DPI.
var sizes = []int{16, 32}

// Render returns a two-entry ICO (16 and 32 px) of the base mark wearing st.
func Render(basePNG []byte, st State) ([]byte, error) {
	base, err := png.Decode(bytes.NewReader(basePNG))
	if err != nil {
		return nil, fmt.Errorf("decode base icon: %w", err)
	}
	var imgs []*image.NRGBA
	for _, size := range sizes {
		img := scale(base, size)
		switch st {
		case StateStopped:
			desaturate(img)
		case StateFatal:
			desaturate(img)
			dot(img)
		}
		imgs = append(imgs, img)
	}
	return encodeICO(imgs)
}

// scale box-averages src into a size×size image. Boxes are computed with
// integer bounds so any source size works; on the 1024² app mark they land
// exactly. Channels average premultiplied and convert to straight alpha once
// at the end — un-premultiplying per source pixel would amplify noise where
// alpha is low.
func scale(src image.Image, size int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	for dy := 0; dy < size; dy++ {
		for dx := 0; dx < size; dx++ {
			x0 := b.Min.X + dx*b.Dx()/size
			x1 := b.Min.X + (dx+1)*b.Dx()/size
			y0 := b.Min.Y + dy*b.Dy()/size
			y1 := b.Min.Y + (dy+1)*b.Dy()/size
			var r, g, bl, a, n uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					pr, pg, pb, pa := src.At(x, y).RGBA()
					r += uint64(pr)
					g += uint64(pg)
					bl += uint64(pb)
					a += uint64(pa)
					n++
				}
			}
			if n == 0 {
				continue
			}
			c := color.NRGBA64Model.Convert(color.RGBA64{
				R: uint16(r / n), G: uint16(g / n), B: uint16(bl / n), A: uint16(a / n),
			}).(color.NRGBA64)
			dst.SetNRGBA(dx, dy, color.NRGBA{
				R: uint8(c.R >> 8), G: uint8(c.G >> 8), B: uint8(c.B >> 8), A: uint8(c.A >> 8),
			})
		}
	}
	return dst
}

// desaturate turns the mark dim grey in place — luminance, then ×0.72 so the
// stopped icon reads hollow next to a lit taskbar, not merely monochrome.
func desaturate(img *image.NRGBA) {
	p := img.Pix
	for i := 0; i < len(p); i += 4 {
		l := (299*uint32(p[i]) + 587*uint32(p[i+1]) + 114*uint32(p[i+2])) / 1000
		l = l * 72 / 100
		p[i], p[i+1], p[i+2] = uint8(l), uint8(l), uint8(l)
	}
}

// dot stamps a solid alert disc in the bottom-right corner, with a ~1.5 px
// transparent moat cut from the art so it still separates at 16 px. The red
// mirrors the UI's --bad token family rather than pure #f00.
func dot(img *image.NRGBA) {
	size := img.Rect.Dx()
	r := float64(size) * 5.0 / 32.0
	cx, cy := float64(size)-r-1, float64(size)-r-1
	moat := r + 1.5
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d2 := dx*dx + dy*dy
			switch {
			case d2 <= r*r:
				img.SetNRGBA(x, y, color.NRGBA{R: 229, G: 72, B: 77, A: 255})
			case d2 <= moat*moat:
				img.SetNRGBA(x, y, color.NRGBA{})
			}
		}
	}
}

// encodeICO writes the images as one ICO: ICONDIR, one ICONDIRENTRY per image,
// then per image a BITMAPINFOHEADER (height doubled per the format), bottom-up
// BGRA XOR rows, and an all-zero 1-bpp AND mask (alpha carries the shape; the
// mask is still mandatory and its rows pad to 4 bytes).
func encodeICO(imgs []*image.NRGBA) ([]byte, error) {
	var dir, data bytes.Buffer
	le := binary.LittleEndian
	hdr := make([]byte, 6)
	le.PutUint16(hdr[2:], 1) // resource type: icon
	le.PutUint16(hdr[4:], uint16(len(imgs)))
	dir.Write(hdr)
	offset := 6 + 16*len(imgs)
	for _, img := range imgs {
		size := img.Rect.Dx()
		maskStride := ((size + 31) / 32) * 4
		imageBytes := 40 + size*size*4 + maskStride*size

		e := make([]byte, 16)
		e[0], e[1] = byte(size%256), byte(size%256) // 0 means 256
		le.PutUint16(e[6:], 1)                      // planes
		le.PutUint16(e[8:], 32)                     // bits per pixel
		le.PutUint32(e[10:], uint32(imageBytes))
		le.PutUint32(e[12:], uint32(offset))
		dir.Write(e)
		offset += imageBytes

		bih := make([]byte, 40)
		le.PutUint32(bih[0:], 40)
		le.PutUint32(bih[4:], uint32(size))
		le.PutUint32(bih[8:], uint32(size*2)) // XOR + AND heights, combined
		le.PutUint16(bih[12:], 1)             // planes
		le.PutUint16(bih[14:], 32)            // bpp
		data.Write(bih)
		for y := size - 1; y >= 0; y-- { // bottom-up rows
			for x := 0; x < size; x++ {
				c := img.NRGBAAt(x, y)
				data.Write([]byte{c.B, c.G, c.R, c.A})
			}
		}
		data.Write(make([]byte, maskStride*size)) // AND mask: all visible
	}
	return append(dir.Bytes(), data.Bytes()...), nil
}
