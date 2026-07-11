//go:build windows

package ipc

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"proxyforward/internal/stats"
)

func TestPipeStatusRoundTrip(t *testing.T) {
	want := Status{
		Role: "agent", Version: "test", PID: 1234,
		LinkUp: true, RTTMillis: 42, LinkUpSinceMs: 1700000000000, PeerAddr: "gw.example:8474",
		Tunnels: []TunnelStatus{{ID: "t1", Name: "mc", PublicPort: 25565, LocalUp: true, LocalKnown: true}},
	}
	history := stats.HistoryResult{WindowMs: 60_000, BucketMs: 200, Buckets: []stats.Bucket{
		{T: 1700000000000, In: 100, Out: 5000, OutH: 50_000, OutC: 48_000, ConnO: 3, ConnH: 7, ConnL: 2, ConnC: 4},
	}}
	peers := []stats.PeerStat{{IP: "203.0.113.44", FirstSeen: 1, LastSeen: 2, TotalBytesIn: 3, TotalBytesOut: 4, TotalConns: 5}}

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, slog.New(slog.DiscardHandler), Sources{
			Status:  func() Status { return want },
			History: func(windowMs int64, maxBuckets int) stats.HistoryResult { return history },
			Peers:   func() []stats.PeerStat { return peers },
		})
	}()

	// The pipe appears asynchronously; retry the dial briefly.
	var (
		c   *Client
		err error
	)
	for range 100 {
		c, err = Dial(200 * time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("dial pipe: %v", err)
	}
	defer c.Close()

	if err := c.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	got, err := c.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.Role != want.Role || got.PID != want.PID || !got.LinkUp || got.RTTMillis != 42 {
		t.Fatalf("status mismatch: %+v", got)
	}
	if got.LinkUpSinceMs != want.LinkUpSinceMs || got.PeerAddr != want.PeerAddr {
		t.Fatalf("link fields mismatch: %+v", got)
	}
	if len(got.Tunnels) != 1 || got.Tunnels[0].PublicPort != 25565 || !got.Tunnels[0].LocalUp {
		t.Fatalf("tunnel status mismatch: %+v", got.Tunnels)
	}

	h, err := c.History(60_000, 300)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if h.BucketMs != 200 || len(h.Buckets) != 1 || h.Buckets[0].Out != 5000 || h.Buckets[0].OutH != 50_000 {
		t.Fatalf("history mismatch: %+v", h)
	}
	if h.Buckets[0].ConnH != 7 || h.Buckets[0].ConnC != 4 {
		t.Fatalf("conn gauge lost over IPC: %+v", h.Buckets[0])
	}
	p, err := c.Peers()
	if err != nil {
		t.Fatalf("peers: %v", err)
	}
	if len(p) != 1 || p[0].IP != "203.0.113.44" || p[0].TotalConns != 5 {
		t.Fatalf("peers mismatch: %+v", p)
	}

	// Shutdown: Serve must return promptly and leave no goroutines behind.
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop after cancel")
	}
}

func TestPipeSecondServerRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, slog.New(slog.DiscardHandler), Sources{Status: func() Status { return Status{} }})
	}()
	// Wait for the first server to own the pipe.
	var c *Client
	var err error
	for range 100 {
		c, err = Dial(200 * time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("first server never came up: %v", err)
	}

	// A second daemon must fail fast, not silently coexist.
	err = Serve(context.Background(), slog.New(slog.DiscardHandler), Sources{Status: func() Status { return Status{} }})
	if err == nil {
		t.Fatal("second Serve on the same pipe succeeded")
	}
	cancel()
	<-serveDone
}
