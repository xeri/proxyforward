// Package agent implements the Server A role: it dials out to the gateway
// (so Server A needs no port forwarding), registers tunnels, and splices
// accepted streams onto the local Minecraft server.
package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
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
	"proxyforward/internal/mcsniff"
	"proxyforward/internal/netid"
	"proxyforward/internal/netnotify"
	"proxyforward/internal/proxyproto"
	"proxyforward/internal/relay"
	"proxyforward/internal/stats"
	"proxyforward/internal/transport"
	"proxyforward/internal/version"
)

const (
	dialTimeout      = 10 * time.Second
	helloTimeout     = 10 * time.Second
	localDialTimeout = 5 * time.Second
	pingInterval     = 5 * time.Second
	// controlIdleTimeout: pongs arrive every pingInterval, so 15s of control
	// silence means the link is dead. Single liveness owner — yamux
	// keepalive is off.
	controlIdleTimeout  = 15 * time.Second
	controlWriteTimeout = 10 * time.Second
	openConnTimeout     = 10 * time.Second

	// lossWindow is how many finalized heartbeats the packet-loss ratio
	// averages over; lossTimeout is how long a ping waits for its pong before
	// counting as lost (two intervals tolerates one reorder without a false
	// positive, and lands well inside controlIdleTimeout).
	lossWindow  = 32
	lossTimeout = 2 * pingInterval
)

// Fatal configuration errors: retrying cannot fix these, so Run returns
// instead of hammering the gateway.
var (
	ErrBadToken = errors.New("gateway rejected our token — re-pair with the gateway's current pairing code")
	// ErrAgentConflict is retained for back-compat: a current gateway admits
	// several agents and never sends agent_conflict, but a legacy single-agent
	// gateway still can, and the agent must treat it as fatal rather than retry.
	ErrAgentConflict = errors.New("this gateway (older build) already serves a different agent — use a distinct gateway or agent identity")
	ErrVersion       = errors.New("protocol version mismatch — update the older side")
)

type Agent struct {
	cfg    *config.Config
	cfgMu  sync.RWMutex // guards cfg.Agent.Tunnels against hot-apply
	logger *slog.Logger

	// rttMillis is the latest heartbeat round-trip, for status surfaces.
	rttMillis atomic.Int64
	// peer holds the gateway's identity (hostname/LAN IPs) and our own public
	// IP as the gateway observed it, learned in the hello exchange. nil while
	// the link is down.
	peer atomic.Pointer[peerIdentity]
	// linkUp reflects an established, authenticated session.
	linkUp atomic.Bool
	// linkUpSinceMs is when the current session came up (unix ms; 0 = down).
	linkUpSinceMs atomic.Int64
	// linkTotals counts raw control-link bytes across all sessions of this
	// process; linkSession is the live session's counters (nil while down).
	linkTotals  stats.LinkCounters
	linkSession atomic.Pointer[stats.LinkCounters]
	// publicPorts maps tunnel ID → actual bound public port (from
	// register_ok). Tests and the GUI read it.
	publicPorts sync.Map

	// localUp maps tunnel ID → last observed health of its local target.
	localUp sync.Map
	// healthSink is the live session's push func; nil while disconnected.
	healthSink atomic.Pointer[healthSinkBox]
	// healthObserver is a process-lifetime health observer (the engine's
	// event recorder); unlike healthSink it is not tied to a session.
	healthObserver atomic.Pointer[healthSinkBox]
	// healthInterval / healthDialTimeout override the probe cadence in tests;
	// zero means the defaults.
	healthInterval    time.Duration
	healthDialTimeout time.Duration

	// curSession is the live session, for hot-apply pushes; nil when
	// disconnected.
	curSession atomic.Pointer[session]

	// offerCaps overrides the capabilities offered in the hello; nil means
	// control.SupportedCapabilities. Tests set an explicit empty slice via
	// SetCapabilityOffer to simulate a legacy agent.
	offerCaps []string

	// Conns tracks live proxied connections for the GUI.
	Conns *conntrack.Registry

	// bwLimiters holds each tunnel's shared bandwidth-cap limiters, keyed by
	// tunnel ID (unique within one agent). Uncapped tunnels resolve to nil.
	bwLimiters *bwcap.Registry
}

func New(cfg *config.Config, logger *slog.Logger) *Agent {
	return &Agent{cfg: cfg, logger: logger, Conns: conntrack.NewRegistry(), bwLimiters: bwcap.NewRegistry()}
}

// SetCapabilityOffer overrides the capabilities offered in the hello
// exchange; nil restores the default (control.SupportedCapabilities) and an
// explicit empty slice simulates a legacy agent. Call before Run.
func (a *Agent) SetCapabilityOffer(caps []string) { a.offerCaps = caps }

// Run is the blocking entrypoint used by the CLI.
func Run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	return New(cfg, logger).Run(ctx)
}

func (a *Agent) LinkUp() bool     { return a.linkUp.Load() }
func (a *Agent) RTTMillis() int64 { return a.rttMillis.Load() }

// peerIdentity captures what the agent learned about the gateway (and about
// itself) during the hello exchange.
type peerIdentity struct {
	hostname   string
	localIPs   []string
	observedIP string // our public IP as the gateway saw it
}

// PeerHostname reports the gateway's hostname, or "" while the link is down.
func (a *Agent) PeerHostname() string {
	if p := a.peer.Load(); p != nil {
		return p.hostname
	}
	return ""
}

// PeerLANIPs reports the gateway's LAN IPv4s, or nil while the link is down.
func (a *Agent) PeerLANIPs() []string {
	if p := a.peer.Load(); p != nil {
		return p.localIPs
	}
	return nil
}

// ObservedIP reports this agent's public IP as the gateway saw it, or "" while
// the link is down.
func (a *Agent) ObservedIP() string {
	if p := a.peer.Load(); p != nil {
		return p.observedIP
	}
	return ""
}

// JitterMillis reports the current control-link jitter EWMA in milliseconds,
// or -1 when unknown (link down or too few samples).
func (a *Agent) JitterMillis() float64 {
	if s := a.curSession.Load(); s != nil && s.quality != nil {
		return s.quality.JitterMillis()
	}
	return -1
}

// PacketLossPct reports the control-link ping loss (0–100), or -1 when unknown.
func (a *Agent) PacketLossPct() float64 {
	if s := a.curSession.Load(); s != nil && s.quality != nil {
		return s.quality.LossPct()
	}
	return -1
}

// LinkUpSinceMs reports when the current session was established (unix
// millis), or 0 while the link is down.
func (a *Agent) LinkUpSinceMs() int64 { return a.linkUpSinceMs.Load() }

// LinkSessionBytes reports raw link bytes of the current session (0,0 when
// the link is down).
func (a *Agent) LinkSessionBytes() (in, out int64) {
	if c := a.linkSession.Load(); c != nil {
		return c.Bytes()
	}
	return 0, 0
}

// LinkTotalBytes reports raw link bytes across every session of this process.
func (a *Agent) LinkTotalBytes() (in, out int64) { return a.linkTotals.Bytes() }

// TunnelPublicPort reports the gateway-confirmed public port of a tunnel.
func (a *Agent) TunnelPublicPort(tunnelID string) (int, bool) {
	v, ok := a.publicPorts.Load(tunnelID)
	if !ok {
		return 0, false
	}
	return v.(int), true
}

// Tunnels returns a copy of the enabled tunnels, safe against hot-apply.
func (a *Agent) Tunnels() []config.Tunnel { return a.snapshotTunnels() }

// MustPublicPort returns the confirmed public port or panics; for tests and
// callers that have already confirmed the tunnel is live.
func (a *Agent) MustPublicPort(tunnelID string) int {
	p, ok := a.TunnelPublicPort(tunnelID)
	if !ok {
		panic("tunnel " + tunnelID + " has no confirmed public port")
	}
	return p
}

// Run maintains the gateway session forever: dial, serve, reconnect with
// jittered backoff (fresh DNS every attempt). It returns on ctx cancel or a
// fatal (non-retryable) error.
func (a *Agent) Run(ctx context.Context) error {
	// Two background workers live for Run's lifetime: the local-target
	// health checker (local status matters even while the link is down) and
	// the network-change/resume watcher whose ticks short-circuit backoff.
	// One defer tears both down: cancel first, then wait — split defers
	// would run in the wrong order (LIFO).
	ctx, cancel := context.WithCancel(ctx)
	checkerDone := make(chan struct{})
	netChanged, netWait := netnotify.Subscribe(ctx, a.logger)
	defer func() {
		cancel()
		<-checkerDone
		netWait()
	}()
	go func() {
		defer close(checkerDone)
		a.runHealthChecker(ctx)
	}()

	backoff := &link.Backoff{}
	for {
		started := time.Now()
		err := a.runSession(ctx)
		a.linkUp.Store(false)
		a.linkUpSinceMs.Store(0)
		a.publicPorts.Range(func(k, _ any) bool { a.publicPorts.Delete(k); return true })
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrBadToken) || errors.Is(err, ErrAgentConflict) || errors.Is(err, ErrVersion) {
			a.logger.Error("giving up: configuration problem", "err", err)
			return err
		}
		backoff.ConnectionEnded(time.Since(started))
		delay := backoff.Next()
		a.logger.Warn("link down — reconnecting", "err", err, "retry_in", delay.Round(time.Millisecond))
		select {
		case <-time.After(delay):
		case <-netChanged:
			a.logger.Info("network changed — reconnecting now")
			backoff.Reset()
		case <-ctx.Done():
			return nil
		}
	}
}

// runSession performs one full connect → serve cycle and returns why the
// session ended.
func (a *Agent) runSession(ctx context.Context) error {
	addr := net.JoinHostPort(a.cfg.Agent.GatewayHost, strconv.Itoa(a.cfg.Agent.GatewayPort))
	dialer := &net.Dialer{Timeout: dialTimeout}
	rawConn, err := dialer.DialContext(ctx, "tcp", addr) // resolves DNS per attempt
	if err != nil {
		return fmt.Errorf("dial gateway %s: %w", addr, err)
	}
	if tcp, ok := rawConn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
	}
	conn := tls.Client(rawConn, link.AgentTLSConfig(a.cfg.Agent.CertFingerprint))
	defer conn.Close()

	// Hello exchange, pre-mux, under one deadline.
	offer := a.offerCaps
	if offer == nil {
		offer = control.SupportedCapabilities
	}
	conn.SetDeadline(time.Now().Add(helloTimeout))
	hn, _ := os.Hostname()
	err = control.WriteMsg(conn, control.TypeHello, control.Hello{
		ProtocolVersion: control.ProtocolVersion,
		Kind:            control.KindControl,
		AgentID:         a.cfg.Agent.AgentID,
		Token:           a.cfg.Agent.Token,
		AppVersion:      version.String(),
		Capabilities:    offer,
		Hostname:        hn,
		LocalIPs:        netid.LocalIPv4s(),
	})
	if err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	env, err := control.ReadMsg(conn, control.PreAuthMaxFrame)
	if err != nil {
		return fmt.Errorf("read hello reply: %w", err)
	}
	var caps control.CapSet
	switch env.Type {
	case control.TypeHelloOK:
		ok, err := control.Decode[control.HelloOK](env)
		if err != nil {
			return err
		}
		caps = control.NewCapSet(ok.Capabilities)
		a.peer.Store(&peerIdentity{hostname: ok.Hostname, localIPs: ok.LocalIPs, observedIP: ok.ObservedIP})
		a.logger.Info("connected to gateway", "gateway", addr, "generation", ok.Generation, "gateway_version", ok.AppVersion, "gateway_host", ok.Hostname, "observed_ip", ok.ObservedIP, "capabilities", ok.Capabilities)
	case control.TypeHelloErr:
		he, err := control.Decode[control.HelloErr](env)
		if err != nil {
			return err
		}
		switch he.Code {
		case control.ErrCodeBadToken:
			return fmt.Errorf("%w (gateway said: %s)", ErrBadToken, he.Message)
		case control.ErrCodeAgentConflict:
			return fmt.Errorf("%w (gateway said: %s)", ErrAgentConflict, he.Message)
		case control.ErrCodeVersion:
			return fmt.Errorf("%w (gateway said: %s)", ErrVersion, he.Message)
		default:
			return fmt.Errorf("gateway refused connection: %s: %s", he.Code, he.Message)
		}
	default:
		return fmt.Errorf("unexpected reply to hello: %q", env.Type)
	}
	conn.SetDeadline(time.Time{})

	// Count every byte crossing the link from here on (yamux framing and
	// control chatter included) — the "agent ↔ gateway" hop the GUI shows.
	sessCounters := &stats.LinkCounters{}
	a.linkSession.Store(sessCounters)
	defer a.linkSession.Store(nil)

	mux, err := transport.NewMuxClient(stats.NewCountingConn(conn, &a.linkTotals, sessCounters))
	if err != nil {
		return err
	}
	defer mux.Close()

	ctrl, err := mux.OpenStream()
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}
	defer ctrl.Close()

	sess := &session{agent: a, mux: mux, ctrl: ctrl, caps: caps, quality: linkquality.New(lossWindow)}
	a.curSession.Store(sess)
	defer a.curSession.Store(nil)
	defer a.peer.Store(nil)
	if err := sess.registerTunnels(); err != nil {
		return err
	}
	a.linkUp.Store(true)
	a.linkUpSinceMs.Store(time.Now().UnixMilli())
	defer func() {
		a.linkUp.Store(false)
		a.linkUpSinceMs.Store(0)
	}()
	return sess.serve(ctx)
}

// session is one live connection's state and goroutines.
type session struct {
	agent *Agent
	mux   transport.Session
	ctrl  transport.Stream
	// ctx is the session's serve ctx, cancelled when the session dies. Each
	// splice is parented on it so a throttled WaitN unblocks promptly on
	// teardown instead of waiting out its rate delay.
	ctx context.Context
	// caps is the capability set negotiated in the hello exchange; immutable
	// for the session's lifetime.
	caps control.CapSet

	writeMu sync.Mutex
	pingSeq atomic.Uint64
	// syncSeq numbers SyncTunnels frames so stale SyncResults are dropped.
	syncSeq atomic.Uint64

	// quality derives jitter/packet-loss from the ping/pong heartbeat.
	quality *linkquality.Tracker
	// probe, when non-nil, is an in-flight on-demand latency measurement that
	// steals matching pongs; see probe.go.
	probe atomic.Pointer[linkquality.ProbeCollector]
}

// Has reports whether the session negotiated a capability.
func (s *session) Has(cap string) bool { return s.caps.Has(cap) }

func (s *session) write(msgType string, payload any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.ctrl.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
	defer s.ctrl.SetWriteDeadline(time.Time{})
	return control.WriteMsg(s.ctrl, msgType, payload)
}

// specFromTunnel builds the wire spec the gateway needs; the bandwidth cap
// travels so the gateway can throttle its half too, while purely agent-local
// options (PP2) stay local.
func specFromTunnel(t config.Tunnel) control.TunnelSpec {
	return control.TunnelSpec{
		ID:                  t.ID,
		Name:                t.Name,
		Type:                t.Type,
		PublicPort:          t.PublicPort,
		OfflineMOTD:         t.Options.OfflineMOTD,
		MinecraftAware:      t.Options.MinecraftAware,
		BandwidthLimitMbps:  t.Options.BandwidthLimitMbps,
		BandwidthLimitScope: t.Options.BandwidthLimitScope,
	}
}

// syncTunnels sends the full desired tunnel set in one frame (CapTunnelSync
// must already be negotiated). The gateway reconciles and answers with a
// SyncResult carrying per-tunnel outcomes.
func (s *session) syncTunnels(tunnels []config.Tunnel) error {
	seq := s.syncSeq.Add(1)
	specs := make([]control.TunnelSpec, 0, len(tunnels))
	for _, t := range tunnels {
		if t.Enabled {
			specs = append(specs, specFromTunnel(t))
		}
	}
	return s.write(control.TypeSyncTunnels, control.SyncTunnels{Seq: seq, Tunnels: specs})
}

func (s *session) registerTunnels() error {
	tunnels := s.agent.snapshotTunnels()
	if len(tunnels) == 0 {
		s.agent.logger.Warn("no enabled tunnels in config — connected but idle")
	}
	if s.Has(control.CapTunnelSync) {
		if err := s.syncTunnels(tunnels); err != nil {
			return fmt.Errorf("sync tunnels: %w", err)
		}
		return nil
	}
	for _, t := range tunnels {
		if err := s.write(control.TypeRegister, control.Register{Tunnel: specFromTunnel(t)}); err != nil {
			return fmt.Errorf("register tunnel %s: %w", t.Name, err)
		}
	}
	return nil
}

// serve pumps the session until it dies: a reader goroutine dispatches
// control frames, the main loop drives pings and accepts data streams.
func (s *session) serve(ctx context.Context) error {
	// Parent each splice on a ctx cancelled when this session ends, so a
	// throttled WaitN unblocks promptly on teardown instead of waiting out its
	// rate delay.
	sctx, cancel := context.WithCancel(ctx)
	s.ctx = sctx
	defer cancel()
	errCh := make(chan error, 3)

	// Route health transitions to the gateway for this session's lifetime,
	// and push the states observed while disconnected so the gateway starts
	// current.
	s.agent.setHealthSink(s.pushHealth)
	defer s.agent.setHealthSink(nil)
	for _, t := range s.agent.snapshotTunnels() {
		if up, known := s.agent.LocalUp(t.ID); known {
			s.pushHealth(t.ID, up)
		}
	}

	// Control reader: pongs, register results. The rolling read deadline is
	// the liveness check.
	go func() {
		for {
			s.ctrl.SetReadDeadline(time.Now().Add(controlIdleTimeout))
			env, err := control.ReadMsg(s.ctrl, control.MaxFrame)
			if err != nil {
				errCh <- fmt.Errorf("control stream: %w", err)
				return
			}
			if err := s.handleControlMsg(env); err != nil {
				errCh <- err
				return
			}
		}
	}()

	// Data stream acceptor.
	go func() {
		for {
			st, err := s.mux.AcceptStream()
			if err != nil {
				errCh <- fmt.Errorf("accept stream: %w", err)
				return
			}
			go s.handleDataStream(st)
		}
	}()

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mux.Close()
			return ctx.Err()
		case <-s.mux.CloseChan():
			return errors.New("session closed by peer")
		case err := <-errCh:
			s.mux.Close()
			return err
		case <-ticker.C:
			s.quality.Sweep(time.Now(), lossTimeout)
			seq := s.pingSeq.Add(1)
			sent := time.Now()
			s.quality.OnSent(seq, sent)
			err := s.write(control.TypePing, control.Ping{Seq: seq, SentUnixNano: sent.UnixNano()})
			if err != nil {
				s.mux.Close()
				return fmt.Errorf("send ping: %w", err)
			}
		}
	}
}

func (s *session) handleControlMsg(env *control.Envelope) error {
	switch env.Type {
	case control.TypePing:
		// The gateway pings us too (bidirectional heartbeat) so it can measure
		// its own RTT/jitter/loss; echo with our receive time for its one-way
		// estimate.
		ping, err := control.Decode[control.Ping](env)
		if err != nil {
			return err
		}
		return s.write(control.TypePong, control.Pong{Seq: ping.Seq, SentUnixNano: ping.SentUnixNano, RecvUnixNano: time.Now().UnixNano()})

	case control.TypePong:
		pong, err := control.Decode[control.Pong](env)
		if err != nil {
			return err
		}
		now := time.Now()
		rtt := now.Sub(time.Unix(0, pong.SentUnixNano))
		// Clamp to ≥1ms: 0 is the "no measurement" sentinel everywhere
		// (status, stats gauges), and a sub-millisecond LAN round-trip must
		// not truncate into it and read as unknown.
		s.agent.rttMillis.Store(max(1, rtt.Milliseconds()))
		s.quality.OnPong(pong.Seq, rtt)
		if pc := s.probe.Load(); pc != nil {
			pc.Record(*pong, now)
		}
		return nil

	case control.TypeRegisterOK:
		ok, err := control.Decode[control.RegisterOK](env)
		if err != nil {
			return err
		}
		s.agent.publicPorts.Store(ok.TunnelID, ok.PublicPort)
		s.agent.logger.Info("tunnel live", "tunnel_id", ok.TunnelID, "public_port", ok.PublicPort)
		return nil

	case control.TypeRegErr:
		re, err := control.Decode[control.RegisterErr](env)
		if err != nil {
			return err
		}
		// One failed tunnel doesn't kill the session; others may be fine.
		s.agent.logger.Error("tunnel rejected by gateway", "tunnel_id", re.TunnelID, "code", re.Code, "reason", re.Message)
		return nil

	case control.TypeSyncResult:
		res, err := control.Decode[control.SyncResult](env)
		if err != nil {
			return err
		}
		if res.Seq != s.syncSeq.Load() {
			s.agent.logger.Debug("dropping stale sync result", "seq", res.Seq, "latest", s.syncSeq.Load())
			return nil
		}
		for _, r := range res.Results {
			if r.OK {
				s.agent.publicPorts.Store(r.TunnelID, r.PublicPort)
				s.agent.logger.Info("tunnel live", "tunnel_id", r.TunnelID, "public_port", r.PublicPort)
				continue
			}
			// One failed tunnel doesn't kill the session; others may be fine.
			s.agent.publicPorts.Delete(r.TunnelID)
			s.agent.logger.Error("tunnel rejected by gateway", "tunnel_id", r.TunnelID, "code", r.Code, "reason", r.Message)
		}
		return nil

	case control.TypeConnStats:
		cs, err := control.Decode[control.ConnStats](env)
		if err != nil {
			return err
		}
		// Route each RTT sample to its live connection by the gateway-issued
		// ConnID (stored as the entry's ConnKey). SetRTT fires the registry's
		// RTT hook, which records the sample into this engine's analytics.
		//
		// Known gap (deliberate): a gateway too old to send conn_stats means
		// no per-player RTT on this agent, and there is no honest substitute —
		// agent-side TCP_INFO would measure the local-server leg, and the
		// control-link RTT would stamp every player with one identical wrong
		// number. Self-heals when the gateway is upgraded.
		for _, st := range cs.Entries {
			if e := s.agent.Conns.EntryByConnKey(st.ConnID); e != nil {
				e.SetRTT(st.RttMs)
			}
		}
		return nil

	default:
		s.agent.logger.Debug("ignoring unknown control message", "type", env.Type)
		return nil
	}
}

// handleDataStream serves one proxied client connection: read the OpenConn
// header, dial the local backend, splice.
func (s *session) handleDataStream(st transport.Stream) {
	defer st.Close()
	st.SetReadDeadline(time.Now().Add(openConnTimeout))
	env, err := control.ReadMsg(st, control.MaxFrame)
	if err != nil || env.Type != control.TypeOpenConn {
		s.agent.logger.Debug("data stream without open_conn header", "err", err)
		return
	}
	st.SetReadDeadline(time.Time{})
	oc, err := control.Decode[control.OpenConn](env)
	if err != nil {
		s.agent.logger.Debug("bad open_conn", "err", err)
		return
	}
	tun := s.agent.tunnelByID(oc.TunnelID)
	if tun == nil {
		s.agent.logger.Warn("open_conn for unknown tunnel", "tunnel_id", oc.TunnelID)
		return
	}
	local, err := net.DialTimeout("tcp", tun.LocalAddr, localDialTimeout)
	if err != nil {
		// Milestone 3's health checks make this state visible before
		// players hit it; milestone 5 serves the offline MOTD instead.
		s.agent.logger.Warn("local server unreachable", "tunnel", tun.Name, "local_addr", tun.LocalAddr, "err", err)
		return
	}
	tcp := local.(*net.TCPConn)
	tcp.SetNoDelay(true)

	// PROXY protocol v2: prepend the real client IP so the local server sees
	// it instead of our loopback dial. Must be the very first bytes on the
	// connection, before any Minecraft traffic.
	if tun.Options.ProxyProtocolV2 {
		if err := writeProxyHeader(tcp, oc.ClientAddr); err != nil {
			s.agent.logger.Warn("proxy-protocol header write failed", "tunnel", tun.Name, "client", oc.ClientAddr, "err", err)
			return
		}
	}

	s.agent.logger.Debug("client connected", "tunnel", tun.Name, "client", oc.ClientAddr)
	// Splice(local, stream): AToB is local→client, so In (client→server) is
	// the B→A side — inIsAToB=false. The gateway-issued ConnID keys this
	// connection for control-link RTT reports; an old gateway sends "" and
	// EntryByConnKey("") already guards. Passed into Open (never written
	// post-hoc) because the control goroutine reads ConnKey concurrently.
	entry, closeEntry := s.agent.Conns.Open(s.agent.cfg.Agent.AgentID, tun.ID, tun.Name, oc.ClientAddr, oc.ConnID, false)
	defer closeEntry()

	// Minecraft-aware tunnels sniff the client's login handshake (which flows
	// in on the stream leg) to attribute the connection to a player. The tap
	// is read-only: bytes pass through untouched, so a parse quirk never
	// disturbs the proxy.
	var src relay.Conn = st
	if tun.Options.MinecraftAware {
		src = mcsniff.Tap(st, entry)
	}
	// Splice(local, stream): AToB is local→client (outbound), BToA is
	// client→local (inbound). Parent on the session ctx so teardown cancels a
	// throttled WaitN.
	inbound, outbound := s.agent.bwLimiters.Resolve(tun.ID, tun.Options.BandwidthLimitMbps, tun.Options.BandwidthLimitScope)
	opts := relay.SpliceOpts{Ctx: s.ctx, LimitAToB: outbound, LimitBToA: inbound}
	if err := relay.Splice(tcp, src, entry.Counters, opts); err != nil {
		s.agent.logger.Debug("splice ended with error", "client", oc.ClientAddr, "err", err)
	}
	s.agent.logger.Debug("client disconnected", "tunnel", tun.Name, "client", oc.ClientAddr)
}

// writeProxyHeader prepends a PROXY protocol v2 header carrying the real
// client address (src) and the local server address (dst) before any tunnel
// bytes flow. clientAddr is an IP:port literal from the gateway (never a
// hostname), so it is parsed without invoking the resolver.
func writeProxyHeader(local *net.TCPConn, clientAddr string) error {
	host, portStr, err := net.SplitHostPort(clientAddr)
	if err != nil {
		return fmt.Errorf("parse client addr %q: %w", clientAddr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("client addr %q is not an IP", clientAddr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("parse client port %q: %w", portStr, err)
	}
	src := &net.TCPAddr{IP: ip, Port: port}
	dst, _ := local.RemoteAddr().(*net.TCPAddr)
	if dst == nil {
		dst = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
	}
	local.SetWriteDeadline(time.Now().Add(localDialTimeout))
	defer local.SetWriteDeadline(time.Time{})
	_, err = local.Write(proxyproto.HeaderV2(src, dst))
	return err
}

// pushHealth sends one tunnel's health to the gateway; write failures are
// left to the liveness machinery (the control reader will notice a dead
// stream long before this matters).
func (s *session) pushHealth(tunnelID string, up bool) {
	if err := s.write(control.TypeHealth, control.Health{TunnelID: tunnelID, LocalUp: up}); err != nil {
		s.agent.logger.Debug("health push failed", "tunnel_id", tunnelID, "err", err)
	}
}

// tunnelByID returns a copy so callers never hold a pointer into the
// hot-appliable config slice.
func (a *Agent) tunnelByID(id string) *config.Tunnel {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	for i := range a.cfg.Agent.Tunnels {
		if a.cfg.Agent.Tunnels[i].ID == id {
			t := a.cfg.Agent.Tunnels[i]
			return &t
		}
	}
	return nil
}
