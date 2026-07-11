package mc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"
	"unicode/utf16"
)

func TestVarIntRoundTrip(t *testing.T) {
	cases := []struct {
		v    int32
		wire []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{2, []byte{0x02}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{255, []byte{0xff, 0x01}},
		{25565, []byte{0xdd, 0xc7, 0x01}},
		{2097151, []byte{0xff, 0xff, 0x7f}},
		{2147483647, []byte{0xff, 0xff, 0xff, 0xff, 0x07}},
		{-1, []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
		{-2147483648, []byte{0x80, 0x80, 0x80, 0x80, 0x08}},
	}
	for _, c := range cases {
		got := AppendVarInt(nil, c.v)
		if !bytes.Equal(got, c.wire) {
			t.Errorf("AppendVarInt(%d) = %x, want %x", c.v, got, c.wire)
		}
		v, n, err := ReadVarInt(bytes.NewReader(c.wire))
		if err != nil || v != c.v || n != len(c.wire) {
			t.Errorf("ReadVarInt(%x) = %d,%d,%v; want %d,%d,nil", c.wire, v, n, err, c.v, len(c.wire))
		}
	}
}

func TestVarIntErrors(t *testing.T) {
	if _, _, err := ReadVarInt(bytes.NewReader([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01})); !errors.Is(err, ErrVarIntTooBig) {
		t.Errorf("6-byte varint: got %v, want ErrVarIntTooBig", err)
	}
	if _, _, err := ReadVarInt(bytes.NewReader([]byte{0x80})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated varint: got %v, want ErrUnexpectedEOF", err)
	}
	if _, _, err := ReadVarInt(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Errorf("empty varint: got %v, want EOF", err)
	}
}

func TestPacketRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	body := []byte("hello world")
	if err := WritePacket(&buf, 0x42, body); err != nil {
		t.Fatal(err)
	}
	id, got, err := ReadPacket(&buf, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if id != 0x42 || !bytes.Equal(got, body) {
		t.Fatalf("got id=%#x body=%q", id, got)
	}
}

func TestPacketLengthCap(t *testing.T) {
	// Declared length 1 MiB with no payload: must reject before reading.
	frame := AppendVarInt(nil, 1<<20)
	if _, _, err := ReadPacket(bytes.NewReader(frame), MaxHandshakePacket); err == nil {
		t.Fatal("oversized packet length accepted")
	}
	// Zero and negative lengths are rejected too.
	for _, l := range []int32{0, -1} {
		frame := AppendVarInt(nil, l)
		if _, _, err := ReadPacket(bytes.NewReader(frame), MaxHandshakePacket); err == nil {
			t.Fatalf("length %d accepted", l)
		}
	}
}

// handshakeBytes builds a full framed handshake packet.
func handshakeBytes(proto int32, addr string, port uint16, next int32) []byte {
	body := AppendVarInt(nil, proto)
	body = AppendString(body, addr)
	body = binary.BigEndian.AppendUint16(body, port)
	body = AppendVarInt(body, next)
	var buf bytes.Buffer
	WritePacket(&buf, 0x00, body)
	return buf.Bytes()
}

func TestParseHandshake(t *testing.T) {
	// Vanilla 1.21 status handshake for mc.example.com:25565.
	wire := handshakeBytes(767, "mc.example.com", 25565, NextStateStatus)
	id, body, err := ReadPacket(bytes.NewReader(wire), MaxHandshakePacket)
	if err != nil {
		t.Fatal(err)
	}
	h, err := ParseHandshake(id, body)
	if err != nil {
		t.Fatal(err)
	}
	if h.ProtocolVersion != 767 || h.ServerAddress != "mc.example.com" || h.ServerPort != 25565 || h.NextState != NextStateStatus {
		t.Fatalf("bad handshake: %+v", h)
	}

	// BungeeCord-style forwarded address (\0-separated extras) must parse.
	bungee := "mc.example.com\x00203.0.113.7\x00069a79f4-44e9-4726-a5be-fca90e38aaf5"
	wire = handshakeBytes(767, bungee, 25565, NextStateLogin)
	id, body, _ = ReadPacket(bytes.NewReader(wire), MaxHandshakePacket)
	h, err = ParseHandshake(id, body)
	if err != nil {
		t.Fatal(err)
	}
	if h.ServerAddress != bungee {
		t.Fatalf("forwarded address mangled: %q", h.ServerAddress)
	}
}

func TestParseHandshakeRejects(t *testing.T) {
	base := func() []byte {
		body := AppendVarInt(nil, 767)
		body = AppendString(body, "example.com")
		body = binary.BigEndian.AppendUint16(body, 25565)
		return AppendVarInt(body, NextStateStatus)
	}
	cases := map[string]struct {
		id   int32
		body []byte
	}{
		"wrong packet id": {0x01, base()},
		"empty body":      {0x00, nil},
		"truncated":       {0x00, base()[:4]},
		"trailing bytes":  {0x00, append(base(), 0xAA)},
		"bad next state":  {0x00, append(base()[:len(base())-1], 9)},
	}
	for name, c := range cases {
		if _, err := ParseHandshake(c.id, c.body); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// runOffline drives ServeOffline over a pipe and returns the client end.
func runOffline(t *testing.T, info OfflineInfo) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		server.SetDeadline(time.Now().Add(5 * time.Second))
		ServeOffline(server, info)
	}()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { client.Close() })
	return client
}

func TestServeOfflineStatusFlow(t *testing.T) {
	c := runOffline(t, OfflineInfo{MOTD: "Server offline — back soon", VersionName: "maintenance"})

	if _, err := c.Write(handshakeBytes(767, "example.com", 25565, NextStateStatus)); err != nil {
		t.Fatal(err)
	}
	WritePacket(c, 0x00, nil) // status request

	id, body, err := ReadPacket(c, 32*1024)
	if err != nil {
		t.Fatal(err)
	}
	if id != 0x00 {
		t.Fatalf("status response id %#x", id)
	}
	rd := &bodyReader{b: body}
	doc := rd.string(32 * 1024)
	if rd.err != nil {
		t.Fatal(rd.err)
	}
	var resp StatusResponse
	if err := json.Unmarshal([]byte(doc), &resp); err != nil {
		t.Fatalf("status JSON invalid: %v\n%s", err, doc)
	}
	if resp.Description.Text != "Server offline — back soon" {
		t.Errorf("MOTD = %q", resp.Description.Text)
	}
	if resp.Version.Protocol != -1 || resp.Version.Name != "maintenance" {
		t.Errorf("version = %+v", resp.Version)
	}

	// Ping → pong echo.
	payload := binary.BigEndian.AppendUint64(nil, 0xDEADBEEFCAFEF00D)
	WritePacket(c, 0x01, payload)
	id, body, err = ReadPacket(c, 64)
	if err != nil {
		t.Fatal(err)
	}
	if id != 0x01 || !bytes.Equal(body, payload) {
		t.Fatalf("pong = id %#x body %x", id, body)
	}
}

func TestServeOfflineLoginDisconnect(t *testing.T) {
	c := runOffline(t, OfflineInfo{MOTD: "Be right back"})
	if _, err := c.Write(handshakeBytes(767, "example.com", 25565, NextStateLogin)); err != nil {
		t.Fatal(err)
	}
	id, body, err := ReadPacket(c, 32*1024)
	if err != nil {
		t.Fatal(err)
	}
	if id != 0x00 {
		t.Fatalf("disconnect id %#x", id)
	}
	rd := &bodyReader{b: body}
	var reason chat
	if err := json.Unmarshal([]byte(rd.string(32*1024)), &reason); err != nil {
		t.Fatal(err)
	}
	if reason.Text != "Be right back" {
		t.Errorf("reason = %q", reason.Text)
	}
}

func TestServeOfflineLegacyPing(t *testing.T) {
	c := runOffline(t, OfflineInfo{MOTD: "Down for maintenance"})
	if _, err := c.Write([]byte{0xFE, 0x01}); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 3)
	if _, err := io.ReadFull(c, head); err != nil {
		t.Fatal(err)
	}
	if head[0] != 0xFF {
		t.Fatalf("legacy response starts with %#x, want 0xFF", head[0])
	}
	n := binary.BigEndian.Uint16(head[1:3])
	raw := make([]byte, 2*int(n))
	if _, err := io.ReadFull(c, raw); err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, n)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(raw[2*i:])
	}
	fields := bytes.Split([]byte(string(utf16.Decode(units))), []byte{0})
	if len(fields) != 6 {
		t.Fatalf("legacy payload has %d fields: %q", len(fields), fields)
	}
	if string(fields[0]) != "§1" || string(fields[3]) != "Down for maintenance" {
		t.Fatalf("legacy fields: %q", fields)
	}
}
