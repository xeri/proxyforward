package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"proxyforward/internal/config"
)

func newTestApp() *App {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New("config.toml", config.Default(), nil, logger)
}

// simulateEngineDeath puts the app in engine mode with a done channel that
// already carries the engine's terminal error — the state statusLocked sees
// when the in-process engine has exited on its own.
func simulateEngineDeath(a *App, err error) {
	done := make(chan error, 1)
	done <- err
	a.mu.Lock()
	a.mode = ModeEngine
	a.done = done
	a.cancel = func() {}
	a.mu.Unlock()
}

func TestEngineFatalPersistsAcrossTicks(t *testing.T) {
	a := newTestApp()
	simulateEngineDeath(a, errors.New("boom"))

	if st := a.Status(); st.EngineFatal != "boom" {
		t.Fatalf("first tick EngineFatal = %q, want \"boom\"", st.EngineFatal)
	}
	// The error must survive the tick that drained a.done.
	if st := a.Status(); st.EngineFatal != "boom" {
		t.Fatalf("second tick EngineFatal = %q, want \"boom\" (error must persist until restart)", st.EngineFatal)
	}
}

func TestStopAfterEngineDeathDoesNotBlock(t *testing.T) {
	a := newTestApp()
	simulateEngineDeath(a, errors.New("boom"))
	a.Status() // drains a.done, leaving it nil

	start := time.Now()
	a.Shutdown(context.Background())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Shutdown took %v after engine death; must not wait on the drained done channel", elapsed)
	}
}
