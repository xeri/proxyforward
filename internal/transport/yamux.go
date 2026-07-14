package transport

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

// Deliberate yamux tuning — never inherit defaults blindly:
//   - Keepalive OFF: the application-level ping (agent every 5 s) is the
//     single liveness owner; two mechanisms produce confusing failures.
//   - MaxStreamWindowSize 1 MiB: a Minecraft chunk burst must fit in flight
//     on one stream without stalling (default 256 KiB stalls on fat pipes).
//   - ConnectionWriteTimeout 30 s: yamux kills the whole session when a
//     write to the underlying conn stalls this long. The default 10 s is
//     tighter than our liveness budget (15 s deadline + margin); 30 s means
//     the app-level heartbeat, not yamux, decides when the link is dead.
func muxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = false
	cfg.MaxStreamWindowSize = 1 << 20
	cfg.ConnectionWriteTimeout = 30 * time.Second
	cfg.LogOutput = io.Discard // yamux internal errors surface via API errors
	return cfg
}

type muxSession struct {
	s *yamux.Session
}

// NewMuxClient wraps an established (TLS, authenticated) conn as the
// dialing side of a multiplexed session.
func NewMuxClient(conn net.Conn) (Session, error) {
	s, err := yamux.Client(conn, muxConfig())
	if err != nil {
		return nil, fmt.Errorf("transport: mux client: %w", err)
	}
	return &muxSession{s: s}, nil
}

// NewMuxServer wraps an established conn as the accepting side.
func NewMuxServer(conn net.Conn) (Session, error) {
	s, err := yamux.Server(conn, muxConfig())
	if err != nil {
		return nil, fmt.Errorf("transport: mux server: %w", err)
	}
	return &muxSession{s: s}, nil
}

func (m *muxSession) OpenStream() (Stream, error) {
	st, err := m.s.OpenStream()
	if err != nil {
		return nil, err
	}
	return &muxStream{st}, nil
}

func (m *muxSession) AcceptStream() (Stream, error) {
	st, err := m.s.AcceptStream()
	if err != nil {
		return nil, err
	}
	return &muxStream{st}, nil
}

func (m *muxSession) Close() error               { return m.s.Close() }
func (m *muxSession) CloseChan() <-chan struct{} { return m.s.CloseChan() }
func (m *muxSession) RemoteAddr() net.Addr       { return m.s.RemoteAddr() }

type muxStream struct {
	*yamux.Stream
}

// CloseWrite maps to yamux Close, which sends FIN but keeps delivering
// inbound data — yamux's Close already has half-close semantics.
func (s *muxStream) CloseWrite() error { return s.Stream.Close() }
