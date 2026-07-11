// Package engine composes one running proxyforward daemon: the role engine
// (agent or gateway) plus the IPC status pipe. Every long-lived mode — the
// headless CLI runs, the Windows service, and the GUI running in-process —
// hosts exactly one Engine, so a GUI can always attach to whichever process
// owns the ports.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"proxyforward/internal/agent"
	"proxyforward/internal/config"
	"proxyforward/internal/gateway"
	"proxyforward/internal/ipc"
	"proxyforward/internal/link"
	"proxyforward/internal/netid"
	"proxyforward/internal/stats"
	"proxyforward/internal/version"
)

// processStart anchors the "program uptime" stat; init time is close enough
// to process start for a status surface.
var processStart = time.Now()

const (
	// sampleInterval drives the bandwidth history's finest tier (100 ms
	// buckets); flushInterval bounds how much history a crash can lose.
	sampleInterval = 100 * time.Millisecond
	flushInterval  = 45 * time.Second
)

type Engine struct {
	cfg        *config.Config
	configDir  string
	configPath string
	logger     *slog.Logger

	// Exactly one of these is non-nil, per cfg.Role.
	Agent   *agent.Agent
	Gateway *gateway.Gateway

	// Stats is the persistent bandwidth-history and lifetime store. Only the
	// engine-owning process writes its file; attached GUIs read over IPC.
	Stats *stats.Store
}

// New constructs the role object for cfg.Role (which must be set and valid).
func New(cfg *config.Config, configDir, configPath string, logger *slog.Logger) (*Engine, error) {
	e := &Engine{cfg: cfg, configDir: configDir, configPath: configPath, logger: logger}
	switch cfg.Role {
	case config.RoleAgent:
		e.Agent = agent.New(cfg, logger)
	case config.RoleGateway:
		e.Gateway = gateway.New(cfg, configDir, logger)
	default:
		return nil, fmt.Errorf("no role configured")
	}
	e.Stats = stats.Open(filepath.Join(configDir, "stats.json"), logger)
	if e.Agent != nil {
		e.Agent.Conns.SetHooks(e.Stats.ConnOpened, e.Stats.ConnClosed)
	} else {
		e.Gateway.Conns.SetHooks(e.Stats.ConnOpened, e.Stats.ConnClosed)
	}
	return e, nil
}

// Run blocks until ctx is cancelled or either the role engine or the IPC
// pipe fails. A pipe conflict (another daemon running) is fatal by design:
// exactly one process may own ports and config.
func (e *Engine) Run(ctx context.Context) error {
	var engineRun func(context.Context) error
	if e.Agent != nil {
		engineRun = e.Agent.Run
	} else {
		engineRun = func(ctx context.Context) error {
			return gateway.RunStarted(ctx, e.Gateway, e.cfg, e.logger)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	go func() {
		err := ipc.Serve(runCtx, e.logger, ipc.Sources{Status: e.Status, History: e.History, Peers: e.Peers})
		if errors.Is(err, ipc.ErrUnsupported) {
			e.logger.Debug("ipc pipe unsupported on this platform")
			err = nil
		} else if err != nil && runCtx.Err() == nil {
			err = fmt.Errorf("ipc pipe failed (is another proxyforward daemon running?): %w", err)
		}
		errCh <- err
	}()
	go func() { errCh <- engineRun(runCtx) }()

	// The stats sampler lives exactly as long as Run: its final flush must
	// complete before Run returns, because a restarting GUI opens a new store
	// on the same file the moment Run is done.
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		e.runSampler(runCtx)
	}()

	// First non-nil error (or first exit) wins; stop the other side, drain.
	err := <-errCh
	cancel()
	if second := <-errCh; err == nil {
		err = second
	}
	<-samplerDone
	if ctx.Err() != nil {
		return nil // orderly shutdown
	}
	return err
}

// runSampler feeds the stats store: byte totals at 10 Hz, periodic flushes,
// link-session-start detection, and one final flush on shutdown.
func (e *Engine) runSampler(ctx context.Context) {
	sample := time.NewTicker(sampleInterval)
	defer sample.Stop()
	flush := time.NewTicker(flushInterval)
	defer flush.Stop()
	var prevUpSince int64
	for {
		select {
		case <-ctx.Done():
			if err := e.Stats.Flush(); err != nil {
				e.logger.Warn("stats: final flush failed", "err", err)
			}
			return
		case now := <-sample.C:
			var appIn, appOut, linkIn, linkOut, upSince int64
			var conns int
			// Both roles run a heartbeat and measure RTT; -1 means unknown
			// (no live link yet).
			rtt := float64(-1)
			if e.Agent != nil {
				appIn, appOut = e.Agent.Conns.Totals()
				linkIn, linkOut = e.Agent.LinkTotalBytes()
				upSince = e.Agent.LinkUpSinceMs()
				conns = e.Agent.Conns.Count()
				if r := e.Agent.RTTMillis(); r > 0 {
					rtt = float64(r)
				}
			} else {
				appIn, appOut = e.Gateway.Conns.Totals()
				linkIn, linkOut = e.Gateway.LinkTotalBytes()
				upSince = e.Gateway.AgentLinkUpSinceMs()
				conns = e.Gateway.Conns.Count()
				if r := e.Gateway.RTTMillis(); r > 0 {
					rtt = float64(r)
				}
			}
			e.Stats.Sample(now, appIn, appOut, linkIn, linkOut, conns, rtt)
			if upSince != 0 && upSince != prevUpSince {
				e.Stats.LinkSessionStarted()
			}
			prevUpSince = upSince
		case <-flush.C:
			if err := e.Stats.Flush(); err != nil {
				e.logger.Warn("stats: flush failed", "err", err)
			}
		}
	}
}

// History serves the bandwidth chart; windowMs 0 means everything.
func (e *Engine) History(windowMs int64, maxBuckets int) stats.HistoryResult {
	return e.Stats.History(windowMs, maxBuckets)
}

// Peers lists per-client lifetime records, most recent first.
func (e *Engine) Peers() []stats.PeerStat {
	return e.Stats.Peers()
}

// healthScore rolls jitter, packet loss, and link uptime into a single
// green/yellow/red verdict for the GUI's tunnel-health badge. Unknown metrics
// (-1) never push the score toward bad on their own; a brand-new link is only
// "warn" until it has been up for a minute.
func healthScore(linkUp bool, jitterMs, lossPct float64, upSinceMs int64) string {
	if !linkUp {
		return "bad"
	}
	var up time.Duration
	if upSinceMs > 0 {
		up = time.Since(time.UnixMilli(upSinceMs))
	}
	switch {
	case lossPct > 5 || jitterMs > 100:
		return "bad"
	case lossPct > 1 || jitterMs > 30 || up < time.Minute:
		return "warn"
	default:
		return "good"
	}
}

// Status snapshots the engine for the IPC pipe and the GUI.
func (e *Engine) Status() ipc.Status {
	localHost, _ := os.Hostname()
	st := ipc.Status{
		Role:           string(e.cfg.Role),
		Version:        version.String(),
		PID:            os.Getpid(),
		ConfigPath:     e.configPath,
		ProcessStartMs: processStart.UnixMilli(),
		LocalHostname:  localHost,
		LocalLANIPs:    netid.LocalIPv4s(),
		JitterMillis:   -1,
		PacketLossPct:  -1,
		HealthScore:    "unknown",
	}
	life := e.Stats.Lifetime()
	st.AllTimeBytesIn, st.AllTimeBytesOut = life.BytesIn, life.BytesOut
	st.CumulativeUptimeMs = life.UptimeMs
	st.LinkSessions = life.LinkSessions
	switch {
	case e.Agent != nil:
		st.LinkUp = e.Agent.LinkUp()
		st.RTTMillis = e.Agent.RTTMillis()
		st.JitterMillis = e.Agent.JitterMillis()
		st.PacketLossPct = e.Agent.PacketLossPct()
		st.LinkUpSinceMs = e.Agent.LinkUpSinceMs()
		st.LinkBytesIn, st.LinkBytesOut = e.Agent.LinkSessionBytes()
		st.PeerAddr = net.JoinHostPort(e.cfg.Agent.GatewayHost, strconv.Itoa(e.cfg.Agent.GatewayPort))
		st.PeerHostname = e.Agent.PeerHostname()
		st.PeerLANIPs = e.Agent.PeerLANIPs()
		st.PublicIP = e.Agent.ObservedIP()
		st.PeerPublicIP = e.cfg.Agent.GatewayHost
		st.HealthScore = healthScore(st.LinkUp, st.JitterMillis, st.PacketLossPct, st.LinkUpSinceMs)
		for _, t := range e.Agent.Tunnels() {
			ts := ipc.TunnelStatus{ID: t.ID, Name: t.Name}
			ts.PublicPort, _ = e.Agent.TunnelPublicPort(t.ID)
			ts.LocalUp, ts.LocalKnown = e.Agent.LocalUp(t.ID)
			st.Tunnels = append(st.Tunnels, ts)
		}
		st.Connections = e.Agent.Conns.Snapshot()
		st.TotalBytesIn, st.TotalBytesOut = e.Agent.Conns.Totals()
	case e.Gateway != nil:
		st.AgentConnected = e.Gateway.AgentConnected()
		st.LinkUpSinceMs = e.Gateway.AgentLinkUpSinceMs()
		st.LinkBytesIn, st.LinkBytesOut = e.Gateway.LinkSessionBytes()
		st.PeerAddr = e.Gateway.AgentRemoteIP()
		st.PeerHostname = e.Gateway.AgentHostname()
		st.PeerLANIPs = e.Gateway.AgentLANIPs()
		st.PublicIP = e.cfg.Gateway.PublicHost
		st.PeerPublicIP = e.Gateway.AgentRemoteIP()
		// The gateway runs its own heartbeat toward the agent, so it reports the
		// same link-quality stats. RTT/jitter/loss are only meaningful while an
		// agent is connected.
		if st.AgentConnected {
			st.RTTMillis = e.Gateway.RTTMillis()
			st.JitterMillis = e.Gateway.JitterMillis()
			st.PacketLossPct = e.Gateway.PacketLossPct()
		}
		st.HealthScore = healthScore(st.AgentConnected, st.JitterMillis, st.PacketLossPct, st.LinkUpSinceMs)
		for _, t := range e.Gateway.Tunnels() {
			st.Tunnels = append(st.Tunnels, ipc.TunnelStatus{
				ID: t.ID, Name: t.Name, PublicPort: t.PublicPort,
				LocalUp: t.LocalUp, LocalKnown: t.LocalKnown,
			})
		}
		st.Connections = e.Gateway.Conns.Snapshot()
		st.TotalBytesIn, st.TotalBytesOut = e.Gateway.Conns.Totals()
	}
	return st
}

// PairingCode builds the gateway's pairing code. Empty host falls back to
// the configured public host, then a placeholder.
func (e *Engine) PairingCode(host string) (string, error) {
	if e.Gateway == nil {
		return "", fmt.Errorf("pairing codes come from the gateway role")
	}
	if host == "" {
		host = e.cfg.Gateway.PublicHost
	}
	if host == "" {
		host = "YOUR-PUBLIC-ADDRESS"
	}
	addr := e.Gateway.ControlAddr()
	if addr == nil {
		return "", fmt.Errorf("gateway is still starting")
	}
	code := link.PairingCode{
		Host:        host,
		Port:        addr.(*net.TCPAddr).Port,
		Token:       e.cfg.Gateway.Token,
		Fingerprint: e.Gateway.Fingerprint(),
	}
	return code.String(), nil
}
