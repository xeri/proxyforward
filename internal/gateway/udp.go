// UDP public listeners.
//
// A UDP port has no accept(): every client's datagrams arrive on one socket,
// and "a new connection" is simply the first datagram from a source address
// this listener has not seen. udpListener manufactures that event so a UDP port
// satisfies net.Listener — which means the actor's ghost-listener wait,
// per-agent eviction, the pause bits, the connection gate, conntrack, and the
// bandwidth caps all apply to UDP through the code that already implements them
// for TCP, rather than through a second copy that drifts.
//
// Ownership: readLoop owns the socket's read side and is the only writer of
// session inboxes. The session map is guarded by mu; a session removes itself on
// Close. Sessions write to the shared socket directly (WriteToUDP is safe from
// many goroutines), so no write is ever serialized behind another player's.
package gateway

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"proxyforward/internal/relay"
)

const (
	// udpSessionQueue is how many datagrams may wait for one player between the
	// shared read loop and that player's splice goroutine. A full queue drops
	// the newest datagram, which is what a real socket's receive buffer does
	// under the same pressure — and dropping is correct for UDP in a way that
	// blocking is not: blocking here would let one stalled player stall the read
	// loop, and with it every other player on the port.
	//
	// 64 covers a burst of roughly three seconds of a Bedrock client's uplink
	// while leaving the per-session footprint near 128 KiB at worst.
	udpSessionQueue = 64

	// udpBufSize is the pooled per-datagram buffer. Sized just over a standard
	// 1500-byte MTU so ordinary game and voice traffic never allocates; a
	// datagram larger than this (nothing this tunnel is built for sends one)
	// takes an exact allocation instead of inflating every pooled buffer to
	// relay.MaxDatagram, which at this queue depth would cost megabytes a
	// player.
	udpBufSize = 2048

	// udpReadErrBackoff paces the read loop after an unexpected socket error, so
	// a persistently failing socket cannot spin a core. Same reasoning as
	// retryAccept's backoff, and the same rule: only a closed socket stops us.
	udpReadErrBackoff = 5 * time.Millisecond
)

// udpBufPool recycles the per-datagram buffers handed from the read loop to a
// session. Buffers are returned once the session's Read has copied the payload
// out.
var udpBufPool = sync.Pool{New: func() any { b := make([]byte, udpBufSize); return &b }}

func udpGetBuf(n int) []byte {
	if n > udpBufSize {
		return make([]byte, n)
	}
	bp := udpBufPool.Get().(*[]byte)
	return (*bp)[:n]
}

func udpPutBuf(b []byte) {
	if cap(b) != udpBufSize {
		return // an exact allocation for an oversize datagram; let it go
	}
	b = b[:udpBufSize]
	udpBufPool.Put(&b)
}

// udpListener presents a bound UDP port as a net.Listener whose Accept yields
// one udpSession per client source address.
type udpListener struct {
	pc     *net.UDPConn
	gate   *connGate
	logger *slog.Logger

	accept chan *udpSession
	closed chan struct{} // closed by Close, before the socket
	done   chan struct{} // closed when readLoop has exited
	once   sync.Once

	mu       sync.Mutex
	sessions map[string]*udpSession // client address → session
	closing  bool
}

// newUDPListener binds a UDP public port. gate is the same connection gate the
// TCP listeners use: a UDP session takes a slot when it is created and releases
// it when it closes, so the session map is bounded by the gate's global cap and
// keyed per source network by the gate's own accounting. That is deliberate —
// the table an attacker can grow must be bounded by construction, and inventing
// a second, UDP-only limit would mean two policies to keep in step.
func newUDPListener(bindAddr string, port int, gate *connGate, logger *slog.Logger) (*udpListener, error) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(bindAddr), Port: port})
	if err != nil {
		return nil, err
	}
	l := &udpListener{
		pc:       pc,
		gate:     gate,
		logger:   logger,
		accept:   make(chan *udpSession, 64),
		closed:   make(chan struct{}),
		done:     make(chan struct{}),
		sessions: make(map[string]*udpSession),
	}
	go l.readLoop()
	return l, nil
}

// Accept returns the next client to send this port a datagram. It reports
// net.ErrClosed once the listener is closed, which is what stops the actor's
// accept loop (isListenerClosed) and nothing else does.
func (l *udpListener) Accept() (net.Conn, error) {
	select {
	case s := <-l.accept:
		return s, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *udpListener) Addr() net.Addr { return l.pc.LocalAddr() }

// Close stops the listener and every session on it, then waits for the read
// loop to exit so no goroutine outlives the bound port — the same promise the
// actor's ghost-listener guarantee makes for TCP, and what keeps the e2e
// goleak check honest.
func (l *udpListener) Close() error {
	l.once.Do(func() {
		l.mu.Lock()
		l.closing = true
		live := make([]*udpSession, 0, len(l.sessions))
		for _, s := range l.sessions {
			live = append(live, s)
		}
		l.mu.Unlock()

		close(l.closed)
		l.pc.Close()
		for _, s := range live {
			s.Close()
		}
		<-l.done
	})
	return nil
}

// readLoop is the single reader of the socket: one datagram at a time into a
// reusable scratch, then a pooled copy handed to the owning session.
func (l *udpListener) readLoop() {
	defer close(l.done)
	scratch := make([]byte, relay.MaxDatagram)
	for {
		n, addr, err := l.pc.ReadFromUDP(scratch)
		if n > 0 && addr != nil {
			l.deliver(addr, scratch[:n])
		}
		if err == nil {
			continue
		}
		if isListenerClosed(err) {
			return
		}
		// Everything else is transient and must not stop the loop. The one that
		// matters on this platform: Windows reports a *previous* send's ICMP
		// Port Unreachable as ECONNRESET on the *next* receive, even though this
		// socket is unconnected and the error belongs to a different peer. A
		// loop that returned on it would take the whole UDP tunnel down the
		// first time any player closed their game — the port would stay bound
		// and answer nothing, which is the failure mode retryAccept exists to
		// prevent for TCP.
		if !errors.Is(err, syscall.ECONNRESET) {
			l.logger.Debug("udp read error", "err", err)
			select {
			case <-time.After(udpReadErrBackoff):
			case <-l.closed:
				return
			}
		}
	}
}

// deliver routes one datagram to its session, creating the session (and thus
// the "accepted connection") when the source is new.
func (l *udpListener) deliver(addr *net.UDPAddr, payload []byte) {
	key := addr.String()

	l.mu.Lock()
	if s, ok := l.sessions[key]; ok {
		l.mu.Unlock()
		s.push(payload)
		return
	}
	if l.closing {
		l.mu.Unlock()
		return
	}
	// A source with no session is a new player: it must pass the same gate a
	// TCP connection does, before it costs anything. Refused means the datagram
	// is dropped with no state created, which is the whole point — the map
	// cannot grow past what the gate allows.
	ip := addr.IP.String()
	if !l.gate.admit(ip) {
		l.mu.Unlock()
		return
	}
	s := newUDPSession(l, addr, key, ip)
	l.sessions[key] = s
	l.mu.Unlock()

	select {
	case l.accept <- s:
		s.push(payload)
	default:
		// The accept backlog is full: the gateway is not taking new players fast
		// enough. Drop this one rather than queue without bound; the client will
		// retry, exactly as it would against a full TCP accept queue.
		s.Close()
	}
}

// remove unregisters a session. Called from the session's own Close.
func (l *udpListener) remove(s *udpSession) {
	l.mu.Lock()
	if cur, ok := l.sessions[s.key]; ok && cur == s {
		delete(l.sessions, s.key)
	}
	l.mu.Unlock()
}

// sessionCount reports how many sessions are live, for tests and diagnostics.
func (l *udpListener) sessionCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sessions)
}

// udpSession is one client's flow on a UDP public port, presented as a net.Conn
// so relay.Splice and everything around it treats it exactly like a TCP player
// connection. Read returns exactly one datagram, Write sends exactly one.
type udpSession struct {
	l    *udpListener
	addr *net.UDPAddr
	key  string
	ip   string

	inbox  chan []byte
	closed chan struct{}
	once   sync.Once

	// Read-deadline machinery. Splice arms a deadline once and re-arms it only
	// when it fires, so a timer per SetReadDeadline costs nothing per datagram —
	// which a timer per Read would not. Each call installs a fresh channel and
	// its own timer closes that channel exactly once.
	dmu    sync.Mutex
	dch    chan struct{}
	dtimer *time.Timer
}

func newUDPSession(l *udpListener, addr *net.UDPAddr, key, ip string) *udpSession {
	return &udpSession{
		l:      l,
		addr:   addr,
		key:    key,
		ip:     ip,
		inbox:  make(chan []byte, udpSessionQueue),
		closed: make(chan struct{}),
	}
}

// push hands one datagram to the session, dropping it if the player's queue is
// full. Called only from the read loop.
func (s *udpSession) push(payload []byte) {
	b := udpGetBuf(len(payload))
	copy(b, payload)
	select {
	case s.inbox <- b:
	default:
		udpPutBuf(b)
	}
}

// Read returns the next whole datagram for this client.
func (s *udpSession) Read(p []byte) (int, error) {
	s.dmu.Lock()
	dch := s.dch
	s.dmu.Unlock()

	select {
	case b := <-s.inbox:
		n := copy(p, b)
		udpPutBuf(b)
		return n, nil
	case <-s.closed:
		return 0, io.EOF
	case <-dch:
		// os.ErrDeadlineExceeded is a net.Error reporting Timeout() — which is
		// what relay's idle reaper tests for. A concrete error type of our own
		// would be read as a hard fault and kill healthy sessions every window.
		return 0, os.ErrDeadlineExceeded
	}
}

// Write sends one datagram to this client. WriteToUDP is safe from multiple
// goroutines, so players never serialize behind one another.
func (s *udpSession) Write(p []byte) (int, error) {
	select {
	case <-s.closed:
		return 0, net.ErrClosed
	default:
	}
	return s.l.pc.WriteToUDP(p, s.addr)
}

// CloseWrite has no meaning on a datagram flow: there is no FIN to send. Splice
// calls it on EOF and treats an error as real, so it must report success.
func (s *udpSession) CloseWrite() error { return nil }

func (s *udpSession) Close() error {
	s.once.Do(func() {
		close(s.closed)
		s.l.remove(s)
		s.l.gate.release(s.ip)
		s.dmu.Lock()
		if s.dtimer != nil {
			s.dtimer.Stop()
		}
		s.dmu.Unlock()
		// Return any queued datagrams to the pool rather than letting the
		// channel's backing array hold them until it is collected.
		for {
			select {
			case b := <-s.inbox:
				udpPutBuf(b)
			default:
				return
			}
		}
	})
	return nil
}

func (s *udpSession) LocalAddr() net.Addr  { return s.l.pc.LocalAddr() }
func (s *udpSession) RemoteAddr() net.Addr { return s.addr }

// SetReadDeadline installs a deadline for subsequent Reads. Zero clears it.
func (s *udpSession) SetReadDeadline(t time.Time) error {
	s.dmu.Lock()
	defer s.dmu.Unlock()
	if s.dtimer != nil {
		s.dtimer.Stop()
		s.dtimer = nil
	}
	ch := make(chan struct{})
	s.dch = ch
	if t.IsZero() {
		return nil // never fires; Read blocks until data, close, or a new deadline
	}
	if d := time.Until(t); d <= 0 {
		close(ch)
	} else {
		s.dtimer = time.AfterFunc(d, func() { close(ch) })
	}
	return nil
}

// SetWriteDeadline is a no-op: the write side is the listener's shared socket,
// and a deadline set for one player would apply to every player on the port.
// Sends to a UDP socket do not block on a peer in any case — there is no
// receiver window to fill — so there is no stall for a deadline to catch.
func (s *udpSession) SetWriteDeadline(time.Time) error { return nil }

func (s *udpSession) SetDeadline(t time.Time) error { return s.SetReadDeadline(t) }

// listenUDPFirstFree is the UDP twin of listenFirstFree: try each candidate port
// in order, return the first that binds. A candidate of 0 asks the OS for any
// free ephemeral port.
func listenUDPFirstFree(bindAddr string, candidates []int, gate *connGate, logger *slog.Logger) *udpListener {
	for _, p := range candidates {
		if l, err := newUDPListener(bindAddr, p, gate, logger); err == nil {
			return l
		}
	}
	return nil
}

// listenerPort extracts the bound port from a listener of either protocol. It
// exists because a public listener is now TCP or UDP, and every caller that
// used to assert *net.TCPAddr would panic on the other one.
func listenerPort(ln net.Listener) int {
	switch a := ln.Addr().(type) {
	case *net.TCPAddr:
		return a.Port
	case *net.UDPAddr:
		return a.Port
	}
	// Unreachable for the two listeners this gateway binds; parse rather than
	// panic, so an exotic listener degrades to a wrong-but-harmless port.
	if _, portStr, err := net.SplitHostPort(ln.Addr().String()); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil {
			return p
		}
	}
	return 0
}
