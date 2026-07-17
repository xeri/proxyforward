package gateway

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
	"proxyforward/internal/transport"
)

// stalledStream models the peer this whole file is about: one that opens the
// control stream, says its piece, and then never grants another byte of
// flow-control credit. A real QUIC stream Write to it blocks until the stream's
// write deadline fires; this one collapses that wait to an instant error, because
// what is under test is whether a deadline is armed at all — how long it is, is a
// tuning number (preAuthTimeout), not a property. With no deadline it does what
// the real one does: never returns.
type stalledStream struct {
	mu     sync.Mutex
	rd     *bytes.Reader
	wdl    time.Time
	closed chan struct{}
	once   sync.Once
}

func newStalledStream(frame []byte) *stalledStream {
	return &stalledStream{rd: bytes.NewReader(frame), closed: make(chan struct{})}
}

func (s *stalledStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	n, err := s.rd.Read(p)
	s.mu.Unlock()
	if err == io.EOF {
		<-s.closed // the frame is delivered; the peer simply stops talking
		return 0, net.ErrClosed
	}
	return n, err
}

func (s *stalledStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	dl := s.wdl
	s.mu.Unlock()
	if dl.IsZero() {
		<-s.closed // no write deadline: park here exactly as long as the real peer would
		return 0, net.ErrClosed
	}
	return 0, os.ErrDeadlineExceeded
}

func (s *stalledStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}
func (s *stalledStream) CloseWrite() error   { return nil }
func (s *stalledStream) LocalAddr() net.Addr { return &net.UDPAddr{} }
func (s *stalledStream) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(203, 0, 113, 5), Port: 4242}
}
func (s *stalledStream) SetReadDeadline(time.Time) error { return nil }

func (s *stalledStream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.wdl = t
	s.mu.Unlock()
	return nil
}

func (s *stalledStream) SetDeadline(t time.Time) error {
	s.SetReadDeadline(t)
	return s.SetWriteDeadline(t)
}

// stalledSession is a transport.Session serving exactly one stalledStream.
type stalledSession struct {
	st     *stalledStream
	handed bool
	mu     sync.Mutex
	closed chan struct{}
	once   sync.Once
}

func (s *stalledSession) OpenStream() (transport.Stream, error) { return nil, net.ErrClosed }

func (s *stalledSession) AcceptStream() (transport.Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handed {
		<-s.closed
		return nil, net.ErrClosed
	}
	s.handed = true
	return s.st, nil
}

func (s *stalledSession) Close() error {
	s.once.Do(func() { close(s.closed) })
	s.st.Close()
	return nil
}
func (s *stalledSession) CloseChan() <-chan struct{} { return s.closed }
func (s *stalledSession) RemoteAddr() net.Addr       { return s.st.RemoteAddr() }

// helloFrame encodes one control frame the way a peer would put it on the wire.
func helloFrame(t *testing.T, h control.Hello) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := control.WriteMsg(&buf, control.TypeHello, h); err != nil {
		t.Fatalf("encode hello: %v", err)
	}
	return buf.Bytes()
}

// TestQUICPreAuthReplyCannotBlockForever is the QUIC half of the pre-auth gate's
// promise, which the TCP half kept and this one did not.
//
// The invariant is that the unauthenticated prologue finishes within
// preAuthTimeout or dies. TCP enforced it with conn.SetDeadline, which bounds
// *both* directions; QUIC armed only SetReadDeadline. Every rejection branch
// answers the peer with a hello error, and on QUIC a Write parks until the peer
// grants flow-control credit — credit a hostile peer simply never grants. So the
// gateway blocked in WriteMsg, never reached sess.Close(), and never returned; the
// deferred release of its pre-auth slot never ran either. Repeat that
// MaxConnsGlobal+256 times, cheaply, and the gate that exists to bound
// unauthenticated connections is itself full forever — every later agent refused,
// with the gate's own accounting doing the refusing.
func TestQUICPreAuthReplyCannotBlockForever(t *testing.T) {
	g := &Gateway{cfg: config.Default(), logger: discardLogger()}
	g.authLim = newAuthLimiter(g.cfg.Gateway.AuthAttemptsPerMin)

	// A hello the gateway must reject, so it takes a branch that writes a reply:
	// a protocol version it does not speak. Rejection is the point — it is the
	// unauthenticated peer that gets an answer, and so the one that can stall it.
	frame := helloFrame(t, control.Hello{
		ProtocolVersion: control.ProtocolVersion + 1,
		Kind:            control.KindControl,
	})
	sess := &stalledSession{st: newStalledStream(frame), closed: make(chan struct{})}
	t.Cleanup(func() { sess.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		g.handleQUICSession(context.Background(), sess, func() {})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the pre-auth prologue never returned: a peer that refuses to read parks the gateway in its own error reply, and the pre-auth slot it holds is never released")
	}
}
