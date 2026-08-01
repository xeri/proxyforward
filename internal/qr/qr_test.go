package qr

import (
	"bytes"
	"strings"
	"testing"
)

// The encoder has no decoder to check it against and no third-party library to
// borrow one from, so correctness is pinned three ways: algebraic properties that
// are provable rather than transcribed (the Reed-Solomon and BCH codes), structural
// properties every reader depends on (finder patterns, timing, format bits), and a
// full read-back through the placement rules in reverse. A transcribed table would
// have been a fourth, but a table nobody can check by eye is how an unscannable
// symbol ships looking correct.

// TestGeneratorHasTheRootsItClaims: the generator polynomial for n error-correction
// codewords must vanish at α^0 … α^(n-1) — that is the definition of the code, and
// it holds without consulting any table.
func TestGeneratorHasTheRootsItClaims(t *testing.T) {
	for _, n := range []int{7, 10, 16, 18, 22, 24, 26} {
		g := generator(n)
		if len(g) != n+1 {
			t.Fatalf("generator(%d) has degree %d, want %d", n, len(g)-1, n)
		}
		for i := 0; i < n; i++ {
			if v := evalPoly(g, gfExp[i]); v != 0 {
				t.Errorf("generator(%d) at α^%d = %d, want 0", n, i, v)
			}
		}
	}
}

// TestReedSolomonLeavesNoSyndrome: a block followed by its error-correction
// codewords is a multiple of the generator, so every syndrome is zero. This is the
// property a reader relies on to notice damage.
func TestReedSolomonLeavesNoSyndrome(t *testing.T) {
	data := []byte("a pairing code, or any other bytes")
	for _, n := range []int{10, 18, 26} {
		full := append(append([]byte{}, data...), reedSolomon(data, n)...)
		for i := 0; i < n; i++ {
			if v := evalPoly(full, gfExp[i]); v != 0 {
				t.Errorf("syndrome %d of a %d-codeword block is %d, want 0", i, n, v)
			}
		}
		// And it must actually detect damage, or zero syndromes prove nothing.
		full[3] ^= 0x5A
		clean := true
		for i := 0; i < n; i++ {
			if evalPoly(full, gfExp[i]) != 0 {
				clean = false
			}
		}
		if clean {
			t.Errorf("a corrupted %d-codeword block produced no syndrome", n)
		}
	}
}

// TestFormatInformationStaysDistinguishable: the 32 format strings are a BCH code
// with minimum distance 7, which is what lets a reader recover the mask from a
// damaged corner. Asserting the distance checks the code itself, rather than
// re-typing the standard's table of bit strings.
func TestFormatInformationStaysDistinguishable(t *testing.T) {
	var all []uint32
	for level := 0; level < 4; level++ {
		for mask := 0; mask < 8; mask++ {
			all = append(all, bch(uint32(level<<3|mask), formatGenerator)^formatMask)
		}
	}
	for i := range all {
		if all[i] >= 1<<15 {
			t.Fatalf("format string %015b is wider than 15 bits", all[i])
		}
		for j := i + 1; j < len(all); j++ {
			if d := popcount(all[i] ^ all[j]); d < 7 {
				t.Errorf("format strings %015b and %015b differ in %d bits, want >= 7", all[i], all[j], d)
			}
		}
	}
}

// TestVersionInformationStaysDistinguishable: the same argument for the 18-bit
// version code, whose minimum distance is 8.
func TestVersionInformationStaysDistinguishable(t *testing.T) {
	var all []uint32
	for v := 7; v <= maxVersion; v++ {
		all = append(all, bch(uint32(v), versionGenerator))
	}
	for i := range all {
		if all[i] >= 1<<18 {
			t.Fatalf("version string %018b is wider than 18 bits", all[i])
		}
		for j := i + 1; j < len(all); j++ {
			if d := popcount(all[i] ^ all[j]); d < 8 {
				t.Errorf("version strings differ in %d bits, want >= 8", d)
			}
		}
	}
}

// TestSymbolStructure: the parts a reader looks for before it reads anything —
// three finder patterns, the timing runs, the dark module, and a size that matches
// the version.
func TestSymbolStructure(t *testing.T) {
	for v := 1; v <= maxVersion; v++ {
		payload := bytes.Repeat([]byte("x"), versions[v].dataCodewords()-3)
		c, err := Encode(payload)
		if err != nil {
			t.Fatalf("version %d: %v", v, err)
		}
		want := 17 + 4*v
		if c.Size != want {
			t.Fatalf("payload sized for version %d produced a %d-module symbol, want %d", v, c.Size, want)
		}
		for _, p := range [][2]int{{0, 0}, {0, c.Size - 7}, {c.Size - 7, 0}} {
			checkFinder(t, c, p[0], p[1])
		}
		for i := 8; i < c.Size-8; i++ {
			if c.Dark(i, 6) != (i%2 == 0) || c.Dark(6, i) != (i%2 == 0) {
				t.Fatalf("version %d: timing pattern broken at %d", v, i)
			}
		}
		if !c.Dark(8, c.Size-8) {
			t.Errorf("version %d: the dark module is not dark", v)
		}
	}
}

func checkFinder(t *testing.T, c *Code, y, x int) {
	t.Helper()
	for dy := 0; dy < 7; dy++ {
		for dx := 0; dx < 7; dx++ {
			d := max(abs(dy-3), abs(dx-3))
			if got := c.Dark(x+dx, y+dy); got != (d != 2) {
				t.Fatalf("finder at (%d,%d): module (%d,%d) is %v", y, x, dy, dx, got)
			}
		}
	}
}

// TestFormatInformationSitsWhereTheStandardPutsIt pins the two copies to explicit
// coordinates, transcribed from ISO/IEC 18004 figure 25 rather than derived from
// the encoder's loops. The layout is asymmetric, and a transposed one is entirely
// self-consistent — read back by this package's own reader it looks perfect, and no
// scanner in the world can read it. That bug was here; this is what catches it
// coming back. Coordinates are (row, column).
func TestFormatInformationSitsWhereTheStandardPutsIt(t *testing.T) {
	c, err := Encode([]byte("format"))
	if err != nil {
		t.Fatal(err)
	}
	n := c.Size
	// Bit i of the format string, in order, for each copy.
	first := [15][2]int{
		{0, 8}, {1, 8}, {2, 8}, {3, 8}, {4, 8}, {5, 8}, {7, 8}, {8, 8},
		{8, 7}, {8, 5}, {8, 4}, {8, 3}, {8, 2}, {8, 1}, {8, 0},
	}
	second := [15][2]int{
		{8, n - 1}, {8, n - 2}, {8, n - 3}, {8, n - 4}, {8, n - 5}, {8, n - 6}, {8, n - 7}, {8, n - 8},
		{n - 7, 8}, {n - 6, 8}, {n - 5, 8}, {n - 4, 8}, {n - 3, 8}, {n - 2, 8}, {n - 1, 8},
	}
	for i := 0; i < 15; i++ {
		a, b := first[i], second[i]
		if got, want := c.Dark(a[1], a[0]), c.Dark(b[1], b[0]); got != want {
			t.Errorf("format bit %d disagrees between its copies: (%d,%d)=%v, (%d,%d)=%v",
				i, a[0], a[1], got, b[0], b[1], want)
		}
	}
	// The two runs must not be confusable with each other: the module between the
	// second copy's halves is the dark module, and it is not format information.
	if !c.Dark(8, n-8) {
		t.Error("the dark module is not dark; the second format copy has taken its place")
	}
}

// TestEncodeReadsBack walks the symbol the way a reader does — recover the mask
// from the format information, unmask, undo the interleaving, check the syndromes,
// then parse the byte-mode header — and must arrive back at the input. It shares
// the encoder's tables but not its direction, so a wrong placement order, a wrong
// mask, or a mis-split block all surface here.
func TestEncodeReadsBack(t *testing.T) {
	for _, payload := range []string{
		"pxf://gw.example.com:8474/v2/pair/k3m7q-x2vn8-p4rt6-9wzhd-2xkb4-m7q3n",
		"x",
		"",
		strings.Repeat("long hostname pairing code ", 7),
	} {
		t.Run(payload[:min(len(payload), 24)], func(t *testing.T) {
			c, err := Encode([]byte(payload))
			if err != nil {
				t.Fatal(err)
			}
			got, err := readBack(c)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != payload {
				t.Fatalf("read back %q, want %q", got, payload)
			}
		})
	}
}

// TestEncodeRefusesWhatItCannotHold: past version 10 the answer is an error the
// caller can show, not a symbol too dense to scan.
func TestEncodeRefusesWhatItCannotHold(t *testing.T) {
	if _, err := Encode(bytes.Repeat([]byte("x"), 1000)); err == nil {
		t.Fatal("expected an error for a payload past the largest supported version")
	}
	// The boundary itself must still encode.
	if _, err := Encode(bytes.Repeat([]byte("x"), versions[maxVersion].dataCodewords()-3)); err != nil {
		t.Fatalf("the largest supported payload was refused: %v", err)
	}
}

// TestMaskIsChosenNotFixed: the mask is picked by score, so different payloads must
// not all land on the same one — if they did, the penalty pass would be dead code
// and symbols would carry avoidable finder-like runs.
func TestMaskIsChosenNotFixed(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 40; i++ {
		c, err := Encode([]byte(strings.Repeat("ab", i+1) + "pairing"))
		if err != nil {
			t.Fatal(err)
		}
		seen[maskOf(c)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("every payload chose mask %v; the penalty scoring is not selecting", seen)
	}
}

// A pairing code is the payload this package exists for: it must fit a symbol
// small enough to read off a screen from arm's length.
func TestPairingCodeFitsASmallSymbol(t *testing.T) {
	c, err := Encode([]byte("pxf://gw.example.com:8474/v2/pair/k3m7q-x2vn8-p4rt6-9wzhd-2xkb4-m7q3n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Size > 45 { // version 7
		t.Fatalf("a pairing code produced a %d-module symbol; that is too dense to scan at card size", c.Size)
	}
}

// --- reader-side helpers, used only by the tests above ---

func evalPoly(coeffs []byte, x byte) byte {
	var acc byte
	for _, c := range coeffs {
		acc = gfMul(acc, x) ^ c
	}
	return acc
}

func popcount(v uint32) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}

// maskOf recovers the mask a symbol was built with from its format information.
func maskOf(c *Code) int {
	var bits uint32
	for i := 0; i <= 5; i++ {
		if c.Dark(8, i) { // column 8, row i
			bits |= 1 << uint(i)
		}
	}
	if c.Dark(8, 7) {
		bits |= 1 << 6
	}
	if c.Dark(8, 8) {
		bits |= 1 << 7
	}
	if c.Dark(7, 8) {
		bits |= 1 << 8
	}
	for i := 9; i <= 14; i++ {
		if c.Dark(14-i, 8) { // row 8, column 14-i
			bits |= 1 << uint(i)
		}
	}
	// bch puts the five-bit value above its ten check bits, so the level and mask
	// live in the top of the fifteen.
	return int((bits^formatMask)>>10) & 7
}

// readBack undoes drawData, interleave and encodeData in turn.
func readBack(c *Code) ([]byte, error) {
	version := (c.Size - 17) / 4
	mask := maskOf(c)

	// Rebuild the function-module map so the walk skips exactly what the encoder
	// skipped, then read the stream back in the same zigzag.
	b := newBuilder(version)
	b.drawFunctionPatterns(version)

	var w bitWriter
	up := true
	for col := c.Size - 1; col > 0; col -= 2 {
		if col == 6 {
			col--
		}
		for i := 0; i < c.Size; i++ {
			y := i
			if up {
				y = c.Size - 1 - i
			}
			for k := 0; k < 2; k++ {
				x := col - k
				if b.isReserved(y, x) {
					continue
				}
				bit := c.Dark(x, y) != masked(mask, y, x)
				var v uint32
				if bit {
					v = 1
				}
				w.write(v, 1)
			}
		}
		up = !up
	}

	codewords := w.bytes[:versions[version].totalCwords]
	data, err := deinterleave(version, codewords)
	if err != nil {
		return nil, err
	}
	return parseByteMode(version, data)
}

// deinterleave splits the codeword stream back into blocks, checks each block's
// syndromes, and returns the concatenated data codewords.
func deinterleave(version int, codewords []byte) ([]byte, error) {
	v := versions[version]
	nblocks := v.group1 + v.group2
	sizes := make([]int, nblocks)
	for i := range sizes {
		sizes[i] = v.group1Data
		if i >= v.group1 {
			sizes[i]++
		}
	}
	blocks := make([][]byte, nblocks)
	at := 0
	for i := 0; i < v.group1Data+1; i++ {
		for j := 0; j < nblocks; j++ {
			if i < sizes[j] {
				blocks[j] = append(blocks[j], codewords[at])
				at++
			}
		}
	}
	for i := 0; i < v.ecPerBlock; i++ {
		for j := 0; j < nblocks; j++ {
			blocks[j] = append(blocks[j], codewords[at])
			at++
		}
	}
	var out []byte
	for _, b := range blocks {
		for i := 0; i < v.ecPerBlock; i++ {
			if evalPoly(b, gfExp[i]) != 0 {
				return nil, errSyndrome
			}
		}
		out = append(out, b[:len(b)-v.ecPerBlock]...)
	}
	return out, nil
}

func parseByteMode(version int, data []byte) ([]byte, error) {
	r := bitReader{data: data}
	if mode := r.read(4); mode != 0b0100 {
		return nil, errMode
	}
	n := int(r.read(countBits(version)))
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(r.read(8))
	}
	return out, nil
}

type bitReader struct {
	data []byte
	at   int
}

func (r *bitReader) read(bits int) uint32 {
	var v uint32
	for i := 0; i < bits; i++ {
		v <<= 1
		if r.data[r.at/8]&(1<<uint(7-r.at%8)) != 0 {
			v |= 1
		}
		r.at++
	}
	return v
}

var (
	errSyndrome = errTest("a block's syndromes were not zero")
	errMode     = errTest("the symbol does not start with a byte-mode header")
)

type errTest string

func (e errTest) Error() string { return string(e) }
