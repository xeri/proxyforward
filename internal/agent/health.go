package agent

import (
	"context"
	"net"
	"time"

	"proxyforward/internal/config"
)

const (
	defaultHealthInterval    = 5 * time.Second
	defaultHealthDialTimeout = 3 * time.Second
)

// healthSinkBox wraps the notify func for atomic.Pointer (which needs a
// concrete type).
type healthSinkBox struct {
	notify func(tunnelID string, up bool)
}

// setHealthSink installs (or, with nil, removes) the live session's health
// push. The checker calls it outside any session lifecycle, so the handoff
// is atomic.
func (a *Agent) setHealthSink(notify func(tunnelID string, up bool)) {
	if notify == nil {
		a.healthSink.Store(nil)
		return
	}
	a.healthSink.Store(&healthSinkBox{notify})
}

// SetHealthObserver installs (or, with nil, removes) a process-lifetime
// observer of tunnel-local health transitions. The engine uses it to record
// tunnel_local uptime events; it fires on every transition regardless of
// whether a gateway session is up, so it is independent of the session sink.
func (a *Agent) SetHealthObserver(notify func(tunnelID string, up bool)) {
	if notify == nil {
		a.healthObserver.Store(nil)
		return
	}
	a.healthObserver.Store(&healthSinkBox{notify})
}

// LocalUp reports the last observed health of a tunnel's local target.
// known is false until the first probe completes.
func (a *Agent) LocalUp(tunnelID string) (up, known bool) {
	v, ok := a.localUp.Load(tunnelID)
	if !ok {
		return false, false
	}
	return v.(bool), true
}

// runHealthChecker probes every enabled tunnel's local address for the
// agent's lifetime. State transitions are logged, remembered for the GUI,
// and pushed to the gateway when a session is up (feeding the offline
// responder).
func (a *Agent) runHealthChecker(ctx context.Context) {
	interval := a.healthInterval
	if interval <= 0 {
		interval = defaultHealthInterval
	}
	dialTimeout := a.healthDialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultHealthDialTimeout
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		a.probeTunnels(ctx, dialTimeout)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) probeTunnels(ctx context.Context, dialTimeout time.Duration) {
	for _, t := range a.snapshotTunnels() {
		if ctx.Err() != nil {
			return
		}
		up := probeOnce(ctx, t.LocalAddr, dialTimeout)
		prev, known := a.LocalUp(t.ID)
		if known && prev == up {
			continue
		}
		a.localUp.Store(t.ID, up)
		if up {
			a.logger.Info("local server is up", "tunnel", t.Name, "local_addr", t.LocalAddr)
		} else {
			a.logger.Warn("local server is down", "tunnel", t.Name, "local_addr", t.LocalAddr)
		}
		if sink := a.healthSink.Load(); sink != nil {
			sink.notify(t.ID, up)
		}
		if obs := a.healthObserver.Load(); obs != nil {
			obs.notify(t.ID, up)
		}
	}
}

// snapshotTunnels copies the enabled tunnels so probing never races config
// hot-apply.
func (a *Agent) snapshotTunnels() []config.Tunnel {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	var out []config.Tunnel
	for _, t := range a.cfg.Agent.Tunnels {
		if t.Enabled {
			out = append(out, t)
		}
	}
	return out
}

func probeOnce(ctx context.Context, addr string, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
