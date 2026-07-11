package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"proxyforward/internal/config"
)

// TestStatsLifecycle runs a real gateway engine briefly and verifies the
// stats store samples and lands on disk at shutdown. The IPC pipe may be
// owned by another process on a dev machine; that only fails Run's error
// path, not the store lifecycle this test asserts on.
func TestStatsLifecycle(t *testing.T) {
	dir := t.TempDir()
	// Config validation requires a concrete port; borrow a free one.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := config.Default()
	cfg.Role = config.RoleGateway
	cfg.Gateway.BindAddr = "127.0.0.1"
	cfg.Gateway.ControlPort = port
	cfg.Gateway.Token = "test-token"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	eng, err := New(cfg, dir, filepath.Join(dir, "config.toml"), logger)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()

	// Long enough for the 100ms sampler to take several samples.
	time.Sleep(1200 * time.Millisecond)

	st := eng.Status()
	if st.ProcessStartMs == 0 {
		t.Error("Status.ProcessStartMs is zero")
	}
	if h := eng.History(15_000, 150); len(h.Buckets) == 0 {
		t.Error("sampler produced no history buckets")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("engine did not stop")
	}

	data, err := os.ReadFile(filepath.Join(dir, "stats.json"))
	if err != nil {
		t.Fatalf("stats.json not written at shutdown: %v", err)
	}
	var f struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal(data, &f); err != nil || f.V != 2 {
		t.Fatalf("stats.json malformed (v=%d, err=%v)", f.V, err)
	}
}
