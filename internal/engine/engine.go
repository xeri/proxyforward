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
			if e.Agent != nil {
				appIn, appOut = e.Agent.Conns.Totals()
				linkIn, linkOut = e.Agent.LinkTotalBytes()
				upSince = e.Agent.LinkUpSinceMs()
				conns = e.Agent.Conns.Count()
			} else {
				appIn, appOut = e.Gateway.Conns.Totals()
				linkIn, linkOut = e.Gateway.LinkTotalBytes()
				upSince = e.Gateway.AgentLinkUpSinceMs()
				conns = e.Gateway.Conns.Count()
			}
			e.Stats.Sample(now, appIn, appOut, linkIn, linkOut, conns)
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

// Status snapshots the engine for the IPC pipe and the GUI.
func (e *Engine) Status() ipc.Status {
	st := ipc.Status{
		Role:           string(e.cfg.Role),
		Version:        version.String(),
		PID:            os.Getpid(),
		ConfigPath:     e.configPath,
		ProcessStartMs: processStart.UnixMilli(),
	}
	life := e.Stats.Lifetime()
	st.AllTimeBytesIn, st.AllTimeBytesOut = life.BytesIn, life.BytesOut
	st.CumulativeUptimeMs = life.UptimeMs
	st.LinkSessions = life.LinkSessions
	switch {
	case e.Agent != nil:
		st.LinkUp = e.Agent.LinkUp()
		st.RTTMillis = e.Agent.RTTMillis()
		st.LinkUpSinceMs = e.Agent.LinkUpSinceMs()
		st.LinkBytesIn, st.LinkBytesOut = e.Agent.LinkSessionBytes()
		st.PeerAddr = net.JoinHostPort(e.cfg.Agent.GatewayHost, strconv.Itoa(e.cfg.Agent.GatewayPort))
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
