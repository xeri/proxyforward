// Package transport abstracts the multiplexed gateway↔agent link. Everything
// outside this package programs against Session/Stream so the default
// yamux-over-TCP implementation can be swapped (per-connection mode, QUIC)
// without touching agent or gateway code. Nothing outside this package may
// import yamux.
package transport

import (
	"net"
)

// Stream is one bidirectional byte stream inside a Session. CloseWrite
// half-closes: it signals EOF to the peer's reader while our reads continue —
// required for correct splice shutdown.
type Stream interface {
	net.Conn
	CloseWrite() error
}

// Session is an established, authenticated link carrying many streams.
type Session interface {
	// OpenStream starts a new stream (gateway → agent for data connections,
	// agent → gateway for the control stream).
	OpenStream() (Stream, error)
	// AcceptStream blocks for the peer's next stream; it errors once the
	// session is dead.
	AcceptStream() (Stream, error)
	Close() error
	// CloseChan is closed when the session dies (either side, any reason).
	CloseChan() <-chan struct{}
	RemoteAddr() net.Addr
}
