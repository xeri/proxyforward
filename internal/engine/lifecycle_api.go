package engine

import (
	"encoding/json"

	"proxyforward/internal/config"
)

// Lifecycle op names — the stable wire contract for driving the tunnel run over
// the IPC pipe, so an attached GUI (and the CLI) can stop and start a daemon it
// does not host. Dispatched over the same JSON envelope as the analytics and
// agent-admin ops, and like the agent-admin ops they act on the live engine,
// never the analytics store, so they answer even when the DB failed to open.
const (
	OpStartRun = "start_run"
	OpStopRun  = "stop_run"
)

// isLifecycleOp reports whether op drives the tunnel run, so analyticsOp routes
// it before the analytics-store availability check.
func isLifecycleOp(op string) bool {
	return op == OpStartRun || op == OpStopRun
}

// StoppedPersister records the run's on/off intent so it survives restart and
// reboot. The app wires one that takes its own lock, because in a GUI-hosted
// daemon the engine and the app share one *config.Config and a bare write would
// race the 2 Hz status read. With none set — the service and headless runs,
// which host no app — the engine persists its own config directly.
type StoppedPersister func(stopped bool) error

// SetStoppedPersister installs the lock-taking persistence hook for the run's
// stopped flag. Mirrors SetConfigPersister: the app owns the config it shares
// with the engine, so the app owns the write.
func (e *Engine) SetStoppedPersister(fn StoppedPersister) { e.stoppedPersister.Store(&fn) }

// setStopped persists the run's on/off intent. Through the app's hook when one
// is set (lock-safe against the shared config), else straight to the engine's
// own config file — safe there because a hostless daemon shares that config
// with nobody.
func (e *Engine) setStopped(stopped bool) error {
	if p := e.stoppedPersister.Load(); p != nil {
		return (*p)(stopped)
	}
	e.cfgWrite(func(c *config.Config) { c.Engine.Stopped = stopped })
	return e.saveConfig()
}

// lifecycleOp drives the tunnel run for a start_run / stop_run request and
// returns the resulting run state. It never blocks on a clean unwind: the IPC
// request/response cycle is bounded well below StopRun's grace period, so stop
// is always prompt here and the GUI watches runState settle over the tick. The
// in-process "wait and report an unclean stop" path stays in the app, where the
// caller can afford to wait.
func (e *Engine) lifecycleOp(op string) (json.RawMessage, error) {
	switch op {
	case OpStopRun:
		// Persist the intent before the stop, exactly as the in-process verb
		// does: a crash mid-drain must still relaunch stopped.
		if err := e.setStopped(true); err != nil {
			return nil, err
		}
		_ = e.StopRun(true) // prompt; force skips the wait we cannot afford over IPC
	case OpStartRun:
		if err := e.setStopped(false); err != nil {
			return nil, err
		}
		if err := e.StartRun(); err != nil {
			return nil, err
		}
	}
	state, _ := e.RunState()
	return encodeResult(struct {
		RunState string `json:"runState"`
	}{string(state)}, nil)
}
