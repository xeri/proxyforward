package stats

import (
	"net"
	"sync/atomic"
)

// LinkCounters are raw control-link byte totals: In = bytes read from the
// conn, Out = bytes written. Includes yamux framing and control chatter —
// this is what actually crossed the link, not just proxied payload.
type LinkCounters struct {
	In  atomic.Int64
	Out atomic.Int64
}

// Bytes reads both counters.
func (l *LinkCounters) Bytes() (in, out int64) {
	return l.In.Load(), l.Out.Load()
}

// NewCountingConn wraps c so every read/write is added to totals (process
// lifetime) and, when non-nil, session (current link session). CloseWrite is
// passed through when the wrapped conn supports it.
func NewCountingConn(c net.Conn, totals, session *LinkCounters) net.Conn {
	cc := &countingConn{Conn: c, totals: totals, session: session}
	if _, ok := c.(closeWriter); ok {
		return &countingConnCW{cc}
	}
	return cc
}

type countingConn struct {
	net.Conn
	totals, session *LinkCounters
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.totals.In.Add(int64(n))
		if c.session != nil {
			c.session.In.Add(int64(n))
		}
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.totals.Out.Add(int64(n))
		if c.session != nil {
			c.session.Out.Add(int64(n))
		}
	}
	return n, err
}

type closeWriter interface{ CloseWrite() error }

type countingConnCW struct{ *countingConn }

func (c *countingConnCW) CloseWrite() error {
	return c.countingConn.Conn.(closeWriter).CloseWrite()
}
