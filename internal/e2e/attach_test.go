package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"proxyforward/internal/analytics"
	"proxyforward/internal/config"
	"proxyforward/internal/engine"
	"proxyforward/internal/ipc"
)

// TestAttachedAnalytics runs a real engine (agent role, its own analytics
// store, a test-private pipe) and drives the analytics envelope over the pipe
// exactly like an attached GUI: summary decodes, players answers an empty
// page, an unknown op comes back as a typed served error. Cancelling the ctx
// must return Run promptly; the package's goleak TestMain then proves the
// writer, resolver, and refresh worker all drained.
func TestAttachedAnalytics(t *testing.T) {
	// A private pipe: parallel test binaries (and a live daemon) must never
	// share the production name.
	old := ipc.PipeName
	ipc.PipeName = fmt.Sprintf(`\\.\pipe\proxyforward-e2e-attach-%d`, os.Getpid())
	t.Cleanup(func() { ipc.PipeName = old })

	dir := t.TempDir()
	cfg := config.Default()
	cfg.Role = config.RoleAgent
	cfg.Agent.AgentID = config.NewID()
	cfg.Agent.Transport = config.TransportMux // this test is about attach, not the transport ladder
	cfg.Agent.GatewayHost = "127.0.0.1"
	cfg.Agent.GatewayPort = 1 // nothing listens; the agent just retries
	cfg.Agent.Token = config.NewToken()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(cfg, dir, filepath.Join(dir, "config.toml"), logger)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("engine did not stop after cancel")
		}
	}()

	// The pipe appears asynchronously; retry the dial briefly.
	var c *ipc.Client
	for range 200 {
		if c, err = ipc.Dial(200 * time.Millisecond); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial engine pipe: %v", err)
	}
	defer c.Close()

	// summary: a real Summary payload decodes over the wire.
	raw, err := c.Analytics(engine.OpSummary, json.RawMessage(`{"rangeMs":86400000}`))
	if err != nil {
		t.Fatalf("summary op: %v", err)
	}
	var sum analytics.Summary
	if err := json.Unmarshal(raw, &sum); err != nil {
		t.Fatalf("summary decode: %v (%s)", err, raw)
	}
	if sum.RangeMs != 86_400_000 {
		t.Fatalf("summary rangeMs = %d, want passthrough 86400000", sum.RangeMs)
	}

	// players: an empty page, not an error, on a fresh store.
	raw, err = c.Analytics(engine.OpPlayers, json.RawMessage(`{"limit":10}`))
	if err != nil {
		t.Fatalf("players op: %v", err)
	}
	var page analytics.PlayersPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("players decode: %v (%s)", err, raw)
	}
	if page.Total != 0 {
		t.Fatalf("fresh store players total = %d, want 0", page.Total)
	}

	// An unknown op is a served error — typed, never a torn pipe.
	_, err = c.Analytics("no_such_op", nil)
	var opErr *ipc.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("unknown op error = %v (%T), want *ipc.OpError", err, err)
	}
	// And the same connection keeps serving afterwards.
	if err := c.Ping(); err != nil {
		t.Fatalf("ping after served error: %v", err)
	}
}
