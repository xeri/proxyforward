//go:build windows

package ipc

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// TestServeHangsUpOnAParkedClient pins the one reason the server takes any
// interest in a live client connection.
//
// A connected GUI spends nearly all its time parked in a blocking read, waiting
// for its next reply. Nothing about cancelling the engine's context reaches that
// read: closing the pipe *listener* only aborts the pending connect, and
// serveConn's `for ctx.Err() == nil` is checked between requests, not during one.
// So unless shutdown closes the connection itself, Serve sits in wg.Wait until
// the client's read deadline expires — idleTimeout, two minutes — with the engine
// notionally stopped the whole time. Stopping the engine is not a background
// chore: a GUI restarting it, or a service stopping, waits exactly this long.
//
// TestPipeStatusRoundTrip appears to cover this and does not: it cancels
// immediately after its last round trip, so it usually catches the handler
// between requests, where the loop's ctx check retires it for free. The sleep
// below is the whole point — it puts the handler where a real client lives
// before the cancel lands.
func TestServeHangsUpOnAParkedClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, slog.New(slog.DiscardHandler), Sources{
			Status: func() Status { return Status{Role: "gateway"} },
		})
	}()

	var (
		c   *Client
		err error
	)
	for range 100 {
		if c, err = Dial(200 * time.Millisecond); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Status(); err != nil {
		t.Fatalf("status: %v", err)
	}

	// The reply is sent, so the handler is back in ReadMsg and staying there:
	// exactly where an attached GUI sits between polls.
	time.Sleep(500 * time.Millisecond)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return: a parked client's read was never unblocked, so every shutdown waits out the two-minute idle timeout")
	}
}
