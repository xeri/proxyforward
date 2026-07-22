package relay

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// MaxDatagram is the largest payload a UDP datagram can carry: 65535 minus the
// 8-byte UDP header and the 20-byte IPv4 header. It bounds every buffer here,
// and a length read off the wire is checked against it *before* the buffer is
// sized — the same rule the control framing follows (control.MaxFrame), for the
// same reason: a peer must never be able to name a number and get memory for it.
//
// The 2-byte prefix can express 65535, which is larger than this, so the check
// is load-bearing rather than decorative.
const MaxDatagram = 65507

// datagramHdr is the length prefix: 2 bytes, big-endian, like the control
// envelope's 4-byte one. Two suffice because MaxDatagram fits in a uint16.
const datagramHdr = 2

// DatagramIdleTimeout is how long a datagram splice may move nothing in either
// direction before it is reaped, and it is much shorter than DefaultIdleTimeout
// for a structural reason: a TCP splice earns ten minutes because a FIN will
// eventually prove the peer is gone, while a UDP flow has no close at all. Left
// on the TCP clock, every player who quit would hold a conn-gate slot, a tunnel
// stream, and a local socket for ten minutes after leaving.
//
// Two minutes sits far above any keepalive this tunnel carries — RakNet pings
// every few seconds and Simple Voice Chat keeps its channel warm — and far below
// the point where a player pausing at a menu would be cut.
//
// Both ends use this one constant so neither can reap a session the other still
// believes in.
const DatagramIdleTimeout = 2 * time.Minute

// DatagramStream adapts a byte-oriented tunnel stream into a datagram-oriented
// Conn: every Read yields exactly one datagram, every Write sends exactly one,
// framed as a 2-byte big-endian length followed by the payload.
//
// Why this exists: Splice is a byte pump, and a byte pump across a reliable
// stream destroys message boundaries — two datagrams sent back to back would
// reach the far side's socket as one, which no UDP protocol survives. Framing
// restores the boundary, and because Splice calls Write once per Read, a framed
// leg spliced against a real UDP socket reproduces datagram semantics end to
// end. Splice itself needs no change, so the TCP hot path is untouched by UDP
// support existing.
//
// Not preserved: a zero-length datagram. Splice acts only on Read returning
// n > 0, so an empty payload cannot be expressed through it; such datagrams are
// dropped here rather than silently ending the copy. Minecraft, RakNet and the
// voice mods never send one (every one of their packets carries a header byte),
// and supporting it would mean changing the shared hot path for a case this
// tunnel does not carry.
//
// Concurrency: Splice runs one goroutine per direction, so Read and Write are
// each single-threaded and touch disjoint fields. Nothing here locks, and
// neither path allocates once its buffer has grown to the traffic's size.
type DatagramStream struct {
	// Conn is the underlying tunnel leg — a mux stream under mux/QUIC
	// transport, or a per-conn data conn. Embedded so CloseWrite, the
	// deadlines, and the addresses pass straight through; CloseWrite keeps its
	// real meaning here (no more datagrams this way).
	Conn

	// Read state. A read deadline can fire between the header and the payload —
	// Splice arms one on every leg — and a naive reader would then parse the
	// next call's payload bytes as a length and desynchronise the stream for
	// good. So a partially received datagram is retained across calls and
	// resumed, and Read reports n > 0 only for a whole one.
	rhdr  [datagramHdr]byte
	rhdrN int    // header bytes received so far; < datagramHdr means mid-header
	rbuf  []byte // payload accumulator, grown to fit and reused
	rgot  int    // payload bytes received so far for the datagram in flight

	// wbuf lets Write emit header+payload in a single underlying Write, so a
	// datagram is one tunnel frame and can never interleave with another
	// writer's bytes. Grown to fit and reused; game traffic keeps it near one
	// MTU rather than at MaxDatagram.
	wbuf []byte
}

// Read returns the next whole datagram. Partial reads (a deadline firing
// mid-datagram) are resumed on the following call rather than surfacing as a
// short, corrupt datagram.
func (d *DatagramStream) Read(p []byte) (int, error) {
	for {
		for d.rhdrN < datagramHdr {
			n, err := d.Conn.Read(d.rhdr[d.rhdrN:])
			d.rhdrN += n
			if err != nil {
				return 0, err
			}
		}
		size := int(binary.BigEndian.Uint16(d.rhdr[:]))
		if size > MaxDatagram {
			// A peer that cannot be talking to us in good faith: refuse rather
			// than size a buffer to it.
			return 0, fmt.Errorf("relay: datagram frame of %d bytes exceeds the %d-byte maximum", size, MaxDatagram)
		}
		if cap(d.rbuf) < size {
			d.rbuf = make([]byte, size)
		}
		for d.rgot < size {
			n, err := d.Conn.Read(d.rbuf[d.rgot:size])
			d.rgot += n
			if err != nil {
				return 0, err
			}
		}
		d.rhdrN, d.rgot = 0, 0
		if size == 0 {
			// Nothing Splice can forward; take the next one instead of
			// returning a (0, nil) it would spin on.
			continue
		}
		if len(p) < size {
			// Unreachable through Splice, whose buffer is BufSize (128 KiB) —
			// far above MaxDatagram. Reported rather than truncated, because a
			// silently shortened datagram is a corruption no caller can detect.
			return 0, io.ErrShortBuffer
		}
		copy(p, d.rbuf[:size])
		return size, nil
	}
}

// Write frames p as one datagram. It reports len(p) written on success so
// Splice's short-write check sees a complete copy.
func (d *DatagramStream) Write(p []byte) (int, error) {
	if len(p) > MaxDatagram {
		return 0, fmt.Errorf("relay: datagram of %d bytes exceeds the %d-byte maximum", len(p), MaxDatagram)
	}
	need := datagramHdr + len(p)
	if cap(d.wbuf) < need {
		d.wbuf = make([]byte, need)
	}
	b := d.wbuf[:need]
	binary.BigEndian.PutUint16(b, uint16(len(p)))
	copy(b[datagramHdr:], p)
	if _, err := d.Conn.Write(b); err != nil {
		return 0, err
	}
	return len(p), nil
}

// DatagramConn adapts a connected UDP socket to Conn. A *net.UDPConn from
// DialUDP already reads and writes one datagram per call, so the only thing
// missing is CloseWrite: there is no FIN on a connectionless socket, so it is a
// no-op that reports success — Splice calls it on EOF and must not read that as
// a failure.
type DatagramConn struct{ net.Conn }

// CloseWrite is the no-op half-close described on DatagramConn.
func (DatagramConn) CloseWrite() error { return nil }
