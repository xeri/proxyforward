// Package engine composes one running proxyforward daemon: the role engine
// (agent or gateway) plus the IPC status pipe. Every long-lived mode — the
// headless CLI runs, the Windows service, and the GUI running in-process —
// hosts exactly one Engine, so a GUI can always attach to whichever process
// owns the ports.
//
// An Engine has two nested lifetimes. Run is the supervisor: it owns the pipe,
// the analytics store, the identity resolver, and the metrics endpoint, and it
// lives as long as the process is configured for a role. Inside it, the
// tunnel run — the agent's dial loop or the gateway's listeners — starts and
// stops independently via StartRun/StopRun. The supervisor outliving the run
// is the point: a stopped or crashed tunnel must not take the pipe with it,
// because the GUI needs the pipe most when the tunnel is down — to show why,
// and to start it again.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"proxyforward/internal/agent"
	"proxyforward/internal/analytics"
	"proxyforward/internal/config"
	"proxyforward/internal/conntrack"
	"proxyforward/internal/gateway"
	"proxyforward/internal/geo"
	"proxyforward/internal/ipc"
	"proxyforward/internal/netid"
	"proxyforward/internal/players"
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
	// sessionSampleInterval is the per-connection traffic sample cadence for
	// the session-replay timeline.
	sessionSampleInterval = 15 * time.Second
	// stopGrace bounds how long StopRun waits for a cancelled run to unwind
	// before reporting that the stop did not land cleanly. It bounds the
	// *caller*, not the run: the run is already cancelled and keeps unwinding
	// either way, so a wedged link can delay the report but never withhold it.
	stopGrace = 10 * time.Second
)

// RunState is the tunnel run's lifecycle, which is a separate axis from the
// supervisor's: a stopped run leaves a live Engine still serving the pipe.
type RunState string

const (
	RunRunning  RunState = "running"
	RunStopping RunState = "stopping"
	RunStopped  RunState = "stopped"
)

var (
	// ErrNoSupervisor means there is no live Run to host a tunnel run — it was
	// never called, or it has returned. Only a fresh Engine fixes it, which is
	// what tells it apart from ErrRunStopping.
	ErrNoSupervisor = errors.New("the daemon is not running")
	// ErrRunStopping means a previous run is still unwinding. Rebuilding is
	// exactly the wrong answer: the ports a new run would bind are still held
	// by the old one. Wait, then start again.
	ErrRunStopping = errors.New("the engine is still stopping; wait for it to finish, then start it again")
)

type Engine struct {
	cfg        *config.Config
	configDir  string
	configPath string
	logger     *slog.Logger

	// Exactly one of these is non-nil, per cfg.Role.
	Agent   *agent.Agent
	Gateway *gateway.Gateway

	// roleRun runs whichever of the two is live, until its ctx dies or it
	// fails. Bound in New, where the role is already decided, so nothing later
	// has to re-derive it.
	roleRun func(context.Context) error

	// Stats is the persistent bandwidth-history and lifetime store. Only the
	// engine-owning process writes it; attached GUIs read over IPC.
	Stats *stats.Store

	// DB is the SQLite analytics store backing Stats and the session/player
	// history. nil when it could not be opened; Stats then runs memory-only
	// for this process's lifetime rather than blocking engine start.
	DB *analytics.DB

	// rec records connection/session history; nil when DB is nil.
	rec *analytics.Recorder

	// resolver turns sniffed usernames into stored identities; nil when DB is
	// nil.
	resolver *players.Resolver

	// geo enriches sessions with GeoLite2 country/network data. Always
	// non-nil; with no databases configured every lookup just misses.
	geo *geo.Resolver

	// runMu guards the tunnel run's lifecycle below. It is never held across a
	// wait: StopRun copies the cancel/done pair out and releases it before
	// blocking, so a wedged run cannot freeze a status read.
	runMu sync.Mutex
	// supCtx is the supervisor's context, set by Run. nil before Run starts,
	// which is what makes StartRun refuse: a run started outside the
	// supervisor's lifetime would have nothing to die with.
	supCtx    context.Context
	runCancel context.CancelFunc
	runDone   chan struct{}
	runState  RunState
	// runErr is why the run stopped on its own, or nil when it was asked to.
	runErr error

	// stoppedPersister persists the run's on/off intent; nil means the engine
	// writes its own config (a hostless service/headless run). See
	// SetStoppedPersister.
	stoppedPersister atomic.Pointer[StoppedPersister]
}

// persistEnrollment records the gateway-assigned identity in the config and
// clears the spent enrollment ticket, so config.toml is credential-free in steady
// state. The bare write is safe only because a hostless daemon shares its config
// with nobody; GUI-hosted runs substitute the app's locked persister.
func (e *Engine) persistEnrollment(assignedID string) error {
	e.cfg.Agent.AgentID = assignedID
	e.cfg.Agent.EnrollTicket = ""
	return e.cfg.Save(e.configPath)
}

// conns returns the active role's connection registry.
func (e *Engine) conns() *conntrack.Registry {
	if e.Agent != nil {
		return e.Agent.Conns
	}
	return e.Gateway.Conns
}

// New constructs the role object for cfg.Role (which must be set and valid).
func New(cfg *config.Config, configDir, configPath string, logger *slog.Logger) (*Engine, error) {
	e := &Engine{cfg: cfg, configDir: configDir, configPath: configPath, logger: logger, runState: RunStopped}
	switch cfg.Role {
	case config.RoleAgent:
		e.Agent = agent.New(cfg, configDir, logger)
		// The gateway-assigned identity must outlive the process; a GUI host
		// overrides this with its lock-taking hook (mirrors SetStoppedPersister).
		e.Agent.SetEnrollmentPersister(e.persistEnrollment)
		// Same split for the pause bits: hostless runs write their own config,
		// a GUI host overrides with its lock-taking hook.
		e.Agent.SetPausePersister(e.persistPause)
		e.roleRun = e.Agent.Run
	case config.RoleGateway:
		e.Gateway = gateway.New(cfg, configDir, logger)
		e.roleRun = func(ctx context.Context) error {
			return gateway.RunStarted(ctx, e.Gateway, e.cfg, e.logger)
		}
	default:
		return nil, fmt.Errorf("no role configured")
	}
	db, err := analytics.Open(configDir, analytics.Options{RetentionDays: cfg.Analytics.RetentionDays}, logger)
	if err != nil {
		logger.Warn("analytics: database unavailable — stats will not persist this run", "err", err)
	} else {
		e.DB = db
		db.ImportLegacyStats(configDir)
	}
	var persist stats.Persister
	if e.DB != nil {
		persist = e.DB // avoid a typed-nil interface when the open failed
	}
	e.Stats = stats.Open(persist, logger)
	// Geo loads before the recorder exists: the recorder's writer goroutine
	// reads its resolver unlocked, so it is fixed at construction.
	e.geo = geo.NewResolver(logger)
	e.geo.Load(cfg.Analytics.GeoIPCityPath, cfg.Analytics.GeoIPASNPath)
	e.rec = e.DB.NewRecorder(e.geo) // nil-safe when DB is nil
	e.resolver = players.New(e.DB, cfg.Analytics.MojangLookups, logger)
	e.conns().SetHooks(e.onConnOpen, e.onConnClose, e.onConnPlayer, e.onConnRTT)
	// The agent owns local-target health probing; mirror its transitions into
	// the uptime event journal. (The gateway learns local health over the wire
	// and does not probe, so it records only link events.)
	if e.Agent != nil {
		e.Agent.SetHealthObserver(func(tunnelID string, up bool) {
			e.rec.RecordEvent(analytics.EventTunnelLocal, e.cfg.Agent.AgentID, tunnelID, up)
		})
	}
	return e, nil
}

// onConnOpen feeds both the lifetime peer store and the session recorder.
func (e *Engine) onConnOpen(en *conntrack.Entry) {
	e.Stats.ConnOpened(en.ClientAddr)
	e.rec.SessionOpened(en)
}

// onConnClose folds final bytes into the peer store and closes the session
// row.
func (e *Engine) onConnClose(en *conntrack.Entry, bytesIn, bytesOut int64) {
	e.Stats.ConnClosed(en.ClientAddr, bytesIn, bytesOut)
	e.rec.SessionClosed(en, bytesIn, bytesOut)
}

// onConnPlayer records a sniffed player identity on the session and hands the
// username to the resolver for UUID lookup.
func (e *Engine) onConnPlayer(en *conntrack.Entry) {
	e.rec.PlayerSeen(en)
	if e.resolver != nil {
		if p := en.Player(); p != nil {
			ip, _, err := net.SplitHostPort(en.ClientAddr)
			if err != nil {
				ip = en.ClientAddr
			}
			e.resolver.Observe(p.Name, ip, p.Protocol)
		}
	}
}

// onConnRTT forwards a fresh round-trip measurement to the recorder, which
// buckets it into per-minute aggregates and the session's running average.
func (e *Engine) onConnRTT(en *conntrack.Entry) {
	e.rec.RecordRTT(en, en.RTT())
}

// Run is the supervisor. It serves the IPC pipe, the stats sampler, the
// identity resolver, and the metrics endpoint, starts the tunnel run, and
// blocks until ctx is cancelled or the pipe fails. A run that stops — asked to
// or crashed — leaves the supervisor standing.
//
// A pipe conflict (another daemon running) is fatal by design: exactly one
// process may own ports and config.
func (e *Engine) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	e.runMu.Lock()
	e.supCtx = runCtx
	e.runMu.Unlock()

	pipeErr := make(chan error, 1)
	go func() {
		// Extra pipe owners are a service-mode affair: there the engine runs as
		// LocalSystem, so granting "the account we run as" grants nobody a GUI can
		// use, and the installer left its SID behind for this. Absent in the
		// GUI-hosted case, where the engine already is the person using it.
		err := ipc.Serve(runCtx, e.logger, ipc.Sources{
			Status:    e.Status,
			History:   e.History,
			Peers:     e.Peers,
			Analytics: e.analyticsSource,
		}, ipc.PipeOwners(e.configDir)...)
		if errors.Is(err, ipc.ErrUnsupported) {
			e.logger.Debug("ipc pipe unsupported on this platform")
			err = nil
		} else if err != nil && runCtx.Err() == nil {
			err = fmt.Errorf("ipc pipe failed (is another proxyforward daemon running?): %w", err)
		}
		pipeErr <- err
	}()

	// The sampler, the resolver, and the metrics endpoint are supervisor-scoped:
	// they describe the process, not the tunnel, so a stop must not silence them
	// — Prometheus keeps answering with the tunnel down, which is when a monitor
	// most wants an answer. They still end with Run: the sampler's final flush
	// must land before it returns, because a GUI that swaps roles opens a new
	// store on the same file the moment Run is done, and the metrics listener
	// must be released for that new Engine to re-bind the port.
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		e.runSampler(runCtx)
	}()
	resolverDone := make(chan struct{})
	go func() {
		defer close(resolverDone)
		e.resolver.Run(runCtx)
	}()
	metricsDone := make(chan struct{})
	go func() {
		defer close(metricsDone)
		e.serveMetrics(runCtx)
	}()

	// A persisted stop is honored here, at the one place both the GUI-hosted
	// and the service/headless launch pass through: the supervisor comes up and
	// serves the pipe, but the tunnel stays down until StartRun. So a stopped
	// service runs and answers, it just proxies nothing — and a reboot does not
	// quietly undo an operator's decision to be off.
	if e.cfg.Engine.Stopped {
		e.logger.Info("engine configured stopped; supervisor up, tunnel run held")
	} else if err := e.StartRun(); err != nil {
		e.logger.Error("engine run failed to start", "err", err)
	}

	// Only the process being asked to stop, or the pipe failing, ends the
	// supervisor. A run exiting on its own does not: that is the state the GUI
	// stays connected to read.
	var err error
	select {
	case err = <-pipeErr:
		// The pipe failed on its own, so it is already released.
		cancel()
	case <-ctx.Done():
		cancel()
		// Wait for the pipe to be released before returning. A fresh Engine —
		// the GUI swapping roles — serves the same name, and would race this
		// listener for it. ipc.Serve reports a cancelled listen as nil, so an
		// orderly shutdown still returns nil without having to mask anything.
		err = <-pipeErr
	}

	// Wait for the run the supervisor's cancel just killed: its final stats
	// flush and its stop event both need the store this teardown closes.
	e.runMu.Lock()
	runDone := e.runDone
	e.runMu.Unlock()
	if runDone != nil {
		<-runDone
	}
	<-samplerDone
	<-resolverDone
	<-metricsDone
	if e.DB != nil {
		if cerr := e.DB.Close(); cerr != nil {
			e.logger.Warn("analytics: close failed", "err", cerr)
		}
	}
	e.geo.Close()
	return err
}

// StartRun starts the tunnel run. It is idempotent — starting a running engine
// is a no-op — and refuses while a previous run is still unwinding, because
// the ports it is about to bind may not be free yet.
func (e *Engine) StartRun() error {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	switch {
	case e.supCtx == nil || e.supCtx.Err() != nil:
		return ErrNoSupervisor
	case e.runState == RunRunning:
		return nil
	case e.runState == RunStopping:
		return ErrRunStopping
	}

	ctx, cancel := context.WithCancel(e.supCtx)
	done := make(chan struct{})
	e.runCancel, e.runDone, e.runState, e.runErr = cancel, done, RunRunning, nil

	// Bracket the run in the uptime journal so uptime queries can treat time
	// between a graceful stop and the next start as unknown, not down. It
	// brackets the run and not the process: an engine stopped for a week must
	// not read as a week of uptime.
	e.rec.RecordEvent(analytics.EventEngine, "", "", true)

	go func() {
		defer close(done)
		err := e.roleRun(ctx)
		// A cancelled run stopped because it was asked to; that is not a fault
		// to report, and only a fault should reach the UI as one.
		if ctx.Err() != nil {
			err = nil
		}
		e.runMu.Lock()
		e.runErr, e.runState = err, RunStopped
		e.runMu.Unlock()
		e.rec.RecordEvent(analytics.EventEngine, "", "", false)
		if err != nil {
			e.logger.Error("engine run stopped with error", "err", err)
		}
	}()
	return nil
}

// StopRun stops the tunnel run: the link is cut, the listeners close, and every
// connection is dropped. It always completes.
//
// Without force it waits up to stopGrace and reports if that ran out. With
// force it returns as soon as the run is cancelled, without waiting — the run
// unwinds on its own either way, so force buys promptness, not power.
func (e *Engine) StopRun(force bool) error {
	e.runMu.Lock()
	cancel, done := e.runCancel, e.runDone
	if e.runState == RunRunning {
		e.runState = RunStopping
	}
	e.runMu.Unlock()
	if cancel == nil {
		return nil // never started
	}
	cancel()
	if force {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(stopGrace):
		return fmt.Errorf("stopped, but the link did not close cleanly within %s", stopGrace)
	}
}

// RunState reports the tunnel run's lifecycle and, when it stopped on its own,
// the error that stopped it. A stopped run says nothing about the supervisor,
// which is still serving the pipe this was read over.
func (e *Engine) RunState() (RunState, error) {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	return e.runState, e.runErr
}

// linkAgentID identifies the agent on the far side of the control link, for
// attributing link-uptime events. On the agent it is our own identity; on the
// gateway it is the connected agent's. Empty when no agent is connected.
func (e *Engine) linkAgentID() string {
	if e.Agent != nil {
		return e.cfg.Agent.AgentID
	}
	return e.Gateway.AgentID()
}

// runSampler feeds the stats store: byte totals at 10 Hz, periodic flushes,
// link-session-start detection, and one final flush on shutdown.
func (e *Engine) runSampler(ctx context.Context) {
	sample := time.NewTicker(sampleInterval)
	defer sample.Stop()
	flush := time.NewTicker(flushInterval)
	defer flush.Stop()
	sessionSample := time.NewTicker(sessionSampleInterval)
	defer sessionSample.Stop()
	var prevUpSince int64
	// linkAgentID is the agent that owned the current link session, captured at
	// the up transition so the matching down event attributes to the same agent
	// even after it has disconnected.
	var linkAgentID string
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
			// (no live link yet). Loss is a gauge where 0 is a real reading.
			rtt := float64(-1)
			// loss is set unconditionally below; PacketLossPct returns -1
			// itself when it has no sample.
			var loss float64
			if e.Agent != nil {
				appIn, appOut = e.Agent.Conns.Totals()
				linkIn, linkOut = e.Agent.LinkTotalBytes()
				upSince = e.Agent.LinkUpSinceMs()
				conns = e.Agent.Conns.Count()
				if r := e.Agent.RTTMillis(); r > 0 {
					rtt = float64(r)
				}
				loss = e.Agent.PacketLossPct()
			} else {
				appIn, appOut = e.Gateway.Conns.Totals()
				linkIn, linkOut = e.Gateway.LinkTotalBytes()
				upSince = e.Gateway.AgentLinkUpSinceMs()
				conns = e.Gateway.Conns.Count()
				if r := e.Gateway.RTTMillis(); r > 0 {
					rtt = float64(r)
				}
				loss = e.Gateway.PacketLossPct()
			}
			players := e.conns().PlayerCount()
			e.Stats.Sample(now, appIn, appOut, linkIn, linkOut, conns, players, rtt, loss)
			// Gateway: also feed each connected agent's own bandwidth history so
			// the dashboard can scope the chart per agent. Per-agent byte totals
			// are monotonic (conntrack folds closed conns per agent), and rtt/loss
			// come from that agent's link. The agent role has a single series
			// (itself) and needs no per-agent split.
			if e.Gateway != nil {
				for agentID, at := range e.Gateway.Conns.AgentTotals() {
					if agentID == "" {
						continue
					}
					aRtt, aLoss := e.Gateway.AgentQuality(agentID)
					e.Stats.SampleAgent(agentID, now, at.BytesIn, at.BytesOut, at.Conns, at.Players, aRtt, aLoss)
				}
			}
			// A change in upSince marks a control-link session boundary: fold it
			// into the lifetime counter and journal the up/down transition.
			if upSince != prevUpSince {
				if prevUpSince != 0 {
					e.rec.RecordEvent(analytics.EventLink, linkAgentID, "", false)
				}
				if upSince != 0 {
					linkAgentID = e.linkAgentID()
					e.Stats.LinkSessionStarted()
					e.rec.RecordEvent(analytics.EventLink, linkAgentID, "", true)
				}
			}
			prevUpSince = upSince
		case <-sessionSample.C:
			if e.rec != nil {
				e.rec.SampleLive(e.conns().Snapshot())
			}
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
	runState, _ := e.RunState()
	st.RunState = string(runState)
	life := e.Stats.Lifetime()
	st.AllTimeBytesIn, st.AllTimeBytesOut = life.BytesIn, life.BytesOut
	st.CumulativeUptimeMs = life.UptimeMs
	st.LinkSessions = life.LinkSessions
	switch {
	case e.Agent != nil:
		st.LinkUp = e.Agent.LinkUp()
		st.RTTMillis = e.Agent.RTTMillis()
		st.Transport = e.Agent.ActiveTransport()
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
		st.PeerHeadless = e.Agent.PeerHeadless()
		for _, t := range e.Agent.Tunnels() {
			ts := ipc.TunnelStatus{
				ID: t.ID, Name: t.Name,
				BandwidthLimitMbps:  t.Options.BandwidthLimitMbps,
				BandwidthLimitScope: t.Options.BandwidthLimitScope,
			}
			ts.PublicPort, _ = e.Agent.TunnelPublicPort(t.ID)
			ts.LocalUp, ts.LocalKnown = e.Agent.LocalUp(t.ID)
			// "mine" is this agent's own bits (tunnel or agent-wide); "theirs"
			// the gateway's reported set, whose all-bit covers() already folds.
			ts.Paused, ts.PausedBy = pausedBy(t.Paused || e.Agent.AgentWidePaused(), e.Agent.PausedByGateway(t.ID))
			// Only the mc-status probe has anything to report, and only while the
			// server is answering — ServerStatus goes nil the moment it stops, so
			// a stale player count can never be shown as current.
			if info := e.Agent.ServerStatus(t.ID); info != nil {
				ts.ServerStatusKnown = true
				ts.ServerMOTD, ts.ServerVersion = info.MOTD, info.Version
				ts.ServerPlayersOnline, ts.ServerPlayersMax = info.PlayersOnline, info.PlayersMax
			}
			st.Tunnels = append(st.Tunnels, ts)
		}
		setStatusConns(&st, e.Agent.Conns.Snapshot())
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
		st.PeerHeadless = e.Gateway.AgentHeadless()
		for _, t := range e.Gateway.Tunnels() {
			ts := ipc.TunnelStatus{
				AgentID: t.AgentID,
				ID:      t.ID, Name: t.Name, PublicPort: t.PublicPort,
				LocalUp: t.LocalUp, LocalKnown: t.LocalKnown,
				BandwidthLimitMbps:  t.BandwidthLimitMbps,
				BandwidthLimitScope: t.BandwidthLimitScope,
			}
			ts.Paused, ts.PausedBy = pausedBy(t.PausedMine, t.PausedTheirs)
			st.Tunnels = append(st.Tunnels, ts)
		}
		// Per-agent link records for the multi-agent dashboard. Tunnel counts
		// come from the flat tunnel list; player counts from conntrack's
		// per-agent totals. Agents is sorted (Gateway.Agents) and clamped.
		tunnelCount := make(map[string]int)
		for _, t := range st.Tunnels {
			tunnelCount[t.AgentID]++
		}
		agentTraffic := e.Gateway.Conns.AgentTotals()
		live := e.Gateway.Agents()
		st.AgentsTotal = len(live)
		for _, ag := range live {
			if len(st.Agents) >= ipc.MaxStatusAgents {
				break
			}
			st.Agents = append(st.Agents, ipc.AgentStatus{
				AgentID:       ag.AgentID,
				Hostname:      ag.Hostname,
				LANIPs:        ag.LANIPs,
				RemoteIP:      ag.RemoteIP,
				LinkUpSinceMs: ag.LinkUpSinceMs,
				RTTMillis:     ag.RTTMillis,
				JitterMillis:  ag.JitterMillis,
				PacketLossPct: ag.PacketLossPct,
				HealthScore:   healthScore(true, ag.JitterMillis, ag.PacketLossPct, ag.LinkUpSinceMs),
				LinkBytesIn:   ag.LinkBytesIn,
				LinkBytesOut:  ag.LinkBytesOut,
				Tunnels:       tunnelCount[ag.AgentID],
				Players:       agentTraffic[ag.AgentID].Players,
			})
		}
		setStatusConns(&st, e.Gateway.Conns.Snapshot())
		st.TotalBytesIn, st.TotalBytesOut = e.Gateway.Conns.Totals()
	}
	return st
}

// setStatusConns clamps the connection list to ipc.MaxStatusConns so the
// status frame always fits control.MaxFrame. The snapshot is ID-ascending, so
// keeping the tail keeps the newest connections.
func setStatusConns(st *ipc.Status, snaps []conntrack.Snapshot) {
	st.ConnectionsTotal = len(snaps)
	if len(snaps) > ipc.MaxStatusConns {
		snaps = snaps[len(snaps)-ipc.MaxStatusConns:]
		st.ConnectionsTruncated = true
	}
	st.Connections = snaps
}
