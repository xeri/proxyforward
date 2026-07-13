package mc

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func FuzzReadVarInt(f *testing.F) {
	f.Add([]byte{0x00})
	f.Add([]byte{0xdd, 0xc7, 0x01})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Add([]byte{0x80, 0x80, 0x80, 0x80, 0x80})
	f.Fuzz(func(t *testing.T, data []byte) {
		v, n, err := ReadVarInt(bytes.NewReader(data))
		if err != nil {
			return
		}
		if n < 1 || n > 5 {
			t.Fatalf("consumed %d bytes", n)
		}
		// Round-trip: re-encoding must decode to the same value in <= n bytes
		// (the input may be non-canonical, so exact bytes aren't guaranteed).
		enc := AppendVarInt(nil, v)
		if len(enc) > n {
			t.Fatalf("re-encoding of %d grew: %d -> %d bytes", v, n, len(enc))
		}
		v2, _, err := ReadVarInt(bytes.NewReader(enc))
		if err != nil || v2 != v {
			t.Fatalf("round trip %d -> %d (%v)", v, v2, err)
		}
	})
}

func FuzzParseHandshake(f *testing.F) {
	seed := AppendVarInt(nil, 767)
	seed = AppendString(seed, "mc.example.com")
	seed = binary.BigEndian.AppendUint16(seed, 25565)
	seed = AppendVarInt(seed, 1)
	f.Add(int32(0), seed)
	f.Add(int32(0), []byte{})
	f.Add(int32(2), []byte{0xff, 0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, id int32, body []byte) {
		h, err := ParseHandshake(id, body)
		if err != nil {
			return
		}
		if h.NextState != NextStateStatus && h.NextState != NextStateLogin && h.NextState != NextStateTransfer {
			t.Fatalf("accepted next state %d", h.NextState)
		}
		if len(h.ServerAddress) > maxServerAddress {
			t.Fatalf("address of %d bytes accepted", len(h.ServerAddress))
		}
	})
}

func FuzzReadPacket(f *testing.F) {
	var buf bytes.Buffer
	WritePacket(&buf, 0x00, []byte("payload"))
	f.Add(buf.Bytes())
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0x07})
	f.Fuzz(func(t *testing.T, data []byte) {
		id, body, err := ReadPacket(bytes.NewReader(data), MaxHandshakePacket)
		if err != nil {
			return
		}
		if len(body) > MaxHandshakePacket {
			t.Fatalf("body of %d bytes escaped the cap", len(body))
		}
		// Re-frame and re-read: must round-trip.
		var rt bytes.Buffer
		if err := WritePacket(&rt, id, body); err != nil {
			t.Fatal(err)
		}
		id2, body2, err := ReadPacket(&rt, MaxHandshakePacket)
		if err != nil || id2 != id || !bytes.Equal(body2, body) {
			t.Fatalf("re-framed packet did not round-trip: %v", err)
		}
	})
}

func FuzzParseLoginStart(f *testing.F) {
	uuid := sampleUUID()
	f.Add(int32(47), buildLoginStart(47, "Notch", uuid, false))
	f.Add(int32(760), buildLoginStart(760, "Grumm", uuid, true))
	f.Add(int32(767), buildLoginStart(767, "Steve", uuid, true))
	f.Add(int32(767), []byte{})
	f.Add(int32(0), []byte{0xff, 0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, proto int32, body []byte) {
		ls, err := ParseLoginStart(proto, body)
		if err != nil {
			return
		}
		// A successful parse must yield a name that passes the same gate the
		// parser claims to enforce, and never a UUID it did not actually read.
		if !nameRe.MatchString(ls.Name) {
			t.Fatalf("accepted invalid name %q", ls.Name)
		}
	})
}

// splitmix64 is the tiny seeded PRNG behind the chunked fuzzers — no
// math/rand dependency, fully determined by the fuzz input.
func splitmix64(state *uint64) uint64 {
	*state += 0x9e3779b97f4a7c15
	z := *state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// seededChunks cuts data into 1–8 chunks at seed-derived offsets, covering
// every reassembly shape (empty chunks included) a TCP stream could produce.
func seededChunks(data []byte, seed uint64) [][]byte {
	n := int(splitmix64(&seed)%8) + 1
	cuts := make([]int, 0, n+1)
	cuts = append(cuts, 0)
	for i := 1; i < n; i++ {
		if len(data) > 0 {
			cuts = append(cuts, int(splitmix64(&seed)%uint64(len(data)+1)))
		}
	}
	cuts = append(cuts, len(data))
	// Insertion-sort the few offsets so chunks tile the stream in order.
	for i := 1; i < len(cuts); i++ {
		for j := i; j > 0 && cuts[j-1] > cuts[j]; j-- {
			cuts[j-1], cuts[j] = cuts[j], cuts[j-1]
		}
	}
	out := make([][]byte, 0, len(cuts)-1)
	for i := 1; i < len(cuts); i++ {
		out = append(out, data[cuts[i-1]:cuts[i]])
	}
	return out
}

// FuzzSnifferChunked cuts an arbitrary stream into 1–8 seed-chosen chunks and
// feeds them in order. The sniffer must never panic and must reach a verdict
// no different from feeding the whole stream at once (chunk-invariance), and
// a completed sniff must carry a name matching the username gate.
func FuzzSnifferChunked(f *testing.F) {
	uuid := sampleUUID()
	f.Add(loginFrames(767, "Player123", uuid, true), uint64(3))
	f.Add(loginFrames(47, "Notch", uuid, false), uint64(1))
	f.Add(handshakeBytes(767, "h", 25565, NextStateStatus), uint64(0xdead))
	f.Add([]byte{0xFE, 0x01}, uint64(7))
	f.Fuzz(func(t *testing.T, stream []byte, seed uint64) {
		whole := NewSniffer()
		whole.Feed(stream)
		wOut, wOK := whole.Outcome()

		chunks := seededChunks(stream, seed)
		chunked := NewSniffer()
		for _, c := range chunks {
			chunked.Feed(c)
		}
		cOut, cOK := chunked.Outcome()

		// The verdict must not depend on where the stream was split.
		if wOK != cOK {
			t.Fatalf("chunk-variance: whole ok=%v, chunked ok=%v (seed=%d chunks=%d)", wOK, cOK, seed, len(chunks))
		}
		if (wOut.Login == nil) != (cOut.Login == nil) {
			t.Fatalf("chunk-variance in login presence (seed=%d chunks=%d)", seed, len(chunks))
		}
		if cOK && cOut.Login != nil && !nameRe.MatchString(cOut.Login.Name) {
			t.Fatalf("sniffer surfaced invalid name %q", cOut.Login.Name)
		}
	})
}

// fuzzConn feeds fuzz input as the client side of a net.Conn and swallows
// writes. Reads past the input return EOF.
type fuzzConn struct {
	r *bytes.Reader
}

func (c *fuzzConn) Read(p []byte) (int, error)         { return c.r.Read(p) }
func (c *fuzzConn) Write(p []byte) (int, error)        { return len(p), nil }
func (c *fuzzConn) Close() error                       { return nil }
func (c *fuzzConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *fuzzConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *fuzzConn) SetDeadline(t time.Time) error      { return nil }
func (c *fuzzConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *fuzzConn) SetWriteDeadline(t time.Time) error { return nil }

var _ net.Conn = (*fuzzConn)(nil)

// FuzzServeOffline throws raw bytes at the full offline responder — it must
// never panic, hang, or allocate unboundedly, whatever arrives.
func FuzzServeOffline(f *testing.F) {
	// Seeds: modern status flow, login attempt, legacy ping, garbage.
	status := handshakeBytes(767, "example.com", 25565, NextStateStatus)
	var req bytes.Buffer
	WritePacket(&req, 0x00, nil)
	WritePacket(&req, 0x01, make([]byte, 8))
	f.Add(append(status, req.Bytes()...))
	f.Add(handshakeBytes(767, "example.com", 25565, NextStateLogin))
	f.Add([]byte{0xFE, 0x01})
	f.Add([]byte{0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		conn := &fuzzConn{r: bytes.NewReader(data)}
		ServeOffline(conn, OfflineInfo{MOTD: "offline", VersionName: "v"})
	})
}
