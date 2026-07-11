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
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/conntrack"
	"proxyforward/internal/control"
	"proxyforward/internal/link"
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
)

// Fatal configuration errors: retrying cannot fix these, so Run returns
// instead of hammering the gateway.
var (
	ErrBadToken      = errors.New("gateway rejected our token — re-pair with the gateway's current pairing code")
	ErrAgentConflict = errors.New("gateway already has a different agent connected — each gateway serves one agent identity")
	ErrVersion       = errors.New("protocol version mismatch — update the older side")
)

type Agent struct {
	cfg    *config.Config
	cfgMu  sync.RWMutex // guards cfg.Agent.Tunnels against hot-apply
	logger *slog.Logger

	// rttMillis is the latest heartbeat round-trip, for status surfaces.
	rttMillis atomic.Int64
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
}

func New(cfg *config.Config, logger *slog.Logger) *Agent {
	return &Agent{cfg: cfg, logger: logger, Conns: conntrack.NewRegistry()}
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
	err = control.WriteMsg(conn, control.TypeHello, control.Hello{
		ProtocolVersion: control.ProtocolVersion,
		Kind:            control.KindControl,
		AgentID:         a.cfg.Agent.AgentID,
		Token:           a.cfg.Agent.Token,
		AppVersion:      version.String(),
		Capabilities:    offer,
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
		a.logger.Info("connected to gateway", "gateway", addr, "generation", ok.Generation, "gateway_version", ok.AppVersion, "capabilities", ok.Capabilities)
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

	sess := &session{agent: a, mux: mux, ctrl: ctrl, caps: caps}
	a.curSession.Store(sess)
	defer a.curSession.Store(nil)
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
	// caps is the capability set negotiated in the hello exchange; immutable
	// for the session's lifetime.
	caps control.CapSet

	writeMu sync.Mutex
	pingSeq uint64
	// syncSeq numbers SyncTunnels frames so stale SyncResults are dropped.
	syncSeq atomic.Uint64

	loopWG sync.WaitGroup // control reader + stream acceptor
	dataWG sync.WaitGroup // active data-stream splices
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

// specFromTunnel builds the wire spec the gateway needs; agent-side options
// (PP2, Minecraft awareness, bandwidth caps) stay local.
func specFromTunnel(t config.Tunnel) control.TunnelSpec {
	return control.TunnelSpec{
		ID:          t.ID,
		Name:        t.Name,
		Type:        t.Type,
		PublicPort:  t.PublicPort,
		OfflineMOTD: t.Options.OfflineMOTD,
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
			s.pingSeq++
			err := s.write(control.TypePing, control.Ping{Seq: s.pingSeq, SentUnixNano: time.Now().UnixNano()})
			if err != nil {
				s.mux.Close()
				return fmt.Errorf("send ping: %w", err)
			}
		}
	}
}

func (s *session) handleControlMsg(env *control.Envelope) error {
	switch env.Type {
	case control.TypePong:
		pong, err := control.Decode[control.Pong](env)
		if err != nil {
			return err
		}
		rtt := time.Since(time.Unix(0, pong.SentUnixNano))
		s.agent.rttMillis.Store(rtt.Milliseconds())
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
	// the B→A side — inIsAToB=false.
	entry, closeEntry := s.agent.Conns.Open(tun.ID, tun.Name, oc.ClientAddr, false)
	defer closeEntry()
	if err := relay.Splice(tcp, st, entry.Counters); err != nil {
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
