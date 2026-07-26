// Package transport abstracts the multiplexed gateway↔agent link. Everything
// outside this package programs against Session/Stream so the default
// yamux-over-TCP implementation can be swapped (per-connection mode, QUIC)
// without touching agent or gateway code. Nothing outside this package may
// import yamux.
package transport

import (
	"context"
	"errors"
	"net"
)

// Stream is one bidirectional byte stream inside a Session. CloseWrite
// half-closes: it signals EOF to the peer's reader while our reads continue —
// required for correct splice shutdown.
type Stream interface {
	net.Conn
	CloseWrite() error
}

// ErrDatagramTooLarge reports that a datagram will not fit what the connection's
// current path can carry — there is no fragmentation, so the caller must send it
// another way. It is this package's own error precisely so callers need not (and
// must not) import quic-go to recognise it: the underlying library's type is
// translated here. The bound it reports is a property of the moment, not a
// constant, which is why a caller decides by the error rather than by a number.
var ErrDatagramTooLarge = errors.New("transport: datagram exceeds what the connection can carry")

// DatagramSession is the optional seam a Session implements when it can carry
// unreliable datagrams alongside its streams (QUIC's RFC 9221 extension). Only
// the QUIC wrapper does; yamux-over-TCP and per-conn are TCP, where unreliable
// delivery does not exist. A caller type-asserts for it and falls back to the
// stream when the assertion fails, so nothing outside this package has to know
// which transport it holds.
type DatagramSession interface {
	Session
	// SupportsDatagrams reports whether datagrams are usable on this connection
	// right now: both ends must have enabled the extension, and the peer's half
	// is only known after the handshake.
	SupportsDatagrams() bool
	// SendDatagram sends one datagram unreliably. It returns ErrDatagramTooLarge
	// when the payload will not fit the current path.
	SendDatagram(b []byte) error
	// ReceiveDatagram returns the next datagram, blocking until one arrives, the
	// connection dies, or ctx is done.
	ReceiveDatagram(ctx context.Context) ([]byte, error)
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
