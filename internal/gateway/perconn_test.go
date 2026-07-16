package gateway

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeConn is a net.Conn stand-in that only records Close; the pending-handoff
// logic touches nothing else. Other net.Conn methods promote from the nil
// embedded interface and would panic if called — they never are here.
type fakeConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *fakeConn) Close() error { c.closed.Store(true); return nil }

// TestPendingConnDeliverThenTake: the normal path — deliver hands the conn to a
// waiting take, and the conn is not closed (handleClient owns it).
func TestPendingConnDeliverThenTake(t *testing.T) {
	pc := &pendingConn{ready: make(chan struct{})}
	c := &fakeConn{}
	if !pc.deliver(c) {
		t.Fatal("deliver to a fresh pending entry must succeed")
	}
	got := pc.take(context.Background(), time.Second)
	if got != net.Conn(c) {
		t.Fatalf("take returned %v, want the delivered conn", got)
	}
	if c.closed.Load() {
		t.Fatal("a delivered conn must stay open — handleClient owns it")
	}
}

// TestPendingConnTakeTimeoutThenDeliver: the waiter gives up first, so a later
// deliver must lose (return false) — the loser-closes contract that tells
// handleDataConn to close the conn instead of leaking it.
func TestPendingConnTakeTimeoutThenDeliver(t *testing.T) {
	pc := &pendingConn{ready: make(chan struct{})}
	if got := pc.take(context.Background(), 5*time.Millisecond); got != nil {
		t.Fatalf("take returned %v, want nil on timeout", got)
	}
	if pc.deliver(&fakeConn{}) {
		t.Fatal("deliver after the waiter gave up must return false (loser closes)")
	}
}

// TestPendingConnTakeCtxCancel: eviction (ctx cancel) unblocks a parked take,
// and a subsequent deliver loses.
func TestPendingConnTakeCtxCancel(t *testing.T) {
	pc := &pendingConn{ready: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := pc.take(ctx, time.Second); got != nil {
		t.Fatalf("take returned %v, want nil after ctx cancel", got)
	}
	if pc.deliver(&fakeConn{}) {
		t.Fatal("deliver after an evicted waiter must return false")
	}
}

// TestPendingConnRaceExactlyOnce hammers deliver against a take that is timing
// out at the same moment. The invariant: take-got-a-conn iff deliver-succeeded.
// The two illegal outcomes are a leaked conn (take gave up but deliver claimed
// success, so nobody closes) and double ownership (take got a conn deliver said
// it never handed over).
func TestPendingConnRaceExactlyOnce(t *testing.T) {
	for i := 0; i < 3000; i++ {
		pc := &pendingConn{ready: make(chan struct{})}
		c := &fakeConn{}
		var delivered atomic.Bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			delivered.Store(pc.deliver(c))
		}()
		got := pc.take(context.Background(), time.Duration(i%3)*time.Microsecond)
		wg.Wait()

		switch {
		case got != nil && !delivered.Load():
			t.Fatalf("iter %d: take got a conn but deliver reported failure", i)
		case got == nil && delivered.Load():
			t.Fatalf("iter %d: take gave up but deliver succeeded — conn leaked", i)
		}
		// Consistent: whichever side lost, the conn has exactly one owner.
		// (In real code, take==nil ⇒ handleDataConn closes on deliver==false.)
		if got == nil {
			c.Close() // stand in for handleDataConn's loser-closes
		}
	}
}
