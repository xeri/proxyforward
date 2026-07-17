package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"proxyforward/internal/config"
	"proxyforward/internal/ipc"
)

// A private pipe: parallel test binaries (and a live daemon) must never share
// the production name, or they deadlock each other (.claude/rules/go-tests.md).
func init() {
	ipc.PipeName = fmt.Sprintf(`\\.\pipe\proxyforward-enginetest-%d`, os.Getpid())
}

// newTestEngine builds a gateway engine on a free port with its role run
// replaced by roleRun. The real Gateway is constructed and never started —
// these tests are about the supervisor/run split, not about proxying.
func newTestEngine(t *testing.T, roleRun func(context.Context) error) *Engine {
	t.Helper()
	dir := t.TempDir()
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
	eng, err := New(cfg, dir, filepath.Join(dir, "config.toml"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	eng.roleRun = roleRun
	return eng
}

// startSupervisor runs eng.Run and returns once the pipe answers. Cleanup
// cancels and waits, so a leaked supervisor fails the test that made it.
func startSupervisor(t *testing.T, eng *Engine) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("supervisor did not stop after cancel")
		}
	})
	waitPipe(t, true)
}

// waitPipe blocks until the pipe's reachability matches want, or fails.
func waitPipe(t *testing.T, want bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := ipc.Dial(200 * time.Millisecond)
		if err == nil {
			c.Close()
		}
		if (err == nil) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pipe reachable = %v, want %v (last dial: %v)", err == nil, want, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitRunState polls until the run reaches want, then returns its error.
func waitRunState(t *testing.T, eng *Engine, want RunState) error {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := eng.RunState()
		if got == want {
			return err
		}
		if time.Now().After(deadline) {
			t.Fatalf("run state = %q, want %q", got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPipeSurvivesRunStop is the whole point of the supervisor/run split: a
// stopped tunnel must leave the pipe serving, because that is the channel the
// GUI needs in order to start it again.
func TestPipeSurvivesRunStop(t *testing.T) {
	eng := newTestEngine(t, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	startSupervisor(t, eng)

	if got, _ := eng.RunState(); got != RunRunning {
		t.Fatalf("run state after Run = %q, want %q", got, RunRunning)
	}
	if err := eng.StopRun(false); err != nil {
		t.Fatalf("StopRun: %v", err)
	}
	if got, runErr := eng.RunState(); got != RunStopped || runErr != nil {
		t.Errorf("run state after stop = %q/%v, want %q/nil", got, runErr, RunStopped)
	}
	// The assertion the split exists for.
	waitPipe(t, true)

	// And the run comes back on the same supervisor.
	if err := eng.StartRun(); err != nil {
		t.Fatalf("StartRun after stop: %v", err)
	}
	if got, _ := eng.RunState(); got != RunRunning {
		t.Errorf("run state after restart = %q, want %q", got, RunRunning)
	}
}

// TestPipeSurvivesFatalRun covers the bug the split fixes: the role run and
// ipc.Serve used to share one first-exit-wins error channel, so a fatal auth
// error killed the pipe alongside the agent — losing the GUI exactly when it
// needed to show "bad token" and let the user fix it.
func TestPipeSurvivesFatalRun(t *testing.T) {
	fatal := errors.New("bad_token")
	eng := newTestEngine(t, func(context.Context) error { return fatal })
	startSupervisor(t, eng)

	runErr := waitRunState(t, eng, RunStopped)
	if !errors.Is(runErr, fatal) {
		t.Errorf("RunState err = %v, want %v", runErr, fatal)
	}
	waitPipe(t, true)
}

// TestStopRunAlwaysCompletes: a run that ignores its cancellation must not
// hold the caller. Without force the wait is bounded and reports that the stop
// did not land cleanly; with force it does not wait at all. Either way the run
// is already cancelled — force buys promptness, not power.
func TestStopRunAlwaysCompletes(t *testing.T) {
	release := make(chan struct{})
	eng := newTestEngine(t, func(context.Context) error {
		<-release // deaf to ctx
		return nil
	})
	startSupervisor(t, eng)
	// Registered after startSupervisor so it runs before startSupervisor's
	// cleanup (t.Cleanup is LIFO): the supervisor's teardown waits on this run
	// unbounded, by design — the store it closes next is one the run still
	// writes to — so the wedge must be released before the cancel lands.
	t.Cleanup(func() { close(release) })

	start := time.Now()
	if err := eng.StopRun(true); err != nil {
		t.Errorf("forced StopRun: %v", err)
	}
	if el := time.Since(start); el > time.Second {
		t.Errorf("forced StopRun waited %v, want no wait", el)
	}
	if got, _ := eng.RunState(); got != RunStopping {
		t.Errorf("run state after forced stop = %q, want %q (the run is still unwinding)", got, RunStopping)
	}
	// A run still unwinding cannot hand its ports to a new one.
	if err := eng.StartRun(); err == nil {
		t.Error("StartRun during stop succeeded, want refusal")
	}
}

// TestStopRunReportsAnUncleanStop is TestStopRunAlwaysCompletes' other half:
// the bounded wait must expire and say so rather than hang.
func TestStopRunReportsAnUncleanStop(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out stopGrace")
	}
	release := make(chan struct{})
	eng := newTestEngine(t, func(context.Context) error {
		<-release // deaf to ctx
		return nil
	})
	startSupervisor(t, eng)
	t.Cleanup(func() { close(release) }) // see TestStopRunAlwaysCompletes

	start := time.Now()
	err := eng.StopRun(false)
	if err == nil {
		t.Fatal("StopRun on a wedged run returned nil, want an unclean-stop report")
	}
	if el := time.Since(start); el < stopGrace {
		t.Errorf("StopRun returned after %v, want it to wait out stopGrace (%v)", el, stopGrace)
	}
}

// TestStoreSurvivesStopStartCycle: the analytics store is supervisor-scoped, so
// a stop/start cycle must not reopen it. Reopening was the race the sampler's
// old restart comment warned about.
func TestStoreSurvivesStopStartCycle(t *testing.T) {
	eng := newTestEngine(t, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	startSupervisor(t, eng)

	before := eng.DB
	if before == nil {
		t.Fatal("no analytics store opened")
	}
	if err := eng.StopRun(false); err != nil {
		t.Fatalf("StopRun: %v", err)
	}
	if err := eng.StartRun(); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if eng.DB != before {
		t.Error("analytics store was reopened across a stop/start cycle")
	}
	// Still usable, not merely the same pointer.
	if _, err := eng.DB.Summary(0, time.Now().UnixMilli()); err != nil {
		t.Errorf("store unusable after stop/start: %v", err)
	}
}
