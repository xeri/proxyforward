package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/engine"
)

// waitStatus polls a.Status() until pred holds, then returns that snapshot.
func waitStatus(t *testing.T, a *App, pred func(UIStatus) bool) UIStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := a.Status()
		if pred(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never satisfied predicate (last: mode %q runState %q)", st.Mode, st.RunState)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// wedgeEngine hands a an engine that never finishes unwinding, and returns the
// done channel that releases it. It fakes the handles rather than starting a
// real engine because the point under test is the lock discipline around the
// wait, not what is waited on.
func wedgeEngine(t *testing.T, a *App) chan error {
	t.Helper()
	done := make(chan error)
	a.mu.Lock()
	a.mode, a.cancel, a.done = ModeEngine, func() {}, done
	a.mu.Unlock()
	return done
}

// TestStopEngineKeepsTheConfiguredMode is the regression the whole run-state
// axis exists for. Stopping used to mean tearing the engine down, which set
// mode back to wizard — and the frontend turns wizard into the entire
// first-run shell. So an off switch built the obvious way would have answered
// "stop my tunnel for a minute" by offering to set the machine up from
// scratch. Stopped is a second axis, not a fourth mode.
func TestStopEngineKeepsTheConfiguredMode(t *testing.T) {
	a := newTestAppDir(t, testAgentConfig())
	// StartEngine rather than Startup: Startup also spawns the tick loop, and
	// its EventsEmit needs the context Wails hands the lifecycle hooks.
	if err := a.StartEngine(); err != nil {
		t.Fatalf("StartEngine: %v", err)
	}
	defer a.Shutdown(context.Background())

	if st := a.Status(); st.Mode != ModeEngine || st.RunState != string(engine.RunRunning) {
		t.Fatalf("after startup: mode %q / runState %q, want %q / %q",
			st.Mode, st.RunState, ModeEngine, engine.RunRunning)
	}
	if err := a.StopEngine(false); err != nil {
		t.Fatalf("StopEngine: %v", err)
	}

	st := a.Status()
	if st.Mode != ModeEngine {
		t.Errorf("mode after stop = %q, want %q — the user is in the wizard", st.Mode, ModeEngine)
	}
	if st.Role == "" {
		t.Error("role empty after stop: the frontend reads that as first-run setup")
	}
	if st.RunState != string(engine.RunStopped) {
		t.Errorf("runState after stop = %q, want %q", st.RunState, engine.RunStopped)
	}
	// Deliberately off is not a crash, and must not paint like one.
	if st.EngineFatal != "" {
		t.Errorf("EngineFatal = %q after a deliberate stop, want empty", st.EngineFatal)
	}

	if err := a.StartEngine(); err != nil {
		t.Fatalf("StartEngine: %v", err)
	}
	if st := a.Status(); st.RunState != string(engine.RunRunning) {
		t.Errorf("runState after start = %q, want %q", st.RunState, engine.RunRunning)
	}
}

// TestStopPersistsAcrossRelaunch: a deliberate stop must survive restart and
// reboot. The app writes [engine] stopped, and a fresh launch reading that
// config brings the supervisor up but holds the run down — a stopped service
// still answers its pipe, it just proxies nothing.
func TestStopPersistsAcrossRelaunch(t *testing.T) {
	cfgPath := t.TempDir() + "/config.toml"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// First launch: configured, running, then stopped.
	a1 := New(cfgPath, testAgentConfig(), nil, logger)
	if err := a1.StartEngine(); err != nil {
		t.Fatalf("StartEngine: %v", err)
	}
	if err := a1.StopEngine(false); err != nil {
		t.Fatalf("StopEngine: %v", err)
	}
	a1.Shutdown(context.Background())

	// The intent is on disk.
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if !reloaded.Engine.Stopped {
		t.Fatal("[engine] stopped was not persisted")
	}

	// Second launch reads it: supervisor up (mode engine), run held down.
	a2 := New(cfgPath, reloaded, nil, logger)
	a2.mu.Lock()
	a2.startEngineLocked()
	a2.mu.Unlock()
	defer a2.Shutdown(context.Background())

	st := waitStatus(t, a2, func(s UIStatus) bool { return s.RunState != "" })
	if st.Mode != ModeEngine {
		t.Errorf("mode = %q, want %q — a stopped daemon still owns its role", st.Mode, ModeEngine)
	}
	if st.RunState != string(engine.RunStopped) {
		t.Errorf("runState = %q, want %q — the persisted stop was not honored", st.RunState, engine.RunStopped)
	}

	// And Start clears it again.
	if err := a2.StartEngine(); err != nil {
		t.Fatalf("StartEngine after relaunch: %v", err)
	}
	if reloaded2, err := config.Load(cfgPath); err != nil {
		t.Fatalf("reload: %v", err)
	} else if reloaded2.Engine.Stopped {
		t.Error("StartEngine did not clear the persisted stop")
	}
}

// TestStartEngineFromWizardBuildsADaemon: with no daemon live, Start is a
// first start rather than a resume, and must say so when there is no role.
func TestStartEngineFromWizardBuildsADaemon(t *testing.T) {
	a := newTestAppDir(t, config.Default()) // no role
	if err := a.StartEngine(); err == nil {
		t.Fatal("StartEngine with no role configured succeeded, want a refusal")
	}

	a2 := newTestAppDir(t, testAgentConfig())
	defer a2.Shutdown(context.Background())
	if err := a2.StartEngine(); err != nil {
		t.Fatalf("StartEngine on a configured app with no daemon: %v", err)
	}
	if st := a2.Status(); st.Mode != ModeEngine || st.RunState != string(engine.RunRunning) {
		t.Errorf("mode %q / runState %q, want %q / %q", st.Mode, st.RunState, ModeEngine, engine.RunRunning)
	}
}

// TestStopEngineDoesNotHoldTheAppLock: the wait for a stopping engine used to
// run under a.mu, so a shutdown that took its full ten seconds froze the 2 Hz
// tick and every binding behind it for the whole duration — the GUI went
// unresponsive exactly when it had something to report.
func TestStopEngineDoesNotHoldTheAppLock(t *testing.T) {
	a := newTestAppDir(t, testAgentConfig())
	done := wedgeEngine(t, a)

	stopped := make(chan error, 1)
	go func() {
		a.engMu.Lock()
		defer a.engMu.Unlock()
		stopped <- a.stopEngine()
	}()

	// A status tick must still get the lock while that wait is in flight.
	ticked := make(chan struct{})
	go func() {
		defer close(ticked)
		a.Status()
	}()
	select {
	case <-ticked:
	case <-time.After(5 * time.Second):
		t.Fatal("a.mu was held across the engine-stop wait: the GUI is frozen")
	}

	// Releasing the wedge lets the stop report a clean stop.
	close(done)
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("stopEngine on a released engine: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stopEngine did not return after the engine unwound")
	}
}

// TestStopEngineReportsAnUncleanStop is the other half: the bounded wait must
// expire and say so. Callers act on that — importSetupFromPath refuses to
// write over a store the old engine may still hold.
func TestStopEngineReportsAnUncleanStop(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out engineStopGrace")
	}
	a := newTestAppDir(t, testAgentConfig())
	wedgeEngine(t, a)

	a.engMu.Lock()
	defer a.engMu.Unlock()
	start := time.Now()
	err := a.stopEngine()
	if err == nil {
		t.Fatal("stopEngine on a wedged engine returned nil, want an unclean-stop report")
	}
	if el := time.Since(start); el < engineStopGrace {
		t.Errorf("stopEngine returned after %v, want it to wait out engineStopGrace (%v)", el, engineStopGrace)
	}
}

// TestImportRefusesOverAnUncleanStop: the imported files must not land while
// the old engine may still hold the store — a late stats flush would clobber
// exactly what was just imported (Windows file locking makes it worse).
func TestImportRefusesOverAnUncleanStop(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out engineStopGrace")
	}
	src := newTestAppDir(t, testAgentConfig())
	exported := t.TempDir() + "/backup.pfsetup"
	if err := src.exportSetupToPath(exported, "swordfish"); err != nil {
		t.Fatal(err)
	}

	dst := newTestAppDir(t, config.Default())
	done := wedgeEngine(t, dst)
	defer close(done)

	err := dst.importSetupFromPath(exported, "swordfish")
	if err == nil {
		t.Fatal("import over a wedged engine succeeded, want a refusal")
	}
	dst.mu.Lock()
	defer dst.mu.Unlock()
	if dst.cfg.Role == config.RoleAgent {
		t.Error("the refused import applied the config anyway")
	}
}
