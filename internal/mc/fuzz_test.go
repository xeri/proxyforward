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
