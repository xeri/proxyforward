//go:build windows

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"proxyforward/internal/stats"
)

// A private pipe keeps this binary from colliding with parallel test
// packages that serve IPC (engine, app, e2e) or a live daemon.
func init() {
	PipeName = fmt.Sprintf(`\\.\pipe\proxyforward-ipctest-%d`, os.Getpid())
}

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

func TestPipeAnalyticsRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, slog.New(slog.DiscardHandler), Sources{
			Status: func() Status { return Status{} },
			Analytics: func(req AnalyticsReq) AnalyticsResp {
				switch req.Op {
				case "players":
					// Echo the request body back inside a result envelope so the
					// test can verify both directions survive the frame.
					return AnalyticsResp{Body: json.RawMessage(`{"total":1,"echo":` + string(req.Body) + `}`)}
				default:
					return AnalyticsResp{Err: "unknown analytics op " + req.Op}
				}
			},
		})
	}()
	defer func() { cancel(); <-serveDone }()

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
		t.Fatalf("dial pipe: %v", err)
	}
	defer c.Close()

	body, err := c.Analytics("players", json.RawMessage(`{"limit":80,"search":"steve"}`))
	if err != nil {
		t.Fatalf("analytics op: %v", err)
	}
	var got struct {
		Total int `json:"total"`
		Echo  struct {
			Limit  int    `json:"limit"`
			Search string `json:"search"`
		} `json:"echo"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode analytics body %s: %v", body, err)
	}
	if got.Total != 1 || got.Echo.Limit != 80 || got.Echo.Search != "steve" {
		t.Fatalf("analytics round trip mangled the payload: %s", body)
	}

	// A daemon-side error comes back as a client error, not a payload.
	if _, err := c.Analytics("bogus", nil); err == nil {
		t.Fatal("unknown op did not error")
	}

	// The connection survives an error reply and keeps serving.
	if _, err := c.Analytics("players", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("op after error reply: %v", err)
	}
}

// TestPipeServedErrorTyping (T4b): a served analytics error must surface as
// *OpError — the transient kind that never latches — while a dead pipe
// yields a plain transport error.
func TestPipeServedErrorTyping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, slog.New(slog.DiscardHandler), Sources{
			Status:    func() Status { return Status{} },
			Analytics: func(req AnalyticsReq) AnalyticsResp { return AnalyticsResp{Err: "boom"} },
		})
	}()

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

	_, err = c.Analytics("players", nil)
	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Op != "players" || opErr.Msg != "boom" {
		t.Fatalf("served error = %v (%T), want *OpError{players, boom}", err, err)
	}

	// Kill the daemon: the same call must now fail as a plain transport
	// error, NOT an OpError.
	cancel()
	<-serveDone
	_, err = c.Analytics("players", nil)
	if err == nil {
		t.Fatal("call against dead server did not error")
	}
	if errors.As(err, &opErr) {
		t.Fatalf("transport failure came back typed as OpError: %v", err)
	}
}

// TestPipeOversizeReplySubstituted (T4a): a reply that would exceed the
// 64 KiB frame is replaced with a served error — an OpError on the client —
// and the connection stays healthy for the next request.
func TestPipeOversizeReplySubstituted(t *testing.T) {
	big := json.RawMessage(`"` + strings.Repeat("A", 100*1024) + `"`)
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, slog.New(slog.DiscardHandler), Sources{
			Status:    func() Status { return Status{} },
			Analytics: func(req AnalyticsReq) AnalyticsResp { return AnalyticsResp{Body: big} },
		})
	}()
	defer func() { cancel(); <-serveDone }()

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
		t.Fatalf("dial pipe: %v", err)
	}
	defer c.Close()

	_, err = c.Analytics("players", nil)
	var opErr *OpError
	if !errors.As(err, &opErr) || !strings.Contains(opErr.Msg, "frame") {
		t.Fatalf("oversize reply error = %v (%T), want frame-limit OpError", err, err)
	}
	// The pipe was never poisoned: the same connection keeps serving.
	if err := c.Ping(); err != nil {
		t.Fatalf("ping after oversize reply: %v", err)
	}
}

func TestPipeAnalyticsNilSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(ctx, slog.New(slog.DiscardHandler), Sources{Status: func() Status { return Status{} }})
	}()
	defer func() { cancel(); <-serveDone }()

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
		t.Fatalf("dial pipe: %v", err)
	}
	defer c.Close()

	// A daemon without an analytics store answers with an error rather than
	// hanging the client.
	if _, err := c.Analytics("players", nil); err == nil {
		t.Fatal("nil analytics source did not error")
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
