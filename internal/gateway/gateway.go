// Package gateway implements the Server B role: a TLS control listener that
// agents dial into, and per-tunnel public listeners that Minecraft clients
// connect to.
//
// Concurrency model: all session/listener lifecycle state is owned by a
// single actor goroutine (see actor.go). Connection handlers ask the actor
// to admit sessions and bind/unbind listeners; the data path itself never
// enters the actor.
package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"proxyforward/internal/bwcap"
	"proxyforward/internal/config"
	"proxyforward/internal/conntrack"
	"proxyforward/internal/control"
	"proxyforward/internal/link"
	"proxyforward/internal/linkquality"
	"proxyforward/internal/mc"
	"proxyforward/internal/mcsniff"
	"proxyforward/internal/netid"
	"proxyforward/internal/relay"
	"proxyforward/internal/stats"
	"proxyforward/internal/tcpinfo"
	"proxyforward/internal/transport"
	"proxyforward/internal/version"
)

const (
	// preAuthTimeout bounds the whole unauthenticated prologue: TCP accept →
	// TLS handshake → hello frame. Internet scanners hitting the control
	// port hold resources for at most this long.
	preAuthTimeout = 10 * time.Second
	// controlIdleTimeout is the liveness read deadline; the agent pings
	// every 5s, so 15s of silence means the link is gone.
	controlIdleTimeout = 15 * time.Second
	// controlWriteTimeout bounds any single control-frame write.
	controlWriteTimeout = 10 * time.Second
	// offlineServeTimeout bounds one offline MOTD exchange with a player.
	offlineServeTimeout = 10 * time.Second

	// The gateway pings the agent on this cadence so it measures the same
	// RTT/jitter/loss the agent does. lossWindow/lossTimeout mirror the agent:
	// a pong not seen within two intervals counts as lost.
	pingInterval = 5 * time.Second
	lossWindow   = linkquality.DefaultWindow
	lossTimeout  = 2 * pingInterval

	// rttSampleInterval is how often the gateway reads each live public
	// connection's kernel RTT and reports it to the agent.
	rttSampleInterval = 5 * time.Second
)

// rttConn is one live public connection tracked for RTT sampling: the raw
// player-facing socket the kernel measures, and the registry entry that
// carries the value into this gateway's own GUI and analytics.
type rttConn struct {
	tcp   *net.TCPConn
	entry *conntrack.Entry
}

type Gateway struct {
	cfg    *config.Config
	dir    string // config dir (certs live here)
	logger *slog.Logger

	fingerprint string
	controlLn   net.Listener
	// quicLn is the UDP QUIC control listener bound alongside controlLn on the
	// same port when Gateway.QUICEnabled; nil otherwise. Its Transport and socket
	// are shared by every QUIC agent session (closed once, on Shutdown).
	quicLn *transport.QUICListener

	// actor is published by Start while status surfaces (the engine's stats
	// sampler, the GUI) may already be polling: the reads below genuinely race
	// the write, so the pointer is atomic. Plain-pointer publication also let a
	// reader observe a half-constructed actor.
	actor   atomic.Pointer[actor]
	authLim *authLimiter
	gate    *connGate
	// validator authenticates every hello, on the control accept path and (with
	// the per-conn data plane) the data accept path. It is a compositeValidator:
	// per-agent Ed25519 identity backed by agents, with the shared token as a
	// migration fallback.
	validator Validator
	// agents is the per-agent identity allowlist + outstanding enrollment tickets;
	// gatewayID is this gateway's derived gw_ display label (from its cert).
	agents    *AgentStore
	gatewayID string

	// Conns tracks live proxied connections for the GUI.
	Conns *conntrack.Registry
	// connSeq issues the per-connection correlation ids (ConnKey / OpenConn
	// ConnID) ahead of registry insertion, so the key is set before the entry
	// is visible to anyone.
	connSeq atomic.Uint64
	// linkTotals counts raw control-link bytes across all agent sessions of
	// this process (control conns and per-conn data conns alike).
	linkTotals stats.LinkCounters

	// pendingData holds per-conn data connections the gateway has asked agents
	// to dial back, keyed by the global connID, awaiting their match. Kept off
	// the actor so the data accept path matches in O(1) without a lifecycle
	// round-trip.
	pendingData sync.Map

	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func New(cfg *config.Config, dir string, logger *slog.Logger) *Gateway {
	return &Gateway{cfg: cfg, dir: dir, logger: logger, Conns: conntrack.NewRegistry()}
}

// Run is the blocking entrypoint used by the CLI.
func Run(ctx context.Context, cfg *config.Config, dir string, logger *slog.Logger) error {
	return RunStarted(ctx, New(cfg, dir, logger), cfg, logger)
}

// RunStarted runs an already-constructed Gateway to completion: start, print
// the pairing code, wait for ctx, shut down. Callers that need the instance
// (status surfaces) construct it themselves and pass it in.
func RunStarted(ctx context.Context, g *Gateway, cfg *config.Config, logger *slog.Logger) error {
	if err := g.Start(ctx); err != nil {
		return err
	}
	code := link.PairingCode{
		Host:        "YOUR-PUBLIC-ADDRESS",
		Port:        g.ControlAddr().(*net.TCPAddr).Port,
		Token:       cfg.Gateway.Token,
		Fingerprint: g.Fingerprint(),
	}
	logger.Info("gateway ready — pair agents with the code below (replace YOUR-PUBLIC-ADDRESS with this machine's public hostname or IP)")
	logger.Info("pairing code", "code", code.String())
	<-ctx.Done()
	g.Shutdown()
	return nil
}

// Start binds the control listener and spawns the accept/actor loops.
func (g *Gateway) Start(ctx context.Context) error {
	cert, fp, err := link.LoadOrCreateCert(g.dir)
	if err != nil {
		return err
	}
	g.fingerprint = fp
	g.gatewayID = link.GatewayID(cert.Certificate[0])
	store, err := LoadAgentStore(g.dir)
	if err != nil {
		return err
	}
	g.agents = store

	addr := net.JoinHostPort(g.cfg.Gateway.BindAddr, strconv.Itoa(g.cfg.Gateway.ControlPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind control port: %w", err)
	}
	g.controlLn = tls.NewListener(ln, link.GatewayTLSConfig(cert))

	runCtx, cancel := context.WithCancel(ctx)
	g.cancel = cancel
	g.authLim = newAuthLimiter(g.cfg.Gateway.AuthAttemptsPerMin)
	g.gate = newConnGate(g.cfg.Gateway.MaxConnsGlobal, g.cfg.Gateway.MaxConnsPerIP)
	var shared *sharedTokenValidator
	if g.cfg.Gateway.AcceptSharedToken {
		shared = &sharedTokenValidator{token: g.cfg.Gateway.Token}
	}
	g.validator = compositeValidator{
		identity: identityValidator{store: g.agents, now: time.Now},
		shared:   shared,
	}
	a := newActor(g.logger)
	g.actor.Store(a)
	g.wg.Add(2)
	go func() {
		defer g.wg.Done()
		a.run(runCtx)
	}()
	go func() {
		defer g.wg.Done()
		g.acceptControl(runCtx)
	}()
	if g.cfg.Gateway.QUICEnabled {
		// A QUIC bind failure must never take the gateway down — TCP transports
		// still serve every agent. Log and carry on with QUIC disabled.
		if err := g.startQUIC(runCtx, cert); err != nil {
			g.logger.Error("quic control listener disabled: bind failed", "err", err)
		}
	}
	g.logger.Info("control listener up", "addr", g.controlLn.Addr().String(), "fingerprint", fp)
	return nil
}

// startQUIC binds a UDP socket on the TCP control port and starts accepting QUIC
// agent sessions on it. TCP and UDP port spaces are independent, so the shared
// port number keeps the pairing code (one host:port) valid for both transports.
func (g *Gateway) startQUIC(ctx context.Context, cert tls.Certificate) error {
	port := g.controlLn.Addr().(*net.TCPAddr).Port // resolved even when ControlPort==0
	udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(g.cfg.Gateway.BindAddr, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("resolve quic addr: %w", err)
	}
	pc, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("bind quic port: %w", err)
	}
	ln, err := transport.ListenQUIC(pc, link.GatewayTLSConfig(cert), &g.linkTotals)
	if err != nil {
		pc.Close()
		return err
	}
	g.quicLn = ln
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.acceptQUIC(ctx)
	}()
	g.logger.Info("quic control listener up", "addr", ln.Addr().String())
	return nil
}

// Shutdown stops accepting, evicts the current session (closing its
// listeners and splices), and waits for every goroutine to exit.
func (g *Gateway) Shutdown() {
	g.cancel()
	g.controlLn.Close()
	if g.quicLn != nil {
		// Joins quic-go's transport goroutines and releases the UDP socket; the
		// cancel above already unblocked acceptQUIC and every serving ctx.
		g.quicLn.Close()
	}
	g.wg.Wait()
}

func (g *Gateway) ControlAddr() net.Addr {
	if g.controlLn == nil {
		return nil // Start not called yet
	}
	return g.controlLn.Addr()
}
func (g *Gateway) Fingerprint() string { return g.fingerprint }

// GatewayID returns this gateway's derived gw_ display identity (empty before Start).
func (g *Gateway) GatewayID() string { return g.gatewayID }

// IssuePairingTicket mints a fresh enrollment ticket. reusable=false is single-use
// (the safe default); a zero exp never expires; scope restricts what the enrolling
// agent may bind.
func (g *Gateway) IssuePairingTicket(reusable bool, exp time.Time, scope Scope) (string, error) {
	if g.agents == nil {
		return "", fmt.Errorf("gateway not started")
	}
	return g.agents.IssueEnrollment(reusable, exp, scope)
}

// ListAgents returns the per-agent identity allowlist (nil before Start).
func (g *Gateway) ListAgents() []AgentRecord {
	if g.agents == nil {
		return nil
	}
	return g.agents.List()
}

// RevokeAgent removes an agent from the allowlist and immediately evicts any live
// session it holds, so revocation takes effect at once rather than only on the next
// connect. Reports whether the agent was found in the allowlist.
func (g *Gateway) RevokeAgent(agentID string) bool {
	if g.agents == nil {
		return false
	}
	ok := g.agents.Revoke(agentID)
	if a := g.act(); a != nil {
		// One actor hop: read the live session and evict it in the same fn. (Calling
		// a.session here would nest a.do inside a.do and deadlock.)
		a.do(func() {
			if s := a.agents[agentID]; s != nil {
				a.evict(s, "agent revoked")
			}
		})
	}
	return ok
}

// RenameAgent sets an agent's display nickname; SetAgentScope replaces its bind
// scope. Both report whether the agent was found.
func (g *Gateway) RenameAgent(agentID, nickname string) bool {
	return g.agents != nil && g.agents.Rename(agentID, nickname)
}

func (g *Gateway) SetAgentScope(agentID string, scope Scope) bool {
	return g.agents != nil && g.agents.SetScope(agentID, scope)
}

// AgentView is one agent's management row for the GUI roster: its persisted
// identity and policy joined with live-session status. An enrolled agent that is
// offline still appears (Connected=false); a live shared-token agent that never
// enrolled appears too (Enrolled=false). Scope is flattened to slices so the Wails
// binding generator can model it without a nested cross-package type.
type AgentView struct {
	AgentID       string   `json:"agentId"`
	Nickname      string   `json:"nickname"`
	Enrolled      bool     `json:"enrolled"`
	Revoked       bool     `json:"revoked"`
	ScopePorts    []int    `json:"scopePorts"`
	ScopeTunnels  []string `json:"scopeTunnels"`
	IssuedAtMs    int64    `json:"issuedAtMs"`
	Connected     bool     `json:"connected"`
	Hostname      string   `json:"hostname"`
	RemoteIP      string   `json:"remoteIp"`
	LinkUpSinceMs int64    `json:"linkUpSinceMs"`
	Tunnels       int      `json:"tunnels"`
}

// ListAgentViews joins the enrollment allowlist with live sessions and tunnel
// counts into the roster the GUI polls: every enrolled agent (online or not) plus
// any connected shared-token agent that isn't enrolled, sorted by agentID.
func (g *Gateway) ListAgentViews() []AgentView {
	live := map[string]AgentLink{}
	for _, l := range g.Agents() {
		live[l.AgentID] = l
	}
	tunCount := map[string]int{}
	for _, t := range g.Tunnels() {
		tunCount[t.AgentID]++
	}
	seen := map[string]bool{}
	var out []AgentView
	for _, r := range g.ListAgents() {
		seen[r.AgentID] = true
		v := AgentView{
			AgentID:      r.AgentID,
			Nickname:     r.Nickname,
			Enrolled:     true,
			Revoked:      r.Revoked,
			ScopePorts:   r.Scope.Ports,
			ScopeTunnels: r.Scope.TunnelIDs,
			IssuedAtMs:   r.IssuedAt.UnixMilli(),
			Tunnels:      tunCount[r.AgentID],
		}
		if l, ok := live[r.AgentID]; ok {
			v.Connected, v.Hostname, v.RemoteIP, v.LinkUpSinceMs = true, l.Hostname, l.RemoteIP, l.LinkUpSinceMs
		}
		out = append(out, v)
	}
	for id, l := range live {
		if seen[id] {
			continue
		}
		out = append(out, AgentView{
			AgentID: id, Enrolled: false, Connected: true,
			Hostname: l.Hostname, RemoteIP: l.RemoteIP, LinkUpSinceMs: l.LinkUpSinceMs,
			Tunnels: tunCount[id],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out
}

// TunnelPort reports the actual bound public port of a tunnel (0, false if
// not currently bound). Used by status surfaces and tests.
func (g *Gateway) TunnelPort(tunnelID string) (int, bool) {
	a := g.act()
	if a == nil {
		return 0, false // Start not called yet (status polled early)
	}
	return a.tunnelPort(tunnelID)
}

// Tunnels enumerates the currently registered tunnels and their bound ports.
func (g *Gateway) Tunnels() []TunnelSnapshot {
	a := g.act()
	if a == nil {
		return nil // Start not called yet (status polled early)
	}
	return a.tunnels()
}

// Events returns the notable auto-fixes/conflicts (port reassignment, suspected
// clone) recorded since the cursor, oldest first — the incremental feed the GUI
// event log polls. A zero cursor returns the whole retained ring.
func (g *Gateway) Events(sinceSeq uint64) []GatewayEvent {
	a := g.act()
	if a == nil {
		return nil // Start not called yet (status polled early)
	}
	return a.eventsSince(sinceSeq)
}

// AgentConnected reports whether any agent session is currently admitted.
func (g *Gateway) AgentConnected() bool {
	a := g.act()
	return a != nil && len(a.sessions()) > 0
}

// AgentID reports the connected agent's identity, or "" when no agent is
// connected. Used to attribute link/uptime events to their agent.
func (g *Gateway) AgentID() string {
	if sess := g.session(); sess != nil {
		return sess.agentID
	}
	return ""
}

// AgentLinkUpSinceMs reports when the current agent session was admitted
// (unix millis), or 0 when no agent is connected.
func (g *Gateway) AgentLinkUpSinceMs() int64 {
	if sess := g.session(); sess != nil {
		return sess.connectedAt.UnixMilli()
	}
	return 0
}

// AgentRemoteIP reports the connected agent's IP, or "" when disconnected.
func (g *Gateway) AgentRemoteIP() string {
	if sess := g.session(); sess != nil {
		return sess.remoteIP
	}
	return ""
}

// AgentHostname reports the connected agent machine's hostname, or "" when
// disconnected or the agent is a legacy build that sent no hostname.
func (g *Gateway) AgentHostname() string {
	if sess := g.session(); sess != nil {
		return sess.hostname
	}
	return ""
}

// AgentLANIPs reports the connected agent machine's LAN IPv4s, or nil.
func (g *Gateway) AgentLANIPs() []string {
	if sess := g.session(); sess != nil {
		return sess.localIPs
	}
	return nil
}

// RTTMillis reports the gateway's own measured round-trip to the agent, or 0
// when no agent is connected. Symmetric with the agent's RTT.
func (g *Gateway) RTTMillis() int64 {
	if sess := g.session(); sess != nil {
		return sess.rttMillis.Load()
	}
	return 0
}

// JitterMillis reports the gateway→agent jitter EWMA in ms, or -1 when unknown.
func (g *Gateway) JitterMillis() float64 {
	if sess := g.session(); sess != nil && sess.quality != nil {
		return sess.quality.JitterMillis()
	}
	return -1
}

// PacketLossPct reports the gateway→agent ping loss (0–100), or -1 when unknown.
func (g *Gateway) PacketLossPct() float64 {
	if sess := g.session(); sess != nil && sess.quality != nil {
		return sess.quality.LossPct()
	}
	return -1
}

// AgentLink is one connected agent's link state for the multi-agent status
// surface. The engine maps it to ipc.AgentStatus (adding health + counts).
type AgentLink struct {
	AgentID       string
	Hostname      string
	LANIPs        []string
	RemoteIP      string
	LinkUpSinceMs int64
	RTTMillis     int64
	JitterMillis  float64
	PacketLossPct float64
	LinkBytesIn   int64
	LinkBytesOut  int64
}

// Agents returns every connected agent's link state, sorted by agentID so the
// status surface (and any legacy first-agent fallback) is deterministic.
func (g *Gateway) Agents() []AgentLink {
	a := g.act()
	if a == nil {
		return nil
	}
	sessions := a.sessions()
	out := make([]AgentLink, 0, len(sessions))
	for _, s := range sessions {
		in, outB := s.link.Bytes()
		jitter, loss := -1.0, -1.0
		if s.quality != nil {
			jitter, loss = s.quality.JitterMillis(), s.quality.LossPct()
		}
		out = append(out, AgentLink{
			AgentID:       s.agentID,
			Hostname:      s.hostname,
			LANIPs:        s.localIPs,
			RemoteIP:      s.remoteIP,
			LinkUpSinceMs: s.connectedAt.UnixMilli(),
			RTTMillis:     s.rttMillis.Load(),
			JitterMillis:  jitter,
			PacketLossPct: loss,
			LinkBytesIn:   in,
			LinkBytesOut:  outB,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out
}

// AgentQuality reports one agent's link RTT (ms, -1 unknown) and packet loss
// (0–100, -1 unknown), the per-agent gauges for the bandwidth-history sampler.
// Returns (-1, -1) when that agent is not the connected session.
func (g *Gateway) AgentQuality(agentID string) (rttMs, lossPct float64) {
	sess := g.session()
	if sess == nil || sess.agentID != agentID {
		return -1, -1
	}
	rttMs = -1
	if r := sess.rttMillis.Load(); r > 0 {
		rttMs = float64(r)
	}
	lossPct = -1
	if sess.quality != nil {
		lossPct = sess.quality.LossPct()
	}
	return rttMs, lossPct
}

// ProbeLatency runs an on-demand latency burst toward the connected agent,
// mirroring the agent's own latency test. Errors when no agent is connected or
// a probe is already running.
func (g *Gateway) ProbeLatency(ctx context.Context, count int, interval time.Duration) (linkquality.ProbeResult, error) {
	if count <= 0 {
		count = 10
	}
	if interval <= 0 {
		interval = 150 * time.Millisecond
	}
	sess := g.session()
	if sess == nil {
		return linkquality.ProbeResult{}, errors.New("no agent connected — connect an agent before testing latency")
	}
	return sess.probeLatency(ctx, count, interval)
}

func (s *agentSession) probeLatency(ctx context.Context, count int, interval time.Duration) (linkquality.ProbeResult, error) {
	pc := linkquality.NewProbeCollector(count)
	if !s.probe.CompareAndSwap(nil, pc) {
		return linkquality.ProbeResult{}, errors.New("a latency test is already running")
	}
	defer s.probe.Store(nil)

	for i := 0; i < count; i++ {
		seq := s.pingSeq.Add(1)
		pc.Mark(seq)
		now := time.Now()
		s.quality.OnSent(seq, now)
		if err := s.writeControl(control.TypePing, control.Ping{Seq: seq, SentUnixNano: now.UnixNano()}); err != nil {
			return linkquality.ProbeResult{}, err
		}
		if i < count-1 {
			select {
			case <-ctx.Done():
				return linkquality.ProbeResult{}, ctx.Err()
			case <-time.After(interval):
			}
		}
	}

	select {
	case <-pc.Full():
	case <-ctx.Done():
		return linkquality.ProbeResult{}, ctx.Err()
	case <-time.After(3 * time.Second):
	}
	res := pc.Summarize()
	if res.Samples == 0 {
		return res, errors.New("no pong received — the link may have dropped")
	}
	return res, nil
}

// LinkSessionBytes reports raw link bytes of the current agent session.
func (g *Gateway) LinkSessionBytes() (in, out int64) {
	if sess := g.session(); sess != nil {
		return sess.link.Bytes()
	}
	return 0, 0
}

// LinkTotalBytes reports raw link bytes across every agent session of this
// process.
func (g *Gateway) LinkTotalBytes() (in, out int64) { return g.linkTotals.Bytes() }

// act returns the running actor, or nil before Start has published it.
func (g *Gateway) act() *actor { return g.actor.Load() }

// session returns the sole connected agent session, or nil when there are zero
// or several. The coarse single-agent status accessors funnel through it, so
// with several agents they read their "unknown" sentinels rather than name an
// arbitrary agent — honest until Phase 3's Status.Agents carries per-agent
// fidelity. Nil-safe before Start.
func (g *Gateway) session() *agentSession {
	a := g.act()
	if a == nil {
		return nil
	}
	ss := a.sessions()
	if len(ss) == 1 {
		return ss[0]
	}
	return nil
}

// TunnelLocalUp reports an agent's last word on a tunnel's local backend,
// scanning every connected agent for the tunnelID (globally-unique in
// practice); known is false when no agent has reported it yet.
func (g *Gateway) TunnelLocalUp(tunnelID string) (up, known bool) {
	a := g.act()
	if a == nil {
		return false, false
	}
	for _, sess := range a.sessions() {
		if v, ok := sess.health.Load(tunnelID); ok {
			return v.(bool), true
		}
	}
	return false, false
}

func (g *Gateway) acceptControl(ctx context.Context) {
	for {
		conn, err := g.controlLn.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			g.logger.Error("control accept failed", "err", err)
			return
		}
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			g.handleControlConn(ctx, conn)
		}()
	}
}

func (g *Gateway) acceptQUIC(ctx context.Context) {
	for {
		sess, err := g.quicLn.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			g.logger.Error("quic accept failed", "err", err)
			return
		}
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			g.handleQUICSession(ctx, sess)
		}()
	}
}

// handleQUICSession walks one accepted QUIC connection through the same pre-auth
// prologue and admission as handleControlConn, differing only in the transport:
// the QUIC (and thus TLS) handshake is already complete, so the hello rides the
// first stream instead of a raw TLS conn, and that same stream becomes the
// control stream. Pre-auth guards mirror the TCP path — per-IP rate limit, a
// preAuthTimeout deadline on the hello read, the PreAuthMaxFrame cap — and
// QUIC's own Retry/address validation covers amplification.
func (g *Gateway) handleQUICSession(ctx context.Context, sess transport.Session) {
	remote := sess.RemoteAddr().String()
	ip := ipFromAddr(sess.RemoteAddr())
	logger := g.logger.With("remote", remote, "transport", "quic")

	// --- Pre-auth boundary: rate limit before spending a stream on an unauth peer. ---
	if !g.authLim.allow(ip) {
		logger.Debug("pre-auth: rate limited after repeated failures")
		sess.Close()
		return
	}
	// The agent opens the control stream first; the hello is its first frame.
	ctrl, err := acceptStreamTimeout(sess, preAuthTimeout)
	if err != nil {
		logger.Debug("pre-auth: no control stream", "err", err)
		g.authLim.fail(ip)
		sess.Close()
		return
	}
	ctrl.SetReadDeadline(time.Now().Add(preAuthTimeout))
	env, err := control.ReadMsg(ctrl, control.PreAuthMaxFrame)
	if err != nil {
		logger.Debug("pre-auth read failed", "err", err)
		g.authLim.fail(ip)
		sess.Close()
		return
	}
	if env.Type != control.TypeHello {
		logger.Debug("pre-auth: first frame was not hello", "type", env.Type)
		g.authLim.fail(ip)
		sess.Close()
		return
	}
	hello, err := control.Decode[control.Hello](env)
	if err != nil {
		logger.Debug("pre-auth: bad hello", "err", err)
		g.authLim.fail(ip)
		sess.Close()
		return
	}
	if hello.ProtocolVersion != control.ProtocolVersion {
		control.WriteMsg(ctrl, control.TypeHelloErr, control.HelloErr{
			Code:    control.ErrCodeVersion,
			Message: fmt.Sprintf("gateway speaks protocol %d, agent sent %d — update the older side", control.ProtocolVersion, hello.ProtocolVersion),
		})
		sess.Close()
		return
	}
	identity, err := g.validator.Validate(hello, g.fingerprint)
	if err != nil {
		code, msg, countFail := authErrorReply(err)
		if countFail {
			g.authLim.fail(ip)
		}
		logger.Warn("agent rejected", "code", code)
		control.WriteMsg(ctrl, control.TypeHelloErr, control.HelloErr{Code: code, Message: msg})
		sess.Close()
		return
	}
	// QUIC has no per-conn dial-back — data streams ride this same connection, so
	// only a control kind is valid; a data hello would have nothing to match.
	if hello.Kind != control.KindControl {
		control.WriteMsg(ctrl, control.TypeHelloErr, control.HelloErr{
			Code:    control.ErrCodeVersion,
			Message: fmt.Sprintf("connection kind %q not supported over quic", hello.Kind),
		})
		sess.Close()
		return
	}

	// --- Admission (shared with the TCP path; conn is nil — the session's own
	// Close is the whole drain boundary). ---
	admitted, negotiated, err := g.buildAndAdmit(ctx, nil, hello, identity, ip, logger)
	if err != nil {
		sess.Close()
		return
	}
	defer g.act().disconnected(admitted)
	admitted.setSession(sess)
	defer sess.Close()

	gwHost, _ := os.Hostname()
	okMsg := control.HelloOK{
		ProtocolVersion:   control.ProtocolVersion,
		SessionGeneration: admitted.gen,
		AppVersion:        version.String(),
		Capabilities:      negotiated,
		Hostname:          gwHost,
		LocalIPs:          netid.LocalIPv4s(),
		ObservedIP:        ip,
	}
	if identity.PubKey != nil {
		okMsg.AssignedAgentID = identity.AgentID
		okMsg.GatewayID = g.gatewayID
		okMsg.ConfigSeedNeeded = g.needsConfigSeed(identity, negotiated)
	}
	if err := control.WriteMsg(ctrl, control.TypeHelloOK, okMsg); err != nil {
		logger.Debug("hello_ok write failed", "err", err)
		return
	}
	ctrl.SetReadDeadline(time.Time{}) // pre-auth over; liveness moves to the control stream

	g.serveAdmitted(ctx, admitted, ctrl, hello, negotiated)
}

// authErrorReply maps a validator error to a hello error code, a user-facing
// message, and whether it counts as a failed credential for the per-IP limiter.
func authErrorReply(err error) (code, msg string, countFail bool) {
	switch {
	case errors.Is(err, ErrRevoked):
		return control.ErrCodeRevoked, "this gateway revoked this agent — re-pair with a fresh code", true
	case errors.Is(err, ErrTicketUnknown), errors.Is(err, ErrTicketConsumed), errors.Is(err, ErrTicketExpired):
		return control.ErrCodeBadToken, err.Error(), true
	case errors.Is(err, ErrMissingAgentID):
		return control.ErrCodeBadToken, "hello is missing agentId", false
	default: // ErrBadToken and anything unexpected
		return control.ErrCodeBadToken, "invalid token — re-pair with the gateway's current pairing code", true
	}
}

// handleControlConn walks one agent connection through the pre-auth
// prologue, admission, and then serves its control stream until the session
// dies.
func (g *Gateway) handleControlConn(ctx context.Context, conn net.Conn) {
	remote := conn.RemoteAddr().String()
	ip := remoteIP(conn)
	logger := g.logger.With("remote", remote)

	// --- Pre-auth boundary: rate limit, strict deadline, tiny frame cap. ---
	if !g.authLim.allow(ip) {
		logger.Debug("pre-auth: rate limited after repeated failures")
		conn.Close()
		return
	}
	conn.SetDeadline(time.Now().Add(preAuthTimeout))
	env, err := control.ReadMsg(conn, control.PreAuthMaxFrame)
	if err != nil {
		logger.Debug("pre-auth read failed", "err", err)
		g.authLim.fail(ip)
		conn.Close()
		return
	}
	if env.Type != control.TypeHello {
		logger.Debug("pre-auth: first frame was not hello", "type", env.Type)
		g.authLim.fail(ip)
		conn.Close()
		return
	}
	hello, err := control.Decode[control.Hello](env)
	if err != nil {
		logger.Debug("pre-auth: bad hello", "err", err)
		g.authLim.fail(ip)
		conn.Close()
		return
	}
	if hello.ProtocolVersion != control.ProtocolVersion {
		control.WriteMsg(conn, control.TypeHelloErr, control.HelloErr{
			Code:    control.ErrCodeVersion,
			Message: fmt.Sprintf("gateway speaks protocol %d, agent sent %d — update the older side", control.ProtocolVersion, hello.ProtocolVersion),
		})
		conn.Close()
		return
	}
	identity, err := g.validator.Validate(hello, g.fingerprint)
	if err != nil {
		// A credential failure counts toward the per-IP auth limiter; a malformed
		// hello (missing agentID) does not.
		code, msg, countFail := authErrorReply(err)
		if countFail {
			g.authLim.fail(ip)
		}
		logger.Warn("agent rejected", "code", code)
		control.WriteMsg(conn, control.TypeHelloErr, control.HelloErr{Code: code, Message: msg})
		conn.Close()
		return
	}
	switch hello.Kind {
	case control.KindControl:
		// Falls through to admission below.
	case control.KindData:
		// Per-conn data plane: this is a dial-back for a waiting player. Match
		// it and hand off the raw conn; it never enters agent admission.
		g.handleDataConn(conn, hello, identity)
		return
	default:
		control.WriteMsg(conn, control.TypeHelloErr, control.HelloErr{
			Code:    control.ErrCodeVersion,
			Message: fmt.Sprintf("connection kind %q not supported", hello.Kind),
		})
		conn.Close()
		return
	}

	// --- Admission: supersede same agentID, admit distinct agents alongside. ---
	sess, negotiated, err := g.buildAndAdmit(ctx, conn, hello, identity, ip, logger)
	if err != nil {
		conn.Close()
		return
	}
	defer g.act().disconnected(sess)

	gwHost, _ := os.Hostname()
	okMsg := control.HelloOK{
		ProtocolVersion:   control.ProtocolVersion,
		SessionGeneration: sess.gen,
		AppVersion:        version.String(),
		Capabilities:      negotiated,
		Hostname:          gwHost,
		LocalIPs:          netid.LocalIPv4s(),
		ObservedIP:        ip,
	}
	if identity.PubKey != nil {
		okMsg.AssignedAgentID = identity.AgentID
		okMsg.GatewayID = g.gatewayID
		okMsg.ConfigSeedNeeded = g.needsConfigSeed(identity, negotiated)
	}
	if err := control.WriteMsg(conn, control.TypeHelloOK, okMsg); err != nil {
		logger.Debug("hello_ok write failed", "err", err)
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{}) // pre-auth over; liveness moves to the control stream

	// Count every post-auth byte on the link (yamux framing included) — the
	// "agent ↔ gateway" hop the GUI shows.
	mux, err := transport.NewMuxServer(stats.NewCountingConn(conn, &g.linkTotals, &sess.link))
	if err != nil {
		logger.Error("mux setup failed", "err", err)
		conn.Close()
		return
	}
	sess.setSession(mux)
	defer mux.Close()

	// The agent opens the control stream first; bound the wait so a stalled
	// peer can't hold a half-admitted session open.
	ctrl, err := acceptStreamTimeout(mux, preAuthTimeout)
	if err != nil {
		logger.Warn("agent never opened control stream", "err", err)
		return
	}
	g.serveAdmitted(ctx, sess, ctrl, hello, negotiated)
}

// buildAndAdmit intersects capabilities, constructs the agent session, and
// admits it on the actor. conn may be nil (the QUIC accept path, where the
// session's own Close is the whole drain boundary). On a transient admission
// refusal it cancels the session ctx and returns the error, leaving the caller
// to close its transport without a HelloOK so the agent retries with backoff.
// The returned session already has gen set.
// needsConfigSeed reports whether the gateway should ask an enrolled gateway-config
// agent to seed its tunnel set (send a propose_config). It is true only on first
// contact, when the gateway holds no authoritative config for this identity yet;
// afterwards the gateway is the source of truth and pushes, so the agent never
// volunteers a set (which would race the connect-time push).
func (g *Gateway) needsConfigSeed(identity Identity, negotiated []string) bool {
	if identity.PubKey == nil || !control.NewCapSet(negotiated).Has(control.CapGatewayConfig) {
		return false
	}
	_, gen, ok := g.agents.DesiredConfig(identity.AgentID)
	return ok && gen == 0
}

// withoutCap returns caps with one capability removed, preserving order. Used to
// negotiate away a capability the peer offered but this session can't honor.
func withoutCap(caps []string, drop string) []string {
	out := caps[:0:0]
	for _, c := range caps {
		if c != drop {
			out = append(out, c)
		}
	}
	return out
}

func (g *Gateway) buildAndAdmit(ctx context.Context, conn net.Conn, hello *control.Hello, identity Identity, peerIP string, logger *slog.Logger) (*agentSession, []string, error) {
	negotiated := control.IntersectCaps(hello.Capabilities, control.SupportedCapabilities)
	// Gateway-authoritative config is keyed to a durable Ed25519 identity, so it is
	// negotiated only for enrolled agents. A shared-token agent that offered the cap
	// has it dropped here and keeps the agent-push SyncTunnels path.
	enrolled := identity.PubKey != nil
	if !enrolled {
		negotiated = withoutCap(negotiated, control.CapGatewayConfig)
	}
	caps := control.NewCapSet(negotiated)
	// Session ctx: a child of the serving ctx, cancelled on eviction so throttled
	// splices unblock per-agent (evict) and on shutdown (parent).
	sctx, cancel := context.WithCancel(ctx)
	sess := &agentSession{
		agentID:     identity.AgentID,
		scope:       identity.Scope,
		conn:        conn,
		logger:      logger.With("agent", hello.AgentID),
		connectedAt: time.Now(),
		remoteIP:    peerIP,
		hostname:    hello.Hostname,
		localIPs:    hello.LocalIPs,
		caps:        caps,
		dp:          g.pickDataPlane(caps),
		quality:     linkquality.New(lossWindow),
		ctx:         sctx,
		cancel:      cancel,

		enrolled:           enrolled,
		reportedConfigHash: hello.ConfigHash,
	}
	sess.agentConfigGen.Store(hello.ConfigGeneration)
	gen, err := g.act().admit(sess)
	if err != nil {
		// The only admission refusals left are transient — anti-flap dampening
		// of a same-agentID contest, or a shutting-down gateway. Neither is fatal.
		logger.Warn("agent admission deferred", "agent", hello.AgentID, "err", err)
		cancel() // never entered the map, so evict won't cancel it
		return nil, nil, err
	}
	sess.gen = gen
	return sess, negotiated, nil
}

// serveAdmitted is the shared serving tail for both accept paths: it brackets
// serveControl with the connected/disconnected log lines. The caller has already
// published sess.session() and deferred the session teardown + actor disconnect.
func (g *Gateway) serveAdmitted(ctx context.Context, sess *agentSession, ctrl transport.Stream, hello *control.Hello, negotiated []string) {
	sess.logger.Info("agent connected", "generation", sess.gen, "agent_version", hello.AppVersion, "capabilities", negotiated)
	g.serveControl(ctx, sess, ctrl)
	sess.logger.Info("agent disconnected", "generation", sess.gen)
}

func acceptStreamTimeout(sess transport.Session, d time.Duration) (transport.Stream, error) {
	type result struct {
		st  transport.Stream
		err error
	}
	ch := make(chan result, 1)
	go func() {
		st, err := sess.AcceptStream()
		ch <- result{st, err}
	}()
	select {
	case r := <-ch:
		return r.st, r.err
	case <-time.After(d):
		sess.Close() // unblocks the goroutine above
		r := <-ch
		if r.err == nil {
			r.st.Close()
		}
		return nil, errors.New("timed out waiting for stream")
	}
}

// serveControl processes the agent's control stream until it dies. The read
// loop's responses and a concurrent ping goroutine both write, serialized by
// sess.writeControl.
func (g *Gateway) serveControl(ctx context.Context, sess *agentSession, ctrl transport.Stream) {
	defer ctrl.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sess.setCtrl(ctrl)
	defer sess.setCtrl(nil)
	go g.pingLoop(ctx, sess)
	go g.rttSampler(ctx, sess)
	// A fresh session starts with no listeners, so an enrolled gateway-config agent
	// gets its authoritative set reconciled (and pushed on drift) before the read
	// loop; a legacy/shared-token agent instead pushes its own set via SyncTunnels.
	if sess.enrolled && sess.Has(control.CapGatewayConfig) {
		g.pushConfigOnConnect(sess)
	}
	for {
		if ctx.Err() != nil {
			return
		}
		ctrl.SetReadDeadline(time.Now().Add(controlIdleTimeout))
		env, err := control.ReadMsg(ctrl, control.MaxFrame)
		if err != nil {
			if ctx.Err() == nil {
				sess.logger.Debug("control stream ended", "err", err)
			}
			return
		}
		if err := g.handleControlMsg(sess, ctrl, env); err != nil {
			sess.logger.Warn("control message failed", "type", env.Type, "err", err)
			return
		}
	}
}

// pingLoop sends periodic pings to the agent so the gateway can measure its own
// RTT/jitter/loss. It exits when the control stream context is cancelled.
func (g *Gateway) pingLoop(ctx context.Context, sess *agentSession) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sess.quality.Sweep(time.Now(), lossTimeout)
			seq := sess.pingSeq.Add(1)
			now := time.Now()
			sess.quality.OnSent(seq, now)
			if err := sess.writeControl(control.TypePing, control.Ping{Seq: seq, SentUnixNano: now.UnixNano()}); err != nil {
				return // stream is going away; the read loop will notice too
			}
		}
	}
}

// rttSampler reads each live public connection's kernel RTT every
// rttSampleInterval, stamps it on the local registry entry (feeding this
// gateway's own GUI and analytics), and — when the agent negotiated
// CapConnStats — reports the batch so the agent attributes the same RTT to its
// player. Bound to the session context; ends when the session does.
func (g *Gateway) rttSampler(ctx context.Context, sess *agentSession) {
	t := time.NewTicker(rttSampleInterval)
	defer t.Stop()
	report := sess.Has(control.CapConnStats)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var batch []control.ConnStat
			sess.rttConns.Range(func(k, v any) bool {
				rc := v.(*rttConn)
				d, ok := tcpinfo.RTT(rc.tcp)
				if !ok {
					return true
				}
				ms := float64(d) / float64(time.Millisecond)
				rc.entry.SetRTT(ms)
				if report {
					batch = append(batch, control.ConnStat{ConnID: k.(string), RttMs: ms})
				}
				return true
			})
			// Chunk so a busy gateway never approaches MaxFrame.
			for i := 0; i < len(batch); i += control.MaxConnStatsPerFrame {
				end := i + control.MaxConnStatsPerFrame
				if end > len(batch) {
					end = len(batch)
				}
				if err := sess.writeControl(control.TypeConnStats, control.ConnStats{Entries: batch[i:end]}); err != nil {
					return // stream is going away; the read loop will notice too
				}
			}
		}
	}
}

func (g *Gateway) handleControlMsg(sess *agentSession, ctrl transport.Stream, env *control.Envelope) error {
	write := func(msgType string, payload any) error {
		return sess.writeControl(msgType, payload)
	}
	switch env.Type {
	case control.TypePing:
		ping, err := control.Decode[control.Ping](env)
		if err != nil {
			return err
		}
		return write(control.TypePong, control.Pong{Seq: ping.Seq, SentUnixNano: ping.SentUnixNano, RecvUnixNano: time.Now().UnixNano()})

	case control.TypePong:
		// Reply to our own ping — measure RTT and feed jitter/loss + any probe.
		pong, err := control.Decode[control.Pong](env)
		if err != nil {
			return err
		}
		now := time.Now()
		rtt := now.Sub(time.Unix(0, pong.SentUnixNano))
		// Clamp to ≥1ms: 0 is the "no measurement" sentinel everywhere
		// (status, stats gauges), and a sub-millisecond LAN round-trip must
		// not truncate into it and read as unknown.
		sess.rttMillis.Store(max(1, rtt.Milliseconds()))
		sess.quality.OnPong(pong.Seq, rtt)
		if pc := sess.probe.Load(); pc != nil {
			pc.Record(*pong, now)
		}
		return nil

	case control.TypeRegister:
		reg, err := control.Decode[control.Register](env)
		if err != nil {
			return err
		}
		port, bindErr := g.bindTunnel(sess, reg.Tunnel)
		if bindErr != nil {
			sess.logger.Warn("tunnel register failed", "tunnel", reg.Tunnel.Name, "port", reg.Tunnel.PublicPort, "err", bindErr.msg)
			return write(control.TypeRegErr, control.RegisterErr{
				TunnelID: reg.Tunnel.ID, Code: bindErr.code, Message: bindErr.msg,
			})
		}
		sess.logger.Info("tunnel registered", "tunnel", reg.Tunnel.Name, "public_port", port)
		return write(control.TypeRegisterOK, control.RegisterOK{TunnelID: reg.Tunnel.ID, PublicPort: port})

	case control.TypeUnregister:
		unreg, err := control.Decode[control.Unregister](env)
		if err != nil {
			return err
		}
		g.act().unbindTunnel(sess, unreg.TunnelID)
		sess.logger.Info("tunnel unregistered", "tunnel_id", unreg.TunnelID)
		return nil

	case control.TypeSyncTunnels:
		if !sess.Has(control.CapTunnelSync) {
			// Defensive: sync without negotiation is a peer bug, not fatal.
			sess.logger.Warn("ignoring sync_tunnels: capability not negotiated")
			return nil
		}
		sync, err := control.Decode[control.SyncTunnels](env)
		if err != nil {
			return err
		}
		return write(control.TypeSyncResult, g.syncTunnels(sess, sync))

	case control.TypeProposeConfig:
		// The agent promotes a local edit (or seeds the gateway on first contact).
		// Only enrolled gateway-config sessions reach the authoritative store.
		if !sess.enrolled || !sess.Has(control.CapGatewayConfig) {
			sess.logger.Warn("ignoring propose_config: not an enrolled gateway-config session")
			return nil
		}
		prop, err := control.Decode[control.ProposeConfig](env)
		if err != nil {
			return err
		}
		g.adoptProposal(sess, prop.Tunnels)
		return nil

	case control.TypeConfigAck:
		// The agent confirms it applied a pushed generation; track it so a later
		// propose is checked against the right basis.
		ack, err := control.Decode[control.ConfigAck](env)
		if err != nil {
			return err
		}
		sess.agentConfigGen.Store(ack.Generation)
		sess.logger.Debug("agent acked config", "generation", ack.Generation)
		return nil

	case control.TypeHealth:
		h, err := control.Decode[control.Health](env)
		if err != nil {
			return err
		}
		sess.health.Store(h.TunnelID, h.LocalUp)
		sess.logger.Debug("tunnel health", "tunnel_id", h.TunnelID, "local_up", h.LocalUp)
		return nil

	default:
		// Unknown types are tolerated for forward compatibility.
		sess.logger.Debug("ignoring unknown control message", "type", env.Type)
		return nil
	}
}

type bindError struct {
	code string
	msg  string
}

// validateSpec checks a tunnel spec against protocol and policy limits; it is
// shared by the legacy register path and the desired-state sync path.
func (g *Gateway) validateSpec(spec control.TunnelSpec, scope Scope) *bindError {
	if spec.ID == "" || spec.Type != config.TunnelTCP {
		return &bindError{control.ErrCodeBadTunnel, fmt.Sprintf("tunnel %q: only tcp tunnels with an id are supported", spec.Name)}
	}
	if spec.PublicPort < 0 || spec.PublicPort > 65535 {
		return &bindError{control.ErrCodeBadTunnel, fmt.Sprintf("tunnel %q: invalid public port %d", spec.Name, spec.PublicPort)}
	}
	// Per-agent scope: a scoped agent may bind only its allowed ports/tunnels. An
	// ephemeral (0) port is gateway-chosen, so it is not port-scoped (mirrors the
	// PortAllowlist rule below).
	if spec.PublicPort != 0 && !scope.AllowsPort(spec.PublicPort) {
		return &bindError{control.ErrCodePortNotAllowed, fmt.Sprintf("port %d is outside this agent's allowed ports", spec.PublicPort)}
	}
	if !scope.AllowsTunnel(spec.ID) {
		return &bindError{control.ErrCodeBadTunnel, fmt.Sprintf("tunnel %q is outside this agent's allowed scope", spec.ID)}
	}
	if len(g.cfg.Gateway.PortAllowlist) > 0 && spec.PublicPort != 0 {
		ok := false
		for _, p := range g.cfg.Gateway.PortAllowlist {
			if p == spec.PublicPort {
				ok = true
				break
			}
		}
		if !ok {
			return &bindError{control.ErrCodePortNotAllowed, fmt.Sprintf("port %d is not in the gateway's allowlist", spec.PublicPort)}
		}
	}
	return nil
}

// bindTunnel validates a spec and asks the actor to bind its public
// listener; accepted client conns are spliced onto fresh streams.
func (g *Gateway) bindTunnel(sess *agentSession, spec control.TunnelSpec) (int, *bindError) {
	if berr := g.validateSpec(spec, sess.scope); berr != nil {
		return 0, berr
	}
	port, err := g.act().bindTunnel(sess, spec, g.cfg.Gateway.BindAddr, g.cfg.Gateway.PortAllowlist, g.handleClient)
	if err != nil {
		return 0, &bindError{control.ErrCodePortInUse, err.Error()}
	}
	return port, nil
}

// syncTunnels applies a full desired tunnel set: invalid specs become error
// results without touching listener state; the rest reconcile atomically on
// the actor.
func (g *Gateway) syncTunnels(sess *agentSession, sync *control.SyncTunnels) control.SyncResult {
	results := make([]control.SyncTunnelResult, 0, len(sync.Tunnels))
	valid := make([]control.TunnelSpec, 0, len(sync.Tunnels))
	for _, spec := range sync.Tunnels {
		if berr := g.validateSpec(spec, sess.scope); berr != nil {
			sess.logger.Warn("tunnel sync rejected spec", "tunnel", spec.Name, "port", spec.PublicPort, "err", berr.msg)
			results = append(results, control.SyncTunnelResult{TunnelID: spec.ID, Code: berr.code, Message: berr.msg})
			continue
		}
		valid = append(valid, spec)
	}
	outcomes, ran := g.act().reconcile(sess, valid, g.cfg.Gateway.BindAddr, g.cfg.Gateway.PortAllowlist, g.handleClient)
	if !ran {
		for _, spec := range valid {
			results = append(results, control.SyncTunnelResult{TunnelID: spec.ID, Code: control.ErrCodePortInUse, Message: "gateway is shutting down"})
		}
		return control.SyncResult{Seq: sync.Seq, Results: results}
	}
	for _, o := range outcomes {
		if o.Err != nil {
			sess.logger.Warn("tunnel sync bind failed", "tunnel_id", o.ID, "err", o.Err)
			results = append(results, control.SyncTunnelResult{TunnelID: o.ID, Code: control.ErrCodePortInUse, Message: o.Err.Error()})
			continue
		}
		results = append(results, control.SyncTunnelResult{TunnelID: o.ID, OK: true, PublicPort: o.Port})
	}
	sess.logger.Info("tunnel set synced", "desired", len(sync.Tunnels), "live", len(valid))
	return control.SyncResult{Seq: sync.Seq, Results: results}
}

// validSpecs drops specs that fail validation against the agent's scope/policy,
// logging each, and returns the survivors — the set actually reconciled onto
// listeners under gateway-authoritative config.
func (g *Gateway) validSpecs(sess *agentSession, specs []control.TunnelSpec) []control.TunnelSpec {
	valid := make([]control.TunnelSpec, 0, len(specs))
	for _, spec := range specs {
		if berr := g.validateSpec(spec, sess.scope); berr != nil {
			sess.logger.Warn("config spec rejected", "tunnel", spec.Name, "port", spec.PublicPort, "err", berr.msg)
			continue
		}
		valid = append(valid, spec)
	}
	return valid
}

// resolvePorts returns specs with each PublicPort replaced by the port the gateway
// actually bound (from reconcile outcomes), so the stored + pushed config carries
// concrete ports even when the agent proposed an ephemeral (0) port.
func resolvePorts(specs []control.TunnelSpec, outcomes []reconcileOutcome) []control.TunnelSpec {
	bound := make(map[string]int, len(outcomes))
	for _, o := range outcomes {
		if o.Err == nil && o.Port != 0 {
			bound[o.ID] = o.Port
		}
	}
	out := make([]control.TunnelSpec, 0, len(specs))
	for _, s := range specs {
		if p, ok := bound[s.ID]; ok {
			s.PublicPort = p
		}
		out = append(out, s)
	}
	return out
}

// pushConfigOnConnect reconciles the gateway-authoritative tunnel set into this
// fresh session's listeners and, when the agent's reported view has drifted, pushes
// the authoritative config so the agent re-syncs. When the gateway holds no config
// yet (generation 0) it does nothing — the agent seeds it with a propose_config.
func (g *Gateway) pushConfigOnConnect(sess *agentSession) {
	stored, gen, ok := g.agents.DesiredConfig(sess.agentID)
	if !ok || gen == 0 {
		return // bootstrap: await the agent's seed propose_config
	}
	g.act().reconcile(sess, g.validSpecs(sess, stored), g.cfg.Gateway.BindAddr, g.cfg.Gateway.PortAllowlist, g.handleClient)
	hash := control.HashTunnels(stored)
	if sess.reportedConfigHash == hash && sess.agentConfigGen.Load() == gen {
		return // agent is already in sync; listeners are (re)bound, nothing to push
	}
	if err := sess.writeControl(control.TypePushConfig, control.PushConfig{Generation: gen, Hash: hash, Tunnels: stored}); err != nil {
		sess.logger.Warn("push_config on connect failed", "err", err)
		return
	}
	sess.logger.Info("pushed authoritative config", "generation", gen, "tunnels", len(stored))
}

// adoptProposal handles a propose_config from an enrolled gateway-config agent: a
// promoted local edit, or the seed on first contact. The gateway is authoritative,
// so a proposal is adopted only when it is based on the current generation (or the
// gateway holds no config yet); a stale proposal is refused and the authoritative
// set re-pushed, so config reconciles deterministically instead of last-write-wins.
func (g *Gateway) adoptProposal(sess *agentSession, proposed []control.TunnelSpec) {
	stored, storedGen, ok := g.agents.DesiredConfig(sess.agentID)
	if !ok {
		return // unknown agent — cannot happen on an enrolled session
	}
	if storedGen != 0 && sess.agentConfigGen.Load() != storedGen {
		sess.logger.Warn("refusing stale config proposal; re-pushing authoritative set",
			"agent_gen", sess.agentConfigGen.Load(), "gateway_gen", storedGen)
		hash := control.HashTunnels(stored)
		_ = sess.writeControl(control.TypePushConfig, control.PushConfig{Generation: storedGen, Hash: hash, Tunnels: stored})
		return
	}
	valid := g.validSpecs(sess, proposed)
	outcomes, ran := g.act().reconcile(sess, valid, g.cfg.Gateway.BindAddr, g.cfg.Gateway.PortAllowlist, g.handleClient)
	if !ran {
		return // gateway shutting down
	}
	resolved := resolvePorts(valid, outcomes)
	gen, ok := g.agents.AdoptConfig(sess.agentID, resolved)
	if !ok {
		return
	}
	sess.agentConfigGen.Store(gen)
	hash := control.HashTunnels(resolved)
	if err := sess.writeControl(control.TypePushConfig, control.PushConfig{Generation: gen, Hash: hash, Tunnels: resolved}); err != nil {
		sess.logger.Warn("push_config after propose failed", "err", err)
		return
	}
	sess.logger.Info("adopted proposed config", "generation", gen, "tunnels", len(resolved))
}

// handleClient runs per accepted public connection: open a stream to the
// agent, send the OpenConn header, splice. When there is no live session or the
// agent reports the local backend down, it answers with the tunnel's offline
// MOTD (when configured) instead of dropping the connection.
func (g *Gateway) handleClient(pl *publicListener, clientConn net.Conn) {
	sess, spec := pl.owner, pl.spec
	defer clientConn.Close()
	ip := remoteIP(clientConn)
	if !g.gate.admit(ip) {
		g.logger.Debug("public conn rejected: connection cap reached", "client", clientConn.RemoteAddr().String(), "tunnel", spec.Name)
		return
	}
	defer g.gate.release(ip)
	mux := sess.session()
	if mux == nil || backendDown(sess, spec.ID) {
		// No live session, or the agent reports the local server down: answer
		// with the offline MOTD when configured, else fall through to a clean
		// close.
		g.serveOffline(clientConn, spec)
		return
	}
	tcp, ok := clientConn.(*net.TCPConn)
	if !ok {
		return
	}
	// ConnID correlates this connection with the RTT reports the gateway sends
	// the agent; the entry's own key lets the local recorder attribute gateway
	// RTT samples. Issued before Open so the key is set before the entry is
	// published (ConnKey is immutable after Open). It also matches a per-conn
	// dial-back to this player, so it must be minted before the data leg.
	connID := strconv.FormatUint(g.connSeq.Add(1), 10)
	// Splice(client, stream): AToB is client→server, so inIsAToB=true.
	entry, closeEntry := g.Conns.Open(sess.agentID, spec.ID, spec.Name, clientConn.RemoteAddr().String(), connID, true)
	defer closeEntry()

	// Acquire the data leg through the session's data plane. Per-conn transport
	// signals the agent to dial back a dedicated TCP+TLS connection for this
	// player (no cross-player head-of-line blocking on the gateway↔agent hop);
	// mux transport opens a stream on the shared session. Either way the leg is a
	// relay.Conn spliced identically below, and an unavailable leg falls back to
	// the offline responder.
	stream, releaseFlow, err := sess.dp.openFlow(sess, connID)
	if err != nil {
		sess.logger.Debug("open data leg failed", "client", clientConn.RemoteAddr().String(), "err", err)
		g.serveOffline(clientConn, spec)
		return
	}
	defer releaseFlow()
	defer stream.Close()

	stream.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
	if err := control.WriteMsg(stream, control.TypeOpenConn, control.OpenConn{
		TunnelID:   spec.ID,
		ClientAddr: clientConn.RemoteAddr().String(),
		ConnID:     connID,
	}); err != nil {
		sess.logger.Debug("open_conn write failed", "err", err)
		return
	}
	stream.SetWriteDeadline(time.Time{})

	// Track for RTT sampling over this connection's lifetime.
	sess.rttConns.Store(connID, &rttConn{tcp: tcp, entry: entry})
	defer sess.rttConns.Delete(connID)

	// Minecraft-aware tunnels sniff the client's login handshake (read from
	// the player leg here) to attribute the connection to a player. Read-only:
	// the tap forwards bytes unchanged.
	var client relay.Conn = tcp
	if spec.MinecraftAware {
		client = mcsniff.Tap(tcp, entry)
	}
	// Splice(client, stream): AToB is client→server (inbound), BToA is
	// server→client (outbound). Parent on the session ctx so eviction cancels a
	// throttled WaitN.
	inbound, outbound := bwcap.Resolve(pl.limiters.Load())
	opts := relay.SpliceOpts{Ctx: sess.ctx, LimitAToB: inbound, LimitBToA: outbound}
	if err := relay.Splice(client, stream, entry.Counters, opts); err != nil {
		sess.logger.Debug("splice ended with error", "client", clientConn.RemoteAddr().String(), "err", err)
	}
}

// backendDown reports whether the agent has told us this tunnel's local server
// is unreachable. Unknown health (never probed) counts as up, so a working
// backend is never pre-empted by the offline responder.
func backendDown(sess *agentSession, tunnelID string) bool {
	v, ok := sess.health.Load(tunnelID)
	if !ok {
		return false
	}
	up, _ := v.(bool)
	return !up
}

// serveOffline answers a player with the tunnel's offline MOTD when the backend
// is unavailable. An empty OfflineMOTD means the feature is off, so it returns
// and lets handleClient's deferred Close drop the connection cleanly — the
// pre-existing behavior. mc.ServeOffline requires the caller to own the deadline
// and the close, both of which handleClient already does.
func (g *Gateway) serveOffline(conn net.Conn, spec control.TunnelSpec) {
	if spec.OfflineMOTD == "" {
		return
	}
	conn.SetDeadline(time.Now().Add(offlineServeTimeout))
	if err := mc.ServeOffline(conn, mc.OfflineInfo{MOTD: spec.OfflineMOTD}); err != nil {
		g.logger.Debug("offline responder ended", "tunnel", spec.Name, "err", err)
	}
}
