package app

import (
	"testing"
	"time"

	"proxyforward/internal/config"
)

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

// TestRegenerateTokenRefusesBeforeStopping: both of its guards precede the
// stop, so a verb that cannot run does not cost the user their engine.
func TestRegenerateTokenRefusesBeforeStopping(t *testing.T) {
	a := newTestAppDir(t, testAgentConfig()) // agent role: cannot issue tokens
	done := wedgeEngine(t, a)
	defer close(done)

	if err := a.RegenerateToken(); err == nil {
		t.Fatal("RegenerateToken on an agent succeeded, want a refusal")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel == nil {
		t.Error("the refused verb stopped the engine anyway")
	}
	if a.mode != ModeEngine {
		t.Errorf("mode = %q after a refused verb, want %q", a.mode, ModeEngine)
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
