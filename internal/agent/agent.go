// Package agent implements the Server A role: it dials out to the gateway
// (so Server A needs no port forwarding), registers tunnels, and splices
// accepted streams onto the local Minecraft server.
package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
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

	// transportReprobeAfter is how long the auto ladder parks a transport that
	// failed to connect before trying it again (a network change re-probes
	// immediately). Long enough that a UDP-blocked network doesn't re-cost a
	// handshake every reconnect, short enough to recover without a restart.
	transportReprobeAfter = 5 * time.Minute
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
	// ErrRevoked: the gateway removed this agent from its per-agent allowlist.
	// Fatal — retrying cannot fix it; re-pair to enroll a fresh identity.
	ErrRevoked = errors.New("this gateway revoked this agent — re-pair with a fresh code")
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
	// defaultOffer(). Tests set an explicit empty slice via SetCapabilityOffer
	// to simulate a legacy agent.
	offerCaps []string

	// activeTransport is the transport the current session is using ("quic" |
	// "per-conn" | "mux"); "" while down. Under auto mode it reflects the rung
	// the fallback ladder settled on, so the GUI can show what actually connected.
	activeTransport atomic.Pointer[string]
	// transportCooldown tracks transports the auto ladder has parked after a
	// failed connect (transport → re-probe-after time), guarded by ladderMu.
	// Cleared on a successful connect of that transport and on network changes.
	ladderMu          sync.Mutex
	transportCooldown map[string]time.Time

	// Conns tracks live proxied connections for the GUI.
	Conns *conntrack.Registry

	// bwLimiters holds each tunnel's shared bandwidth-cap limiters, keyed by
	// tunnel ID (unique within one agent). Uncapped tunnels resolve to nil.
	bwLimiters *bwcap.Registry

	// sessionCache lets per-conn data connections resume the control
	// connection's TLS session, skipping the full handshake on every player
	// join. Process-lifetime, shared across the control conn and every data
	// conn. The fingerprint pin runs on full handshakes and is inherited by
	// resumed ones, so trust is preserved.
	sessionCache tls.ClientSessionCache

	// dir is the config directory; the agent's long-term Ed25519 identity key
	// (agent_identity.key) lives here. identOnce guards a one-time load of it.
	dir       string
	identOnce sync.Once
	identPriv ed25519.PrivateKey
	identPub  ed25519.PublicKey
	identErr  error

	// configGen is the gateway-authoritative config generation the agent currently
	// holds (CapGatewayConfig); 0 until the first push. Reported in the hello and
	// advanced by applyPushedConfig. In-memory in this build — the gateway re-pushes
	// on reconnect, and disk persistence rides the optional configPersister below.
	configGen atomic.Uint64
	// configSeedNeeded is the gateway's hello_ok answer to "should I seed you?": true
	// only on first contact, when the gateway holds no config for this identity. The
	// agent seeds iff it is set, so a reconnect never volunteers a set that would race
	// the gateway's authoritative push. Set per hello, read by registerTunnels.
	configSeedNeeded atomic.Bool
	// configPersister, when set by the app, persists a pushed tunnel set + generation
	// so it survives a restart. nil keeps the pushed set in memory only.
	configPersister atomic.Pointer[ConfigPersister]
}

// ConfigPersister persists a gateway-pushed tunnel set and its generation to disk so
// it survives a restart. The app wires one; with none set the agent keeps the pushed
// set in memory and the gateway re-pushes on the next reconnect.
type ConfigPersister func(tunnels []config.Tunnel, generation uint64) error

// SetConfigPersister installs the disk-persistence hook for gateway-pushed config.
func (a *Agent) SetConfigPersister(fn ConfigPersister) { a.configPersister.Store(&fn) }

func New(cfg *config.Config, dir string, logger *slog.Logger) *Agent {
	return &Agent{
		cfg:               cfg,
		dir:               dir,
		logger:            logger,
		Conns:             conntrack.NewRegistry(),
		bwLimiters:        bwcap.NewRegistry(),
		sessionCache:      tls.NewLRUClientSessionCache(0),
		transportCooldown: map[string]time.Time{},
	}
}

// identity lazily loads (or generates on first run) the agent's long-term Ed25519
// identity from its config dir; the public key is the canonical identity the
// gateway allowlists, and agt_<fingerprint> is derived from it.
func (a *Agent) identity() (ed25519.PrivateKey, ed25519.PublicKey, error) {
	a.identOnce.Do(func() {
		a.identPriv, a.identPub, a.identErr = link.LoadOrCreateIdentity(a.dir)
	})
	return a.identPriv, a.identPub, a.identErr
}

// authFields returns the per-agent identity fields for a hello: the public key, a
// proof-of-possession signature bound to the gateway's pinned fingerprint, and any
// pending enrollment ticket. All empty if the identity can't be loaded, so the
// hello falls back to shared-token auth.
func (a *Agent) authFields() (pub, sig []byte, ticket string) {
	priv, pubKey, err := a.identity()
	if err != nil {
		a.logger.Warn("agent identity unavailable; using shared token", "err", err)
		return nil, nil, ""
	}
	return pubKey, link.SignAgentAuth(priv, a.cfg.Agent.CertFingerprint), a.cfg.Agent.EnrollTicket
}

// SetCapabilityOffer overrides the capabilities offered in the hello
// exchange; nil restores the default (defaultOffer) and an explicit empty
// slice simulates a legacy agent. Call before Run.
func (a *Agent) SetCapabilityOffer(caps []string) { a.offerCaps = caps }

// offerFor is the capability set to offer when connecting over a specific
// transport. It is transport-independent (tunnel-sync + conn-stats + gateway-config)
// plus per-conn-data only for the per-conn transport — deliberately NOT
// control.SupportedCapabilities, which would offer per-conn regardless and force
// it on every agent. QUIC and mux offer no per-conn-data (QUIC rides the mux data
// plane; the auto ladder computes this per rung). gateway-config is offered by every
// agent but the gateway negotiates it away unless the agent is enrolled (it is keyed
// to a durable identity), so a shared-token agent falls back to tunnel-sync.
func offerFor(transport string) []string {
	caps := []string{control.CapTunnelSync, control.CapConnStats, control.CapGatewayConfig}
	if transport == config.TransportPerConn {
		caps = append(caps, control.CapPerConn)
	}
	return caps
}

// Run is the blocking entrypoint used by the CLI.
func Run(ctx context.Context, cfg *config.Config, dir string, logger *slog.Logger) error {
	return New(cfg, dir, logger).Run(ctx)
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
		if errors.Is(err, ErrBadToken) || errors.Is(err, ErrAgentConflict) || errors.Is(err, ErrVersion) || errors.Is(err, ErrRevoked) {
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
			a.clearAllCooldowns() // a new network may unblock a parked transport (e.g. UDP)
		case <-ctx.Done():
			return nil
		}
	}
}

// dialGateway opens a TCP+TLS connection to the gateway's control port: DNS is
// re-resolved every call, Nagle is off, and the TLS config carries the shared
// client-session cache so per-conn data connections can resume the control
// session instead of doing a full handshake per player. Used by runSession for
// the control conn and by dialBackData for each data conn.
func (a *Agent) dialGateway(ctx context.Context) (*tls.Conn, error) {
	addr := net.JoinHostPort(a.cfg.Agent.GatewayHost, strconv.Itoa(a.cfg.Agent.GatewayPort))
	rawConn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial gateway %s: %w", addr, err)
	}
	if tcp, ok := rawConn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
	}
	cfg := link.AgentTLSConfig(a.cfg.Agent.CertFingerprint)
	cfg.ClientSessionCache = a.sessionCache
	return tls.Client(rawConn, cfg), nil
}

// runSession performs one full connect → serve cycle and returns why the
// session ended. It picks a transport-specific connector (each yields an
// established transport.Session, its control stream, and the negotiated caps),
// then runs the shared serve tail. The two connectors differ only in where the
// hello rides: yamux does it pre-mux on the raw TLS conn; QUIC does it on the
// control stream of an already-handshaked session.
func (a *Agent) runSession(ctx context.Context) error {
	if a.cfg.Agent.Transport == config.TransportAuto {
		return a.runSessionAuto(ctx)
	}
	_, err := a.runSessionWith(ctx, a.cfg.Agent.Transport)
	return err
}

// transportPreference is the auto ladder, best-isolation first: QUIC (one UDP
// connection, per-stream flow control) → per-conn (dedicated TCP per player,
// works where UDP is blocked) → mux (one TCP, max compatibility).
var transportPreference = []string{config.TransportQUIC, config.TransportPerConn, config.TransportMux}

// runSessionAuto walks the fallback ladder for one connect cycle. It tries each
// non-cooled transport in preference order; a transport that *connects* is used
// (and its session served until it ends, at which point Run backs off and the
// ladder re-evaluates). A transport that fails to *connect* falls through
// immediately to the next. The UDP-blocked heuristic: a transport that failed to
// connect is only cooled once a LATER one succeeds — if every transport fails
// the link is simply down, so nothing is cooled and the full ladder is retried.
func (a *Agent) runSessionAuto(ctx context.Context) error {
	var failedBefore []string
	var lastErr error
	for _, tr := range a.ladderOrder() {
		connected, err := a.runSessionWith(ctx, tr)
		if connected {
			a.coolTransports(failedBefore) // the ones UDP/TCP-blocked before this success
			a.clearCooldown(tr)
			return err
		}
		if ctx.Err() != nil {
			return err
		}
		if isFatal(err) {
			return err // bad token/version/conflict — no other transport will help
		}
		a.logger.Warn("transport failed to connect — trying next", "transport", tr, "err", err)
		lastErr = err
		failedBefore = append(failedBefore, tr)
	}
	return lastErr
}

// runSessionWith performs one connect+serve cycle over a specific transport. The
// bool reports whether the transport *connected* (got past the hello): false
// means the caller (the auto ladder) may fall through to another transport; true
// means the session served and this is a normal disconnect to back off from.
func (a *Agent) runSessionWith(ctx context.Context, tr string) (bool, error) {
	// Count every byte crossing the link (framing and control chatter included) —
	// the "agent ↔ gateway" hop the GUI shows. The counter is live for the whole
	// session; peer identity is cleared on any exit.
	sessCounters := &stats.LinkCounters{}
	a.linkSession.Store(sessCounters)
	defer a.linkSession.Store(nil)
	defer a.peer.Store(nil)

	offer := a.offerCaps
	if offer == nil {
		offer = offerFor(tr)
	}

	var (
		mux  transport.Session
		ctrl transport.Stream
		caps control.CapSet
		err  error
	)
	switch tr {
	case config.TransportQUIC:
		mux, ctrl, caps, err = a.connectQUIC(ctx, sessCounters, offer)
	default:
		mux, ctrl, caps, err = a.connectMux(ctx, sessCounters, offer)
	}
	if err != nil {
		return false, err // connect/handshake failed — not serving
	}
	defer mux.Close()
	defer ctrl.Close()

	active := tr
	a.activeTransport.Store(&active)
	defer a.activeTransport.Store(nil)

	sess := &session{agent: a, mux: mux, ctrl: ctrl, caps: caps, quality: linkquality.New(lossWindow), linkCounters: sessCounters}
	a.curSession.Store(sess)
	defer a.curSession.Store(nil)
	if err := sess.registerTunnels(); err != nil {
		return true, err // connected, so no fallback; Run backs off
	}
	a.linkUp.Store(true)
	a.linkUpSinceMs.Store(time.Now().UnixMilli())
	defer func() {
		a.linkUp.Store(false)
		a.linkUpSinceMs.Store(0)
	}()
	return true, sess.serve(ctx)
}

// ladderOrder returns the auto preference list with any transport still inside
// its post-failure cooldown dropped. If everything is cooled (shouldn't happen —
// mux is never cooled), it returns the full list so a connect is always tried.
func (a *Agent) ladderOrder() []string {
	a.ladderMu.Lock()
	defer a.ladderMu.Unlock()
	now := time.Now()
	out := make([]string, 0, len(transportPreference))
	for _, tr := range transportPreference {
		if until, cooled := a.transportCooldown[tr]; cooled && now.Before(until) {
			continue
		}
		out = append(out, tr)
	}
	if len(out) == 0 {
		return transportPreference
	}
	return out
}

func (a *Agent) coolTransports(trs []string) {
	if len(trs) == 0 {
		return
	}
	a.ladderMu.Lock()
	defer a.ladderMu.Unlock()
	until := time.Now().Add(transportReprobeAfter)
	for _, tr := range trs {
		a.transportCooldown[tr] = until
		a.logger.Info("cooling transport after failed connect — will re-probe", "transport", tr, "after", transportReprobeAfter)
	}
}

func (a *Agent) clearCooldown(tr string) {
	a.ladderMu.Lock()
	defer a.ladderMu.Unlock()
	delete(a.transportCooldown, tr)
}

// clearAllCooldowns re-arms every transport for an immediate re-probe — called on
// a network change, where a previously-blocked transport (e.g. UDP) may now work.
func (a *Agent) clearAllCooldowns() {
	a.ladderMu.Lock()
	defer a.ladderMu.Unlock()
	clear(a.transportCooldown)
}

// ActiveTransport reports the transport the current session is using ("quic" |
// "per-conn" | "mux"), or "" while the link is down.
func (a *Agent) ActiveTransport() string {
	if p := a.activeTransport.Load(); p != nil {
		return *p
	}
	return ""
}

// isFatal reports whether an error means retrying (on any transport) is futile.
func isFatal(err error) bool {
	return errors.Is(err, ErrBadToken) || errors.Is(err, ErrAgentConflict) || errors.Is(err, ErrVersion) || errors.Is(err, ErrRevoked)
}

// connectMux dials the gateway over TCP+TLS, performs the pre-mux hello exchange
// on the raw conn, then wraps it in a yamux client session and opens the control
// stream. The returned session owns the conn (its Close closes it).
func (a *Agent) connectMux(ctx context.Context, sessCounters *stats.LinkCounters, offer []string) (transport.Session, transport.Stream, control.CapSet, error) {
	conn, err := a.dialGateway(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	caps, err := a.helloExchange(conn, conn.RemoteAddr().String(), offer)
	if err != nil {
		conn.Close()
		return nil, nil, nil, err
	}
	mux, err := transport.NewMuxClient(stats.NewCountingConn(conn, &a.linkTotals, sessCounters))
	if err != nil {
		conn.Close()
		return nil, nil, nil, err
	}
	ctrl, err := mux.OpenStream()
	if err != nil {
		mux.Close() // closes the underlying conn too
		return nil, nil, nil, fmt.Errorf("open control stream: %w", err)
	}
	return mux, ctrl, caps, nil
}

// connectQUIC dials the gateway over QUIC (the QUIC handshake carries TLS), opens
// the control stream, and does the hello exchange on it — the gateway accepts
// that first stream as the control stream. The returned session owns its UDP
// socket and transport (its Close closes both).
func (a *Agent) connectQUIC(ctx context.Context, sessCounters *stats.LinkCounters, offer []string) (transport.Session, transport.Stream, control.CapSet, error) {
	sess, err := a.dialGatewayQUIC(ctx, sessCounters)
	if err != nil {
		return nil, nil, nil, err
	}
	ctrl, err := sess.OpenStream()
	if err != nil {
		sess.Close()
		return nil, nil, nil, fmt.Errorf("open control stream: %w", err)
	}
	caps, err := a.helloExchange(ctrl, sess.RemoteAddr().String(), offer)
	if err != nil {
		sess.Close()
		return nil, nil, nil, err
	}
	return sess, ctrl, caps, nil
}

// dialGatewayQUIC opens a client QUIC connection to the gateway over a fresh
// ephemeral UDP socket. Nagle has no analogue (QUIC paces itself); DNS is
// re-resolved inside DialQUIC. One socket carries one session, so both process
// and per-session link bytes are exact. DialQUIC closes the socket on failure.
func (a *Agent) dialGatewayQUIC(ctx context.Context, sessCounters *stats.LinkCounters) (transport.Session, error) {
	udp, err := net.ListenUDP("udp", nil) // ephemeral local socket
	if err != nil {
		return nil, fmt.Errorf("quic local socket: %w", err)
	}
	addr := net.JoinHostPort(a.cfg.Agent.GatewayHost, strconv.Itoa(a.cfg.Agent.GatewayPort))
	return transport.DialQUIC(ctx, udp, addr, link.AgentTLSConfig(a.cfg.Agent.CertFingerprint), &a.linkTotals, sessCounters)
}

// helloConn is the minimal surface the hello exchange needs: it reads and writes
// frames under one deadline. Both *tls.Conn (mux) and transport.Stream (quic)
// satisfy it, so a single hello exchange serves both transports.
type helloConn interface {
	io.Reader
	io.Writer
	SetDeadline(time.Time) error
}

// helloExchange sends the agent's hello and processes the gateway's reply under
// one deadline, returning the negotiated capability set. remoteAddr is only for
// the connected-gateway log line. On success it records the gateway's identity
// (a.peer) and clears the deadline; a fatal HelloErr maps to a sentinel error so
// Run stops instead of retry-hammering.
func (a *Agent) helloExchange(rw helloConn, remoteAddr string, offer []string) (control.CapSet, error) {
	rw.SetDeadline(time.Now().Add(helloTimeout))
	hn, _ := os.Hostname()
	pub, sig, ticket := a.authFields()
	// Report our gateway-config view only when offering the capability, so an agent
	// that doesn't participate sends a hello byte-identical to a legacy peer.
	var cfgHash string
	var cfgGen uint64
	if control.NewCapSet(offer).Has(control.CapGatewayConfig) {
		cfgHash, cfgGen = a.configState()
	}
	if err := control.WriteMsg(rw, control.TypeHello, control.Hello{
		ProtocolVersion:  control.ProtocolVersion,
		Kind:             control.KindControl,
		AgentID:          a.cfg.Agent.AgentID,
		Token:            a.cfg.Agent.Token,
		AppVersion:       version.String(),
		Capabilities:     offer,
		Hostname:         hn,
		LocalIPs:         netid.LocalIPv4s(),
		AgentPubKey:      pub,
		AgentSig:         sig,
		EnrollTicket:     ticket,
		ConfigHash:       cfgHash,
		ConfigGeneration: cfgGen,
	}); err != nil {
		return nil, fmt.Errorf("send hello: %w", err)
	}
	env, err := control.ReadMsg(rw, control.PreAuthMaxFrame)
	if err != nil {
		return nil, fmt.Errorf("read hello reply: %w", err)
	}
	switch env.Type {
	case control.TypeHelloOK:
		ok, err := control.Decode[control.HelloOK](env)
		if err != nil {
			return nil, err
		}
		a.peer.Store(&peerIdentity{hostname: ok.Hostname, localIPs: ok.LocalIPs, observedIP: ok.ObservedIP})
		a.configSeedNeeded.Store(ok.ConfigSeedNeeded)
		a.logger.Info("connected to gateway", "gateway", remoteAddr, "generation", ok.SessionGeneration, "gateway_version", ok.AppVersion, "gateway_host", ok.Hostname, "observed_ip", ok.ObservedIP, "capabilities", ok.Capabilities)
		rw.SetDeadline(time.Time{})
		return control.NewCapSet(ok.Capabilities), nil
	case control.TypeHelloErr:
		he, err := control.Decode[control.HelloErr](env)
		if err != nil {
			return nil, err
		}
		switch he.Code {
		case control.ErrCodeBadToken:
			return nil, fmt.Errorf("%w (gateway said: %s)", ErrBadToken, he.Message)
		case control.ErrCodeAgentConflict:
			return nil, fmt.Errorf("%w (gateway said: %s)", ErrAgentConflict, he.Message)
		case control.ErrCodeVersion:
			return nil, fmt.Errorf("%w (gateway said: %s)", ErrVersion, he.Message)
		case control.ErrCodeRevoked:
			return nil, fmt.Errorf("%w (gateway said: %s)", ErrRevoked, he.Message)
		default:
			return nil, fmt.Errorf("gateway refused connection: %s: %s", he.Code, he.Message)
		}
	default:
		return nil, fmt.Errorf("unexpected reply to hello: %q", env.Type)
	}
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

	// linkCounters counts this session's link bytes — the control conn and
	// every per-conn data conn — mirrored into a.linkTotals. Without counting
	// the data conns the GUI's link card would under-report per-conn payload.
	linkCounters *stats.LinkCounters
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

// enabledSpecs is the wire form of the enabled tunnels in a set — the shared
// desired-state payload for SyncTunnels, ProposeConfig, and the hello's config hash.
func enabledSpecs(tunnels []config.Tunnel) []control.TunnelSpec {
	specs := make([]control.TunnelSpec, 0, len(tunnels))
	for _, t := range tunnels {
		if t.Enabled {
			specs = append(specs, specFromTunnel(t))
		}
	}
	return specs
}

// syncTunnels sends the full desired tunnel set in one frame (CapTunnelSync
// must already be negotiated). The gateway reconciles and answers with a
// SyncResult carrying per-tunnel outcomes.
func (s *session) syncTunnels(tunnels []config.Tunnel) error {
	seq := s.syncSeq.Add(1)
	return s.write(control.TypeSyncTunnels, control.SyncTunnels{Seq: seq, Tunnels: enabledSpecs(tunnels)})
}

// proposeConfig promotes the agent's local tunnel set to a gateway-authoritative
// gateway (CapGatewayConfig): a first-contact seed, or a user-promoted edit. The
// gateway validates, adopts, bumps the generation, and pushes the resolved set back.
func (s *session) proposeConfig(tunnels []config.Tunnel) error {
	return s.write(control.TypeProposeConfig, control.ProposeConfig{Tunnels: enabledSpecs(tunnels)})
}

// configState is the agent's current gateway-config view for a hello: the content
// hash of its enabled tunnel set and the generation it last applied.
func (a *Agent) configState() (string, uint64) {
	return control.HashTunnels(enabledSpecs(a.snapshotTunnels())), a.configGen.Load()
}

// seedPublicPorts publishes the concrete public ports already recorded in the agent's
// tunnels, so a gateway-config session that stays in sync (no push) still surfaces
// them to the GUI without waiting for a frame.
func (a *Agent) seedPublicPorts(tunnels []config.Tunnel) {
	for _, t := range tunnels {
		if t.Enabled && t.PublicPort != 0 {
			a.publicPorts.Store(t.ID, t.PublicPort)
		}
	}
}

func (s *session) registerTunnels() error {
	tunnels := s.agent.snapshotTunnels()
	if len(tunnels) == 0 {
		s.agent.logger.Warn("no enabled tunnels in config — connected but idle")
	}
	if s.Has(control.CapGatewayConfig) {
		// Gateway-authoritative: the gateway owns the desired set. It tells us in the
		// hello_ok whether to seed (first contact); otherwise it reconciles its set and
		// pushes only on drift, so we just reflect the concrete ports we already hold.
		if s.agent.configSeedNeeded.Load() {
			s.agent.logger.Info("seeding gateway with local tunnel set", "enabled", len(enabledSpecs(tunnels)))
			return s.proposeConfig(tunnels)
		}
		s.agent.seedPublicPorts(tunnels)
		return nil
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

	// Data stream acceptor. Feeds the same handleDataStream sink as the per-conn
	// dial-back path. Not mode-gated: under per-conn transport the gateway never
	// opens data streams, so this simply blocks on AcceptStream for the session's
	// life — left unconditional so a mux-opened stream is always served.
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

	case control.TypePushConfig:
		// Gateway-authoritative config: replace our enabled set with the gateway's,
		// then confirm the generation so a later propose is checked against it.
		pc, err := control.Decode[control.PushConfig](env)
		if err != nil {
			return err
		}
		s.agent.applyPushedConfig(pc.Tunnels, pc.Generation)
		return s.write(control.TypeConfigAck, control.ConfigAck{Generation: pc.Generation, Hash: pc.Hash})

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

	case control.TypeOpenData:
		od, err := control.Decode[control.OpenData](env)
		if err != nil {
			return err
		}
		// Dial back a dedicated data connection for this player. Spawn it so a
		// slow dial never blocks the control reader (which owns liveness).
		go s.dialBackData(od.ConnID)
		return nil

	default:
		s.agent.logger.Debug("ignoring unknown control message", "type", env.Type)
		return nil
	}
}

// handleDataStream serves one proxied client connection: read the OpenConn
// header, dial the local backend, splice. The leg is a relay.Conn — a mux
// stream under mux transport, or a byte-counted per-conn data conn under
// per-conn transport — acquired by the caller (accept loop or dialBackData).
func (s *session) handleDataStream(st relay.Conn) {
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

// dialBackData opens a fresh KindData connection to the gateway for one player,
// identified by connID, and serves it like any other data stream. The gateway
// matches connID to the waiting player, then writes the OpenConn header this
// reads via handleDataStream. No HelloOK is expected on a data conn — the agent
// goes straight to reading OpenConn; a rejected dial-back simply fails that
// read. Per-conn transport only; runs in its own goroutine off the control
// reader.
func (s *session) dialBackData(connID string) {
	conn, err := s.agent.dialGateway(s.ctx)
	if err != nil {
		s.agent.logger.Debug("per-conn dial-back failed", "conn_id", connID, "err", err)
		return
	}
	conn.SetDeadline(time.Now().Add(helloTimeout))
	pub, sig, _ := s.agent.authFields()
	err = control.WriteMsg(conn, control.TypeHello, control.Hello{
		ProtocolVersion: control.ProtocolVersion,
		Kind:            control.KindData,
		AgentID:         s.agent.cfg.Agent.AgentID,
		Token:           s.agent.cfg.Agent.Token,
		ConnID:          connID,
		AgentPubKey:     pub,
		AgentSig:        sig,
	})
	if err != nil {
		s.agent.logger.Debug("per-conn data hello failed", "conn_id", connID, "err", err)
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})
	// Count this data conn's bytes into the session + process link totals, just
	// as the counting conn under the control mux does. The wrapper preserves
	// CloseWrite (the inner *tls.Conn has it), so it is a valid relay.Conn.
	wrapped := stats.NewCountingConn(conn, &s.agent.linkTotals, s.linkCounters)
	rc, ok := wrapped.(relay.Conn)
	if !ok {
		conn.Close()
		return
	}
	s.handleDataStream(rc)
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
