// Package mc implements the small slice of the Minecraft protocol
// proxyforward needs: VarInt/string primitives, packet framing, handshake
// parsing, and a status responder for the offline MOTD. Everything here
// reads untrusted bytes from the internet, so every length is capped before
// allocation and every parser has a fuzz target.
package mc

import (
	"errors"
	"fmt"
	"io"
)

var (
	ErrVarIntTooBig  = errors.New("mc: varint exceeds 5 bytes")
	ErrStringTooLong = errors.New("mc: string exceeds allowed length")
	ErrTruncated     = errors.New("mc: truncated data")
)

// ReadVarInt reads one Minecraft VarInt (little-endian base-128, at most 5
// bytes encoding an int32). It returns the value and the number of bytes
// consumed. Like the vanilla decoder it does not reject non-canonical
// encodings, only over-length ones.
func ReadVarInt(r io.Reader) (int32, int, error) {
	var (
		v   uint32
		n   int
		buf [1]byte
	)
	for {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			if err == io.EOF && n > 0 {
				err = io.ErrUnexpectedEOF // EOF mid-value is a truncation
			}
			return 0, n, err
		}
		b := buf[0]
		v |= uint32(b&0x7f) << (7 * n)
		n++
		if b&0x80 == 0 {
			return int32(v), n, nil
		}
		if n == 5 {
			return 0, n, ErrVarIntTooBig
		}
	}
}

// AppendVarInt appends the VarInt encoding of v to dst.
func AppendVarInt(dst []byte, v int32) []byte {
	u := uint32(v)
	for {
		b := byte(u & 0x7f)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		dst = append(dst, b)
		if u == 0 {
			return dst
		}
	}
}

// AppendString appends a length-prefixed UTF-8 string (VarInt byte length +
// bytes) to dst.
func AppendString(dst []byte, s string) []byte {
	dst = AppendVarInt(dst, int32(len(s)))
	return append(dst, s...)
}

// bodyReader parses packet bodies from a byte slice with sticky errors, so
// call sites read fields linearly and check once.
type bodyReader struct {
	b   []byte
	off int
	err error
}

func (r *bodyReader) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

func (r *bodyReader) varint() int32 {
	if r.err != nil {
		return 0
	}
	var (
		v uint32
		n int
	)
	for {
		if r.off >= len(r.b) {
			r.fail(ErrTruncated)
			return 0
		}
		b := r.b[r.off]
		r.off++
		v |= uint32(b&0x7f) << (7 * n)
		n++
		if b&0x80 == 0 {
			return int32(v)
		}
		if n == 5 {
			r.fail(ErrVarIntTooBig)
			return 0
		}
	}
}

// string reads a VarInt-prefixed UTF-8 string of at most maxBytes bytes.
func (r *bodyReader) string(maxBytes int) string {
	n := r.varint()
	if r.err != nil {
		return ""
	}
	if n < 0 || int(n) > maxBytes {
		r.fail(fmt.Errorf("%w: declared %d bytes, cap %d", ErrStringTooLong, n, maxBytes))
		return ""
	}
	if r.off+int(n) > len(r.b) {
		r.fail(ErrTruncated)
		return ""
	}
	s := string(r.b[r.off : r.off+int(n)])
	r.off += int(n)
	return s
}

func (r *bodyReader) uint16() uint16 {
	if r.err != nil {
		return 0
	}
	if r.off+2 > len(r.b) {
		r.fail(ErrTruncated)
		return 0
	}
	v := uint16(r.b[r.off])<<8 | uint16(r.b[r.off+1])
	r.off += 2
	return v
}

func (r *bodyReader) remaining() int { return len(r.b) - r.off }
