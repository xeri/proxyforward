package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/quic-go/quic-go"

	"proxyforward/internal/stats"
)

// QUIC transport: a Session backed by one QUIC connection, one Stream per QUIC
// stream. Loss on one player's stream cannot head-of-line-block another's — the
// per-conn benefit over a single connection/handshake/NAT-entry. Only this
// package imports quic-go, exactly as with yamux.
//
// Link-byte accounting differs from yamux by necessity. yamux muxes over one
// net.Conn, so the gateway/agent wrap that conn once (stats.NewCountingConn) and
// every byte is counted. QUIC handshakes over a UDP socket; wrapping that socket
// in a plain net.PacketConn would drop quic-go off its GSO/GRO/ECN fast path
// (quic-go only enables those for an *net.UDPConn), costing throughput. So we
// pass the raw socket to quic-go and count stream payload here instead — the sum
// excludes QUIC's own framing/ACK overhead, an acceptable approximation for the
// GUI's link-throughput display. The gateway shares one socket across sessions,
// so it counts process totals only (session counter nil → per-agent link bytes
// render "—"); the agent's socket is 1:1 with its session, so both are exact.

// quicStream adapts a *quic.Stream to transport.Stream (net.Conn + CloseWrite).
// A quic.Stream is not a net.Conn — it lacks LocalAddr/RemoteAddr — so those are
// snapshotted from the owning connection at wrap time. totals/session (either may
// be nil) accumulate payload bytes read/written.
type quicStream struct {
	s               *quic.Stream
	localAddr       net.Addr
	remoteAddr      net.Addr
	totals, session *stats.LinkCounters
}

func (q *quicStream) Read(p []byte) (int, error) {
	n, err := q.s.Read(p)
	if n > 0 {
		if q.totals != nil {
			q.totals.In.Add(int64(n))
		}
		if q.session != nil {
			q.session.In.Add(int64(n))
		}
	}
	return n, err
}

func (q *quicStream) Write(p []byte) (int, error) {
	n, err := q.s.Write(p)
	if n > 0 {
		if q.totals != nil {
			q.totals.Out.Add(int64(n))
		}
		if q.session != nil {
			q.session.Out.Add(int64(n))
		}
	}
	return n, err
}

func (q *quicStream) SetDeadline(t time.Time) error      { return q.s.SetDeadline(t) }
func (q *quicStream) SetReadDeadline(t time.Time) error  { return q.s.SetReadDeadline(t) }
func (q *quicStream) SetWriteDeadline(t time.Time) error { return q.s.SetWriteDeadline(t) }
func (q *quicStream) LocalAddr() net.Addr                { return q.localAddr }
func (q *quicStream) RemoteAddr() net.Addr               { return q.remoteAddr }

// CloseWrite half-closes: quic.Stream.Close sends a FIN on the send side (peer
// reads io.EOF) while our reads keep working — identical to yamux CloseWrite,
// required for correct splice shutdown.
func (q *quicStream) CloseWrite() error { return q.s.Close() }

// Close fully tears the stream down. CancelRead sends STOP_SENDING so a peer that
// is still writing stops (and unblocks any parked Read); Close FINs the send side
// (a no-op if CloseWrite already ran). Callers defer this only after the splice
// has drained both directions, so it never truncates live payload on the happy
// path — matching muxStream, whose Close also full-closes.
func (q *quicStream) Close() error {
	q.s.CancelRead(0)
	return q.s.Close()
}

// quicSession is the accepting (server) side: it owns only the *quic.Conn. The
// listener's Transport and UDP socket are shared across every agent's session, so
// closing one session must not touch them — Close closes just this connection,
// which tears down all of its streams. The client side (quicClientSession) owns
// its Transport/socket 1:1 and closes them too.
type quicSession struct {
	conn            *quic.Conn
	totals, session *stats.LinkCounters
}

func (q *quicSession) OpenStream() (Stream, error) {
	// OpenStreamSync waits (rather than erroring) if the peer's MAX_STREAMS is
	// momentarily exhausted; the conn context ends the wait when the conn dies.
	st, err := q.conn.OpenStreamSync(q.conn.Context())
	if err != nil {
		return nil, err
	}
	return q.wrap(st), nil
}

func (q *quicSession) AcceptStream() (Stream, error) {
	st, err := q.conn.AcceptStream(q.conn.Context())
	if err != nil {
		return nil, err
	}
	return q.wrap(st), nil
}

func (q *quicSession) wrap(st *quic.Stream) *quicStream {
	return &quicStream{
		s:          st,
		localAddr:  q.conn.LocalAddr(),
		remoteAddr: q.conn.RemoteAddr(),
		totals:     q.totals,
		session:    q.session,
	}
}

func (q *quicSession) Close() error               { return q.conn.CloseWithError(0, "") }
func (q *quicSession) CloseChan() <-chan struct{} { return q.conn.Context().Done() }
func (q *quicSession) RemoteAddr() net.Addr       { return q.conn.RemoteAddr() }

// TLSConnectionState exposes the negotiated TLS state so callers/tests can assert
// the handshake (e.g. the PQ hybrid KEM) — the QUIC analogue of *tls.Conn's
// ConnectionState.
func (q *quicSession) TLSConnectionState() tls.ConnectionState {
	return q.conn.ConnectionState().TLS
}

// quicClientSession is the dialing side. Unlike the server, a client dial is 1:1
// with its UDP socket and Transport, so Close tears down all three: the QUIC
// connection, the Transport (which joins quic-go's background goroutines), then
// the packet conn (Transport.Close does not close a caller-supplied socket).
type quicClientSession struct {
	quicSession
	tr *quic.Transport
	pc net.PacketConn
}

func (q *quicClientSession) Close() error {
	_ = q.conn.CloseWithError(0, "")
	_ = q.tr.Close()
	return q.pc.Close()
}

// QUICListener accepts inbound QUIC connections on one UDP socket. Its Transport
// and socket are shared by every session it produces; totals accumulate every
// session's link bytes (per-session counting is not attributable on a shared
// socket).
type QUICListener struct {
	tr     *quic.Transport
	ln     *quic.Listener
	pc     net.PacketConn
	totals *stats.LinkCounters
}

// ListenQUIC starts a QUIC listener over an already-bound UDP socket (the caller
// owns binding). tlsConf is the plain gateway TLS config; withALPN injects the
// mandatory ALPN without touching CurvePreferences (so the PQ hybrid KEM
// survives). totals (may be nil) accumulates process link bytes across sessions.
func ListenQUIC(pc net.PacketConn, tlsConf *tls.Config, totals *stats.LinkCounters) (*QUICListener, error) {
	tr := &quic.Transport{Conn: pc}
	ln, err := tr.Listen(withALPN(tlsConf), quicConfig())
	if err != nil {
		_ = tr.Close()
		return nil, fmt.Errorf("transport: quic listen: %w", err)
	}
	return &QUICListener{tr: tr, ln: ln, pc: pc, totals: totals}, nil
}

// Accept returns the next agent session after its QUIC (and thus TLS) handshake
// completes.
func (l *QUICListener) Accept(ctx context.Context) (Session, error) {
	conn, err := l.ln.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return &quicSession{conn: conn, totals: l.totals}, nil
}

func (l *QUICListener) Addr() net.Addr { return l.ln.Addr() }

// Close stops accepting and joins quic-go's transport goroutines, then releases
// the UDP socket. Individual accepted sessions are closed separately (eviction),
// leaving the shared listener and other agents untouched.
func (l *QUICListener) Close() error {
	_ = l.ln.Close()
	_ = l.tr.Close()
	return l.pc.Close()
}

// DialQUIC opens a client QUIC connection to remoteAddr over the given UDP socket
// and returns the established session (handshake complete). The returned session
// owns pc and its Transport and closes both on Session.Close. totals/session (may
// be nil) accumulate this session's link bytes — both exact, since one client
// socket carries exactly one session.
func DialQUIC(ctx context.Context, pc net.PacketConn, remoteAddr string, tlsConf *tls.Config, totals, session *stats.LinkCounters) (Session, error) {
	ua, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("transport: resolve %q: %w", remoteAddr, err)
	}
	tr := &quic.Transport{Conn: pc}
	conn, err := tr.Dial(ctx, ua, withALPN(tlsConf), quicConfig())
	if err != nil {
		_ = tr.Close()
		_ = pc.Close()
		return nil, fmt.Errorf("transport: quic dial %s: %w", remoteAddr, err)
	}
	return &quicClientSession{
		quicSession: quicSession{conn: conn, totals: totals, session: session},
		tr:          tr,
		pc:          pc,
	}, nil
}

// withALPN returns a clone of cfg with a QUIC ALPN set when the caller left one
// unset. It clones to avoid mutating the shared link config, and it never touches
// CurvePreferences — leaving it nil keeps Go's X25519MLKEM768 hybrid default.
func withALPN(cfg *tls.Config) *tls.Config {
	if len(cfg.NextProtos) > 0 {
		return cfg
	}
	c := cfg.Clone()
	c.NextProtos = []string{quicALPN}
	return c
}
