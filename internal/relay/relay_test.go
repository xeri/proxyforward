package relay

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// tcpPair returns two connected TCP conns on loopback.
func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ch := make(chan *net.TCPConn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		ch <- c.(*net.TCPConn)
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-ch
	return c.(*net.TCPConn), server
}

// TestSpliceFinalBytesSurviveHalfClose is the disconnect-message test: one
// side writes a payload and immediately closes; every byte must still arrive
// at the far end, followed by a clean EOF — not a reset.
func TestSpliceFinalBytesSurviveHalfClose(t *testing.T) {
	// Chain: client <-> (a1|a2 spliced with b1|b2) <-> server
	clientSide, a := tcpPair(t) // a is one leg of the splice
	b, serverSide := tcpPair(t) // b is the other leg
	done := make(chan error, 1)
	go func() { done <- Splice(a, b, nil) }()

	payload := []byte("Disconnected: server is restarting")
	if _, err := clientSide.Write(payload); err != nil {
		t.Fatal(err)
	}
	clientSide.CloseWrite() // FIN right behind the data

	serverSide.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(serverSide)
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mangled: got %q want %q", got, payload)
	}
	// Reverse direction still works after the forward half-close.
	reply := []byte("ack")
	if _, err := serverSide.Write(reply); err != nil {
		t.Fatal(err)
	}
	serverSide.CloseWrite()
	clientSide.SetReadDeadline(time.Now().Add(5 * time.Second))
	gotReply, err := io.ReadAll(clientSide)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(gotReply, reply) {
		t.Fatalf("reply mangled: got %q want %q", gotReply, reply)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("splice returned error on clean shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("splice did not finish after both directions closed")
	}
}

// TestSpliceBulkIntegrity pushes multiple MiB with a hash check both ways at
// once to catch buffer reuse bugs.
func TestSpliceBulkIntegrity(t *testing.T) {
	clientSide, a := tcpPair(t)
	b, serverSide := tcpPair(t)
	var counters Counters
	done := make(chan error, 1)
	go func() { done <- Splice(a, b, &counters) }()

	const size = 8 << 20
	up := make([]byte, size)
	down := make([]byte, size)
	rand.Read(up)
	rand.Read(down)

	var wg sync.WaitGroup
	var gotUp, gotDown []byte
	var upErr, downErr error
	wg.Add(4)
	go func() { defer wg.Done(); clientSide.Write(up); clientSide.CloseWrite() }()
	go func() { defer wg.Done(); serverSide.Write(down); serverSide.CloseWrite() }()
	go func() { defer wg.Done(); gotUp, upErr = io.ReadAll(serverSide) }()
	go func() { defer wg.Done(); gotDown, downErr = io.ReadAll(clientSide) }()
	wg.Wait()

	if upErr != nil || downErr != nil {
		t.Fatalf("reads failed: up=%v down=%v", upErr, downErr)
	}
	if !bytes.Equal(gotUp, up) {
		t.Fatal("upstream payload corrupted in transit")
	}
	if !bytes.Equal(gotDown, down) {
		t.Fatal("downstream payload corrupted in transit")
	}
	if err := <-done; err != nil {
		t.Fatalf("splice error: %v", err)
	}
	if counters.AToB.Load() != size || counters.BToA.Load() != size {
		t.Fatalf("counters wrong: a->b=%d b->a=%d want %d each", counters.AToB.Load(), counters.BToA.Load(), size)
	}
}

// TestSpliceAbruptClientDeath: killing one end must unblock and finish the
// splice promptly, not leak it.
func TestSpliceAbruptClientDeath(t *testing.T) {
	clientSide, a := tcpPair(t)
	b, serverSide := tcpPair(t)
	done := make(chan error, 1)
	go func() { done <- Splice(a, b, nil) }()

	// Server writes steadily; client dies mid-stream with unread data (RST).
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := serverSide.Write(buf); err != nil {
				return
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	clientSide.SetLinger(0) // force RST so we exercise the ugly path
	clientSide.Close()

	select {
	case <-done:
		// Any error is acceptable here (reset is expected to be filtered,
		// but a raced closed-conn error may surface); the point is it ends.
	case <-time.After(5 * time.Second):
		t.Fatal("splice leaked after abrupt client death")
	}
	serverSide.Close()
}
