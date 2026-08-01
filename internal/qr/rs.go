package qr

// Reed-Solomon error correction over GF(256), the field QR uses: byte-wide
// symbols modulo the primitive polynomial x^8+x^4+x^3+x^2+1 (0x11D), with 2 as the
// generating element. Nothing here is QR-specific beyond that choice.

const gfPrimitive = 0x11D

var (
	// gfExp is doubled in length so a sum of two logarithms indexes it without a
	// modulo — the inner loop of every multiplication below.
	gfExp [512]byte
	gfLog [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		if x <<= 1; x&0x100 != 0 {
			x ^= gfPrimitive
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// generator returns the polynomial whose roots are α^0 … α^(n-1), the divisor that
// produces n error-correction codewords. Coefficients are highest power first.
func generator(n int) []byte {
	g := []byte{1}
	for i := 0; i < n; i++ {
		// Multiply by (x + α^i).
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}
		g = next
	}
	return g
}

// reedSolomon returns the n error-correction codewords for one block: the
// remainder of the block's polynomial divided by the generator.
func reedSolomon(data []byte, n int) []byte {
	g := generator(n)
	rem := make([]byte, len(data)+n)
	copy(rem, data)
	for i := range data {
		lead := rem[i]
		if lead == 0 {
			continue
		}
		for j, c := range g {
			rem[i+j] ^= gfMul(c, lead)
		}
	}
	return rem[len(data):]
}

// bch computes the check bits the standard's format and version information carry:
// value shifted up by the generator's degree, reduced modulo the generator. Both
// fields are BCH codes; computing them beats transcribing two tables of bit strings
// that no reviewer can check by eye.
func bch(value, generator uint32) uint32 {
	deg := bitLen(generator) - 1
	rem := value << uint(deg)
	for bitLen(rem) >= bitLen(generator) {
		rem ^= generator << uint(bitLen(rem)-bitLen(generator))
	}
	return value<<uint(deg) | rem
}

func bitLen(v uint32) int {
	n := 0
	for ; v != 0; v >>= 1 {
		n++
	}
	return n
}
