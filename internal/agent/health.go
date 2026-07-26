package agent

import (
	"context"
	"net"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/mc"
)

const (
	defaultHealthInterval    = 5 * time.Second
	defaultHealthDialTimeout = 3 * time.Second
	// defaultHealthMaxFailed is how many consecutive failures flip a tunnel down
	// when the operator has not tuned it: one, the behaviour that shipped. Raising
	// it debounces a backend that stutters; it never delays recovery, because a
	// single success always flips the tunnel back up and clears the counter.
	defaultHealthMaxFailed = 1
	// healthTickResolution is the checker's coarsest wake-up. One goroutine serves
	// every tunnel, so the loop wakes at this granularity and probes whichever
	// tunnels are due — rather than one ticker per tunnel. A configured interval
	// shorter than this pulls the tick down with it.
	healthTickResolution = time.Second
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

// ServerStatus reports what the local Minecraft server last told us about
// itself, or nil when this tunnel does not use the mc-status probe, has not
// been probed yet, or is currently down. Only ever the last *successful* ping —
// never a remembered figure from before an outage, which would read as current.
func (a *Agent) ServerStatus(tunnelID string) *mc.StatusInfo {
	v, ok := a.lastStatus.Load(tunnelID)
	if !ok {
		return nil
	}
	return v.(*mc.StatusInfo)
}

// healthState is one tunnel's probe bookkeeping, owned entirely by the checker
// goroutine. failures counts CONSECUTIVE failures and is cleared by any success
// — frp's equivalent never resets it, which quietly degrades its max-failed
// setting to 1 after the backend's first ever blip.
type healthState struct {
	failures  int
	lastProbe time.Time
}

// runHealthChecker probes every enabled tunnel's local address for the agent's
// lifetime, each on its own configured interval. State transitions are logged,
// remembered for the GUI, and pushed to the gateway when a session is up
// (feeding the offline responder and group eligibility).
//
// One goroutine, not one per tunnel: probes are sequential, so a slow backend
// delays its peers by at most its own timeout — which is bounded, and which the
// alternative (a goroutine and a timer each) buys at the cost of a lifecycle to
// get wrong on every config hot-apply.
func (a *Agent) runHealthChecker(ctx context.Context) {
	states := map[string]*healthState{}
	for {
		tunnels := a.snapshotTunnels()
		a.probeTunnels(ctx, tunnels, states)
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.tickFor(tunnels)):
		}
	}
}

// tickFor is how long the loop sleeps: the coarse resolution, pulled down by any
// tunnel that asked to be probed more often than that.
func (a *Agent) tickFor(tunnels []config.Tunnel) time.Duration {
	tick := healthTickResolution
	for _, t := range tunnels {
		tick = min(tick, a.intervalFor(t))
	}
	return max(tick, time.Millisecond)
}

// intervalFor / timeoutFor / maxFailedFor resolve one tunnel's tuning: its own
// option, else the agent-wide value (which tests override), else the default.
func (a *Agent) intervalFor(t config.Tunnel) time.Duration {
	if t.Options.HealthIntervalSec > 0 {
		return time.Duration(t.Options.HealthIntervalSec) * time.Second
	}
	if a.healthInterval > 0 {
		return a.healthInterval
	}
	return defaultHealthInterval
}

func (a *Agent) timeoutFor(t config.Tunnel) time.Duration {
	if t.Options.HealthTimeoutSec > 0 {
		return time.Duration(t.Options.HealthTimeoutSec) * time.Second
	}
	if a.healthDialTimeout > 0 {
		return a.healthDialTimeout
	}
	return defaultHealthDialTimeout
}

func (a *Agent) maxFailedFor(t config.Tunnel) int {
	if t.Options.HealthMaxFailed > 0 {
		return t.Options.HealthMaxFailed
	}
	return defaultHealthMaxFailed
}

// probeTunnels probes every tunnel whose interval has elapsed and retires the
// bookkeeping of any that left the config.
func (a *Agent) probeTunnels(ctx context.Context, tunnels []config.Tunnel, states map[string]*healthState) {
	live := make(map[string]bool, len(tunnels))
	for _, t := range tunnels {
		if ctx.Err() != nil {
			return
		}
		live[t.ID] = true
		if t.Type == config.TunnelUDP {
			// A UDP local address cannot be probed. There is no handshake, so
			// net.Dial("udp") connects to a dead port just as happily as a live
			// one and would report every UDP tunnel healthy; a real check would
			// have to speak the server's protocol, which "any UDP tunnel" rules
			// out. So report nothing instead of a verdict we cannot support —
			// unknown health counts as up (backendDown), which keeps a working
			// server serving, and the GUI shows "unknown" rather than a
			// convincing lie. Any stale entry from when this tunnel was TCP is
			// dropped so it cannot outlive the type change.
			a.localUp.Delete(t.ID)
			a.lastStatus.Delete(t.ID)
			delete(states, t.ID)
			continue
		}
		st := states[t.ID]
		if st == nil {
			st = &healthState{}
			states[t.ID] = st
		}
		now := time.Now()
		if !st.lastProbe.IsZero() && now.Sub(st.lastProbe) < a.intervalFor(t) {
			continue
		}
		st.lastProbe = now
		a.recordProbe(t, st, a.probeBackend(ctx, t))
	}
	for id := range states {
		if !live[id] {
			delete(states, id)
		}
	}
}

// probeBackend runs one tunnel's configured probe, returning what the backend
// said about itself or nil for "not answering". The status ping is the only
// probe with anything to report; a bare dial learns nothing beyond "it
// accepted", which is exactly why it cannot tell a live server from a hung one.
func (a *Agent) probeBackend(ctx context.Context, t config.Tunnel) *mc.StatusInfo {
	timeout := a.timeoutFor(t)
	if t.Options.HealthProbe == config.HealthProbeMCStatus {
		info, err := mc.StatusPing(ctx, t.LocalAddr, timeout)
		if err != nil {
			a.logger.Debug("status ping failed", "tunnel", t.Name, "local_addr", t.LocalAddr, "err", err)
			return nil
		}
		return info
	}
	if probeOnce(ctx, t.LocalAddr, timeout) {
		return &mc.StatusInfo{PlayersOnline: -1, PlayersMax: -1}
	}
	return nil
}

// recordProbe folds one probe result into the tunnel's state: a success clears
// the failure counter and flips the tunnel up immediately; a failure flips it
// down only once maxFailed consecutive probes have failed. Notifications fire on
// transitions only, so a steady state costs nothing on the wire.
func (a *Agent) recordProbe(t config.Tunnel, st *healthState, info *mc.StatusInfo) {
	up := info != nil
	if up {
		st.failures = 0
		if t.Options.HealthProbe == config.HealthProbeMCStatus {
			a.lastStatus.Store(t.ID, info)
		}
	} else {
		st.failures++
		if st.failures < a.maxFailedFor(t) {
			return // inside the debounce window; the tunnel stays as it was
		}
		// Nothing is known about a server that is not answering.
		a.lastStatus.Delete(t.ID)
	}
	prev, known := a.LocalUp(t.ID)
	if known && prev == up {
		return
	}
	a.localUp.Store(t.ID, up)
	if up {
		a.logger.Info("local server is up", "tunnel", t.Name, "local_addr", t.LocalAddr)
	} else {
		a.logger.Warn("local server is down", "tunnel", t.Name, "local_addr", t.LocalAddr, "consecutive_failures", st.failures)
	}
	if sink := a.healthSink.Load(); sink != nil {
		sink.notify(t.ID, up)
	}
	if obs := a.healthObserver.Load(); obs != nil {
		obs.notify(t.ID, up)
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
