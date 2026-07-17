package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"syscall"
	"testing"
	"time"
)

// scriptedListener hands out whatever Accept should return next, and signals
// each call so a test can tell "the loop came back for more" from "the loop
// exited".
type scriptedListener struct {
	next     chan any // error or net.Conn
	accepted chan struct{}
}

func newScriptedListener() *scriptedListener {
	return &scriptedListener{next: make(chan any), accepted: make(chan struct{}, 16)}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	select {
	case l.accepted <- struct{}{}:
	default:
	}
	switch v := (<-l.next).(type) {
	case error:
		return nil, v
	case net.Conn:
		return v, nil
	}
	panic("unreachable")
}

func (l *scriptedListener) Close() error   { return nil }
func (l *scriptedListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4zero} }

func testGateway(ln net.Listener) *Gateway {
	g := &Gateway{
		controlLn: ln,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		authLim:   newAuthLimiter(10),
	}
	g.preAuth = newPreAuthGate(4)
	return g
}

// TestControlAcceptSurvivesATransientError: the accept loop used to return on
// *any* Accept error. That turns a momentary shortage into a permanent outage of
// the worst kind — the process stays up, the GUI still says "running", the public
// ports still take players, and no agent can ever reconnect, so the tunnel is
// dead until someone restarts it by hand. Every plausible Accept error is
// transient (the OS out of descriptors or buffers, a peer resetting between SYN
// and accept); only a closed listener means stop.
func TestControlAcceptSurvivesATransientError(t *testing.T) {
	ln := newScriptedListener()
	g := testGateway(ln)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); g.acceptControl(ctx) }()

	<-ln.accepted // the loop is in its first Accept
	ln.next <- syscall.ECONNABORTED

	select {
	case <-ln.accepted:
		// It came back. Good.
	case <-done:
		t.Fatal("the accept loop exited on a transient error: the gateway is still up and still serving players, but no agent can ever reconnect")
	case <-time.After(10 * time.Second):
		t.Fatal("the accept loop stopped calling Accept after a transient error")
	}

	// And a closed listener must still stop it, or shutdown spins forever.
	ln.next <- net.ErrClosed
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the accept loop ignored net.ErrClosed; shutdown would never finish")
	}
}

// TestControlAcceptBacksOffOnRepeatedErrors: a listener erroring every time must
// not become a hot spin that buries the log and pins a core. The retry is capped
// backoff, so a hard failure costs about one line a second.
func TestControlAcceptBacksOffOnRepeatedErrors(t *testing.T) {
	ln := newScriptedListener()
	g := testGateway(ln)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); g.acceptControl(ctx) }()

	start := time.Now()
	const rounds = 6
	for range rounds {
		<-ln.accepted
		ln.next <- syscall.ECONNABORTED
	}
	// 5ms doubling per round: rounds 1..5 already owe 5+10+20+40+80 = 155ms.
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("%d failing accepts took %v — the loop is spinning, not backing off", rounds, elapsed)
	}

	cancel()
	// Unblock the Accept the loop is parked in so it can notice the cancel.
	select {
	case ln.next <- syscall.ECONNABORTED:
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop stuck")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the accept loop ignored a cancelled context while backing off")
	}
}

// TestPreAuthGateBoundsUnauthenticatedConns is the resource half of the same
// bug. allow() only refuses a network *after* its failures land, and a stalled
// connection does not fail until preAuthTimeout — so for the first 10 s a peer
// opens as many connections as it likes, each costing a goroutine, a descriptor
// and a TLS handshake buffer. That is how Accept starts erroring in the first
// place. The gate bounds the unauthenticated set to a constant.
func TestPreAuthGateBoundsUnauthenticatedConns(t *testing.T) {
	g := newPreAuthGate(2)

	// Both calls must run (no short-circuit): the point is that two conns fit.
	first, second := g.admit(), g.admit()
	if !first || !second {
		t.Fatal("the gate refused conns inside its own cap")
	}
	if g.admit() {
		t.Error("the gate admitted past its cap: the unauthenticated set is unbounded again")
	}

	g.release()
	if !g.admit() {
		t.Error("a released slot was not reusable")
	}

	// Release is idempotent per connection: it runs on every failure path and
	// again when auth succeeds, and a slot freed twice would grow the cap.
	rel := g.releaser()
	if g.admit() {
		t.Fatal("gate should be full")
	}
	rel()
	rel()
	rel()
	if !g.admit() {
		t.Fatal("the released slot never came back")
	}
	if g.admit() {
		t.Error("a double release inflated the cap: the bound is not a bound")
	}
}

// TestAcceptErrorIsFatalOnlyWhenClosed pins the predicate the loops turn on.
// Both directions are load-bearing: calling a transient error fatal is the
// permanent outage above, and calling a closed listener transient is a shutdown
// that never finishes.
func TestAcceptErrorIsFatalOnlyWhenClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"closed listener stops the loop", net.ErrClosed, true},
		// Accept errors reach us wrapped in *net.OpError; matching on identity
		// rather than errors.Is would silently never fire.
		{"wrapped closed listener still stops it", &net.OpError{Op: "accept", Err: net.ErrClosed}, true},
		{"out of descriptors is transient", syscall.EMFILE, false},
		{"reset between SYN and accept is transient", syscall.ECONNABORTED, false},
		{"no error at all", nil, false},
	} {
		if got := isListenerClosed(tc.err); got != tc.want {
			t.Errorf("%s: isListenerClosed(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}
