package gateway

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// udpSpec is a minimal enabled UDP tunnel spec.
func udpSpec(id string, port int) control.TunnelSpec {
	return control.TunnelSpec{ID: id, Name: id, Type: config.TunnelUDP, PublicPort: port}
}

// dialTo returns a connected UDP socket pointed at a listener.
func dialTo(t *testing.T, l *udpListener) *net.UDPConn {
	t.Helper()
	c, err := net.DialUDP("udp", nil, l.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// newTestUDPListener binds a UDP listener on loopback with the given gate.
func newTestUDPListener(t *testing.T, gate *connGate) *udpListener {
	t.Helper()
	l, err := newUDPListener("127.0.0.1", 0, gate, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// TestUDPListenerAcceptsOncePerSource is the core of presenting a connectionless
// socket as a net.Listener (U1): the *first* datagram from an address is the
// accept, and every datagram after it belongs to that same session. Accepting
// twice would give one player two tunnel streams and two conn-gate slots.
func TestUDPListenerAcceptsOncePerSource(t *testing.T) {
	l := newTestUDPListener(t, newConnGate(64, 64))
	c := dialTo(t, l)

	if _, err := c.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	conn, err := l.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	buf := make([]byte, 2048)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "first" {
		t.Fatalf("read %q, %v; want %q", buf[:n], err, "first")
	}

	// A second datagram from the same source must reach the same session, not
	// produce another accept.
	if _, err := c.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err = conn.Read(buf)
	if err != nil || string(buf[:n]) != "second" {
		t.Fatalf("read %q, %v; want %q", buf[:n], err, "second")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := l.Accept(); err == nil {
			t.Error("a second datagram from one source produced a second accept")
		}
	}()
	select {
	case <-done:
		t.Fatal("accept returned for an existing session")
	case <-time.After(200 * time.Millisecond):
	}
	l.Close()
	<-done
}

// TestUDPListenerGateBoundsTheSessionTable proves the session map is bounded by
// construction (U2). The map is the one structure a spoofing attacker can grow
// without completing anything, so it must be the connection gate — the same
// per-network policy TCP gets — that decides whether an entry is created at all,
// not a check that happens after the entry exists.
func TestUDPListenerGateBoundsTheSessionTable(t *testing.T) {
	// One slot globally: the second distinct source must create nothing.
	l := newTestUDPListener(t, newConnGate(1, 1))

	first := dialTo(t, l)
	if _, err := first.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Accept(); err != nil {
		t.Fatalf("first source: %v", err)
	}
	if got := l.sessionCount(); got != 1 {
		t.Fatalf("session count %d, want 1", got)
	}

	second := dialTo(t, l)
	if _, err := second.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	// Give the read loop time to have processed (and refused) it.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if l.sessionCount() > 1 {
			t.Fatal("a refused source still created a session; the table is not gate-bounded")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestUDPSessionCloseReleasesItsGateSlot (U3): a session holds a gate slot for
// its whole life, so failing to release on close would leak the tunnel's
// capacity one departed player at a time until the port refused everyone.
func TestUDPSessionCloseReleasesItsGateSlot(t *testing.T) {
	gate := newConnGate(1, 1)
	l := newTestUDPListener(t, gate)

	c := dialTo(t, l)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	// The one slot is taken.
	if gate.admit("203.0.113.9") {
		t.Fatal("gate admitted past its global cap while a udp session held the slot")
	}
	conn.Close()
	if !gate.admit("203.0.113.9") {
		t.Fatal("closing a udp session did not release its gate slot")
	}
	if got := l.sessionCount(); got != 0 {
		t.Fatalf("session count %d after close, want 0", got)
	}
}

// TestUDPSessionReadDeadlineReportsATimeout (U4). relay's idle reaper decides
// whether a quiet leg is merely quiet or dead by asking net.Error.Timeout(). A
// session that reported its deadline as any other kind of error would be torn
// down as a hard fault every single idle window.
func TestUDPSessionReadDeadlineReportsATimeout(t *testing.T) {
	l := newTestUDPListener(t, newConnGate(64, 64))
	c := dialTo(t, l)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("read past the deadline returned no error")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("deadline error %v is not a net.Error with Timeout(); relay would kill the session", err)
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("deadline error %v is not os.ErrDeadlineExceeded", err)
	}
}

// TestUDPSessionQueueDropsRatherThanBlocks (U5). One shared read loop feeds
// every player on the port, so a session whose reader has stopped must lose
// datagrams instead of stalling the loop — a block there would freeze every
// other player on that port behind one stuck client.
func TestUDPSessionQueueDropsRatherThanBlocks(t *testing.T) {
	l := newTestUDPListener(t, newConnGate(64, 64))
	slow := dialTo(t, l)
	if _, err := slow.Write([]byte("open")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Accept(); err != nil {
		t.Fatal(err)
	}

	// Overrun the queue without ever reading from the session.
	payload := make([]byte, 512)
	for range udpSessionQueue * 3 {
		if _, err := slow.Write(payload); err != nil {
			t.Fatal(err)
		}
	}

	// The read loop must still be serving: a brand-new source still gets in.
	fresh := dialTo(t, l)
	if _, err := fresh.Write([]byte("still alive")); err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("read loop stalled behind a session that stopped reading")
	}
}

// TestUDPListenerCloseStopsEverything (U6) is the ghost-listener promise for
// UDP: once Close returns, the port is free, Accept reports the one error that
// stops an accept loop, and no goroutine of this listener's is still running —
// which is what keeps the e2e goleak check honest.
func TestUDPListenerCloseStopsEverything(t *testing.T) {
	gate := newConnGate(64, 64)
	l, err := newUDPListener("127.0.0.1", 0, gate, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().(*net.UDPAddr)

	c, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Accept(); err != nil {
		t.Fatal(err)
	}

	l.Close()

	if _, err := l.Accept(); !isListenerClosed(err) {
		t.Fatalf("Accept after Close returned %v; only net.ErrClosed stops an accept loop", err)
	}
	if got := l.sessionCount(); got != 0 {
		t.Fatalf("%d sessions survived Close", got)
	}
	// The port must be genuinely free: rebinding it is the proof.
	again, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("port %d still held after Close: %v", addr.Port, err)
	}
	again.Close()

	// Close is idempotent — eviction paths may call it more than once.
	l.Close()
}

// TestUDPListenerRejectsBindWithoutAGate (U7) pins the refusal in bindLocked.
// A UDP listener with no gate has nothing bounding its session table, so the
// bind must fail loudly rather than succeed unbounded.
func TestUDPListenerRejectsBindWithoutAGate(t *testing.T) {
	a := newActor(discardLogger())
	sess := &agentSession{agentID: "agt_udp", logger: discardLogger()}
	sess.pauseMine.Store(nil)
	sess.pauseTheirs.Store(nil)

	spec := udpSpec("t_udp", 0)
	_, err := a.bindLocked(sess, spec, bindPolicy{bindAddr: "127.0.0.1"}, func(*publicListener, net.Conn) {})
	if err == nil {
		t.Fatal("bound a udp listener with no connection gate")
	}
}
