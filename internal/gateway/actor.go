package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"proxyforward/internal/bwcap"
	"proxyforward/internal/control"
	"proxyforward/internal/linkquality"
	"proxyforward/internal/portowner"
	"proxyforward/internal/stats"
	"proxyforward/internal/transport"
)

// agentSession is the gateway's record of one connected agent.
type agentSession struct {
	agentID string
	gen     uint64
	conn    net.Conn
	logger  *slog.Logger

	// connectedAt/remoteIP/link feed the GUI's control-link card: when the
	// session was admitted, the agent's IP, and raw link bytes this session.
	connectedAt time.Time
	remoteIP    string
	link        stats.LinkCounters

	// hostname/localIPs are the agent machine's identity from its hello, shown
	// in the gateway GUI's identity badges. Set at admission, immutable after.
	hostname string
	localIPs []string
	// scope restricts which public ports / tunnel IDs this agent may bind, from
	// its per-agent allowlist record (empty = unrestricted). Set at admission,
	// immutable after — enforced in validateSpec.
	scope Scope

	// enrolled is true when the agent authenticated per-identity (an Ed25519
	// pubkey), not via the legacy shared token. Gateway-authoritative config
	// (CapGatewayConfig) is keyed to that identity, so it engages only when enrolled.
	// reportedConfigHash is the config hash the agent sent in its hello, compared
	// once at connect to decide whether to push. Both immutable after admission.
	enrolled           bool
	reportedConfigHash string
	// agentConfigGen is the config generation the agent currently holds: seeded from
	// its hello, advanced on each config_ack. It is the basis a propose_config is
	// checked against, so a stale proposal can't clobber newer authoritative state.
	agentConfigGen atomic.Uint64

	// caps is the negotiated capability set, fixed before admission —
	// immutable for the session's lifetime, so reads need no locking.
	caps control.CapSet
	// dp is the data plane chosen from caps at admission (mux vs per-conn);
	// handleClient acquires every player's leg through it. Immutable like caps.
	dp dataPlane

	sess    atomic.Pointer[sessionBox]
	evicted atomic.Bool

	// ctx is a child of the gateway's serving ctx, cancelled when this session
	// is evicted (and on shutdown). handleClient parents each splice on it so a
	// throttled WaitN unblocks promptly and per-agent on eviction — closing the
	// mux alone can't unblock a copy parked in a limiter's WaitN.
	ctx    context.Context
	cancel context.CancelFunc

	// The gateway runs its own ping loop toward the agent (bidirectional
	// heartbeat) so it reports the same RTT/jitter/loss stats the agent does.
	// ctrlWriteMu serializes control-stream writes between the read loop's
	// responses, the ping goroutine, and an on-demand probe; it also guards
	// ctrl, which is live only while serveControl runs.
	ctrlWriteMu sync.Mutex
	ctrl        transport.Stream
	pingSeq     atomic.Uint64
	rttMillis   atomic.Int64
	quality     *linkquality.Tracker
	// probe holds an in-flight on-demand latency measurement; nil otherwise.
	probe atomic.Pointer[linkquality.ProbeCollector]

	// health maps tunnelID → the agent's last reported local-backend state;
	// the offline responder consults it.
	health sync.Map

	// rttConns tracks this session's live public connections for the RTT
	// sampler: connID (string) → *rttConn. Populated by handleClient for the
	// connection's lifetime.
	rttConns sync.Map

	// dataConns tracks this session's live per-conn data connections (connID →
	// net.Conn) so eviction can close them. In per-conn transport the data
	// splices ride dedicated conns, not the mux, so closing the mux alone can't
	// tear them down — and an uncapped splice parked in Read only unblocks on
	// conn close (the ctx cancel unblocks only throttled WaitN and the pending
	// dial-back wait). Empty under mux transport.
	dataConns sync.Map
}

// setCtrl publishes (or clears, with nil) the control stream for writers.
func (s *agentSession) setCtrl(ctrl transport.Stream) {
	s.ctrlWriteMu.Lock()
	s.ctrl = ctrl
	s.ctrlWriteMu.Unlock()
}

// writeControl serializes a control-stream write against the ping goroutine and
// any probe. It errors once the stream is gone.
func (s *agentSession) writeControl(msgType string, payload any) error {
	s.ctrlWriteMu.Lock()
	defer s.ctrlWriteMu.Unlock()
	if s.ctrl == nil {
		return errNoControlStream
	}
	s.ctrl.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
	defer s.ctrl.SetWriteDeadline(time.Time{})
	return control.WriteMsg(s.ctrl, msgType, payload)
}

var errNoControlStream = fmt.Errorf("control stream is not active")

// sessionBox exists because atomic.Pointer needs a concrete type and
// transport.Session is an interface.
type sessionBox struct{ s transport.Session }

func (a *agentSession) setSession(s transport.Session) { a.sess.Store(&sessionBox{s}) }

// Has reports whether the session negotiated a capability.
func (a *agentSession) Has(cap string) bool { return a.caps.Has(cap) }

func (a *agentSession) session() transport.Session {
	if b := a.sess.Load(); b != nil {
		return b.s
	}
	return nil
}

// closeAll tears down the underlying conn (which kills the mux, its streams,
// and every splice riding them) plus every per-conn data connection (whose
// splices ride dedicated conns, not the mux). Together these drain exactly this
// agent's connections and none of another's. Under QUIC transport conn is nil —
// closing the session (a *quic.Conn) is itself the whole drain boundary (all its
// streams die with it) and dataConns is empty.
func (a *agentSession) closeAll() {
	if s := a.session(); s != nil {
		s.Close()
	}
	if a.conn != nil {
		a.conn.Close()
	}
	a.dataConns.Range(func(_, v any) bool {
		v.(net.Conn).Close()
		return true
	})
}

// publicListener is one bound public port serving one tunnel of one session.
type publicListener struct {
	spec  control.TunnelSpec
	owner *agentSession
	ln    net.Listener
	done  chan struct{} // closed when the accept loop has fully exited
	// limiters holds this tunnel's bandwidth cap, keyed structurally by
	// (agentID, tunnelID) via the nested listeners map. Read off-actor by
	// handleClient (hence atomic); written by bind/reconcile on the actor. nil
	// pointer / uncapped set = the relay fast path.
	limiters atomic.Pointer[bwcap.LimiterSet]
}

// actor owns all session/listener lifecycle state. Every mutation runs on
// the single run() goroutine, so admission, binding, and eviction are
// naturally serialized — a re-registered port can never race its own dying
// listener because eviction (including waiting for accept-loop exit)
// completes before the next command is processed.
type actor struct {
	logger  *slog.Logger
	cmds    chan func()
	stopped chan struct{} // closed when run() exits; unblocks late do() callers

	// State below is touched only from run().
	agents     map[string]*agentSession              // agentID → live session
	generation uint64                                // global-monotonic supersede ordinal
	listeners  map[string]map[string]*publicListener // agentID → tunnelID → listener
	admitDamp  map[string]dampState                  // agentID → anti-flap state
	// events is the ring of notable auto-fixes/conflicts (port reassign, suspected
	// clone) the GUI event log polls. Written on this goroutine, read via do().
	events *eventRing

	clientWG sync.WaitGroup // tracks handleClient goroutines across all agents
}

// dampState is the per-agentID anti-flap record: a genuine collision between two
// live machines claiming the same agentID degrades to a slow contest with a log
// line rather than a tight teardown/rebind loop. Touched only from run().
type dampState struct {
	lastSupersedeAt time.Time
	penalty         time.Duration
}

// errAdmitDampened is a transient admission refusal: the newcomer should back
// off and retry, not treat it as fatal.
var errAdmitDampened = fmt.Errorf("admission dampened: same agentID reconnecting too fast")

// supersedeFlapWindow: supersedes closer together than this look like two live
// machines contesting one agentID rather than a single reconnect. Shared by the
// anti-flap penalty (noteSupersede) and the cloned-key detector (admit).
const supersedeFlapWindow = 2 * time.Second

// eventRingCap bounds the gateway event ring: enough to show a meaningful recent
// history in the GUI event log without unbounded growth on a busy fleet.
const eventRingCap = 256

func newActor(logger *slog.Logger) *actor {
	return &actor{
		logger:    logger,
		cmds:      make(chan func()),
		stopped:   make(chan struct{}),
		agents:    make(map[string]*agentSession),
		listeners: make(map[string]map[string]*publicListener),
		admitDamp: make(map[string]dampState),
		events:    newEventRing(eventRingCap),
	}
}

func (a *actor) run(ctx context.Context) {
	defer close(a.stopped)
	for {
		select {
		case <-ctx.Done():
			// Shutdown: evict every agent, then drain all splices. clientWG is
			// global; it is waited only here, never inside a per-agent evict.
			for _, sess := range snapshotSessions(a.agents) {
				a.evict(sess, "gateway shutting down")
			}
			a.clientWG.Wait()
			return
		case cmd := <-a.cmds:
			cmd()
		}
	}
}

// snapshotSessions copies the live sessions so an evict loop can delete from the
// map without racing the range.
func snapshotSessions(m map[string]*agentSession) []*agentSession {
	out := make([]*agentSession, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out
}

// do runs fn on the actor goroutine and waits for it, reporting whether fn
// ran. After shutdown it returns false without running fn — the state fn
// would touch is already torn down, and blocking here would deadlock late
// disconnect callbacks.
func (a *actor) do(fn func()) bool {
	done := make(chan struct{})
	wrapped := func() {
		defer close(done)
		fn()
	}
	select {
	case a.cmds <- wrapped:
		<-done
		return true
	case <-a.stopped:
		return false
	}
}

// recordEvent appends a notable event to the ring. Must run on the actor
// goroutine (its callers — admit, bindLocked — already do), so it needs no lock.
// It stamps TimeMs; the caller sets Kind and any structured fields.
func (a *actor) recordEvent(ev GatewayEvent) {
	ev.TimeMs = time.Now().UnixMilli()
	a.events.push(ev)
}

// eventsSince returns the ring's events newer than cursor, for the engine's
// incremental poll. Reads on the actor goroutine so it never races a push.
func (a *actor) eventsSince(cursor uint64) []GatewayEvent {
	var out []GatewayEvent
	a.do(func() { out = a.events.since(cursor) })
	return out
}

// admit decides what happens when an authenticated agent arrives on the shared
// gateway token. A matching agentID supersedes the existing session (reconnect
// semantics — closing it and its listeners first); a *different* agentID is
// admitted alongside, so one gateway serves several agents. Supersede is
// anti-flap dampened: two live machines colliding on an agentID degrade to a
// slow contest, not a CPU-burning loop. A dampened newcomer gets a transient
// error and should back off, never a fatal one.
func (a *actor) admit(sess *agentSession) (uint64, error) {
	var (
		gen    uint64
		outErr error
	)
	ran := a.do(func() {
		if incumbent, ok := a.agents[sess.agentID]; ok {
			d := a.admitDamp[sess.agentID]
			if d.penalty > 0 && time.Since(d.lastSupersedeAt) < d.penalty {
				outErr = errAdmitDampened
				a.logger.Warn("admission dampened; keeping incumbent", "agent", sess.agentID, "penalty", d.penalty)
				return
			}
			// A rapid supersede from a *different* IP than the incumbent is the
			// fingerprint of a cloned key: a derived identity is otherwise
			// unforgeable, so two machines holding the same key take turns stealing
			// the session. A single supersede from a new IP is just a reconnect
			// after a network change, so require a recent prior supersede before
			// flagging — one contest is normal, a two-sided volley is a clone.
			if incumbent.remoteIP != "" && sess.remoteIP != "" && incumbent.remoteIP != sess.remoteIP &&
				!d.lastSupersedeAt.IsZero() && time.Since(d.lastSupersedeAt) < supersedeFlapWindow {
				a.recordEvent(GatewayEvent{
					Kind:    EventCloneSuspected,
					AgentID: sess.agentID,
					Message: fmt.Sprintf("agent %s is being contested from two addresses (%s and %s) — its identity key looks cloned; re-enroll one machine for its own identity", sess.agentID, incumbent.remoteIP, sess.remoteIP),
				})
				a.logger.Warn("suspected cloned agent identity", "agent", sess.agentID, "incumbent_ip", incumbent.remoteIP, "newcomer_ip", sess.remoteIP)
			}
			a.logger.Info("superseding previous session from same agent", "agent", sess.agentID, "old_generation", incumbent.gen)
			a.evict(incumbent, "superseded by new connection from the same agent")
			a.noteSupersede(sess.agentID)
		}
		a.generation++
		gen = a.generation
		a.agents[sess.agentID] = sess
	})
	if !ran {
		return 0, fmt.Errorf("gateway is shutting down")
	}
	return gen, outErr
}

// noteSupersede arms the anti-flap penalty only when supersedes come in rapid
// succession — the signature of two live machines contesting one agentID. An
// isolated supersede (a normal reconnect, even one that races the old session's
// teardown) leaves the penalty at zero, so legitimate restarts are never
// dampened. A sustained contest backs off exponentially to a cap. No timer —
// everything decays by timestamp comparison.
func (a *actor) noteSupersede(agentID string) {
	const (
		basePenalty = 1 * time.Second
		capPenalty  = 30 * time.Second
	)
	d := a.admitDamp[agentID]
	if !d.lastSupersedeAt.IsZero() && time.Since(d.lastSupersedeAt) < supersedeFlapWindow {
		if d.penalty == 0 {
			d.penalty = basePenalty
		} else {
			d.penalty = min(2*d.penalty, capPenalty)
		}
	} else {
		d.penalty = 0 // isolated supersede: no dampening
	}
	d.lastSupersedeAt = time.Now()
	a.admitDamp[agentID] = d
}

// disconnected cleans up after a session's control handler exits. A session
// that is no longer the live one for its agentID (superseded, or a dampened
// newcomer that never entered the map) is a no-op.
func (a *actor) disconnected(sess *agentSession) {
	a.do(func() {
		if a.agents[sess.agentID] != sess {
			return
		}
		a.evict(sess, "agent disconnected")
	})
}

// evict runs on the actor goroutine: drop sess from the agent map, close ONLY
// its listeners (waiting each accept loop for the ghost-listener guarantee),
// then close its mux/conn — which tears down exactly this agent's splices and
// none of another's. It never waits clientWG (that is global; waiting it here
// would block on other agents' live splices). Isolation-safe: evicting one
// agent touches no other agent's listeners or connections.
// Precondition: a.agents[sess.agentID] == sess.
func (a *actor) evict(sess *agentSession, reason string) {
	delete(a.agents, sess.agentID)
	sess.evicted.Store(true)
	if sess.cancel != nil {
		sess.cancel() // unblock any throttled WaitN riding this agent's splices
	}
	if subtree := a.listeners[sess.agentID]; subtree != nil {
		for id, pl := range subtree {
			pl.ln.Close()
			<-pl.done // ghost-listener guarantee: port is free before we move on
			delete(subtree, id)
		}
		delete(a.listeners, sess.agentID) // don't leak an empty subtree
	}
	sess.closeAll()
	a.logger.Debug("session evicted", "agent", sess.agentID, "generation", sess.gen, "reason", reason)
}

// bindLocked runs on the actor goroutine: replace this agent's existing listener
// for the spec's ID (config hot-apply) and open a fresh public listener. The
// nested map keys by agentID first, so a re-register can only replace the SAME
// agent's listener — agent B can never steal agent A's. A public-port clash
// across agents (or with an outside process) is caught by net.Listen (global
// FCFS); rather than fail the tunnel, bindLocked reassigns it to a policy-valid
// free port and records the clash so the GUI can offer to reclaim the port.
func (a *actor) bindLocked(sess *agentSession, spec control.TunnelSpec, bindAddr string, allowlist []int, handle func(*publicListener, net.Conn)) (int, error) {
	subtree := a.listeners[sess.agentID]
	if old, ok := subtree[spec.ID]; ok {
		old.ln.Close()
		<-old.done
		delete(subtree, spec.ID)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bindAddr, strconv.Itoa(spec.PublicPort)))
	if err != nil {
		if !portowner.IsAddrInUse(err) {
			return 0, portowner.DecorateBindError(spec.PublicPort, err)
		}
		// The requested port is taken. Bind a policy-valid alternative instead of
		// failing, so the tunnel comes up; the listener keeps the *requested* spec
		// so a later reconcile of the same spec is a no-op (sameListener) and the
		// reassigned port stays put rather than re-contending on every sync.
		reln := listenFirstFree(bindAddr, reassignCandidates(spec.PublicPort, sess.scope, allowlist))
		if reln == nil {
			return 0, portowner.DecorateBindError(spec.PublicPort, err)
		}
		actual := reln.Addr().(*net.TCPAddr).Port
		a.recordEvent(GatewayEvent{
			Kind:          EventPortReassigned,
			AgentID:       sess.agentID,
			TunnelID:      spec.ID,
			RequestedPort: spec.PublicPort,
			ActualPort:    actual,
			Message:       fmt.Sprintf("%q could not take port %d (in use by %s); it is on port %d until you reclaim %d", spec.Name, spec.PublicPort, a.portHolderDesc(spec.PublicPort), actual, spec.PublicPort),
		})
		a.logger.Warn("public port in use; reassigned tunnel", "tunnel_id", spec.ID, "requested_port", spec.PublicPort, "actual_port", actual)
		ln = reln
	}
	pl := &publicListener{spec: spec, owner: sess, ln: ln, done: make(chan struct{})}
	pl.limiters.Store(bwcap.BuildSet(spec.BandwidthLimitMbps, spec.BandwidthLimitScope))
	if subtree == nil {
		subtree = make(map[string]*publicListener)
		a.listeners[sess.agentID] = subtree
	}
	subtree[spec.ID] = pl
	go a.acceptClients(pl, handle)
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// listenFirstFree tries to bind each candidate port in order and returns the
// first listener that opens. A candidate of 0 asks the OS for any free ephemeral
// port. Returns nil when every candidate is taken (or the list is empty).
func listenFirstFree(bindAddr string, candidates []int) net.Listener {
	for _, p := range candidates {
		ln, err := net.Listen("tcp", net.JoinHostPort(bindAddr, strconv.Itoa(p)))
		if err == nil {
			return ln
		}
	}
	return nil
}

// portHolderDesc names whatever holds a public port, for a reassignment message:
// another of this gateway's agents (found in the listener map — the "evict other"
// case) when it is an internal clash, else the owning OS process (portowner) when
// identifiable. Runs on the actor goroutine, so reading listeners is race-free.
func (a *actor) portHolderDesc(port int) string {
	for agentID, subtree := range a.listeners {
		for _, pl := range subtree {
			if pl.ln.Addr().(*net.TCPAddr).Port == port {
				return "agent " + agentID
			}
		}
	}
	if o, ok := portowner.Lookup(port); ok {
		return o.String()
	}
	return "another process"
}

// unbindLocked runs on the actor goroutine: close one tunnel's listener if it
// belongs to sess.
func (a *actor) unbindLocked(sess *agentSession, tunnelID string) {
	subtree := a.listeners[sess.agentID]
	pl, ok := subtree[tunnelID]
	if !ok || pl.owner != sess {
		return
	}
	pl.ln.Close()
	<-pl.done
	delete(subtree, tunnelID)
	if len(subtree) == 0 {
		delete(a.listeners, sess.agentID)
	}
}

// bindTunnel opens the public listener for a tunnel spec. Runs net.Listen on
// the actor goroutine — binds are rare and this keeps port state serialized.
func (a *actor) bindTunnel(sess *agentSession, spec control.TunnelSpec, bindAddr string, allowlist []int, handle func(*publicListener, net.Conn)) (int, error) {
	var (
		port   int
		outErr error
	)
	ran := a.do(func() {
		if a.agents[sess.agentID] != sess || sess.evicted.Load() {
			outErr = fmt.Errorf("session is no longer active")
			return
		}
		port, outErr = a.bindLocked(sess, spec, bindAddr, allowlist, handle)
	})
	if !ran {
		return 0, fmt.Errorf("gateway is shutting down")
	}
	return port, outErr
}

// unbindTunnel closes one tunnel's listener (agent unregistered it).
func (a *actor) unbindTunnel(sess *agentSession, tunnelID string) {
	a.do(func() {
		a.unbindLocked(sess, tunnelID)
	})
}

// reconcileOutcome is one tunnel's result from a reconcile pass.
type reconcileOutcome struct {
	ID   string
	Port int
	Err  error
}

// reconcile makes the session's listener set equal desired, as one atomic
// actor command — eviction can never interleave with a half-applied set.
// A desired spec equal to the live listener's spec (comparison is against
// the *requested* spec, so PublicPort-0 ephemeral tunnels stay stable) is
// left untouched: live client connections survive re-syncs of identical
// state. The bool result is false when the gateway is shutting down.
func (a *actor) reconcile(sess *agentSession, desired []control.TunnelSpec, bindAddr string, allowlist []int, handle func(*publicListener, net.Conn)) ([]reconcileOutcome, bool) {
	outcomes := make([]reconcileOutcome, 0, len(desired))
	ran := a.do(func() {
		if a.agents[sess.agentID] != sess || sess.evicted.Load() {
			err := fmt.Errorf("session is no longer active")
			for _, spec := range desired {
				outcomes = append(outcomes, reconcileOutcome{ID: spec.ID, Err: err})
			}
			return
		}
		want := make(map[string]struct{}, len(desired))
		for _, spec := range desired {
			want[spec.ID] = struct{}{}
		}
		// Remove this agent's listeners not in the desired set. Collect the ids
		// first — unbindLocked may delete the subtree out from under a range.
		subtree := a.listeners[sess.agentID]
		var remove []string
		for id := range subtree {
			if _, ok := want[id]; !ok {
				remove = append(remove, id)
			}
		}
		for _, id := range remove {
			a.unbindLocked(sess, id)
			a.logger.Debug("reconcile: tunnel removed", "tunnel_id", id)
		}
		for _, spec := range desired {
			if pl, ok := a.listeners[sess.agentID][spec.ID]; ok && sameListener(pl.spec, spec) {
				// Same public listener. If only the bandwidth cap changed, apply
				// it to the existing limiter set (rate-only in place, scope/flip
				// swaps the pointer) and keep the listener and its live
				// connections — no rebind, no drop. pl.spec is left untouched
				// (handleClient reads it off-actor); the limiter set is the live
				// cap.
				if pl.spec != spec {
					if set, swapped := bwcap.Reconcile(pl.limiters.Load(), spec.BandwidthLimitMbps, spec.BandwidthLimitScope); swapped {
						pl.limiters.Store(set)
					}
				}
				outcomes = append(outcomes, reconcileOutcome{ID: spec.ID, Port: pl.ln.Addr().(*net.TCPAddr).Port})
				continue
			}
			port, err := a.bindLocked(sess, spec, bindAddr, allowlist, handle)
			outcomes = append(outcomes, reconcileOutcome{ID: spec.ID, Port: port, Err: err})
		}
	})
	return outcomes, ran
}

// acceptClients is the per-listener accept loop (not on the actor
// goroutine); it must close done on exit — eviction blocks on it.
func (a *actor) acceptClients(pl *publicListener, handle func(*publicListener, net.Conn)) {
	defer close(pl.done)
	for {
		conn, err := pl.ln.Accept()
		if err != nil {
			return // listener closed (eviction, unregister, or shutdown)
		}
		a.clientWG.Add(1)
		go func() {
			defer a.clientWG.Done()
			handle(pl, conn)
		}()
	}
}

// sameListener reports whether two specs describe the same public listener —
// everything the bound port, accept loop, and login sniffer depend on. It
// excludes the bandwidth fields, which the pl's limiter set enforces and which
// can change (rate in place, scope by swap) without rebinding the listener.
func sameListener(a, b control.TunnelSpec) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Type == b.Type &&
		a.PublicPort == b.PublicPort && a.OfflineMOTD == b.OfflineMOTD &&
		a.MinecraftAware == b.MinecraftAware
}

// sessions returns every connected agent session.
func (a *actor) sessions() []*agentSession {
	var out []*agentSession
	a.do(func() { out = snapshotSessions(a.agents) })
	return out
}

// session returns the live session for one agentID, or nil.
func (a *actor) session(agentID string) *agentSession {
	var sess *agentSession
	a.do(func() { sess = a.agents[agentID] })
	return sess
}

// TunnelSnapshot is one registered tunnel's live state, for status surfaces.
type TunnelSnapshot struct {
	AgentID    string // the agent that registered this tunnel
	ID         string
	Name       string
	PublicPort int // actual bound port
	// RequestedPort is the port the spec asked for. When it differs from PublicPort
	// (and is non-zero), the port was in use and the tunnel was auto-reassigned — the
	// signal the GUI turns into a "reclaim port" card.
	RequestedPort       int
	LocalUp             bool // agent's last reported backend health
	LocalKnown          bool
	BandwidthLimitMbps  int    // configured cap (0 = unlimited)
	BandwidthLimitScope string // combined | per-direction | per-connection
}

// tunnels snapshots every registered tunnel and its bound port, joined with
// the owning session's last reported backend health.
func (a *actor) tunnels() []TunnelSnapshot {
	var out []TunnelSnapshot
	a.do(func() {
		for _, subtree := range a.listeners {
			for _, pl := range subtree {
				ts := TunnelSnapshot{
					AgentID:             pl.owner.agentID,
					ID:                  pl.spec.ID,
					Name:                pl.spec.Name,
					PublicPort:          pl.ln.Addr().(*net.TCPAddr).Port,
					RequestedPort:       pl.spec.PublicPort,
					BandwidthLimitMbps:  pl.spec.BandwidthLimitMbps,
					BandwidthLimitScope: pl.spec.BandwidthLimitScope,
				}
				if v, ok := pl.owner.health.Load(pl.spec.ID); ok {
					ts.LocalUp, ts.LocalKnown = v.(bool), true
				}
				out = append(out, ts)
			}
		}
	})
	// Map iteration order is random; sort so the GUI list is stable.
	sort.Slice(out, func(i, j int) bool {
		if out[i].AgentID != out[j].AgentID {
			return out[i].AgentID < out[j].AgentID
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// tunnelPort reports the bound port for a tunnel ID, scanning every agent. Bare
// tunnelIDs are globally-unique UUIDs in practice, so the first match is the
// one; the multi-agent status surfaces (Phase 3) key by (agentID, tunnelID).
func (a *actor) tunnelPort(tunnelID string) (int, bool) {
	var (
		port  int
		found bool
	)
	a.do(func() {
		for _, subtree := range a.listeners {
			if pl, ok := subtree[tunnelID]; ok {
				port, found = pl.ln.Addr().(*net.TCPAddr).Port, true
				return
			}
		}
	})
	return port, found
}
