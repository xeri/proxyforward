package proxyproto

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

func TestHeaderV2_TCP4(t *testing.T) {
	src := &net.TCPAddr{IP: net.ParseIP("203.0.113.44"), Port: 51422}
	dst := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 25565}
	h := HeaderV2(src, dst)

	if !bytes.Equal(h[:12], signature) {
		t.Fatalf("bad signature: %x", h[:12])
	}
	if h[12] != verCmdProxy {
		t.Errorf("ver/cmd = %#x, want %#x", h[12], verCmdProxy)
	}
	if h[13] != famTCP4 {
		t.Errorf("family = %#x, want TCP4 %#x", h[13], famTCP4)
	}
	if l := binary.BigEndian.Uint16(h[14:16]); l != 12 {
		t.Errorf("addr block length = %d, want 12", l)
	}
	if len(h) != 16+12 {
		t.Fatalf("total length = %d, want 28", len(h))
	}
	if got := net.IP(h[16:20]).String(); got != "203.0.113.44" {
		t.Errorf("src ip = %s", got)
	}
	if got := net.IP(h[20:24]).String(); got != "127.0.0.1" {
		t.Errorf("dst ip = %s", got)
	}
	if p := binary.BigEndian.Uint16(h[24:26]); p != 51422 {
		t.Errorf("src port = %d", p)
	}
	if p := binary.BigEndian.Uint16(h[26:28]); p != 25565 {
		t.Errorf("dst port = %d", p)
	}
}

func TestHeaderV2_TCP6(t *testing.T) {
	src := &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 40000}
	dst := &net.TCPAddr{IP: net.ParseIP("2001:db8::2"), Port: 25565}
	h := HeaderV2(src, dst)
	if h[13] != famTCP6 {
		t.Errorf("family = %#x, want TCP6 %#x", h[13], famTCP6)
	}
	if l := binary.BigEndian.Uint16(h[14:16]); l != 36 {
		t.Errorf("addr block length = %d, want 36", l)
	}
	if len(h) != 16+36 {
		t.Fatalf("total length = %d, want 52", len(h))
	}
}

// A mixed-family pair must widen both to IPv6 so the families match (spec
// requirement) — a v4 client to a v4-mapped loopback, etc.
func TestHeaderV2_MixedFamilyWidensToV6(t *testing.T) {
	src := &net.TCPAddr{IP: net.ParseIP("2001:db8::9"), Port: 1}
	dst := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 25565}
	h := HeaderV2(src, dst)
	if h[13] != famTCP6 {
		t.Fatalf("family = %#x, want TCP6", h[13])
	}
	// dst IPv4 becomes v4-mapped ::ffff:127.0.0.1.
	if got := net.IP(h[16+16 : 16+32]).String(); got != "127.0.0.1" {
		t.Errorf("dst widened ip = %s, want 127.0.0.1", got)
	}
}
