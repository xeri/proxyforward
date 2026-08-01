// Package qr encodes a short ASCII string as a QR matrix.
//
// It exists for one artifact: the pairing code. Handing a machine a code by
// pointing a phone at the screen beats reading thirty characters aloud, and the
// alternative to ~400 lines here was a JavaScript dependency in a frontend that
// ships four of them and has no test runner — this way the encoder sits behind
// `go test`, where the rest of the repo's correctness lives.
//
// Scope is deliberately narrow: byte mode, error correction level M, versions 1
// through 10 (up to 216 bytes, where a pairing code is ~70). Anything longer is an
// error, not a bigger symbol — the caller shows its unavailable state rather than
// rendering something no camera will read at card size. Kanji, ECI, structured
// append and the other modes are absent because nothing here would ever emit them.
//
// The implementation follows ISO/IEC 18004. Its fixed tables are the standard's;
// its variable-length ones (format and version information) are computed with the
// BCH codes the standard specifies, because a transcribed table is a typo waiting
// to become an unscannable symbol.
//
// Cross-checked once against an independent implementation (kazuhikoarase's
// qrcode-generator), module for module, at versions 1, 5 and 6 — which covers
// alignment patterns, multi-block interleaving, both character-count widths, and
// the format and version information. To repeat it: dump this encoder's matrices
// as rows of 0/1 and compare against `qrcode(0,'M').addData(text,'Byte')`.
//
// The two agree everywhere except which of the eight masks they pick, and that
// difference is the reference's: its penalty rule 3 omits the four light modules
// the current standard requires beside the finder-like pattern, and its rule 4
// does not floor the dark-ratio deviation to a multiple of five. Either choice
// yields a valid symbol — the format information names the mask — so this follows
// the standard's text rather than the reference. Forcing the reference's mask
// reproduces its matrices exactly.
package qr

import (
	"fmt"
)

// Code is a rendered QR symbol: Size×Size modules, row-major, true = dark.
type Code struct {
	Size    int
	Modules []bool
}

// Dark reports whether the module at (x, y) is dark. Out-of-range is light, so
// callers can walk a quiet zone without bounds arithmetic.
func (c *Code) Dark(x, y int) bool {
	if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
		return false
	}
	return c.Modules[y*c.Size+x]
}

// maxVersion is where this encoder stops. A pairing code needs version 3; the
// headroom covers a long gateway hostname, and past it a symbol is too dense to
// scan from a screen at the size the GUI shows one anyway.
const maxVersion = 10

// ecBlocks describes one version's error-correction layout at level M: how many
// codewords each block corrects, and the data codeword count of each block group.
// Blocks in the second group hold exactly one more data codeword than the first.
type ecBlocks struct {
	ecPerBlock  int
	group1      int // number of blocks in the first group
	group1Data  int // data codewords in each of them
	group2      int // number of blocks in the second group (often 0)
	totalCwords int // data + EC codewords for the whole symbol
}

// Level M, versions 1..10, from ISO/IEC 18004 table 9. Index 0 is unused so the
// slot number is the version number.
var versions = [maxVersion + 1]ecBlocks{
	1:  {ecPerBlock: 10, group1: 1, group1Data: 16, totalCwords: 26},
	2:  {ecPerBlock: 16, group1: 1, group1Data: 28, totalCwords: 44},
	3:  {ecPerBlock: 26, group1: 1, group1Data: 44, totalCwords: 70},
	4:  {ecPerBlock: 18, group1: 2, group1Data: 32, totalCwords: 100},
	5:  {ecPerBlock: 24, group1: 2, group1Data: 43, totalCwords: 134},
	6:  {ecPerBlock: 16, group1: 4, group1Data: 27, totalCwords: 172},
	7:  {ecPerBlock: 18, group1: 4, group1Data: 31, totalCwords: 196},
	8:  {ecPerBlock: 22, group1: 2, group1Data: 38, group2: 2, totalCwords: 242},
	9:  {ecPerBlock: 22, group1: 3, group1Data: 36, group2: 2, totalCwords: 292},
	10: {ecPerBlock: 26, group1: 4, group1Data: 43, group2: 1, totalCwords: 346},
}

// dataCodewords is the payload capacity of a version: every codeword that is not
// error correction.
func (b ecBlocks) dataCodewords() int {
	return b.totalCwords - (b.group1+b.group2)*b.ecPerBlock
}

// alignmentCenters are the row/column coordinates of alignment pattern centres per
// version (ISO/IEC 18004 table E.1). A pattern is placed at every pairing of these
// except the three that would sit on a finder pattern.
var alignmentCenters = [maxVersion + 1][]int{
	2: {6, 18}, 3: {6, 22}, 4: {6, 26}, 5: {6, 30}, 6: {6, 34},
	7: {6, 22, 38}, 8: {6, 24, 42}, 9: {6, 26, 46}, 10: {6, 28, 50},
}

// Encode renders data as the smallest level-M QR symbol that holds it, choosing
// the mask that scores best under the standard's penalty rules.
func Encode(data []byte) (*Code, error) {
	version, err := fitVersion(len(data))
	if err != nil {
		return nil, err
	}
	codewords := interleave(version, encodeData(version, data))

	best, bestScore := (*Code)(nil), -1
	for mask := 0; mask < 8; mask++ {
		c := draw(version, codewords, mask)
		if s := penalty(c); bestScore < 0 || s < bestScore {
			best, bestScore = c, s
		}
	}
	return best, nil
}

// fitVersion picks the smallest version whose data capacity holds n payload bytes
// plus the mode nibble and the character count that precede them.
func fitVersion(n int) (int, error) {
	for v := 1; v <= maxVersion; v++ {
		if 4+countBits(v)+n*8 <= versions[v].dataCodewords()*8 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("qr: %d bytes exceeds what a version-%d level-M symbol holds (%d)",
		n, maxVersion, versions[maxVersion].dataCodewords()-1-countBits(maxVersion)/8)
}

// countBits is the width of the character-count field in byte mode: 8 bits up to
// version 9, 16 from version 10 (ISO/IEC 18004 table 3).
func countBits(version int) int {
	if version <= 9 {
		return 8
	}
	return 16
}

// encodeData builds the version's data codewords: the byte-mode header, the
// payload, the terminator, and the alternating pad bytes that fill the rest.
func encodeData(version int, data []byte) []byte {
	var b bitWriter
	b.write(0b0100, 4) // byte mode
	b.write(uint32(len(data)), countBits(version))
	for _, c := range data {
		b.write(uint32(c), 8)
	}

	capacity := versions[version].dataCodewords() * 8
	b.write(0, min(4, capacity-b.len())) // terminator, truncated if it would not fit
	b.write(0, (8-b.len()%8)%8)          // pad to a codeword boundary

	// The standard's pad pattern, alternating until the capacity is met.
	for pad := 0; b.len() < capacity; pad++ {
		if pad%2 == 0 {
			b.write(0xEC, 8)
		} else {
			b.write(0x11, 8)
		}
	}
	return b.bytes
}

// interleave splits the data into the version's blocks, appends each block's
// Reed-Solomon codewords, and reorders both by column — the standard's
// interleaving, which is what makes a burst of damage land across many blocks
// instead of destroying one.
func interleave(version int, data []byte) []byte {
	v := versions[version]
	blocks := make([][]byte, 0, v.group1+v.group2)
	ecs := make([][]byte, 0, v.group1+v.group2)
	for i, at := 0, 0; i < v.group1+v.group2; i++ {
		n := v.group1Data
		if i >= v.group1 {
			n++ // the second group's blocks are one codeword longer
		}
		block := data[at : at+n]
		at += n
		blocks = append(blocks, block)
		ecs = append(ecs, reedSolomon(block, v.ecPerBlock))
	}

	out := make([]byte, 0, v.totalCwords)
	for i := 0; i < v.group1Data+1; i++ {
		for _, b := range blocks {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	for i := 0; i < v.ecPerBlock; i++ {
		for _, e := range ecs {
			out = append(out, e[i])
		}
	}
	return out
}

// bitWriter accumulates a big-endian bit stream into whole bytes.
type bitWriter struct {
	bytes []byte
	nbits int
}

func (b *bitWriter) len() int { return b.nbits }

func (b *bitWriter) write(v uint32, bits int) {
	for i := bits - 1; i >= 0; i-- {
		if b.nbits%8 == 0 {
			b.bytes = append(b.bytes, 0)
		}
		if v&(1<<uint(i)) != 0 {
			b.bytes[b.nbits/8] |= 1 << uint(7-b.nbits%8)
		}
		b.nbits++
	}
}
