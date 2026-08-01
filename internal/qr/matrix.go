package qr

// Symbol construction: the function patterns every reader looks for, the data
// placed around them, and the mask chosen to keep that data from looking like a
// function pattern. ISO/IEC 18004 §6.7-6.8.
//
// Coordinates are (y, x) — row then column — throughout, so the drawing order
// matches the standard's figures.

const (
	// formatGenerator/formatMask are the BCH(15,5) code and the XOR the standard
	// applies to format information, so an all-zero format (level M, mask 0) is
	// still visually distinct from a blank symbol.
	formatGenerator = 0x537
	formatMask      = 0x5412
	// versionGenerator is the BCH(18,6) code for version information, carried only
	// by version 7 and up.
	versionGenerator = 0x1F25
	// ecLevelM is level M's two-bit code in the format information.
	ecLevelM = 0b00
)

// builder is one symbol under construction: its modules, and which of them belong
// to function patterns and so must not take data.
type builder struct {
	size     int
	modules  []bool
	reserved []bool
}

func newBuilder(version int) *builder {
	size := 17 + 4*version
	return &builder{size: size, modules: make([]bool, size*size), reserved: make([]bool, size*size)}
}

func (b *builder) set(y, x int, dark bool)  { b.modules[y*b.size+x] = dark }
func (b *builder) reserve(y, x int)         { b.reserved[y*b.size+x] = true }
func (b *builder) isReserved(y, x int) bool { return b.reserved[y*b.size+x] }
func (b *builder) fn(y, x int, dark bool)   { b.set(y, x, dark); b.reserve(y, x) }
func (b *builder) inBounds(y, x int) bool   { return y >= 0 && x >= 0 && y < b.size && x < b.size }

// draw builds the whole symbol for one mask: function patterns, then data, then
// the format and version information that describe it.
func draw(version int, codewords []byte, mask int) *Code {
	b := newBuilder(version)
	b.drawFunctionPatterns(version)
	b.drawData(codewords, mask)
	b.drawFormat(mask)
	b.drawVersion(version)
	return &Code{Size: b.size, Modules: b.modules}
}

func (b *builder) drawFunctionPatterns(version int) {
	// Finder patterns and their separators, at three corners.
	for _, p := range [][2]int{{0, 0}, {0, b.size - 7}, {b.size - 7, 0}} {
		b.drawFinder(p[0], p[1])
	}
	// Timing patterns: alternating modules along row and column 6, which give a
	// reader its module pitch.
	for i := 8; i < b.size-8; i++ {
		b.fn(6, i, i%2 == 0)
		b.fn(i, 6, i%2 == 0)
	}
	b.drawAlignment(version)
	// The dark module: always set, always here.
	b.fn(b.size-8, 8, true)
	b.reserveFormat()
	if version >= 7 {
		b.reserveVersion()
	}
}

// drawFinder places one 7×7 finder pattern at (y, x) and the one-module light
// separator around it, clipped at the symbol's edges.
func (b *builder) drawFinder(y, x int) {
	for dy := -1; dy <= 7; dy++ {
		for dx := -1; dx <= 7; dx++ {
			py, px := y+dy, x+dx
			if !b.inBounds(py, px) {
				continue
			}
			// The pattern is a filled 3×3 core, a light ring, and a dark border:
			// true wherever the Chebyshev distance from the centre is 0, 1 or 3.
			d := max(abs(dy-3), abs(dx-3))
			b.fn(py, px, d != 2 && d <= 3)
		}
	}
}

// drawAlignment places a 5×5 alignment pattern at each pairing of the version's
// centre coordinates, except the three that would land on a finder pattern.
func (b *builder) drawAlignment(version int) {
	centers := alignmentCenters[version]
	for _, cy := range centers {
		for _, cx := range centers {
			onFinder := (cy == 6 && cx == 6) ||
				(cy == 6 && cx == b.size-7) ||
				(cy == b.size-7 && cx == 6)
			if onFinder {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					b.fn(cy+dy, cx+dx, max(abs(dy), abs(dx)) != 1)
				}
			}
		}
	}
}

// reserveFormat marks the modules format information will occupy, so the data
// placement walks around them. The bits themselves land after masking, because
// they describe the mask.
func (b *builder) reserveFormat() {
	for i := 0; i <= 8; i++ {
		if i != 6 { // column/row 6 is the timing pattern, already placed
			b.reserve(8, i)
			b.reserve(i, 8)
		}
	}
	for i := 0; i < 8; i++ {
		b.reserve(b.size-1-i, 8)
		b.reserve(8, b.size-1-i)
	}
}

func (b *builder) reserveVersion() {
	for i := 0; i < 18; i++ {
		a, c := i/3, i%3
		b.reserve(b.size-11+c, a)
		b.reserve(a, b.size-11+c)
	}
}

// drawFormat writes the 15-bit format information — error correction level and
// mask — twice, so losing one corner does not cost a reader the ability to
// interpret the rest.
func (b *builder) drawFormat(mask int) {
	bits := bch(uint32(ecLevelM<<3|mask), formatGenerator) ^ formatMask
	at := func(i int) bool { return bits&(1<<uint(i)) != 0 }

	// First copy, around the top-left finder: bits 0-5 down column 8, then the two
	// modules either side of the timing row's crossing, then bits 9-14 leftward
	// along row 8. The layout is not symmetric and must not be transposed — a
	// mirrored format block is self-consistent to this package and unreadable to
	// every scanner.
	for i := 0; i <= 5; i++ {
		b.set(i, 8, at(i))
	}
	b.set(7, 8, at(6))
	b.set(8, 8, at(7))
	b.set(8, 7, at(8))
	for i := 9; i <= 14; i++ {
		b.set(8, 14-i, at(i))
	}

	// Second copy: bits 0-7 leftward along row 8 from the right edge, bits 8-14
	// down column 8 from above the bottom-left finder. Eight modules and seven,
	// because the module between them is the dark module.
	for i := 0; i <= 7; i++ {
		b.set(8, b.size-1-i, at(i))
	}
	for i := 8; i <= 14; i++ {
		b.set(b.size-15+i, 8, at(i))
	}
}

// drawVersion writes the 18-bit version information, present from version 7 where
// a reader can no longer infer the size reliably from the finder spacing alone.
func (b *builder) drawVersion(version int) {
	if version < 7 {
		return
	}
	bits := bch(uint32(version), versionGenerator)
	for i := 0; i < 18; i++ {
		on := bits&(1<<uint(i)) != 0
		a, c := i/3, i%3
		b.set(b.size-11+c, a, on)
		b.set(a, b.size-11+c, on)
	}
}

// drawData walks the codeword stream into the symbol: upward and downward through
// two-module-wide columns from the right edge, skipping function modules, applying
// the mask as it goes. Column 6 is skipped whole — it is the vertical timing
// pattern, and the standard's zigzag steps over it rather than around it.
func (b *builder) drawData(codewords []byte, mask int) {
	bit, up := 0, true
	for col := b.size - 1; col > 0; col -= 2 {
		if col == 6 {
			col--
		}
		for i := 0; i < b.size; i++ {
			y := i
			if up {
				y = b.size - 1 - i
			}
			for c := 0; c < 2; c++ {
				x := col - c
				if b.isReserved(y, x) {
					continue
				}
				dark := false
				if bit < len(codewords)*8 {
					dark = codewords[bit/8]&(1<<uint(7-bit%8)) != 0
					bit++
				}
				// Remainder bits past the last codeword stay light before masking,
				// which the mask then flips like any other data module.
				b.set(y, x, dark != masked(mask, y, x))
			}
		}
		up = !up
	}
}

// masked reports whether the mask flips the module at (y, x). The eight patterns
// are the standard's; the point of having eight is that whichever way the data
// falls, one of them breaks up the runs and blocks a reader would mistake for a
// function pattern.
func masked(mask, y, x int) bool {
	switch mask {
	case 0:
		return (y+x)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (y+x)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return (y*x)%2+(y*x)%3 == 0
	case 6:
		return ((y*x)%2+(y*x)%3)%2 == 0
	default:
		return ((y+x)%2+(y*x)%3)%2 == 0
	}
}

// penalty scores a masked symbol by the standard's four rules; the mask with the
// lowest score is the one shipped. Lower is more scannable: the rules punish long
// runs, solid blocks, anything resembling a finder pattern, and an overall balance
// far from half dark.
func penalty(c *Code) int {
	return penaltyRuns(c) + penaltyBlocks(c) + penaltyFinderLike(c) + penaltyBalance(c)
}

// Rule 1: five or more same-coloured modules in a row cost 3, plus 1 per module
// past the fifth.
func penaltyRuns(c *Code) int {
	score := 0
	count := func(at func(i int) bool) {
		run, prev := 0, false
		for i := 0; i < c.Size; i++ {
			if v := at(i); i > 0 && v == prev {
				run++
			} else {
				run, prev = 1, v
			}
			if run == 5 {
				score += 3
			} else if run > 5 {
				score++
			}
		}
	}
	for i := 0; i < c.Size; i++ {
		count(func(j int) bool { return c.Dark(j, i) })
		count(func(j int) bool { return c.Dark(i, j) })
	}
	return score
}

// Rule 2: every 2×2 block of one colour costs 3.
func penaltyBlocks(c *Code) int {
	score := 0
	for y := 0; y+1 < c.Size; y++ {
		for x := 0; x+1 < c.Size; x++ {
			v := c.Dark(x, y)
			if c.Dark(x+1, y) == v && c.Dark(x, y+1) == v && c.Dark(x+1, y+1) == v {
				score += 3
			}
		}
	}
	return score
}

// Rule 3: the 1:1:3:1:1 finder proportion with four light modules on either side
// costs 40 wherever it appears in the data, because a reader would take it for a
// finder pattern and mis-locate the symbol.
func penaltyFinderLike(c *Code) int {
	pattern := []bool{true, false, true, true, true, false, true}
	light4 := []bool{false, false, false, false}
	matches := func(at func(i int) bool, start int, want []bool) bool {
		for i, w := range want {
			if start+i >= c.Size || at(start+i) != w {
				return false
			}
		}
		return true
	}
	score := 0
	scan := func(at func(i int) bool) {
		for i := 0; i < c.Size; i++ {
			if matches(at, i, pattern) && (matches(at, i+7, light4) || (i >= 4 && matches(at, i-4, light4))) {
				score += 40
			}
		}
	}
	for i := 0; i < c.Size; i++ {
		scan(func(j int) bool { return c.Dark(j, i) })
		scan(func(j int) bool { return c.Dark(i, j) })
	}
	return score
}

// Rule 4: 10 points for every 5% the dark proportion strays from half.
func penaltyBalance(c *Code) int {
	dark := 0
	for _, m := range c.Modules {
		if m {
			dark++
		}
	}
	percent := dark * 100 / len(c.Modules)
	return abs(percent-50) / 5 * 10
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
