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
	"proxyforward/internal/panicguard"
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
	ErrBadToken = errors.New("gateway rejected our credentials — re-pair with the gateway's current pairing code")
	// ErrNoIdentity: the Ed25519 identity key can't be loaded or created, and it
	// is the only authenticator — nothing to fall back to, so retrying is futile.
	ErrNoIdentity = errors.New("agent identity key unavailable — check the config directory and agent_identity.key permissions")
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
	cfg *config.Config
	// cfgMu guards every field of cfg this agent mutates or reads while running:
	// the tunnel set against hot-apply, the pause bits, and the identity pair
	// enrollment rewrites. cfg is this engine's OWN document (the app hands the
	// engine a clone) — the fields nothing here writes, and that a settings change
	// only reaches through a restart (gateway host/port, fingerprint, transport,
	// pool size), are read without it.
	cfgMu  sync.RWMutex
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
	// flowStats counts which path this agent's udp datagrams took — the
	// unreliable one or a flow's stream. Shared by every flow; nothing branches
	// on it.
	flowStats relay.FlowStats
	// publicPorts maps tunnel ID → actual bound public port (from
	// register_ok). Tests and the GUI read it.
	publicPorts sync.Map

	// localUp maps tunnel ID → last observed health of its local target.
	localUp sync.Map
	// lastStatus maps tunnel ID → *mc.StatusInfo from the last SUCCESSFUL
	// mc-status probe. Absent for tcp-probed tunnels, and deleted the moment a
	// tunnel goes down — a retained player count would render as current.
	lastStatus sync.Map
	// healthSink is the live session's push func; nil while disconnected.
	healthSink atomic.Pointer[healthSinkBox]
	// healthObserver is a process-lifetime health observer (the engine's
	// event recorder); unlike healthSink it is not tied to a session.
	healthObserver atomic.Pointer[healthSinkBox]
	// healthInterval / healthDialTimeout override the probe cadence in tests;
	// zero means the defaults.
	healthInterval    time.Duration
	healthDialTimeout time.Duration

	// poolGrant is how many data connections the gateway will hold parked for us
	// (HelloOK.PoolSize, never more than we asked for); 0 means no pooling, which
	// is what a legacy gateway and every non-per-conn transport say. Written by
	// the hello exchange, read when the session starts its pool maintainer.
	poolGrant atomic.Int32

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

	// pinned is the gateway's identity as this agent knows it: the pin it was
	// paired with, plus the whole fingerprint the first handshake proved. A v2
	// pairing code carries only a prefix, and the proof-of-possession signature
	// must be over the whole hash — the gateway has nothing else to check it
	// against. Process-lifetime, shared by the control conn and every data conn.
	pinned *link.PinnedPeer

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
	// enrollPersister persists the gateway-assigned identity once enrollment
	// completes (see EnrollmentPersister). nil (bare test agents) drops the
	// notification; the engine always wires one in production.
	enrollPersister atomic.Pointer[EnrollmentPersister]

	// pinPersister widens a stored pin to the whole fingerprint once a handshake
	// has proved it (see PinPersister). nil (bare test agents) drops the write.
	pinPersister atomic.Pointer[PinPersister]

	// headless mirrors into Hello.Headless: this process runs with no GUI
	// attached (service/CLI), so nobody may be watching for a consent proposal
	// or a pause notice. Set before Run, immutable after.
	headless bool
	// pausedByGateway is the gateway's last reported pause set (CapPause) —
	// informational for the UI ("paused by the gateway"); the gateway enforces
	// its own bits itself. Cleared at session start, re-asserted after hello.
	pausedByGateway atomic.Pointer[gwPause]
	// pausePersister persists the agent's own pause bits. They live in the
	// config the app owns under its own lock, so like the pushed-config and
	// stopped-flag persisters the agent never writes that disk bare.
	pausePersister atomic.Pointer[PausePersister]
	// liveConns tracks this agent's live proxied connections (serial →
	// tunnel + local leg) so a pause-with-disconnect can drop exactly one
	// tunnel's players by closing its local legs — the relay owns both legs of
	// a splice, so the data leg unwinds with it. Connection-setup cost only,
	// never per-byte.
	liveConns   sync.Map
	liveConnSeq atomic.Uint64
}

// gwPause is the gateway's reported pause set — same shape as the wire
// message, held immutably so readers never see a partial update.
type gwPause struct {
	tunnels map[string]struct{}
	all     bool
}

func newGwPause(tunnelIDs []string, all bool) *gwPause {
	p := &gwPause{all: all, tunnels: make(map[string]struct{}, len(tunnelIDs))}
	for _, id := range tunnelIDs {
		p.tunnels[id] = struct{}{}
	}
	return p
}

func (p *gwPause) covers(tunnelID string) bool {
	if p == nil {
		return false
	}
	if p.all {
		return true
	}
	_, ok := p.tunnels[tunnelID]
	return ok
}

// liveConn is one tracked proxied connection: enough to kick a tunnel's
// players by closing their local legs.
type liveConn struct {
	tunnelID string
	local    net.Conn
}

// PausePersister persists the agent's own pause bits (the agent-wide flag and
// the per-tunnel Paused fields riding the tunnels slice). The app wires one;
// nil keeps the bits in memory only.
type PausePersister func(agentWide bool, tunnels []config.Tunnel) error

// SetPausePersister installs the disk-persistence hook for the pause bits.
func (a *Agent) SetPausePersister(fn PausePersister) { a.pausePersister.Store(&fn) }

// SetHeadless marks this process as running without a GUI; reported in the
// hello so the peer's UI can temper expectations (a consent proposal may wait).
// Call before Run.
func (a *Agent) SetHeadless(on bool) { a.headless = on }

// ConfigPersister persists a gateway-pushed tunnel set and its generation to disk so
// it survives a restart. The app wires one; with none set the agent keeps the pushed
// set in memory and the gateway re-pushes on the next reconnect.
type ConfigPersister func(tunnels []config.Tunnel, generation uint64) error

// SetConfigPersister installs the disk-persistence hook for gateway-pushed config.
func (a *Agent) SetConfigPersister(fn ConfigPersister) { a.configPersister.Store(&fn) }

// EnrollmentPersister persists the gateway-assigned identity after a successful
// hello: Agent.AgentID becomes the assigned agt_ id and the spent EnrollTicket is
// cleared, so config.toml holds no live credential in steady state. The agent has
// already recorded the assignment in its own document by the time this runs; the
// persister is what makes it durable, and in a GUI-hosted run also what carries it
// into the app's separate copy.
type EnrollmentPersister func(assignedID string) error

// SetEnrollmentPersister installs the hook that persists the gateway-assigned
// identity and clears the spent enrollment ticket.
func (a *Agent) SetEnrollmentPersister(fn EnrollmentPersister) { a.enrollPersister.Store(&fn) }

// PinPersister persists the gateway's whole certificate fingerprint once a
// handshake has proved it. It exists for the same reason EnrollmentPersister does:
// the host owns the durable document, and in a GUI-hosted run a separate copy of it.
type PinPersister func(fingerprint string) error

// SetPinPersister installs the hook that widens a stored pin to the fingerprint
// the gateway actually presented.
func (a *Agent) SetPinPersister(fn PinPersister) { a.pinPersister.Store(&fn) }

// widenPin records the whole fingerprint the handshake proved, replacing the
// prefix a v2 pairing code delivered. A pin is only as strong as it is wide, and
// 80 bits is chosen to survive the pairing act, not the installation's life — so
// the moment the agent has met the certificate, it keeps the whole hash and every
// later connection is pinned to exactly that one.
//
// Runs after a successful hello, on the session goroutine, so a reconnect that
// re-reads the config sees the wider pin. A failed persist is not fatal: the
// in-memory PinnedPeer has already tightened for this process, and the next
// successful connect tries the write again.
func (a *Agent) widenPin() {
	proved := a.pinned.Fingerprint()
	if proved == "" {
		return
	}
	a.cfgMu.Lock()
	if a.cfg.Agent.CertFingerprint == proved {
		a.cfgMu.Unlock()
		return // already the whole thing — a v1 code, or a later reconnect
	}
	a.cfg.Agent.CertFingerprint = proved
	a.cfgMu.Unlock()

	a.logger.Info("pinned the gateway's full certificate fingerprint")
	if p := a.pinPersister.Load(); p != nil {
		if err := (*p)(proved); err != nil {
			a.logger.Warn("wider pin not persisted; the pairing code's prefix stays in the config", "err", err)
		}
	}
}

// finishEnrollment hands the gateway-assigned identity to the persister when the
// hello that carried it completed (or repeated) an enrollment. It runs on the
// session goroutine — the same one that builds the next hello — so by the time a
// reconnect reads the config, a successful persist has already landed.
func (a *Agent) finishEnrollment(assigned string) {
	a.cfgMu.Lock()
	if assigned == "" || (assigned == a.cfg.Agent.AgentID && a.cfg.Agent.EnrollTicket == "") {
		a.cfgMu.Unlock()
		return // steady state: nothing new to record
	}
	// Record it in our own copy before handing it to the persister: the host owns
	// a separate document, so its write is what makes the change durable, and this
	// one is what makes the next hello, and every connection opened from here on,
	// use the identity the gateway just assigned.
	a.cfg.Agent.AgentID = assigned
	a.cfg.Agent.EnrollTicket = ""
	a.cfgMu.Unlock()

	a.logger.Info("enrolled with gateway", "agent_id", assigned)
	if p := a.enrollPersister.Load(); p != nil {
		if err := (*p)(assigned); err != nil {
			a.logger.Warn("enrollment not persisted; the spent ticket stays in the config", "err", err)
		}
	}
}

// agentID is this agent's identity as it stands now. Read under the lock because
// enrollment rewrites it mid-run, and the data path reads it per connection.
func (a *Agent) agentID() string {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg.Agent.AgentID
}

// AgentID is agentID for the host process, which shares this document and reads
// the identity from its own goroutines (status, uptime journal).
func (a *Agent) AgentID() string { return a.agentID() }

// ReadConfig runs fn with the configuration document locked for reading, and
// WriteConfig with it locked for writing. They exist for the process that hosts
// this agent: a hostless engine hands over the very document the agent mutates,
// so its status reads and its config saves have to hold the agent's lock or they
// are reading a struct mid-write. fn must not call back into the agent.
func (a *Agent) ReadConfig(fn func(*config.Config)) {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	fn(a.cfg)
}

func (a *Agent) WriteConfig(fn func(*config.Config)) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	fn(a.cfg)
}

func New(cfg *config.Config, dir string, logger *slog.Logger) *Agent {
	return &Agent{
		cfg:               cfg,
		dir:               dir,
		logger:            logger,
		Conns:             conntrack.NewRegistry(),
		bwLimiters:        bwcap.NewRegistry(),
		sessionCache:      tls.NewLRUClientSessionCache(0),
		transportCooldown: map[string]time.Time{},
		pinned:            link.NewPinnedPeer(cfg.Agent.CertFingerprint),
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
// pending enrollment ticket. The identity is the only authenticator, so a load
// failure is ErrNoIdentity — fatal, not a degraded retry.
func (a *Agent) authFields() (pub, sig []byte, ticket string, err error) {
	priv, pubKey, err := a.identity()
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: %v", ErrNoIdentity, err)
	}
	a.cfgMu.RLock()
	ticket = a.cfg.Agent.EnrollTicket
	fp := a.cfg.Agent.CertFingerprint
	a.cfgMu.RUnlock()
	// Sign what the handshake proved, not what config stored: a v2 pairing code
	// carries a prefix of the fingerprint, and the gateway checks this signature
	// against the whole one. Every caller reaches here after a handshake, so the
	// stored value is a fallback for the case where it is already the whole hash.
	if proved := a.pinned.Fingerprint(); proved != "" {
		fp = proved
	}
	return pubKey, link.SignAgentAuth(priv, fp), ticket, nil
}

// SetCapabilityOffer overrides the capabilities offered in the hello
// exchange; nil restores the default (defaultOffer) and an explicit empty
// slice simulates a legacy agent. Call before Run.
func (a *Agent) SetCapabilityOffer(caps []string) { a.offerCaps = caps }

// offerFor is the capability set to offer when connecting over a specific
// transport. It is transport-independent (tunnel-sync + conn-stats + gateway-config
// + config-consent) plus per-conn-data only for the per-conn transport —
// deliberately NOT control.SupportedCapabilities, which would offer per-conn
// regardless and force it on every agent. QUIC and mux offer no per-conn-data (QUIC
// rides the mux data plane; the auto ladder computes this per rung).
//
// Connection pooling rides the same rung, and only that one: mux and QUIC open a
// stream for free, so there is nothing a parked connection would save them.
func offerFor(transport string) []string {
	caps := []string{control.CapTunnelSync, control.CapConnStats, control.CapGatewayConfig, control.CapConfigConsent, control.CapPause, control.CapTunnelUDP, control.CapUDPDatagram}
	if transport == config.TransportPerConn {
		caps = append(caps, control.CapPerConn, control.CapConnPool)
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
	headless   bool   // the gateway runs with no GUI attached (HelloOK.Headless)
}

// PeerHeadless reports whether the gateway said nobody may be watching its UI
// (service/CLI run); false while the link is down.
func (a *Agent) PeerHeadless() bool {
	if p := a.peer.Load(); p != nil {
		return p.headless
	}
	return false
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
		if isFatal(err) {
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
		_ = tcp.SetNoDelay(true)
	}
	cfg := a.pinned.TLSConfig()
	cfg.ClientSessionCache = a.sessionCache
	conn := tls.Client(rawConn, cfg)
	// Handshake here rather than letting the first write drive it: the hello
	// carries a signature over the fingerprint this handshake proves, so it has to
	// have happened before the hello is built. A failure closes the socket the
	// caller would otherwise have to unwind.
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("tls handshake with gateway %s: %w", addr, err)
	}
	return conn, nil
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
	defer func() { _ = mux.Close() }()
	defer ctrl.Close()

	active := tr
	a.activeTransport.Store(&active)
	defer a.activeTransport.Store(nil)

	sess := &session{agent: a, mux: mux, ctrl: ctrl, caps: caps, quality: linkquality.New(lossWindow), linkCounters: sessCounters}
	a.curSession.Store(sess)
	defer a.curSession.Store(nil)
	// The gateway's pause bits are session-scoped knowledge: clear the stale
	// view now and let the gateway re-assert its set after the hello (it does,
	// whenever CapPause negotiated — complete-set semantics make that safe).
	a.pausedByGateway.Store(nil)
	if err := sess.registerTunnels(); err != nil {
		return true, err // connected, so no fallback; Run backs off
	}
	// Re-assert our own pause bits in the other direction, for the gateway's
	// UI and event log; enforcement of our bits is ours either way.
	if sess.Has(control.CapPause) {
		if err := sess.write(control.TypePauseState, a.localPauseState()); err != nil {
			a.logger.Debug("pause_state write failed", "err", err)
		}
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
	return errors.Is(err, ErrBadToken) || errors.Is(err, ErrAgentConflict) || errors.Is(err, ErrVersion) || errors.Is(err, ErrRevoked) || errors.Is(err, ErrNoIdentity)
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
		_ = conn.Close()
		return nil, nil, nil, err
	}
	mux, err := transport.NewMuxClient(stats.NewCountingConn(conn, &a.linkTotals, sessCounters))
	if err != nil {
		_ = conn.Close()
		return nil, nil, nil, err
	}
	ctrl, err := mux.OpenStream()
	if err != nil {
		_ = mux.Close() // closes the underlying conn too
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
		_ = sess.Close()
		return nil, nil, nil, fmt.Errorf("open control stream: %w", err)
	}
	caps, err := a.helloExchange(ctrl, sess.RemoteAddr().String(), offer)
	if err != nil {
		_ = sess.Close()
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
	// DialQUIC completes the handshake before it returns, so the fingerprint is
	// proved by the time the hello is built on the control stream.
	return transport.DialQUIC(ctx, udp, addr, a.pinned.TLSConfig(), &a.linkTotals, sessCounters)
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
	_ = rw.SetDeadline(time.Now().Add(helloTimeout))
	hn, _ := os.Hostname()
	pub, sig, ticket, err := a.authFields()
	if err != nil {
		return nil, err
	}
	offered := control.NewCapSet(offer)
	// Report our gateway-config view only when offering the capability, so an agent
	// that doesn't participate sends a hello byte-identical to a legacy peer.
	var cfgHash string
	var cfgGen uint64
	if offered.Has(control.CapGatewayConfig) {
		cfgHash, cfgGen = a.configState()
	}
	// Same rule for the pool request: asked for only when offered, so the frame a
	// non-pooling agent sends is unchanged down to the byte.
	var poolReq int
	if offered.Has(control.CapConnPool) {
		poolReq = a.cfg.Agent.PoolSize
	}
	if err := control.WriteMsg(rw, control.TypeHello, control.Hello{
		ProtocolVersion:  control.ProtocolVersion,
		Kind:             control.KindControl,
		AppVersion:       version.String(),
		Capabilities:     offer,
		Hostname:         hn,
		LocalIPs:         netid.LocalIPv4s(),
		AgentPubKey:      pub,
		AgentSig:         sig,
		EnrollTicket:     ticket,
		ConfigHash:       cfgHash,
		ConfigGeneration: cfgGen,
		Headless:         a.headless,
		PoolSize:         poolReq,
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
		a.peer.Store(&peerIdentity{hostname: ok.Hostname, localIPs: ok.LocalIPs, observedIP: ok.ObservedIP, headless: ok.Headless})
		a.configSeedNeeded.Store(ok.ConfigSeedNeeded)
		// The grant can only shrink the request by construction — but our own
		// footprint must be bounded by our own config whatever the peer says, so
		// take the smaller of the two rather than the number we were handed.
		a.poolGrant.Store(int32(min(ok.PoolSize, poolReq)))
		a.finishEnrollment(ok.AssignedAgentID)
		// Only after the gateway has accepted us: a certificate that satisfies the
		// pin but fails the hello is not one to widen the pin to.
		a.widenPin()
		a.logger.Info("connected to gateway", "gateway", remoteAddr, "generation", ok.SessionGeneration, "gateway_version", ok.AppVersion, "gateway_host", ok.Hostname, "observed_ip", ok.ObservedIP, "capabilities", ok.Capabilities)
		_ = rw.SetDeadline(time.Time{})
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

	// flowMux demultiplexes this session's QUIC datagrams to the udp flows riding
	// it (control.CapUDPDatagram); nil until the first flow the gateway names a
	// flow id for, and on every session whose transport cannot carry datagrams.
	// flowMu guards only the lazy construction.
	flowMu  sync.Mutex
	flowMux *relay.FlowMux

	// parked holds the data connections this session has left at the gateway
	// waiting for a player (control.CapConnPool), keyed by parkSeq. A parked
	// connection has no player and no splice to end it and its reader carries no
	// deadline, so session teardown closing it is the only thing that ever
	// unblocks that reader; the map is what teardown works from. An entry lives
	// only while the connection is parked — once a player takes it, it is an
	// ordinary data leg.
	parked  sync.Map
	parkSeq atomic.Uint64

	// pendingIncoming holds a gateway-authored config edit awaiting this machine's
	// approval; pendingLocal an edit authored here awaiting the gateway's; and
	// lastDecline the reason the gateway last refused one (control.CapConfigConsent
	// — see consent.go). Living on the session makes them session-scoped by
	// construction: a disconnect drops them, exactly as intended.
	pendingIncoming atomic.Pointer[pendingConsent]
	pendingLocal    atomic.Pointer[pendingConsent]
	lastDecline     atomic.Pointer[string]
}

// Has reports whether the session negotiated a capability.
func (s *session) Has(cap string) bool { return s.caps.Has(cap) }

func (s *session) write(msgType string, payload any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.ctrl.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
	defer func() { _ = s.ctrl.SetWriteDeadline(time.Time{}) }()
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
	// Connection pooling, where negotiated: park a few authenticated data conns
	// at the gateway so a joining player is served on one that already exists.
	// One defer for both halves — cancel first, then wait — because split defers
	// would run in the wrong order (LIFO) and wait on a maintainer nothing had
	// told to stop.
	poolDone := s.startPoolMaintainer(sctx)
	defer func() {
		cancel()
		<-poolDone
		s.closeFlowMux()
	}()
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
			_ = s.ctrl.SetReadDeadline(time.Now().Add(controlIdleTimeout))
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
			_ = s.mux.Close()
			return ctx.Err()
		case <-s.mux.CloseChan():
			return errors.New("session closed by peer")
		case err := <-errCh:
			_ = s.mux.Close()
			return err
		case <-ticker.C:
			s.quality.Sweep(time.Now(), lossTimeout)
			seq := s.pingSeq.Add(1)
			sent := time.Now()
			s.quality.OnSent(seq, sent)
			err := s.write(control.TypePing, control.Ping{Seq: seq, SentUnixNano: sent.UnixNano()})
			if err != nil {
				_ = s.mux.Close()
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

	case control.TypeConfigProposal:
		// A gateway-authored edit awaiting this machine's consent; held, never
		// applied here (see consent.go).
		if !s.Has(control.CapConfigConsent) {
			s.agent.logger.Warn("ignoring config_proposal: capability not negotiated")
			return nil
		}
		prop, err := control.Decode[control.ConfigProposal](env)
		if err != nil {
			return err
		}
		s.holdGatewayProposal(*prop)
		return nil

	case control.TypeConfigDecision:
		// The gateway's verdict on an edit authored here (see consent.go).
		if !s.Has(control.CapConfigConsent) {
			s.agent.logger.Warn("ignoring config_decision: capability not negotiated")
			return nil
		}
		dec, err := control.Decode[control.ConfigDecision](env)
		if err != nil {
			return err
		}
		s.resolveLocalDecision(*dec)
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

	case control.TypePauseState:
		// The gateway's complete pause set (CapPause) — informational: the
		// gateway enforces its own bits at its listeners; we surface "paused
		// by the gateway" in the UI. Without the negotiated capability the
		// frame is ignored like any unknown type.
		if !s.Has(control.CapPause) {
			return nil
		}
		ps, err := control.Decode[control.PauseState](env)
		if err != nil {
			return err
		}
		s.agent.pausedByGateway.Store(newGwPause(ps.TunnelIDs, ps.AgentWide))
		s.agent.logger.Info("gateway pause state updated", "agent_wide", ps.AgentWide, "tunnels", len(ps.TunnelIDs))
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
	oc, err := readOpenConn(st, openConnTimeout)
	if err != nil {
		s.agent.logger.Debug("data stream without a usable open_conn header", "err", err)
		return
	}
	s.serveOpenConn(st, oc)
}

// readOpenConn reads the header naming the tunnel this leg serves. timeout
// bounds the wait; zero waits indefinitely, which is what a parked connection
// does — nothing is late until a player arrives, and the session's teardown
// closes the connection rather than a deadline expiring on it.
func readOpenConn(st relay.Conn, timeout time.Duration) (*control.OpenConn, error) {
	if timeout > 0 {
		_ = st.SetReadDeadline(time.Now().Add(timeout))
	}
	env, err := control.ReadMsg(st, control.MaxFrame)
	if err != nil {
		return nil, err
	}
	if env.Type != control.TypeOpenConn {
		return nil, fmt.Errorf("data stream opened with %q, want open_conn", env.Type)
	}
	_ = st.SetReadDeadline(time.Time{})
	return control.Decode[control.OpenConn](env)
}

// serveOpenConn is everything a data leg does once its header has been read:
// find the tunnel, dial the local backend, splice. Shared by every way a leg is
// acquired — a mux stream, a dial-back, or a connection that was parked at the
// gateway long before this player existed.
func (s *session) serveOpenConn(st relay.Conn, oc *control.OpenConn) {
	// Outermost defer: a panic serving one player ends that player's connection
	// rather than the session, its tunnels, and every other player on them.
	// Guarding here rather than one frame up is what makes it cover all three
	// ways a leg arrives — and the parked path has a second reason for it: it
	// reports its pool slot freed BEFORE calling this (connpool.go parkAndServe),
	// so a guard above that call would have to decide whether to report again.
	defer panicguard.Conn(s.agent.logger, st, "tunnel_id", oc.TunnelID, "client", oc.ClientAddr, "conn_id", oc.ConnID)

	tun := s.agent.enabledTunnelByID(oc.TunnelID)
	if tun == nil {
		// Unknown or not enabled — either way this agent does not serve it, and the
		// next line would otherwise dial whatever address it names.
		s.agent.logger.Warn("open_conn for a tunnel this agent does not serve", "tunnel_id", oc.TunnelID)
		return
	}
	if tun.Paused || s.agent.AgentWidePaused() {
		// Our own pause bits, enforced on our side of the wire too: a paused
		// tunnel never dials the local server, so it costs no Minecraft slot —
		// even against a legacy gateway that can't enforce pause at its
		// listeners. (The gateway's bits are the gateway's to enforce.)
		s.agent.logger.Debug("refusing open_conn for a paused tunnel", "tunnel_id", oc.TunnelID)
		return
	}
	if tun.LocalAddr == "" {
		// A tunnel this agent never authored: a push naming an unknown id creates
		// it (mergeTunnels) and the wire carries no LocalAddr to create it with.
		// Refused here rather than left to fail in the dial below, which would
		// log "local server unreachable" against a server the operator never
		// named — and would say it once per player, forever, since nothing about
		// the local machine can fix it.
		s.agent.logger.Warn("open_conn for a tunnel with no local address; set one on this agent",
			"tunnel", tun.Name, "tunnel_id", tun.ID)
		return
	}
	if tun.Type == config.TunnelUDP {
		// Same header, same stream, different body: from here the leg carries
		// framed datagrams rather than a byte stream. Split out rather than
		// branched inline because none of what follows — the TCP dial, NoDelay,
		// PROXY protocol, the Minecraft tap — applies to a datagram flow.
		s.serveDatagramStream(st, tun, oc)
		return
	}
	local, err := net.DialTimeout("tcp", tun.LocalAddr, localDialTimeout)
	if err != nil {
		// Milestone 3's health checks make this state visible before
		// players hit it; milestone 5 serves the offline MOTD instead.
		s.agent.logger.Warn("local server unreachable", "tunnel", tun.Name, "local_addr", tun.LocalAddr, "err", err)
		return
	}
	// Owned from the moment it exists, exactly as serveDatagramStream owns its
	// socket. Splice closes both legs when it ends, but it is the *last* thing
	// here, and every early return before it (the PROXY header below today,
	// anything added later) would otherwise drop this connection on the floor: a
	// descriptor and a Minecraft connection slot held until the local server's
	// own timeout, once per player, at a rate the gateway chooses — oc.ClientAddr
	// is what decides, and it comes off the wire.
	defer local.Close()
	tcp := local.(*net.TCPConn)
	_ = tcp.SetNoDelay(true)

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
	entry, closeEntry := s.agent.Conns.Open(s.agent.agentID(), tun.ID, tun.Name, "", oc.ClientAddr, oc.ConnID, false)
	defer closeEntry()
	// Track the local leg so pause-with-disconnect can drop this tunnel's
	// players by closing it (either leg closing unwinds the splice).
	lcID := s.agent.liveConnSeq.Add(1)
	s.agent.liveConns.Store(lcID, &liveConn{tunnelID: tun.ID, local: tcp})
	defer s.agent.liveConns.Delete(lcID)

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

// datagramFlows returns this session's demultiplexer and datagram sender when a
// udp flow may ride QUIC datagrams, and nil when it may not: the transport must
// be able to carry datagrams at all (only QUIC can) and the peer's QUIC must
// advertise them. The third gate — the capability — was already applied by the
// gateway, which is what naming a non-zero flow id means. The mux is built on
// first use, so a session with no udp flow never starts the goroutine.
func (s *session) datagramFlows() (*relay.FlowMux, relay.FlowSender) {
	ds, ok := s.mux.(transport.DatagramSession)
	if !ok || !ds.SupportsDatagrams() {
		return nil, nil
	}
	s.flowMu.Lock()
	defer s.flowMu.Unlock()
	if s.flowMux == nil {
		s.flowMux = relay.NewFlowMux(s.ctx, ds)
	}
	return s.flowMux, ds
}

// closeFlowMux joins the demux goroutine at session teardown. The session ctx
// would end it anyway; doing it here makes that deterministic.
func (s *session) closeFlowMux() {
	s.flowMu.Lock()
	mux := s.flowMux
	s.flowMu.Unlock()
	if mux != nil {
		mux.Close()
	}
}

// serveDatagramStream is handleDataStream's UDP half: dial the local server as
// a connected UDP socket and splice datagrams to it, with the tunnel leg framed
// so message boundaries survive the reliable hop.
//
// A connected socket (DialUDP, not ListenUDP) is what makes one flow per player
// work: the kernel delivers only that server's replies to it, so no demultiplex
// step is needed on this side and a stray datagram from elsewhere cannot be
// forwarded to a player as if the server had sent it.
func (s *session) serveDatagramStream(st relay.Conn, tun *config.Tunnel, oc *control.OpenConn) {
	local, err := net.DialTimeout("udp", tun.LocalAddr, localDialTimeout)
	if err != nil {
		// A UDP dial only fails on a malformed or unresolvable address — there is
		// no handshake to refuse — so this is a configuration fault, not a server
		// that happens to be down. The health probe is what reports the latter.
		s.agent.logger.Warn("cannot open a local udp socket", "tunnel", tun.Name, "local_addr", tun.LocalAddr, "err", err)
		return
	}
	defer local.Close()

	s.agent.logger.Debug("client connected", "tunnel", tun.Name, "client", oc.ClientAddr)
	// Splice(local, stream): AToB is local→client, so In (client→server) is the
	// B→A side — inIsAToB=false, exactly as on the TCP path.
	entry, closeEntry := s.agent.Conns.Open(s.agent.agentID(), tun.ID, tun.Name, "", oc.ClientAddr, oc.ConnID, false)
	defer closeEntry()
	// Track the local leg so pause-with-disconnect can drop this tunnel's
	// players by closing it (either leg closing unwinds the splice).
	lcID := s.agent.liveConnSeq.Add(1)
	s.agent.liveConns.Store(lcID, &liveConn{tunnelID: tun.ID, local: local})
	defer s.agent.liveConns.Delete(lcID)

	inbound, outbound := s.agent.bwLimiters.Resolve(tun.ID, tun.Options.BandwidthLimitMbps, tun.Options.BandwidthLimitScope)
	opts := relay.SpliceOpts{
		Ctx: s.ctx, LimitAToB: outbound, LimitBToA: inbound,
		// The gateway reaps its side of an idle datagram session on the same
		// clock; matching it here keeps the local socket from outliving the
		// player it belongs to, since UDP offers no close to observe.
		IdleTimeout: relay.DatagramIdleTimeout,
	}
	// A non-zero flow id is the gateway saying this flow's payload rides the
	// connection's unreliable datagrams, with the stream kept for the oversize
	// ones and for the flow's lifecycle. Still a relay.Conn, still the same
	// Splice.
	var leg relay.Conn = &relay.DatagramStream{Conn: st}
	if oc.FlowID != 0 {
		if mux, sender := s.datagramFlows(); mux != nil {
			fc := relay.NewFlowgramConn(mux, sender, oc.FlowID, st, &s.agent.flowStats)
			defer func() { _ = fc.Close() }()
			leg = fc
		} else {
			// Unreachable: the gateway allocates a flow id only for a session that
			// reported datagram support, and support is a property of the one
			// connection both ends share. If it ever happens, serving on the
			// stream loses the gateway's datagram sends rather than the player —
			// survivable on a udp tunnel, and loud enough to find.
			s.agent.logger.Warn("gateway named a datagram flow this session cannot carry", "tunnel", tun.Name, "flow_id", oc.FlowID)
		}
	}
	if err := relay.Splice(relay.DatagramConn{Conn: local}, leg, entry.Counters, opts); err != nil {
		s.agent.logger.Debug("splice ended with error", "client", oc.ClientAddr, "err", err)
	}
	s.agent.logger.Debug("client disconnected", "tunnel", tun.Name, "client", oc.ClientAddr)
}

// dialBackData opens a fresh KindData connection to the gateway for one player,
// identified by connID, and serves it like any other data stream. The gateway
// matches connID to the waiting player, then writes the OpenConn header this
// reads via handleDataStream. Per-conn transport only; runs in its own goroutine
// off the control reader.
func (s *session) dialBackData(connID string) {
	rc, err := s.dialData(connID, false)
	if err != nil {
		s.agent.logger.Debug("per-conn dial-back failed", "conn_id", connID, "err", err)
		return
	}
	s.handleDataStream(rc)
}

// dialData opens an authenticated KindData connection to the gateway. connID
// names the player it answers; pooled marks it as one parked for a player who
// has not arrived yet (control.CapConnPool) — exactly one of the two is ever
// set. No HelloOK is expected on a data conn: the agent goes straight to
// reading the OpenConn header, and a rejected connection simply fails that
// read. The returned conn's bytes are counted into the session + process link
// totals, just as the counting conn under the control mux is.
func (s *session) dialData(connID string, pooled bool) (relay.Conn, error) {
	conn, err := s.agent.dialGateway(s.ctx)
	if err != nil {
		return nil, err
	}
	if pooled {
		// This connection is about to go silent until a player arrives — possibly
		// for hours, which is long enough for a NAT between here and the gateway
		// to forget it. Keepalive holds the mapping open and, on both sides, is
		// what turns a peer that vanished into an error instead of a black hole.
		if err := link.KeepParkedAlive(conn); err != nil {
			s.agent.logger.Debug("parking a data conn without keepalive", "err", err)
		}
	}
	_ = conn.SetDeadline(time.Now().Add(helloTimeout))
	pub, sig, _, err := s.agent.authFields()
	if err != nil {
		// The control conn authenticates with the same identity, so a load failure
		// has already killed the session fatally; this dial just gives up.
		_ = conn.Close()
		return nil, fmt.Errorf("data conn has no identity: %w", err)
	}
	err = control.WriteMsg(conn, control.TypeHello, control.Hello{
		ProtocolVersion: control.ProtocolVersion,
		Kind:            control.KindData,
		ConnID:          connID,
		Pooled:          pooled,
		AgentPubKey:     pub,
		AgentSig:        sig,
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("data hello: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	// The counting wrapper preserves CloseWrite (the inner *tls.Conn has it), so
	// it is a valid relay.Conn.
	wrapped := stats.NewCountingConn(conn, &s.agent.linkTotals, s.linkCounters)
	rc, ok := wrapped.(relay.Conn)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("counted data conn cannot half-close")
	}
	return rc, nil
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
	_ = local.SetWriteDeadline(time.Now().Add(localDialTimeout))
	defer func() { _ = local.SetWriteDeadline(time.Time{}) }()
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

// SetPause flips this agent's own pause bit for one tunnel (tunnelID "" = the
// agent-wide bit): the tunnel stays enabled and keeps its public port, but
// players are turned away until resumed. disconnect additionally drops the
// affected players now — the deliberate extra, never the default. Pause is
// state, not config: the change persists locally (PausePersister), rides
// TypePauseState to the gateway when the session negotiated CapPause, and
// never touches config sync or consent.
func (a *Agent) SetPause(tunnelID string, paused, disconnect bool) error {
	a.cfgMu.Lock()
	if tunnelID == "" {
		a.cfg.Agent.Paused = paused
	} else {
		found := false
		for i := range a.cfg.Agent.Tunnels {
			if a.cfg.Agent.Tunnels[i].ID == tunnelID {
				a.cfg.Agent.Tunnels[i].Paused = paused
				found = true
			}
		}
		if !found {
			a.cfgMu.Unlock()
			return fmt.Errorf("no tunnel with id %q — refresh and try again", tunnelID)
		}
	}
	agentWide := a.cfg.Agent.Paused
	tunnels := append([]config.Tunnel(nil), a.cfg.Agent.Tunnels...)
	a.cfgMu.Unlock()

	if fn := a.pausePersister.Load(); fn != nil {
		if err := (*fn)(agentWide, tunnels); err != nil {
			a.logger.Warn("persist pause bits", "err", err)
		}
	}
	if s := a.curSession.Load(); s != nil && s.Has(control.CapPause) {
		if err := s.write(control.TypePauseState, a.localPauseState()); err != nil {
			a.logger.Debug("pause_state write failed", "err", err)
		}
	}
	if paused && disconnect {
		a.kickLocal(tunnelID)
	}
	return nil
}

// localPauseState is the agent's complete current pause set in wire form —
// complete-set semantics keep it idempotent for the connect-time re-assert.
func (a *Agent) localPauseState() control.PauseState {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	ps := control.PauseState{AgentWide: a.cfg.Agent.Paused}
	for _, t := range a.cfg.Agent.Tunnels {
		if t.Paused {
			ps.TunnelIDs = append(ps.TunnelIDs, t.ID)
		}
	}
	return ps
}

// AgentWidePaused reports the agent-wide pause bit.
func (a *Agent) AgentWidePaused() bool {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg.Agent.Paused
}

// PausedByGateway reports whether the gateway's own bits cover a tunnel — the
// UI's "paused by the gateway" badge. tunnelID "" asks about the agent-wide bit.
func (a *Agent) PausedByGateway(tunnelID string) bool {
	p := a.pausedByGateway.Load()
	if tunnelID == "" {
		return p != nil && p.all
	}
	return p.covers(tunnelID)
}

// kickLocal closes the local legs of this agent's live proxied connections for
// one tunnel ("" = all). Closing either leg unwinds the splice, so this drops
// the players without any wire help — it works against a legacy gateway too.
func (a *Agent) kickLocal(tunnelID string) {
	a.liveConns.Range(func(_, v any) bool {
		lc := v.(*liveConn)
		if tunnelID == "" || lc.tunnelID == tunnelID {
			lc.local.Close()
		}
		return true
	})
}

// enabledTunnelByID resolves a tunnel id the gateway named to a tunnel this agent
// is actually serving, returning a copy so callers never hold a pointer into the
// hot-appliable config slice.
//
// Disabled tunnels are invisible here, and that is a security boundary rather than
// tidiness: the id comes off the wire in open_conn, and the only thing the caller
// does with the result is dial its LocalAddr. A disabled draft still names a real
// local service — keeping the address is what makes re-enabling one click — so
// resolving one would let a gateway reach a service its owner switched off, just by
// naming an id it can guess (ids are name slugs: tnl_rcon, tnl_ssh). A gateway is
// routinely someone else's machine. mergeTunnels refuses to *enable* a draft; this
// refuses to *serve* one, and this is the half that closes the door — a gateway
// after a switched-off service never had to push a config for it.
// Regression: TestEnabledTunnelByIDIgnoresDisabledTunnels.
//
// The gateway only binds listeners for the enabled set, so a legitimate open_conn
// never names a disabled tunnel; refusing costs nothing and fails closed if the
// owner disables one while a flow is in flight.
func (a *Agent) enabledTunnelByID(id string) *config.Tunnel {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	for i := range a.cfg.Agent.Tunnels {
		if a.cfg.Agent.Tunnels[i].ID == id && a.cfg.Agent.Tunnels[i].Enabled {
			t := a.cfg.Agent.Tunnels[i]
			return &t
		}
	}
	return nil
}
