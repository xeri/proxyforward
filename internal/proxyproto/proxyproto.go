// Package proxyproto builds HAProxy PROXY protocol v2 headers. The agent
// prepends one when dialing the local Minecraft server (per-tunnel opt-in) so
// Paper/Velocity see the real player IP instead of the tunnel's loopback
// address. Header-only: we never parse PP2 (the local server does that).
//
// Spec: https://www.haproxy.org/download/2.8/doc/proxy-protocol.txt (v2).
package proxyproto

import (
	"encoding/binary"
	"net"
)

// v2 signature: the fixed 12-byte block that opens every PROXY v2 header.
var signature = []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}

const (
	verCmdProxy = 0x21 // version 2 (0x20) | PROXY command (0x01)
	verCmdLocal = 0x20 // version 2 | LOCAL command (no address block)

	famTCP4 = 0x11 // AF_INET  | STREAM
	famTCP6 = 0x21 // AF_INET6 | STREAM
)

// HeaderV2 encodes a PROXY v2 header describing a TCP connection from src
// (the real client) to dst (the address the client reached). When both
// addresses are IPv4 it emits a TCP4 block; if either is IPv6 both are
// widened to 16-byte IPv6 form (IPv4 addresses become v4-mapped) so the two
// address families always match, as the spec requires.
func HeaderV2(src, dst *net.TCPAddr) []byte {
	buf := make([]byte, 0, 16+36)
	buf = append(buf, signature...)
	buf = append(buf, verCmdProxy)

	sip, dip := src.IP, dst.IP
	if sip.To4() != nil && dip.To4() != nil {
		buf = append(buf, famTCP4)
		buf = binary.BigEndian.AppendUint16(buf, 12) // 2×4-byte IP + 2×2-byte port
		buf = append(buf, sip.To4()...)
		buf = append(buf, dip.To4()...)
		buf = binary.BigEndian.AppendUint16(buf, uint16(src.Port))
		buf = binary.BigEndian.AppendUint16(buf, uint16(dst.Port))
		return buf
	}

	buf = append(buf, famTCP6)
	buf = binary.BigEndian.AppendUint16(buf, 36) // 2×16-byte IP + 2×2-byte port
	buf = append(buf, sip.To16()...)
	buf = append(buf, dip.To16()...)
	buf = binary.BigEndian.AppendUint16(buf, uint16(src.Port))
	buf = binary.BigEndian.AppendUint16(buf, uint16(dst.Port))
	return buf
}

// LocalV2 is the zero-address LOCAL header, used for health-check style
// connections that should bypass source spoofing. Included for completeness;
// the tunnel path always uses HeaderV2.
func LocalV2() []byte {
	buf := make([]byte, 0, 16)
	buf = append(buf, signature...)
	buf = append(buf, verCmdLocal, famTCP4)
	buf = binary.BigEndian.AppendUint16(buf, 0)
	return buf
}
