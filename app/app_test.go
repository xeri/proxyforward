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

// TestDeepLinkColdStartIntake covers the observable half of the deep-link path: a
// pxf:// link delivered before the window is up (ctx nil — a cold protocol launch)
// is stashed and then drained exactly once, so the frontend opens pairing on mount
// and never re-opens it on a later mount. The warm path emits a Wails event and
// needs a live runtime context, so it is exercised manually, not here.
func TestDeepLinkColdStartIntake(t *testing.T) {
	a := newTestApp()
	const link = "pxf://gw.example.com:8474/v1/pair/tok#sha256:abc"
	a.HandleDeepLink(link)
	if got := a.TakePendingDeepLink(); got != link {
		t.Fatalf("TakePendingDeepLink = %q, want the stashed link %q", got, link)
	}
	if got := a.TakePendingDeepLink(); got != "" {
		t.Fatalf("second TakePendingDeepLink = %q, want empty (drains once)", got)
	}
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
