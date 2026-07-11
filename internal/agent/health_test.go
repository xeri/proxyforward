package agent

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/config"
)

func waitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHealthCheckerTransitions(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	cfg := config.Default()
	cfg.Agent.Tunnels = []config.Tunnel{{
		ID: "t1", Name: "test", Type: config.TunnelTCP,
		LocalAddr: ln.Addr().String(), PublicPort: 25565, Enabled: true,
	}}
	a := New(cfg, slog.New(slog.DiscardHandler))
	a.healthInterval = 30 * time.Millisecond
	a.healthDialTimeout = 500 * time.Millisecond

	// Capture pushes the way a live session would.
	var mu sync.Mutex
	var pushes []bool
	a.setHealthSink(func(id string, up bool) {
		mu.Lock()
		defer mu.Unlock()
		pushes = append(pushes, up)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.runHealthChecker(ctx)
	}()
	defer func() { cancel(); <-done }()

	waitCond(t, "initial up state", func() bool {
		up, known := a.LocalUp("t1")
		return known && up
	})

	ln.Close() // backend goes away
	waitCond(t, "down transition", func() bool {
		up, known := a.LocalUp("t1")
		return known && !up
	})

	mu.Lock()
	defer mu.Unlock()
	if len(pushes) != 2 || pushes[0] != true || pushes[1] != false {
		t.Fatalf("pushes = %v, want [true false] (transitions only)", pushes)
	}
}
