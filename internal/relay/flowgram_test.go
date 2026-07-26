package relay_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/relay"
)

// fakeSession stands in for a QUIC connection's datagram half: sends land in a
// slice a test can read, and receives come from a channel a test feeds. Sends
// larger than tooLargeOver (when set) are refused the way a real path-MTU
// refusal is, which is what drives the fallback.
type fakeSession struct {
	inbox chan []byte

	tooLargeOver int

	mu   sync.Mutex
	sent [][]byte
}

func newFakeSession() *fakeSession {
	return &fakeSession{inbox: make(chan []byte, 64)}
}

func (s *fakeSession) SendDatagram(b []byte) error {
	if s.tooLargeOver > 0 && len(b) > s.tooLargeOver {
		return errors.New("datagram too large for the current path")
	}
	cp := make([]byte, len(b)) // the real sender copies too; a test must not alias wbuf
	copy(cp, b)
	s.mu.Lock()
	s.sent = append(s.sent, cp)
	s.mu.Unlock()
	return nil
}

func (s *fakeSession) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case b := <-s.inbox:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *fakeSession) sentFrames() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.sent))
	copy(out, s.sent)
	return out
}

// deliver feeds one datagram to the mux as if it arrived on the connection.
func (s *fakeSession) deliver(flowID uint32, payload []byte) {
	b := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(b, flowID)
	copy(b[4:], payload)
	s.inbox <- b
}

// pipeStream is a relay.Conn over net.Pipe, for the stream leg a flow keeps.
type pipeStream struct{ net.Conn }

func (p pipeStream) CloseWrite() error { return p.Conn.Close() }

// flowFixture is one session and its one mux — the real shape, since a mux
// demultiplexes a whole connection's datagrams. Two muxes over one session would
// race each other for every datagram, so tests never build a second.
type flowFixture struct {
	sess *fakeSession
	mux  *relay.FlowMux
}

func newFlowFixture(t *testing.T) *flowFixture {
	t.Helper()
	sess := newFakeSession()
	ctx, cancel := context.WithCancel(context.Background())
	mux := relay.NewFlowMux(ctx, sess)
	t.Cleanup(func() {
		cancel()
		mux.Close()
	})
	return &flowFixture{sess: sess, mux: mux}
}

// flow registers one flow on the fixture's mux and returns it with the far end
// of its stream leg.
func (f *flowFixture) flow(t *testing.T, id uint32) (*relay.FlowgramConn, net.Conn) {
	t.Helper()
	near, far := net.Pipe()
	fc := relay.NewFlowgramConn(f.mux, f.sess, id, pipeStream{near})
	t.Cleanup(func() {
		fc.Close()
		far.Close()
	})
	return fc, far
}

// readOne reads one datagram with a bounded deadline.
func readOne(t *testing.T, fc *relay.FlowgramConn) []byte {
	t.Helper()
	_ = fc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	n, err := fc.Read(buf)
	if err != nil {
		t.Fatalf("read datagram: %v", err)
	}
	return buf[:n]
}

// TestFlowgramDemuxRoutesByFlowID: a connection's datagrams are shared by every
// flow riding it, so the 4-byte header is the only thing that says whose a
// datagram is. Each flow must see its own and nothing else, and a datagram for
// a flow nobody registered must be dropped rather than delivered to whoever is
// nearest. (udp-datagram)
func TestFlowgramDemuxRoutesByFlowID(t *testing.T) {
	fx := newFlowFixture(t)
	one, _ := fx.flow(t, 1)
	two, _ := fx.flow(t, 2)

	fx.sess.deliver(1, []byte("for one"))
	fx.sess.deliver(2, []byte("for two"))
	fx.sess.deliver(9, []byte("for nobody"))

	if got := readOne(t, one); string(got) != "for one" {
		t.Errorf("flow 1 read %q", got)
	}
	if got := readOne(t, two); string(got) != "for two" {
		t.Errorf("flow 2 read %q", got)
	}

	// The unregistered flow's datagram must not have gone anywhere: a second read
	// on either flow times out rather than producing it.
	_ = one.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if n, err := one.Read(make([]byte, 2048)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("flow 1 read %d bytes / err %v after its own datagram; a stray flow id was delivered", n, err)
	}
}

// TestFlowgramOversizeFallsBackToStream: a QUIC datagram cannot be fragmented,
// so one too large for the path must ride the stream instead — and the decision
// is driven by the send being refused, not only by the size constant, because
// the real bound is a property of the moment. Both sizes must arrive intact and
// be readable as whole datagrams by a peer. (udp-datagram)
func TestFlowgramOversizeFallsBackToStream(t *testing.T) {
	fx := newFlowFixture(t)
	sess := fx.sess
	fc, far := fx.flow(t, 7)

	// The far end reads the framed stream leg concurrently: net.Pipe is
	// synchronous, so a stream write blocks until someone reads it.
	type framed struct {
		data []byte
		err  error
	}
	got := make(chan framed, 2)
	go func() {
		peer := &relay.DatagramStream{Conn: pipeStream{far}}
		buf := make([]byte, relay.MaxDatagram)
		for {
			n, err := peer.Read(buf)
			if err != nil {
				got <- framed{err: err}
				return
			}
			d := make([]byte, n)
			copy(d, buf[:n])
			got <- framed{data: d}
		}
	}()

	small := bytes.Repeat([]byte{0xAB}, 200)
	if n, err := fc.Write(small); err != nil || n != len(small) {
		t.Fatalf("write small: n=%d err=%v", n, err)
	}
	frames := sess.sentFrames()
	if len(frames) != 1 {
		t.Fatalf("small datagram produced %d unreliable sends, want 1", len(frames))
	}
	if id := binary.BigEndian.Uint32(frames[0]); id != 7 {
		t.Errorf("unreliable send named flow %d, want 7", id)
	}
	if !bytes.Equal(frames[0][4:], small) {
		t.Error("unreliable send did not carry the payload")
	}

	// Well past quicDatagramTryMax: never offered to the unreliable path.
	big := bytes.Repeat([]byte{0xCD}, 4000)
	if n, err := fc.Write(big); err != nil || n != len(big) {
		t.Fatalf("write big: n=%d err=%v", n, err)
	}
	if len(sess.sentFrames()) != 1 {
		t.Errorf("an oversize datagram was offered to the unreliable path: %d sends", len(sess.sentFrames()))
	}
	select {
	case f := <-got:
		if f.err != nil {
			t.Fatalf("stream leg read: %v", f.err)
		}
		if !bytes.Equal(f.data, big) {
			t.Errorf("stream leg carried %d bytes, want the %d-byte datagram intact", len(f.data), len(big))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the oversize datagram never reached the stream leg")
	}

	// And a send the connection refuses for size falls back too, whatever the
	// size constant says — correctness comes from the error.
	sess.tooLargeOver = 1 // from here the connection refuses every unreliable send
	refused := bytes.Repeat([]byte{0xEF}, 300)
	if n, err := fc.Write(refused); err != nil || n != len(refused) {
		t.Fatalf("write refused-size: n=%d err=%v", n, err)
	}
	select {
	case f := <-got:
		if f.err != nil {
			t.Fatalf("stream leg read: %v", f.err)
		}
		if !bytes.Equal(f.data, refused) {
			t.Error("a refused unreliable send did not fall back to the stream intact")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a refused unreliable send was dropped instead of falling back")
	}
}

// TestFlowgramQueueDropsWhenFull: one flow that stops reading must not stall the
// session's single demux goroutine, because that goroutine serves every other
// flow on the connection. The newest datagram is dropped instead — what a real
// socket does under the same pressure, and what every UDP protocol this tunnel
// carries already handles. (udp-datagram)
func TestFlowgramQueueDropsWhenFull(t *testing.T) {
	fx := newFlowFixture(t)
	sess := fx.sess
	slow, _ := fx.flow(t, 1)
	other, _ := fx.flow(t, 2)

	// Far more than the queue depth, from a flow nobody is reading.
	for i := 0; i < 500; i++ {
		sess.deliver(1, []byte{byte(i)})
	}
	// The demux is still alive and serving the other flow, which is the property
	// under test: a full queue drops, it does not block.
	sess.deliver(2, []byte("still moving"))
	if got := readOne(t, other); string(got) != "still moving" {
		t.Fatalf("flow 2 read %q — the demux was stalled by a flow that stopped reading", got)
	}

	// The slow flow kept a bounded amount, not 500 datagrams.
	kept := 0
	for {
		_ = slow.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if _, err := slow.Read(make([]byte, 2048)); err != nil {
			break
		}
		kept++
		if kept > 200 {
			t.Fatal("the queue is unbounded: a flow that stops reading holds everything sent to it")
		}
	}
	if kept == 0 {
		t.Fatal("the queue dropped everything, including what it had room for")
	}
	t.Logf("bounded queue kept %d of 500 datagrams", kept)
}

// TestFlowgramReadDeadline: the idle reaper works by arming a read deadline and
// recognising the timeout, so a flow leg that ignored deadlines would never be
// reaped — and a UDP flow has no FIN, which makes the reaper the only thing that
// retires it. The error must report Timeout(), not a fault of its own. (udp-datagram)
func TestFlowgramReadDeadline(t *testing.T) {
	fx := newFlowFixture(t)
	sess := fx.sess
	fc, _ := fx.flow(t, 3)

	_ = fc.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	start := time.Now()
	_, err := fc.Read(make([]byte, 2048))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read past the deadline returned %v, want os.ErrDeadlineExceeded", err)
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("deadline error %v does not report Timeout(); the idle reaper tests for exactly that", err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("read returned after %s, well before its deadline", elapsed)
	}

	// Clearing the deadline lets a later datagram through.
	_ = fc.SetReadDeadline(time.Time{})
	sess.deliver(3, []byte("after"))
	if got := readOne(t, fc); string(got) != "after" {
		t.Errorf("read %q after clearing the deadline", got)
	}
}

// TestFlowgramCloseUnregisters: a closed flow's late datagrams must be dropped
// by the mux rather than delivered into a queue nobody drains, and Close must
// join the goroutine merging its stream leg — an unjoined one would outlive
// every flow the session ever carried. The stream's EOF is also what ends a
// peer's reads, since a datagram flow has no FIN of its own. (udp-datagram)
func TestFlowgramCloseUnregisters(t *testing.T) {
	fx := newFlowFixture(t)
	sess := fx.sess
	fc, _ := fx.flow(t, 5)

	sess.deliver(5, []byte("before close"))
	if got := readOne(t, fc); string(got) != "before close" {
		t.Fatalf("read %q", got)
	}
	if err := fc.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("close: %v", err)
	}
	// Idempotent, and the second call must not block on an already-joined merge.
	if err := fc.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second close: %v", err)
	}

	// The mux keeps serving other flows after one closes.
	live, _ := fx.flow(t, 6)
	sess.deliver(5, []byte("too late"))
	sess.deliver(6, []byte("live flow"))
	if got := readOne(t, live); string(got) != "live flow" {
		t.Errorf("flow 6 read %q — a closed flow's datagram was not dropped", got)
	}

	// The closed flow reports EOF rather than hanging.
	_ = fc.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := fc.Read(make([]byte, 2048)); !errors.Is(err, io.EOF) {
		t.Errorf("read on a closed flow returned %v, want io.EOF", err)
	}
}

// TestFlowgramStreamEOFEndsReads: when the peer half-closes the flow's stream —
// the only FIN a udp flow has — a reader waiting on datagrams must learn of it
// at once. Waiting out the idle reaper instead would hold the player's session,
// its gate slot and its local socket for minutes after they left. (udp-datagram)
func TestFlowgramStreamEOFEndsReads(t *testing.T) {
	fx := newFlowFixture(t)
	fc, far := fx.flow(t, 4)

	go func() {
		time.Sleep(50 * time.Millisecond)
		far.Close() // the peer's CloseWrite, as net.Pipe expresses it
	}()
	_ = fc.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := fc.Read(make([]byte, 2048)); !errors.Is(err, io.EOF) {
		t.Fatalf("read after the stream leg ended returned %v, want io.EOF", err)
	}
}
