// Package app is the Wails-bound application layer: everything the frontend
// can call lives on App, and all GUI-bound events are emitted from here
// (coalesced — never per-packet or per-connection).
//
// On startup the app probes the daemon pipe: a running daemon (service or
// headless run) means this GUI attaches as a thin client; otherwise it runs
// the engine in-process (and serves the pipe itself). Exactly one process
// ever owns ports and config.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"proxyforward/internal/config"
	"proxyforward/internal/engine"
	"proxyforward/internal/ipc"
	"proxyforward/internal/link"
	"proxyforward/internal/linkquality"
	"proxyforward/internal/logging"
	"proxyforward/internal/stats"
	"proxyforward/internal/svc"
	"proxyforward/internal/version"
)

// Modes of operation.
const (
	ModeWizard   = "wizard"   // no role configured yet
	ModeEngine   = "engine"   // engine runs in this process
	ModeAttached = "attached" // thin client to an external daemon
)

// tickInterval is the GUI snapshot cadence (the only Go→JS status traffic).
const tickInterval = 500 * time.Millisecond

// engineStopGrace bounds how long a lifecycle verb waits for the in-process
// engine to unwind before reporting that it did not stop cleanly. It bounds
// the caller, not the engine: the engine is already cancelled and keeps
// unwinding either way.
const engineStopGrace = 10 * time.Second

type App struct {
	ctx        context.Context
	configPath string
	configDir  string
	hostname   string
	ring       *logging.Ring
	logger     *slog.Logger

	// engMu serializes engine lifecycle transitions (stop → mutate → start) so
	// two of them can never interleave a config swap. It is the OUTER lock: a
	// transition holds engMu for its whole duration but takes a.mu only to read
	// fields, swap engine handles, and mutate cfg — never across the wait for a
	// stopping engine, which is what used to freeze the 2 Hz tick and every
	// binding behind it for ten seconds. Never take engMu while holding a.mu.
	engMu sync.Mutex

	mu     sync.Mutex // guards cfg, mode, engine handles, ipcClient
	cfg    *config.Config
	mode   string
	eng    *engine.Engine
	cancel context.CancelFunc
	done   chan error
	client *ipc.Client
	// engineFatal holds the engine's terminal error until the next start,
	// so every status tick (not just the one that drained done) reports it.
	engineFatal string
	// pendingDeepLink holds a pxf:// pairing link delivered by the OS before the
	// frontend was ready to receive the event (a cold start via the protocol
	// handler); the frontend drains it once on mount via TakePendingDeepLink.
	pendingDeepLink string
	// historyUnsupported latches after an attached daemon fails a history
	// request (older version): stop asking instead of eating a timeout per
	// poll.
	historyUnsupported bool
	// analyticsUnsupported latches the same way for the analytics envelope, so
	// a GUI attached to a pre-analytics daemon degrades to empty states rather
	// than timing out on every poll. It latches only on transport-level
	// failures — a served error (ipc.OpError) is transient and never latches.
	analyticsUnsupported bool

	// anMu serializes attached-mode analytics round-trips on their own pipe
	// connection (anClient, dialed lazily), so a slow analytics query can
	// never park the 2 Hz status tick behind a.mu. anClient is touched only
	// under anMu; the reset points close it via closeAnalyticsConn.
	anMu     sync.Mutex
	anClient *ipc.Client

	// tray is non-nil once RegisterTray ran; trayActive (under a.mu) means the
	// icon really exists — systray's onReady set it — and is the only licence
	// to hide the window instead of quitting. Hiding without an icon would
	// strand the app invisible.
	tray       *tray
	trayActive bool
	// quitConfirmed (under a.mu) records that the user answered a disconnect
	// confirm; the next BeforeClose lets the quit through instead of re-asking.
	quitConfirmed bool

	// avatarMu serializes creator-avatar cache reads/refreshes.
	avatarMu sync.Mutex

	// avatars is the player-head cache behind /pf/avatar/ (see avatars.go).
	avatars *avatarCache
}

func New(configPath string, cfg *config.Config, ring *logging.Ring, logger *slog.Logger) *App {
	hn, _ := os.Hostname()
	configDir := filepath.Dir(configPath)
	return &App{
		configPath: configPath,
		configDir:  configDir,
		hostname:   hn,
		cfg:        cfg,
		ring:       ring,
		logger:     logger,
		mode:       ModeWizard,
		avatars:    newAvatarCache(filepath.Join(configDir, "avatars"), logger),
	}
}

// AdoptClient hands Startup an already-verified daemon connection. runGUI
// probes the pipe before the window exists — the tray must register on the
// main thread before wails.Run, and whether a tray belongs at all depends on
// this very answer — so the probe happens once, there, and lands here.
func (a *App) AdoptClient(c *ipc.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = c
	a.mode = ModeAttached
	a.logger.Info("attached to running daemon over ipc pipe")
}

// Startup is wired to Wails OnStartup: start the engine if this process owns
// it (runGUI already decided attached-vs-not), and begin the status ticker.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx

	a.mu.Lock()
	if a.mode != ModeAttached && a.cfg.Role != config.RoleUnset && a.cfg.Validate() == nil {
		a.startEngineLocked()
	}
	a.mu.Unlock()

	go a.tickLoop(ctx)
}

// HandleDeepLink routes an OS-delivered pxf:// pairing link into the UI. Once the
// window is up it emits a "pxf:deeplink" event and surfaces the window so the click
// feels like it opened the app; before startup (a cold protocol launch) it stashes
// the link for the frontend to pull via TakePendingDeepLink when it mounts.
func (a *App) HandleDeepLink(rawURL string) {
	if a.ctx == nil {
		a.mu.Lock()
		a.pendingDeepLink = rawURL
		a.mu.Unlock()
		return
	}
	runtime.EventsEmit(a.ctx, "pxf:deeplink", rawURL)
	a.surfaceWindow()
}

// OnSecondInstance handles a second launch of the GUI: the OS forwards that
// process's args here and exits it. A pxf:// deep link routes into pairing; either
// way the existing window is brought to the front.
func (a *App) OnSecondInstance(deepLink string) {
	if deepLink != "" {
		a.HandleDeepLink(deepLink)
		return
	}
	a.surfaceWindow()
}

// TakePendingDeepLink returns and clears any pxf:// link captured before the
// frontend was listening (a cold start via the OS protocol handler). Bound to Wails;
// the frontend calls it once on mount.
func (a *App) TakePendingDeepLink() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	url := a.pendingDeepLink
	a.pendingDeepLink = ""
	return url
}

// surfaceWindow brings the running GUI window to the foreground (un-minimising if
// needed). A no-op before startup.
func (a *App) surfaceWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

// Shutdown is wired to Wails OnShutdown.
func (a *App) Shutdown(ctx context.Context) {
	a.engMu.Lock()
	defer a.engMu.Unlock()
	if err := a.stopEngine(); err != nil {
		a.logger.Warn("shutdown", "err", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeAnalyticsConnLocked()
	if a.client != nil {
		a.client.Close()
		a.client = nil
	}
}

// closeAnalyticsConnLocked drops the dedicated analytics pipe and clears the
// unsupported latch; a.mu must be held. Closing is safe against an in-flight
// round-trip — that op fails with a transport error and re-checks state
// before touching the latch.
func (a *App) closeAnalyticsConnLocked() {
	if a.anClient != nil {
		a.anClient.Close()
		a.anClient = nil
	}
	a.analyticsUnsupported = false
}

// startEngineLocked launches the in-process engine; a.mu must be held.
func (a *App) startEngineLocked() {
	eng, err := engine.New(a.cfg, a.configDir, a.configPath, a.logger)
	if err != nil {
		// Near-unreachable (New only rejects an unset role, and every caller
		// checks that first), but it used to log and return with the mode
		// unchanged — a construction failure with nowhere to be seen. There is
		// somewhere now.
		a.logger.Error("engine start failed", "err", err)
		a.engineFatal, a.mode = err.Error(), ModeEngine
		return
	}
	if eng.Agent != nil {
		// Make the gateway's authoritative push durable: without this the pushed
		// set lives only in memory and the config file keeps a set the gateway no
		// longer holds until the next connect re-pushes it.
		eng.Agent.SetConfigPersister(a.persistPushedConfig)
		// And land the assigned identity under a.mu — this config is shared with
		// the 2 Hz status read, so the engine's bare-write default must not run.
		eng.Agent.SetEnrollmentPersister(a.persistEnrollment)
	}
	// A lifecycle op over the pipe (an attached GUI, or another GUI driving this
	// engine) persists the stopped flag through a.mu — this config is shared
	// with the app, so the engine must not write it bare.
	eng.SetStoppedPersister(a.persistStoppedErr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	a.eng, a.cancel, a.done, a.mode = eng, cancel, done, ModeEngine
	a.engineFatal = ""
	a.historyUnsupported = false
	a.closeAnalyticsConnLocked()
	go func() {
		err := eng.Run(ctx)
		done <- err
		if err != nil {
			a.logger.Error("engine stopped with error", "err", err)
		}
	}()
}

// stopEngine stops the in-process engine and waits for it to unwind, reporting
// if that took longer than engineStopGrace. a.mu must NOT be held — the wait is
// the whole reason this exists — and engMu must be, so that the window where
// a.mu is free cannot be used to start a second engine.
//
// The engine handles are cleared before the wait, so anything that reads them
// during it sees no engine, which is true: it is already cancelled.
func (a *App) stopEngine() error {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.eng, a.cancel, a.done = nil, nil, nil
	if a.mode == ModeEngine {
		a.mode = ModeWizard
	}
	a.mu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()
	// done is nil when statusLocked already drained it (the engine had exited
	// on its own); waiting on a nil channel would burn the whole grace period.
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(engineStopGrace):
		return fmt.Errorf("the engine did not stop within %s; it is still shutting down", engineStopGrace)
	}
}

// refuseIfAttached rejects a lifecycle verb when the engine belongs to another
// process. msg is the caller's whole message: what it wanted to do is what
// makes the refusal actionable, and that differs per verb.
func (a *App) refuseIfAttached(msg string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mode == ModeAttached {
		return errors.New(msg)
	}
	return nil
}

// UIStatus is the frontend's per-tick snapshot. It mirrors ipc.Status with
// app-local types because the Wails binding generator cannot model
// cross-package embedded structs.
type UIStatus struct {
	Mode       string `json:"mode"`
	Role       string `json:"role"`
	Version    string `json:"version"`
	Hostname   string `json:"hostname"`
	PID        int    `json:"pid"`
	ConfigPath string `json:"configPath"`

	// RunState is the tunnel run's lifecycle — running | stopping | stopped —
	// a second axis crossing Mode rather than a fourth value of it. "Configured
	// but deliberately off" is not the wizard, and telling those apart is the
	// whole point: EngineFatal says which kind of stopped this is, so an empty
	// one with RunState "stopped" means someone chose it.
	RunState string `json:"runState"`

	LinkUp         bool  `json:"linkUp"`
	RTTMillis      int64 `json:"rttMillis"`
	AgentConnected bool  `json:"agentConnected"`

	// Transport is the data plane the live agent session settled on ("quic" |
	// "per-conn" | "mux"); "" while down or on the gateway role. Shows what the
	// auto ladder connected over.
	Transport string `json:"transport"`

	// Link quality + health rollup. Both roles run their own heartbeat and
	// report the same stats; values are -1/"unknown" until the link is up.
	JitterMillis  float64 `json:"jitterMillis"`
	PacketLossPct float64 `json:"packetLossPct"`
	HealthScore   string  `json:"healthScore"`

	// Identity of the peer machine, for the dashboard's identity badges.
	// (This machine's own hostname is Hostname above.) Public addresses are
	// shown prominently; LAN IPs are secondary.
	PeerHostname string   `json:"peerHostname"`
	PublicIP     string   `json:"publicIp"`
	PeerPublicIP string   `json:"peerPublicIp"`
	LocalLANIPs  []string `json:"localLanIps"`
	PeerLANIPs   []string `json:"peerLanIps"`

	// Agents is the per-agent link state on a gateway (empty on the agent
	// role); the Agents screen and drill-in read it. Tunnels/Connections stay
	// flat and carry agentId so the GUI groups them per agent.
	Agents []AgentUI `json:"agents"`

	Tunnels       []TunnelUI `json:"tunnels"`
	Connections   []ConnUI   `json:"connections"`
	TotalBytesIn  int64      `json:"totalBytesIn"`
	TotalBytesOut int64      `json:"totalBytesOut"`

	// Control-link/session metadata. PeerAddr is the other end of the tunnel
	// link: gateway host:port on the agent, agent IP on the gateway.
	LinkUpSinceMs  int64  `json:"linkUpSinceMs"`
	ProcessStartMs int64  `json:"processStartMs"`
	PeerAddr       string `json:"peerAddr"`
	LinkBytesIn    int64  `json:"linkBytesIn"`
	LinkBytesOut   int64  `json:"linkBytesOut"`

	// Lifetime aggregates from the persistent stats store.
	AllTimeBytesIn     int64 `json:"allTimeBytesIn"`
	AllTimeBytesOut    int64 `json:"allTimeBytesOut"`
	CumulativeUptimeMs int64 `json:"cumulativeUptimeMs"`
	LinkSessions       int64 `json:"linkSessions"`

	// HistoryUnsupported is set in attached mode when the daemon predates
	// the history protocol, so the chart can explain its empty state.
	HistoryUnsupported bool `json:"historyUnsupported,omitempty"`

	// AnalyticsUnsupported is set when the analytics surface cannot answer:
	// attached to a pre-analytics daemon (the transport latch), or the local
	// store failed to open. Analytics/Players render an explanatory empty
	// state instead of skeletons that never fill.
	AnalyticsUnsupported bool `json:"analyticsUnsupported,omitempty"`

	// ConnectionsTruncated/ConnectionsTotal mirror the ipc.Status clamp:
	// Connections holds at most the newest ipc.MaxStatusConns entries.
	ConnectionsTruncated bool `json:"connectionsTruncated,omitempty"`
	ConnectionsTotal     int  `json:"connectionsTotal,omitempty"`

	// EngineFatal carries the engine's terminal error (bad token etc.) so
	// the dashboard can show it instead of a silent dead link.
	EngineFatal string `json:"engineFatal,omitempty"`
}

// TunnelUI is one tunnel's live state for the frontend.
type TunnelUI struct {
	AgentID             string `json:"agentId,omitempty"` // owning agent (gateway role)
	ID                  string `json:"id"`
	Name                string `json:"name"`
	PublicPort          int    `json:"publicPort"`
	LocalUp             bool   `json:"localUp"`
	LocalKnown          bool   `json:"localKnown"`
	BandwidthLimitMbps  int    `json:"bandwidthLimitMbps"`  // configured cap (0 = unlimited)
	BandwidthLimitScope string `json:"bandwidthLimitScope"` // combined | per-direction | per-connection
}

// ConnUI is one live connection for the frontend.
type ConnUI struct {
	ID         uint64 `json:"id"`
	AgentID    string `json:"agentId,omitempty"` // owning agent (gateway role)
	TunnelName string `json:"tunnelName"`
	ClientAddr string `json:"clientAddr"`
	StartedAt  int64  `json:"startedAt"` // unix millis
	BytesIn    int64  `json:"bytesIn"`
	BytesOut   int64  `json:"bytesOut"`
	// Player identity from Minecraft login sniffing; empty when unknown.
	PlayerName string `json:"playerName,omitempty"`
	PlayerUUID string `json:"playerUuid,omitempty"`
	// RttMs is the gateway-measured round-trip time to this client; -1 unknown.
	RttMs float64 `json:"rttMs"`
}

// AgentUI is one connected agent's link state on a gateway, mirroring
// ipc.AgentStatus for the Agents dashboard and drill-in.
type AgentUI struct {
	AgentID       string   `json:"agentId"`
	Hostname      string   `json:"hostname"`
	LANIPs        []string `json:"lanIps"`
	RemoteIP      string   `json:"remoteIp"`
	LinkUpSinceMs int64    `json:"linkUpSinceMs"`
	RTTMillis     int64    `json:"rttMillis"`
	JitterMillis  float64  `json:"jitterMillis"`
	PacketLossPct float64  `json:"packetLossPct"`
	HealthScore   string   `json:"healthScore"`
	LinkBytesIn   int64    `json:"linkBytesIn"`
	LinkBytesOut  int64    `json:"linkBytesOut"`
	Tunnels       int      `json:"tunnels"`
	Players       int      `json:"players"`
}

// applyIPCStatus copies a daemon snapshot into the UI shape.
func (st *UIStatus) applyIPCStatus(s ipc.Status) {
	st.Role = s.Role
	st.Version = s.Version
	st.PID = s.PID
	// A daemon too old to have an off switch reports nothing here, and its
	// tunnel is running for the same reason it has no way to say otherwise.
	st.RunState = s.RunState
	if st.RunState == "" {
		st.RunState = string(engine.RunRunning)
	}
	if s.ConfigPath != "" {
		st.ConfigPath = s.ConfigPath
	}
	if s.LocalHostname != "" {
		st.Hostname = s.LocalHostname
	}
	st.LinkUp = s.LinkUp
	st.RTTMillis = s.RTTMillis
	st.Transport = s.Transport
	st.JitterMillis = s.JitterMillis
	st.PacketLossPct = s.PacketLossPct
	st.HealthScore = s.HealthScore
	st.AgentConnected = s.AgentConnected
	st.PeerHostname = s.PeerHostname
	st.PublicIP = s.PublicIP
	st.PeerPublicIP = s.PeerPublicIP
	st.LocalLANIPs = s.LocalLANIPs
	st.PeerLANIPs = s.PeerLANIPs
	st.TotalBytesIn = s.TotalBytesIn
	st.TotalBytesOut = s.TotalBytesOut
	st.LinkUpSinceMs = s.LinkUpSinceMs
	st.ProcessStartMs = s.ProcessStartMs
	st.PeerAddr = s.PeerAddr
	st.LinkBytesIn = s.LinkBytesIn
	st.LinkBytesOut = s.LinkBytesOut
	st.AllTimeBytesIn = s.AllTimeBytesIn
	st.AllTimeBytesOut = s.AllTimeBytesOut
	st.CumulativeUptimeMs = s.CumulativeUptimeMs
	st.LinkSessions = s.LinkSessions
	st.Agents = make([]AgentUI, 0, len(s.Agents))
	for _, ag := range s.Agents {
		st.Agents = append(st.Agents, AgentUI{
			AgentID: ag.AgentID, Hostname: ag.Hostname, LANIPs: ag.LANIPs,
			RemoteIP: ag.RemoteIP, LinkUpSinceMs: ag.LinkUpSinceMs,
			RTTMillis: ag.RTTMillis, JitterMillis: ag.JitterMillis,
			PacketLossPct: ag.PacketLossPct, HealthScore: ag.HealthScore,
			LinkBytesIn: ag.LinkBytesIn, LinkBytesOut: ag.LinkBytesOut,
			Tunnels: ag.Tunnels, Players: ag.Players,
		})
	}
	st.Tunnels = make([]TunnelUI, 0, len(s.Tunnels))
	for _, t := range s.Tunnels {
		st.Tunnels = append(st.Tunnels, TunnelUI{
			AgentID: t.AgentID,
			ID:      t.ID, Name: t.Name, PublicPort: t.PublicPort,
			LocalUp: t.LocalUp, LocalKnown: t.LocalKnown,
			BandwidthLimitMbps:  t.BandwidthLimitMbps,
			BandwidthLimitScope: t.BandwidthLimitScope,
		})
	}
	st.ConnectionsTruncated = s.ConnectionsTruncated
	st.ConnectionsTotal = s.ConnectionsTotal
	st.Connections = make([]ConnUI, 0, len(s.Connections))
	for _, c := range s.Connections {
		st.Connections = append(st.Connections, ConnUI{
			ID: c.ID, AgentID: c.AgentID, TunnelName: c.TunnelName, ClientAddr: c.ClientAddr,
			StartedAt: c.StartedAt, BytesIn: c.BytesIn, BytesOut: c.BytesOut,
			PlayerName: c.PlayerName, PlayerUUID: c.PlayerUUID, RttMs: c.RttMs,
		})
	}
}

// Status returns the current snapshot (also pushed as the "tick" event).
func (a *App) Status() UIStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusLocked()
}

func (a *App) statusLocked() UIStatus {
	st := UIStatus{Mode: a.mode}
	st.Version = version.String()
	st.Hostname = a.hostname
	st.ConfigPath = a.configPath
	st.Role = string(a.cfg.Role)
	// Defaults for the "no live link" case; applyIPCStatus overwrites them.
	st.JitterMillis = -1
	st.PacketLossPct = -1
	st.HealthScore = "unknown"

	switch a.mode {
	case ModeEngine:
		if a.eng != nil {
			st.applyIPCStatus(a.eng.Status())
			st.AnalyticsUnsupported = !a.eng.AnalyticsReady()
			// The run's own error, not a.done: since the supervisor/run split a
			// fatal run leaves the daemon standing, so nothing is delivered to
			// a.done and this is the only place the fatal surfaces. StartRun
			// clears it, which is why it is read live rather than latched.
			if _, runErr := a.eng.RunState(); runErr != nil {
				st.EngineFatal = runErr.Error()
			}
		} else {
			// Mode says this process owns an engine, but there is none: its
			// construction failed, and engineFatal below says why.
			st.RunState = string(engine.RunStopped)
		}
		select {
		case err := <-a.done:
			// The supervisor itself died — the pipe failed, which ends this
			// process's claim on the ports and is fatal by design.
			if err != nil {
				a.engineFatal = err.Error()
			}
			a.done = nil
		default:
		}
		if a.engineFatal != "" {
			st.EngineFatal = a.engineFatal // a dead daemon outranks a dead run
		}
	case ModeAttached:
		st.HistoryUnsupported = a.historyUnsupported
		st.AnalyticsUnsupported = a.analyticsUnsupported
		if a.client != nil {
			if remote, err := a.client.Status(); err == nil {
				st.applyIPCStatus(*remote)
			} else {
				// Daemon went away: fall back to running our own engine (or
				// the wizard) on the next tick.
				a.logger.Warn("daemon connection lost", "err", err)
				a.client.Close()
				a.client = nil
				a.closeAnalyticsConnLocked()
				if a.cfg.Role != config.RoleUnset && a.cfg.Validate() == nil {
					a.startEngineLocked()
				} else {
					a.mode = ModeWizard
				}
			}
		}
	}
	return st
}

// tickLoop pushes coalesced status snapshots at 2 Hz.
func (a *App) tickLoop(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st := a.Status()
			a.trayUpdate(st)
			runtime.EventsEmit(a.ctx, "tick", st)
		}
	}
}

// ---- Setup / wizard ----

// SetupGateway configures this machine as a gateway (first-run wizard) and
// starts the engine. publicHost may be empty.
func (a *App) SetupGateway(publicHost string) error {
	a.engMu.Lock()
	defer a.engMu.Unlock()
	if err := a.refuseIfAttached("a daemon is already running on this machine — configure it instead"); err != nil {
		return err
	}
	if err := a.stopEngine(); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Role = config.RoleGateway
	a.cfg.Gateway.PublicHost = publicHost
	if err := a.cfg.Save(a.configPath); err != nil {
		return err
	}
	a.startEngineLocked()
	return nil
}

// SetupAgent applies a pairing code (first-run wizard) and starts the agent.
func (a *App) SetupAgent(pairingCode, localAddr string, publicPort int) error {
	code, err := link.ParsePairingCode(pairingCode)
	if err != nil {
		return err
	}
	a.engMu.Lock()
	defer a.engMu.Unlock()
	if err := a.refuseIfAttached("a daemon is already running on this machine — configure it instead"); err != nil {
		return err
	}
	if err := a.stopEngine(); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Role = config.RoleAgent
	if a.cfg.Agent.AgentID == "" {
		a.cfg.Agent.AgentID = config.NewID()
	}
	a.cfg.Agent.GatewayHost = code.Host
	a.cfg.Agent.GatewayPort = code.Port
	a.cfg.Agent.CertFingerprint = code.Fingerprint
	// Identity enrollment is the only auth path: the code must carry a tkt_
	// enrollment ticket. A bare-hex payload is a legacy shared-token code from an
	// old gateway build, and pairing with it would only fail at the hello — say
	// so now, with the fix.
	if !link.IsEnrollTicket(code.Token) {
		return fmt.Errorf("this pairing code is from an old gateway build — generate a fresh code on the gateway (Overview, or the Agents screen) and pair with that")
	}
	a.cfg.Agent.EnrollTicket = code.Token
	if localAddr == "" {
		localAddr = "127.0.0.1:25565"
	}
	if publicPort == 0 {
		publicPort = config.DefaultPublicPort
	}
	if len(a.cfg.Agent.Tunnels) == 0 {
		a.cfg.Agent.Tunnels = []config.Tunnel{{
			// The only tunnel, so nothing is taken yet — a typed tnl_ id
			// (link.TunnelID) instead of a random hex one.
			ID:         link.TunnelID("Minecraft", func(string) bool { return false }),
			Name:       "Minecraft",
			Type:       config.TunnelTCP,
			LocalAddr:  localAddr,
			PublicPort: publicPort,
			Enabled:    true,
		}}
	}
	if err := a.cfg.Save(a.configPath); err != nil {
		return err
	}
	a.startEngineLocked()
	return nil
}

// ---- Config ----

// GetConfig returns the current configuration for the settings screens.
func (a *App) GetConfig() *config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// SaveTunnels validates and persists a new tunnel set, hot-applying it to a
// live agent (no restart, no dropped sessions on unchanged tunnels).
//
// Under mutual consent (a config-consent session) it instead *proposes* the edit
// and changes nothing — not the running set, not the file. An approval returns as
// the gateway's authoritative push, which applies and persists it
// (persistPushedConfig); a rejection leaves the agent exactly as it was, with the
// reason on PendingConsent. So a save there means "submitted", and the UI says so.
func (a *App) SaveTunnels(tunnels []config.Tunnel) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	trial := *a.cfg
	trial.Agent.Tunnels = tunnels
	if err := trial.Validate(); err != nil {
		return err
	}
	if a.eng != nil && a.eng.Agent != nil && a.eng.Agent.ConsentActive() {
		proposed, err := a.eng.Agent.ProposeTunnels(tunnels)
		if err != nil || proposed {
			// Awaiting the gateway's decision: persist nothing yet. Its approval
			// arrives as a push, and persistPushedConfig writes that.
			return err
		}
		// Nothing the gateway owns changed, so the edit already applied locally —
		// only this side has to remember it.
	}
	a.cfg.Agent.Tunnels = tunnels
	if err := a.cfg.Save(a.configPath); err != nil {
		return err
	}
	if a.eng != nil && a.eng.Agent != nil && !a.eng.Agent.ConsentActive() {
		a.eng.Agent.ApplyTunnels(tunnels)
	}
	return nil
}

// persistPushedConfig saves a gateway-pushed tunnel set to disk. The push is the
// single commit point for every authoritative config change (a consent approval, a
// port reclaim, an auto-reassign), and the app and agent share one *config.Config,
// so the new set is already what the editor reads — this is what makes it survive a
// restart instead of being silently re-pushed every connect.
//
// It runs on the agent's control-reader goroutine and takes a.mu itself; the agent
// calls it without holding its own cfgMu, so SaveTunnels' a.mu → cfgMu order is
// never inverted. A failed write is reported to the agent, which logs and relies on
// the gateway's re-push at the next connect.
func (a *App) persistPushedConfig(tunnels []config.Tunnel, _ uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Agent.Tunnels = tunnels
	return a.cfg.Save(a.configPath)
}

// SaveSettings persists settings edits. Engine-affecting changes (role
// ports, gateway address) take effect after RestartEngine.
func (a *App) SaveSettings(cfg config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := cfg.Validate(); err != nil {
		return err
	}
	*a.cfg = cfg
	return a.cfg.Save(a.configPath)
}

// SetTheme persists just the UI theme ("dark"|"light"|"system"). It is a
// narrow, engine-independent write so the toggle can't fail on unrelated
// validation.
func (a *App) SetTheme(theme string) error {
	if theme != "dark" && theme != "light" && theme != "system" {
		return fmt.Errorf("theme must be dark, light or system")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.UI.Theme = theme
	return a.cfg.Save(a.configPath)
}

// StopEngine stops the tunnel run: the link is cut, the listeners close, and
// every player is dropped. The daemon stays up — it keeps the pipe, so this is
// undoable from the GUI, and it keeps answering for the port it no longer
// serves. force skips the wait for a clean unwind.
//
// This is deliberately not a teardown. Tearing the daemon down would drop back
// to the wizard, which is a lie: the role is still configured, it is just off.
func (a *App) StopEngine(force bool) error {
	a.mu.Lock()
	mode := a.mode
	a.mu.Unlock()
	// Attached: the run belongs to another process, so drive it over the pipe.
	// That daemon persists its own stopped flag and returns promptly (the IPC
	// cycle is too short to wait out a clean unwind); the GUI watches runState
	// settle over the tick.
	if mode == ModeAttached {
		return a.analyticsCall(engine.OpStopRun, stopRunReq{Force: force}, nil)
	}

	a.engMu.Lock()
	defer a.engMu.Unlock()
	a.mu.Lock()
	eng := a.eng
	a.mu.Unlock()
	// Persist the intent before the stop, not after: a crash mid-drain must
	// still relaunch stopped, because the operator already decided. Best-effort
	// and never a reason to skip the stop — stop always completes, and a failed
	// write only risks the next launch disagreeing.
	a.persistStopped(true)
	if eng == nil {
		return nil // nothing configured, or already down
	}
	return eng.StopRun(force)
}

// stopRunReq is the OpStopRun body; force skips the clean-unwind wait.
type stopRunReq struct {
	Force bool `json:"force"`
}

// StartEngine starts the tunnel run. Usually a resume: the daemon is already
// up and only its run is stopped, so it costs one StartRun. When no daemon is
// live — a first start, or one whose supervisor died with the pipe — it builds
// and starts a fresh one.
func (a *App) StartEngine() error {
	a.mu.Lock()
	mode := a.mode
	a.mu.Unlock()
	// Attached: start the other process's run over the pipe. It clears its own
	// stopped flag. There is no rebuild path here — a daemon that is not running
	// is not one an attached client can conjure, it is one nobody is serving.
	if mode == ModeAttached {
		return a.analyticsCall(engine.OpStartRun, nil, nil)
	}

	a.engMu.Lock()
	defer a.engMu.Unlock()

	a.mu.Lock()
	eng := a.eng
	a.mu.Unlock()
	if eng != nil {
		err := eng.StartRun()
		// Anything but a missing supervisor is answered by the daemon we have:
		// ErrRunStopping especially, where a fresh daemon would race the old
		// run for the ports it has not released yet.
		if !errors.Is(err, engine.ErrNoSupervisor) {
			if err == nil {
				a.persistStopped(false)
			}
			return err
		}
	}

	if err := a.stopEngine(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Role == config.RoleUnset {
		return errors.New("no role configured yet — finish setup first")
	}
	if err := a.cfg.Validate(); err != nil {
		return err
	}
	// Clear the persisted stop before the rebuild: startEngineLocked runs the
	// supervisor, which reads this flag and would otherwise hold the run down
	// exactly when the operator asked for it up.
	a.cfg.Engine.Stopped = false
	if err := a.cfg.Save(a.configPath); err != nil {
		return fmt.Errorf("persist started state: %w", err)
	}
	a.startEngineLocked()
	return nil
}

// persistEnrollment overrides the engine's bare-write default: the assigned
// agt_ identity replaces Agent.AgentID and the spent enrollment ticket is
// cleared, under a.mu because this config is shared with the status read and the
// settings screens. Mirrors persistStopped.
func (a *App) persistEnrollment(assignedID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Agent.AgentID = assignedID
	a.cfg.Agent.EnrollTicket = ""
	return a.cfg.Save(a.configPath)
}

// persistStopped records the run's on/off intent so it survives restart and
// reboot. It takes a.mu itself; callers must not hold it. A failed write is
// logged, not fatal — the run is already in the asked-for state, and the worst
// case is the next launch disagreeing, which the operator can redo.
func (a *App) persistStopped(stopped bool) {
	if err := a.persistStoppedErr(stopped); err != nil {
		a.logger.Error("persist engine stopped state", "stopped", stopped, "err", err)
	}
}

// persistStoppedErr is persistStopped surfacing its error, wired into the
// engine as its StoppedPersister: a lifecycle op arriving over the pipe (an
// attached GUI, or another GUI driving this one's engine) persists through the
// same a.mu that guards the config the engine and the app share.
func (a *App) persistStoppedErr(stopped bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Engine.Stopped = stopped
	return a.cfg.Save(a.configPath)
}

// RestartEngine bounces the in-process engine to pick up settings changes.
func (a *App) RestartEngine() error {
	a.engMu.Lock()
	defer a.engMu.Unlock()
	if err := a.refuseIfAttached("the engine runs in another process (service or headless run); restart it there"); err != nil {
		return err
	}
	if err := a.stopEngine(); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Role == config.RoleUnset {
		return nil
	}
	if err := a.cfg.Validate(); err != nil {
		return err
	}
	a.startEngineLocked()
	return nil
}

// OpenConfigDir reveals the config directory in the system file manager.
func (a *App) OpenConfigDir() error {
	return openInFileManager(a.configDir)
}

// OpenExternal opens a URL in the system browser. Only http(s) is honored so a
// stray link can't launch an arbitrary scheme handler.
func (a *App) OpenExternal(rawURL string) {
	if u, err := url.Parse(rawURL); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		runtime.BrowserOpenURL(a.ctx, rawURL)
	}
}

// ---- Bandwidth history & peer stats ----

// BandwidthHistory returns the trailing windowMs of bandwidth history (0 =
// everything) aggregated to at most maxBuckets buckets. The chart polls this
// at a per-range cadence.
func (a *App) BandwidthHistory(windowMs int64, maxBuckets int) stats.HistoryResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	empty := stats.HistoryResult{Buckets: []stats.Bucket{}}
	switch a.mode {
	case ModeEngine:
		if a.eng != nil {
			return a.eng.History(windowMs, maxBuckets)
		}
	case ModeAttached:
		if a.client == nil || a.historyUnsupported {
			return empty
		}
		h, err := a.client.History(windowMs, maxBuckets)
		if err != nil {
			// An old daemon never answers this request type; the call times
			// out once and we degrade for the rest of the attachment.
			a.logger.Warn("daemon does not serve bandwidth history (older version?)", "err", err)
			a.historyUnsupported = true
			return empty
		}
		if h.Buckets == nil {
			h.Buckets = []stats.Bucket{}
		}
		return *h
	}
	return empty
}

// PeerStats returns per-client lifetime records, most recently seen first.
func (a *App) PeerStats() []stats.PeerStat {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch a.mode {
	case ModeEngine:
		if a.eng != nil {
			return a.eng.Peers()
		}
	case ModeAttached:
		if a.client == nil || a.historyUnsupported {
			return []stats.PeerStat{}
		}
		peers, err := a.client.Peers()
		if err != nil {
			a.logger.Warn("daemon does not serve peer stats (older version?)", "err", err)
			a.historyUnsupported = true
			return []stats.PeerStat{}
		}
		return peers
	}
	return []stats.PeerStat{}
}

// ---- Windows integration ----

// FirewallStatus reports whether the inbound rule exists.
func (a *App) FirewallStatus() (bool, error) {
	return svc.FirewallRulePresent()
}

// FirewallRepair (re-)creates the inbound rule via the elevation helper.
func (a *App) FirewallRepair() error {
	if svc.IsElevated() {
		return svc.AddFirewallRule()
	}
	return svc.RunElevatedTask(svc.TaskAddFirewall)
}

// ServiceStatus reports the Windows service state.
func (a *App) ServiceStatus() (string, error) {
	return svc.ServiceStatus()
}

// InstallService installs the Windows service via the elevation helper. The
// GUI should prompt the user to close afterwards (the service takes over).
func (a *App) InstallService() error {
	if svc.IsElevated() {
		return svc.InstallService(nil)
	}
	return svc.RunElevatedTask(svc.TaskInstallService)
}

// UninstallService removes the Windows service via the elevation helper.
func (a *App) UninstallService() error {
	if svc.IsElevated() {
		return svc.UninstallService()
	}
	return svc.RunElevatedTask(svc.TaskUninstallService)
}

// StartService starts the installed Windows service via the elevation helper.
// SCM control needs admin even to start a service someone else installed, so
// this takes the same elevated path as install/uninstall.
func (a *App) StartService() error {
	if svc.IsElevated() {
		return svc.StartService()
	}
	return svc.RunElevatedTask(svc.TaskStartService)
}

// StopService stops the running Windows service via the elevation helper. In
// service mode this is what the GUI's Stop reaches for — the run belongs to the
// service, and stopping the service is how you turn it off.
func (a *App) StopService() error {
	if svc.IsElevated() {
		return svc.StopService()
	}
	return svc.RunElevatedTask(svc.TaskStopService)
}

// ---- Tools ----

// TestReachability checks the full public path of a tunnel: dial the
// gateway's public port over the real network, exactly like a player would.
func (a *App) TestReachability(tunnelID string) (string, error) {
	a.mu.Lock()
	if a.cfg.Role != config.RoleAgent {
		a.mu.Unlock()
		return "", fmt.Errorf("the reachability test runs from the agent side")
	}
	host := a.cfg.Agent.GatewayHost
	var port int
	var ok bool
	if a.eng != nil && a.eng.Agent != nil {
		port, ok = a.eng.Agent.TunnelPublicPort(tunnelID)
	}
	a.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("tunnel is not live (no confirmed public port)")
	}
	return testReachability(host, port)
}

// LatencyResult is the outcome of the one-click latency test. Round-trip
// values are always meaningful; the one-way estimate is half the RTT, and the
// raw per-direction values (OneWayUp/Down) are only populated when the gateway
// echoed its receive time — they depend on both clocks being NTP-synced, which
// the frontend surfaces via ClockSyncCaveat.
type LatencyResult struct {
	Samples          int     `json:"samples"`
	RTTAvgMs         float64 `json:"rttAvgMs"`
	RTTMinMs         float64 `json:"rttMinMs"`
	RTTMaxMs         float64 `json:"rttMaxMs"`
	JitterMs         float64 `json:"jitterMs"`
	OneWayEstimateMs float64 `json:"oneWayEstimateMs"`
	OneWayUpMs       float64 `json:"oneWayUpMs"`
	OneWayDownMs     float64 `json:"oneWayDownMs"`
	HaveOneWay       bool    `json:"haveOneWay"`
	ClockSyncCaveat  bool    `json:"clockSyncCaveat"`
}

// MeasureLatency runs an on-demand latency burst on the live link and reports
// round-trip stats, a ½-RTT one-way estimate, and (when available) raw
// per-direction one-way latency. Works from either role — the agent probes the
// gateway, the gateway probes the agent. Engine mode only.
func (a *App) MeasureLatency() (LatencyResult, error) {
	a.mu.Lock()
	var probe func(ctx context.Context, count int, interval time.Duration) (linkquality.ProbeResult, error)
	if a.eng != nil {
		switch {
		case a.eng.Agent != nil:
			probe = a.eng.Agent.ProbeLatency
		case a.eng.Gateway != nil:
			probe = a.eng.Gateway.ProbeLatency
		}
	}
	a.mu.Unlock()

	if probe == nil {
		return LatencyResult{}, fmt.Errorf("latency test needs the in-process engine (unavailable while attached to a service daemon)")
	}

	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	res, err := probe(ctx, 10, 150*time.Millisecond)
	if err != nil {
		return LatencyResult{}, err
	}

	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	out := LatencyResult{
		Samples:          res.Samples,
		RTTAvgMs:         ms(res.RTTAvg),
		RTTMinMs:         ms(res.RTTMin),
		RTTMaxMs:         ms(res.RTTMax),
		JitterMs:         ms(res.Jitter),
		OneWayEstimateMs: ms(res.RTTAvg) / 2,
		HaveOneWay:       res.HaveOneWay,
	}
	if res.HaveOneWay {
		out.OneWayUpMs = ms(res.OneWayUp)
		out.OneWayDownMs = ms(res.OneWayDown)
		out.ClockSyncCaveat = true
	}
	return out, nil
}

// ExportDiagnostics writes a support bundle (logs + redacted config +
// version) to a user-chosen path and returns it.
func (a *App) ExportDiagnostics() (string, error) {
	defaultName := fmt.Sprintf("proxyforward-diagnostics-%s.zip", time.Now().Format("20060102-150405"))
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: defaultName,
		Title:           "Export diagnostics bundle",
		Filters:         []runtime.FileFilter{{DisplayName: "Zip archives", Pattern: "*.zip"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	a.mu.Lock()
	cfg := *a.cfg
	var status *ipc.Status
	if a.eng != nil {
		st := a.eng.Status()
		status = &st
	}
	a.mu.Unlock()
	if err := writeDiagnostics(path, &cfg, a.configDir, status, a.ring); err != nil {
		return "", err
	}
	return path, nil
}

// LogsSince returns ring log entries newer than seq; the frontend polls this
// at its own cadence instead of receiving push events per line.
func (a *App) LogsSince(seq uint64) []logging.Entry {
	if a.ring == nil {
		return nil
	}
	return a.ring.EntriesSince(seq)
}

// Version returns the build version for the About view.
func (a *App) Version() string {
	return version.String()
}
