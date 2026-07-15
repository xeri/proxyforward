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
	"crypto/subtle"
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

	// actor is published by Start while status surfaces (the engine's stats
	// sampler, the GUI) may already be polling: the reads below genuinely race
	// the write, so the pointer is atomic. Plain-pointer publication also let a
	// reader observe a half-constructed actor.
	actor   atomic.Pointer[actor]
	authLim *authLimiter
	gate    *connGate

	// Conns tracks live proxied connections for the GUI.
	Conns *conntrack.Registry
	// connSeq issues the per-connection correlation ids (ConnKey / OpenConn
	// ConnID) ahead of registry insertion, so the key is set before the entry
	// is visible to anyone.
	connSeq atomic.Uint64
	// linkTotals counts raw control-link bytes across all agent sessions of
	// this process.
	linkTotals stats.LinkCounters

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
	g.logger.Info("control listener up", "addr", g.controlLn.Addr().String(), "fingerprint", fp)
	return nil
}

// Shutdown stops accepting, evicts the current session (closing its
// listeners and splices), and waits for every goroutine to exit.
func (g *Gateway) Shutdown() {
	g.cancel()
	g.controlLn.Close()
	g.wg.Wait()
}

func (g *Gateway) ControlAddr() net.Addr {
	if g.controlLn == nil {
		return nil // Start not called yet
	}
	return g.controlLn.Addr()
}
func (g *Gateway) Fingerprint() string { return g.fingerprint }

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

// AgentConnected reports whether an agent session is currently admitted.
func (g *Gateway) AgentConnected() bool {
	a := g.act()
	return a != nil && a.currentSession() != nil
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

// session returns the current agent session, nil-safe before Start.
func (g *Gateway) session() *agentSession {
	a := g.act()
	if a == nil {
		return nil
	}
	return a.currentSession()
}

// TunnelLocalUp reports the agent's last word on a tunnel's local backend;
// known is false when no agent is connected or it has not reported yet.
func (g *Gateway) TunnelLocalUp(tunnelID string) (up, known bool) {
	sess := g.session()
	if sess == nil {
		return false, false
	}
	v, ok := sess.health.Load(tunnelID)
	if !ok {
		return false, false
	}
	return v.(bool), true
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
	if subtle.ConstantTimeCompare([]byte(hello.Token), []byte(g.cfg.Gateway.Token)) != 1 {
		logger.Warn("agent rejected: bad token")
		g.authLim.fail(ip)
		control.WriteMsg(conn, control.TypeHelloErr, control.HelloErr{
			Code:    control.ErrCodeBadToken,
			Message: "invalid token — re-pair with the gateway's current pairing code",
		})
		conn.Close()
		return
	}
	if hello.Kind != control.KindControl {
		// KindData arrives with the per-conn transport mode (milestone 5).
		control.WriteMsg(conn, control.TypeHelloErr, control.HelloErr{
			Code:    control.ErrCodeVersion,
			Message: fmt.Sprintf("connection kind %q not supported yet", hello.Kind),
		})
		conn.Close()
		return
	}
	if hello.AgentID == "" {
		control.WriteMsg(conn, control.TypeHelloErr, control.HelloErr{
			Code:    control.ErrCodeBadToken,
			Message: "hello is missing agentId",
		})
		conn.Close()
		return
	}

	// --- Admission: supersede same agent, reject a second distinct agent. ---
	negotiated := control.IntersectCaps(hello.Capabilities, control.SupportedCapabilities)
	sess := &agentSession{
		agentID:     hello.AgentID,
		conn:        conn,
		logger:      logger.With("agent", hello.AgentID),
		connectedAt: time.Now(),
		remoteIP:    ip,
		hostname:    hello.Hostname,
		localIPs:    hello.LocalIPs,
		caps:        control.NewCapSet(negotiated),
		quality:     linkquality.New(lossWindow),
	}
	gen, err := g.act().admit(sess)
	if err != nil {
		logger.Warn("agent rejected", "agent", hello.AgentID, "err", err)
		control.WriteMsg(conn, control.TypeHelloErr, control.HelloErr{
			Code:    control.ErrCodeAgentConflict,
			Message: err.Error(),
		})
		conn.Close()
		return
	}
	sess.gen = gen
	defer g.act().disconnected(sess)

	gwHost, _ := os.Hostname()
	if err := control.WriteMsg(conn, control.TypeHelloOK, control.HelloOK{
		ProtocolVersion: control.ProtocolVersion,
		Generation:      gen,
		AppVersion:      version.String(),
		Capabilities:    negotiated,
		Hostname:        gwHost,
		LocalIPs:        netid.LocalIPv4s(),
		ObservedIP:      ip,
	}); err != nil {
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
	sess.logger.Info("agent connected", "generation", gen, "agent_version", hello.AppVersion, "capabilities", negotiated)
	g.serveControl(ctx, sess, ctrl)
	sess.logger.Info("agent disconnected", "generation", gen)
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
func (g *Gateway) validateSpec(spec control.TunnelSpec) *bindError {
	if spec.ID == "" || spec.Type != config.TunnelTCP {
		return &bindError{control.ErrCodeBadTunnel, fmt.Sprintf("tunnel %q: only tcp tunnels with an id are supported", spec.Name)}
	}
	if spec.PublicPort < 0 || spec.PublicPort > 65535 {
		return &bindError{control.ErrCodeBadTunnel, fmt.Sprintf("tunnel %q: invalid public port %d", spec.Name, spec.PublicPort)}
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
	if berr := g.validateSpec(spec); berr != nil {
		return 0, berr
	}
	port, err := g.act().bindTunnel(sess, spec, g.cfg.Gateway.BindAddr, g.handleClient)
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
		if berr := g.validateSpec(spec); berr != nil {
			sess.logger.Warn("tunnel sync rejected spec", "tunnel", spec.Name, "port", spec.PublicPort, "err", berr.msg)
			results = append(results, control.SyncTunnelResult{TunnelID: spec.ID, Code: berr.code, Message: berr.msg})
			continue
		}
		valid = append(valid, spec)
	}
	outcomes, ran := g.act().reconcile(sess, valid, g.cfg.Gateway.BindAddr, g.handleClient)
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

// handleClient runs per accepted public connection: open a stream to the
// agent, send the OpenConn header, splice. When there is no live session or the
// agent reports the local backend down, it answers with the tunnel's offline
// MOTD (when configured) instead of dropping the connection.
func (g *Gateway) handleClient(sess *agentSession, spec control.TunnelSpec, clientConn net.Conn) {
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
	stream, err := mux.OpenStream()
	if err != nil {
		sess.logger.Debug("open stream for client failed", "client", clientConn.RemoteAddr().String(), "err", err)
		g.serveOffline(clientConn, spec)
		return
	}
	defer stream.Close()
	tcp, ok := clientConn.(*net.TCPConn)
	if !ok {
		return
	}
	// ConnID correlates this connection with the RTT reports the gateway sends
	// the agent; the entry's own key lets the local recorder attribute gateway
	// RTT samples. Issued before Open so the key is set before the entry is
	// published (ConnKey is immutable after Open).
	connID := strconv.FormatUint(g.connSeq.Add(1), 10)
	// Splice(client, stream): AToB is client→server, so inIsAToB=true.
	entry, closeEntry := g.Conns.Open(spec.ID, spec.Name, clientConn.RemoteAddr().String(), connID, true)
	defer closeEntry()

	stream.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
	err = control.WriteMsg(stream, control.TypeOpenConn, control.OpenConn{
		TunnelID:   spec.ID,
		ClientAddr: clientConn.RemoteAddr().String(),
		ConnID:     connID,
	})
	if err != nil {
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
	if err := relay.Splice(client, stream, entry.Counters); err != nil {
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
