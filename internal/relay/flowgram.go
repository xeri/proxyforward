package relay

// Flowgrams: a udp tunnel's payload carried on QUIC's unreliable datagrams
// (RFC 9221) instead of its stream.
//
// A QUIC stream is reliable and ordered, so a lost packet carrying one datagram
// stalls the next until the retransmit lands — latency added to recover data
// that RakNet or a voice mod would rather have dropped. A QUIC connection's
// datagrams, though, are per-connection rather than per-stream, so each one must
// say which flow it belongs to: [4-byte big-endian flowID][payload].
//
// What does NOT change: the flow's stream. It is still opened, still carries
// open_conn, and is still what closes, half-closes, and is reaped — the datagram
// path is an optimization on the payload alone. FlowgramConn is a relay.Conn, so
// Splice is untouched, which is the whole point of presenting UDP as a stream in
// the first place.
//
// Ownership: FlowMux owns one demux goroutine per session and the flow table.
// Each FlowgramConn owns its queue, its stream-merge goroutine, and its
// registration — Close unregisters, closes the stream, and joins the merge.

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// flowgramHdr is the flow-id prefix on every datagram: 4 bytes, big-endian,
	// like the control envelope's length prefix. A connection's datagrams are
	// shared by every flow riding it, so without this a receiver cannot tell one
	// player's traffic from another's.
	flowgramHdr = 4

	// flowQueueDepth is how many datagrams may wait for one flow between the
	// session's single demux goroutine and that flow's splice. A full queue drops
	// the newest, for the reason udpSessionQueue does: blocking here would let one
	// stalled player stall the demux loop, and with it every player on the
	// connection — and dropping is what a real socket does under the same
	// pressure, which is precisely the semantics UDP callers already handle.
	flowQueueDepth = 64

	// quicDatagramTryMax is the payload size above which a datagram is not even
	// offered to the unreliable path. QUIC datagrams cannot be fragmented, so one
	// larger than the path allows is refused; 1200 bytes sits under the smallest
	// path any QUIC connection negotiates, which makes this an optimization bound
	// and nothing more. Correctness comes from the error: a send refused for size
	// falls back to the stream whatever this number says, so a wrong constant
	// costs a wasted attempt, never a lost datagram.
	quicDatagramTryMax = 1200
)

// FlowSender is the datagram half of the session a flow rides. It is declared
// here, structurally, rather than imported: relay must not depend on the
// transport package, and transport.DatagramSession satisfies this as written.
type FlowSender interface {
	SendDatagram(b []byte) error
	ReceiveDatagram(ctx context.Context) ([]byte, error)
}

// FlowMux routes one session's incoming datagrams to the flows riding it. One
// goroutine reads, splits the header, and hands the payload to the addressed
// flow's queue; a datagram for a flow that is not registered is dropped, which
// is the right answer for one that just closed and for a peer's stale
// retransmit alike.
type FlowMux struct {
	sess   FlowSender
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu    sync.RWMutex
	flows map[uint32]*FlowgramConn
}

// NewFlowMux starts the session's demux goroutine. It exits when ctx is
// cancelled (the session ended) or the connection dies, so a caller that parents
// this on the session ctx needs no separate teardown; Close is for the callers
// that want to join it explicitly.
func NewFlowMux(ctx context.Context, sess FlowSender) *FlowMux {
	ctx, cancel := context.WithCancel(ctx)
	m := &FlowMux{
		sess:   sess,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		flows:  make(map[uint32]*FlowgramConn),
	}
	go m.run()
	return m
}

func (m *FlowMux) run() {
	defer close(m.done)
	for {
		b, err := m.sess.ReceiveDatagram(m.ctx)
		if err != nil {
			return // ctx cancelled or the connection is gone; either ends the mux
		}
		if len(b) < flowgramHdr {
			continue // too short to name a flow
		}
		id := binary.BigEndian.Uint32(b)
		m.mu.RLock()
		f := m.flows[id]
		m.mu.RUnlock()
		if f == nil {
			continue
		}
		f.receiveDatagram(b[flowgramHdr:])
	}
}

// Register routes datagrams for id to f. Registering an id that is already
// present replaces it — a flow id is issued once per flow, so this only happens
// if one is reused, and the newer flow is the live one.
func (m *FlowMux) Register(id uint32, f *FlowgramConn) {
	m.mu.Lock()
	m.flows[id] = f
	m.mu.Unlock()
}

// Unregister stops routing for id. Datagrams still in flight for it are dropped
// by run, which is what makes a closed flow's late arrivals harmless.
func (m *FlowMux) Unregister(id uint32) {
	m.mu.Lock()
	delete(m.flows, id)
	m.mu.Unlock()
}

// Close stops the demux goroutine and waits for it.
func (m *FlowMux) Close() {
	m.cancel()
	<-m.done
}

// FlowStats counts which path a flow's datagrams took, so a caller can prove
// the fast path is carrying traffic without measuring time — a fast stream
// looks exactly like an unreliable datagram on a loopback stopwatch. One
// instance is shared by every flow on a gateway or agent; the adds are atomic,
// in the same class as the splice's own byte counters. May be nil.
type FlowStats struct {
	// Datagrams is what went out unreliably; Streamed is what fell back to a
	// flow's stream, for size or because the connection refused the send.
	Datagrams atomic.Int64
	Streamed  atomic.Int64
}

func (s *FlowStats) countDatagram() {
	if s != nil {
		s.Datagrams.Add(1)
	}
}

func (s *FlowStats) countStreamed() {
	if s != nil {
		s.Streamed.Add(1)
	}
}

// FlowgramConn is one udp flow's leg, presented as a relay.Conn so Splice,
// the idle reaper and the byte counters treat it exactly like the stream leg it
// replaces. Reads yield whole datagrams from either path; writes take the
// unreliable path when the payload fits and the reliable one when it does not.
//
// The stream remains the flow's lifecycle anchor: Close and CloseWrite delegate
// to it, and its EOF is what ends this leg's reads.
type FlowgramConn struct {
	mux    *FlowMux
	sess   FlowSender
	id     uint32
	stream Conn
	stats  *FlowStats
	// framed is the stream leg in its datagram clothes — the oversize write path,
	// and the source the merge goroutine reads. Its Read and Write touch disjoint
	// state, and each is single-threaded here (merge reads; the splice writes),
	// exactly as under Splice.
	framed *DatagramStream

	queue chan []byte
	// eof closes when the stream leg ends, so a Read waiting on datagrams learns
	// the peer is done instead of waiting out the idle reaper. A datagram flow has
	// no FIN of its own; the stream's is the only one there is.
	eof       chan struct{}
	closed    chan struct{}
	mergeDone chan struct{}
	once      sync.Once

	// peerReady is the only thing that lets this side use the unreliable path.
	// A flow id reaches the peer in the open_conn header, on the stream, and a
	// datagram sent before the peer has read it and registered would be dropped
	// as unknown — losing a player's packet, which the stream path never does.
	// So nothing goes unreliably until a datagram has come back for this flow:
	// the peer cannot have sent one without registering first. That is the whole
	// interlock, and it needs no acknowledgement message because the announce
	// below already is one.
	peerReady atomic.Bool

	// wbuf holds [flowID][payload] so a datagram is built without allocating per
	// write. quic-go copies what it is handed, so reusing this is safe.
	wbuf []byte

	// Read-deadline machinery, the udpSession pattern: Splice arms a deadline
	// once and re-arms it only when it fires, so a timer per SetReadDeadline
	// costs nothing per datagram — which a timer per Read would not.
	dmu    sync.Mutex
	dch    chan struct{}
	dtimer *time.Timer
}

// NewFlowgramConn registers flow id on mux and starts merging the stream leg
// into the same queue. sess is the datagram sender; stream is the flow's own
// stream, which stays open for its lifetime; stats (may be nil) counts which
// path each outgoing datagram took.
func NewFlowgramConn(mux *FlowMux, sess FlowSender, id uint32, stream Conn, stats *FlowStats) *FlowgramConn {
	f := &FlowgramConn{
		mux:       mux,
		sess:      sess,
		id:        id,
		stream:    stream,
		stats:     stats,
		framed:    &DatagramStream{Conn: stream},
		queue:     make(chan []byte, flowQueueDepth),
		eof:       make(chan struct{}),
		closed:    make(chan struct{}),
		mergeDone: make(chan struct{}),
	}
	mux.Register(id, f)
	go f.merge()
	// Announce: a datagram carrying this flow id and nothing else, sent the
	// moment the flow is routable. It is what lets the peer start using the
	// unreliable path without waiting for traffic to prove we are here. Best
	// effort by construction — losing it costs nothing, because the first real
	// datagram this side sends says the same thing.
	var hdr [flowgramHdr]byte
	binary.BigEndian.PutUint32(hdr[:], id)
	_ = sess.SendDatagram(hdr[:])
	return f
}

// receiveDatagram takes one datagram the mux routed to this flow. Any datagram
// at all proves the peer has registered, which is what unblocks our own
// unreliable sends; an empty one is the announce, which says exactly that and
// nothing more.
func (f *FlowgramConn) receiveDatagram(payload []byte) {
	f.peerReady.Store(true)
	if len(payload) == 0 {
		// Splice acts only on a Read returning n > 0, so an empty payload cannot
		// be expressed through it — the same rule DatagramStream follows.
		return
	}
	select {
	case f.queue <- payload:
	default: // the flow is behind; drop the newest, never block the demux loop
	}
}

// merge delivers the stream leg's datagrams — the ones too large for the
// unreliable path — into the same queue as the fast ones, so a reader sees one
// sequence and never has to know which path a datagram took. Per-path order is
// preserved; across the two it is not, which is the reordering every UDP
// protocol this tunnel carries already handles.
func (f *FlowgramConn) merge() {
	defer close(f.mergeDone)
	defer close(f.eof)
	// The splice's own pool: its buffers are BufSize, comfortably above
	// MaxDatagram, so one read never needs more and no flow carries a 64 KiB
	// buffer of its own for a path it may never use.
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	buf := *bp
	for {
		n, err := f.framed.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		// The queue outlives this iteration, so the datagram needs a buffer of its
		// own. Only the oversize path pays this, and by construction it is the
		// rare one — the fast path hands on quic-go's buffer untouched.
		b := make([]byte, n)
		copy(b, buf[:n])
		select {
		case f.queue <- b:
		default: // behind: drop the newest, as on the datagram path
		}
	}
}

// Read returns the next whole datagram for this flow, from either path.
func (f *FlowgramConn) Read(p []byte) (int, error) {
	// Queued datagrams win over every other outcome, so a flow that is closing
	// still delivers what already arrived.
	select {
	case b := <-f.queue:
		return deliverDatagram(p, b)
	default:
	}
	f.dmu.Lock()
	dch := f.dch
	f.dmu.Unlock()
	select {
	case b := <-f.queue:
		return deliverDatagram(p, b)
	case <-f.eof:
		// The stream ended: the peer has stopped sending on this flow. Drain what
		// the datagram path delivered meanwhile, then report EOF so the splice
		// unwinds now rather than at the idle reaper minutes later.
		select {
		case b := <-f.queue:
			return deliverDatagram(p, b)
		default:
			return 0, io.EOF
		}
	case <-f.closed:
		return 0, io.EOF
	case <-dch:
		// os.ErrDeadlineExceeded is a net.Error reporting Timeout(), which is what
		// the idle reaper tests for. A concrete error of our own would read as a
		// hard fault and kill healthy flows every window.
		return 0, os.ErrDeadlineExceeded
	}
}

// deliverDatagram copies one whole datagram out. A caller's buffer too small
// for it is reported rather than truncated — unreachable through Splice, whose
// buffer is BufSize, but a silently shortened datagram is a corruption no caller
// could detect (the rule DatagramStream.Read follows for the same reason).
func deliverDatagram(p, b []byte) (int, error) {
	if len(p) < len(b) {
		return 0, io.ErrShortBuffer
	}
	return copy(p, b), nil
}

// Write sends one datagram. Small enough for the path, with the peer known to be
// listening for this flow, and the unreliable send carries it; anything else —
// a flow the peer has not confirmed yet, a payload too large, or a send the
// connection refuses — goes on the stream, framed, exactly as before this
// existed.
func (f *FlowgramConn) Write(p []byte) (int, error) {
	if len(p) <= quicDatagramTryMax && f.peerReady.Load() {
		need := flowgramHdr + len(p)
		if cap(f.wbuf) < need {
			f.wbuf = make([]byte, need)
		}
		b := f.wbuf[:need]
		binary.BigEndian.PutUint32(b, f.id)
		copy(b[flowgramHdr:], p)
		if err := f.sess.SendDatagram(b); err == nil {
			f.stats.countDatagram()
			return len(p), nil
		}
		// Any refusal falls back rather than failing: too large for the current
		// path is the expected one, and every other reason (the connection is
		// going away) fails the stream write below too, where it belongs.
	}
	n, err := f.framed.Write(p)
	if err == nil {
		f.stats.countStreamed()
	}
	return n, err
}

// CloseWrite half-closes the stream, which is this flow's only FIN. The peer's
// merge goroutine reads that EOF and its own Read reports it, which is what lets
// a udp splice unwind on demand instead of on the reaper's clock.
func (f *FlowgramConn) CloseWrite() error { return f.stream.CloseWrite() }

// Close unregisters the flow, closes the stream, and joins the merge goroutine.
// Late datagrams for an unregistered flow are dropped by the mux, so a closed
// flow's arrivals are harmless.
func (f *FlowgramConn) Close() error {
	var err error
	f.once.Do(func() {
		f.mux.Unregister(f.id)
		close(f.closed)
		f.dmu.Lock()
		if f.dtimer != nil {
			f.dtimer.Stop()
		}
		f.dmu.Unlock()
		err = f.stream.Close() // unblocks merge's read
		<-f.mergeDone
	})
	return err
}

func (f *FlowgramConn) LocalAddr() net.Addr  { return f.stream.LocalAddr() }
func (f *FlowgramConn) RemoteAddr() net.Addr { return f.stream.RemoteAddr() }

// SetReadDeadline installs a deadline for subsequent Reads; zero clears it. The
// stream's own read deadline is deliberately left alone — the merge goroutine
// reads it continuously, and a deadline there would kill the flow rather than
// time out a Read.
func (f *FlowgramConn) SetReadDeadline(t time.Time) error {
	f.dmu.Lock()
	defer f.dmu.Unlock()
	if f.dtimer != nil {
		f.dtimer.Stop()
		f.dtimer = nil
	}
	ch := make(chan struct{})
	f.dch = ch
	if t.IsZero() {
		return nil // never fires; Read blocks until data, EOF, close, or a new deadline
	}
	if d := time.Until(t); d <= 0 {
		close(ch)
	} else {
		f.dtimer = time.AfterFunc(d, func() { close(ch) })
	}
	return nil
}

// SetWriteDeadline applies to the stream, the only write here that can block on
// a peer. An unreliable send has no receiver window to fill; it can wait only on
// the connection's own send queue, which drains or dies with the connection.
func (f *FlowgramConn) SetWriteDeadline(t time.Time) error { return f.stream.SetWriteDeadline(t) }

func (f *FlowgramConn) SetDeadline(t time.Time) error {
	if err := f.SetReadDeadline(t); err != nil {
		return err
	}
	return f.SetWriteDeadline(t)
}
