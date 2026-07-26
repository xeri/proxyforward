package link

import (
	"crypto/tls"
	"errors"
	"net"
	"time"
)

// PoolKeepAlivePeriod is how long a parked data connection may sit silent
// before the kernel starts probing the peer. A pooled connection carries no
// application traffic at all until a player arrives, which can be hours — long
// enough for a NAT or a stateful firewall between agent and gateway to forget
// the mapping and start dropping the flow silently. Keepalive is what keeps the
// mapping warm; 30 s is comfortably inside the shortest idle timeout such a box
// plausibly uses (typically 5 minutes), and three probes' worth of traffic is
// nothing next to the TLS handshake it saves.
//
// It is also the only liveness signal a parked connection has. Nobody reads it
// on the gateway side, so a peer that vanished is discovered when the kernel's
// probes go unanswered and the socket errors — which surfaces on the header
// write at take time, where the caller can still fall back.
const PoolKeepAlivePeriod = 30 * time.Second

// poolKeepAliveProbes is how many unanswered probes declare the peer gone. The
// default on most systems is 9, which would take minutes past the first missed
// probe; a parked connection has nothing to lose by being declared dead early,
// because the fallback (dial one) is what would have happened anyway.
const poolKeepAliveProbes = 3

// errNotTCP is returned for a connection with no TCP socket under it. Keepalive
// is a TCP mechanism: QUIC paces itself and a test's in-memory pipe has no
// kernel to ask.
var errNotTCP = errors.New("link: connection has no TCP socket to keep alive")

// KeepParkedAlive arms TCP keepalive on a connection that is about to sit idle
// with no application traffic — a pooled data connection on either side of the
// link. It unwraps the TLS layer to reach the socket, and reports (rather than
// swallows) a failure so the caller can log it: a parked connection without
// keepalive still works, it just discovers death later.
func KeepParkedAlive(conn net.Conn) error {
	tcp, ok := tcpSocket(conn)
	if !ok {
		return errNotTCP
	}
	return tcp.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     PoolKeepAlivePeriod,
		Interval: PoolKeepAlivePeriod,
		Count:    poolKeepAliveProbes,
	})
}

// tcpSocket digs the TCP connection out of the two shapes the link ever holds:
// the raw socket (the agent has it before wrapping) and a TLS connection over
// it (what the gateway accepts).
func tcpSocket(conn net.Conn) (*net.TCPConn, bool) {
	switch c := conn.(type) {
	case *net.TCPConn:
		return c, true
	case *tls.Conn:
		tcp, ok := c.NetConn().(*net.TCPConn)
		return tcp, ok
	}
	return nil, false
}
