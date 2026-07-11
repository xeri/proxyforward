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

	"proxyforward/internal/control"
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

	// caps is the negotiated capability set, fixed before admission —
	// immutable for the session's lifetime, so reads need no locking.
	caps control.CapSet

	sess    atomic.Pointer[sessionBox]
	evicted atomic.Bool

	// health maps tunnelID → the agent's last reported local-backend state;
	// the offline responder consults it.
	health sync.Map
}

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
// and every splice riding them).
func (a *agentSession) closeAll() {
	if s := a.session(); s != nil {
		s.Close()
	}
	a.conn.Close()
}

// publicListener is one bound public port serving one tunnel of one session.
type publicListener struct {
	spec  control.TunnelSpec
	owner *agentSession
	ln    net.Listener
	done  chan struct{} // closed when the accept loop has fully exited
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
	current    *agentSession
	generation uint64
	listeners  map[string]*publicListener // tunnelID → listener

	clientWG sync.WaitGroup // tracks handleClient goroutines for Shutdown
}

func newActor(logger *slog.Logger) *actor {
	return &actor{
		logger:    logger,
		cmds:      make(chan func()),
		stopped:   make(chan struct{}),
		listeners: make(map[string]*publicListener),
	}
}

func (a *actor) run(ctx context.Context) {
	defer close(a.stopped)
	for {
		select {
		case <-ctx.Done():
			// Shutdown: evict whatever is connected, then drain splices.
			a.evictLocked("gateway shutting down")
			a.clientWG.Wait()
			return
		case cmd := <-a.cmds:
			cmd()
		}
	}
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

// admit decides what happens when an authenticated agent arrives: same
// agentID supersedes the existing session (closing it and its listeners
// first); a different agentID is rejected while one is connected.
func (a *actor) admit(sess *agentSession) (uint64, error) {
	var (
		gen    uint64
		outErr error
	)
	ran := a.do(func() {
		if a.current != nil && a.current.agentID != sess.agentID {
			outErr = fmt.Errorf("another agent (%s…) is already connected to this gateway; disconnect it first or reuse its identity", shortID(a.current.agentID))
			return
		}
		if a.current != nil {
			a.logger.Info("superseding previous session from same agent", "agent", sess.agentID, "old_generation", a.current.gen)
			a.evictLocked("superseded by new connection from the same agent")
		}
		a.generation++
		gen = a.generation
		a.current = sess
	})
	if !ran {
		return 0, fmt.Errorf("gateway is shutting down")
	}
	return gen, outErr
}

// disconnected cleans up after a session's control handler exits. A session
// that was already evicted (superseded) is a no-op.
func (a *actor) disconnected(sess *agentSession) {
	a.do(func() {
		if a.current != sess {
			return
		}
		a.evictLocked("agent disconnected")
	})
}

// evictLocked runs on the actor goroutine: close every listener owned by the
// current session, wait for each accept loop to fully exit, then close the
// session itself.
func (a *actor) evictLocked(reason string) {
	if a.current == nil {
		return
	}
	sess := a.current
	a.current = nil
	sess.evicted.Store(true)
	for id, pl := range a.listeners {
		pl.ln.Close()
		<-pl.done // ghost-listener guarantee: port is free before we return
		delete(a.listeners, id)
	}
	sess.closeAll()
	a.logger.Debug("session evicted", "agent", sess.agentID, "generation", sess.gen, "reason", reason)
}

// bindLocked runs on the actor goroutine: replace any existing listener for
// the spec's ID and open a fresh public listener.
func (a *actor) bindLocked(sess *agentSession, spec control.TunnelSpec, bindAddr string, handle func(*agentSession, control.TunnelSpec, net.Conn)) (int, error) {
	if old, ok := a.listeners[spec.ID]; ok {
		// Re-register of the same tunnel (config hot-apply): replace.
		old.ln.Close()
		<-old.done
		delete(a.listeners, spec.ID)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bindAddr, strconv.Itoa(spec.PublicPort)))
	if err != nil {
		return 0, portowner.DecorateBindError(spec.PublicPort, err)
	}
	pl := &publicListener{spec: spec, owner: sess, ln: ln, done: make(chan struct{})}
	a.listeners[spec.ID] = pl
	go a.acceptClients(pl, handle)
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// unbindLocked runs on the actor goroutine: close one tunnel's listener if it
// belongs to sess.
func (a *actor) unbindLocked(sess *agentSession, tunnelID string) {
	pl, ok := a.listeners[tunnelID]
	if !ok || pl.owner != sess {
		return
	}
	pl.ln.Close()
	<-pl.done
	delete(a.listeners, tunnelID)
}

// bindTunnel opens the public listener for a tunnel spec. Runs net.Listen on
// the actor goroutine — binds are rare and this keeps port state serialized.
func (a *actor) bindTunnel(sess *agentSession, spec control.TunnelSpec, bindAddr string, handle func(*agentSession, control.TunnelSpec, net.Conn)) (int, error) {
	var (
		port   int
		outErr error
	)
	ran := a.do(func() {
		if a.current != sess || sess.evicted.Load() {
			outErr = fmt.Errorf("session is no longer active")
			return
		}
		port, outErr = a.bindLocked(sess, spec, bindAddr, handle)
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
func (a *actor) reconcile(sess *agentSession, desired []control.TunnelSpec, bindAddr string, handle func(*agentSession, control.TunnelSpec, net.Conn)) ([]reconcileOutcome, bool) {
	outcomes := make([]reconcileOutcome, 0, len(desired))
	ran := a.do(func() {
		if a.current != sess || sess.evicted.Load() {
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
		for id, pl := range a.listeners {
			if pl.owner != sess {
				continue
			}
			if _, ok := want[id]; !ok {
				a.unbindLocked(sess, id)
				a.logger.Debug("reconcile: tunnel removed", "tunnel_id", id)
			}
		}
		for _, spec := range desired {
			if pl, ok := a.listeners[spec.ID]; ok && pl.owner == sess && pl.spec == spec {
				// Identical desired state: keep the listener and its live
				// connections.
				outcomes = append(outcomes, reconcileOutcome{ID: spec.ID, Port: pl.ln.Addr().(*net.TCPAddr).Port})
				continue
			}
			port, err := a.bindLocked(sess, spec, bindAddr, handle)
			outcomes = append(outcomes, reconcileOutcome{ID: spec.ID, Port: port, Err: err})
		}
	})
	return outcomes, ran
}

// acceptClients is the per-listener accept loop (not on the actor
// goroutine); it must close done on exit — eviction blocks on it.
func (a *actor) acceptClients(pl *publicListener, handle func(*agentSession, control.TunnelSpec, net.Conn)) {
	defer close(pl.done)
	for {
		conn, err := pl.ln.Accept()
		if err != nil {
			return // listener closed (eviction, unregister, or shutdown)
		}
		a.clientWG.Add(1)
		go func() {
			defer a.clientWG.Done()
			handle(pl.owner, pl.spec, conn)
		}()
	}
}

// currentSession returns the connected agent session, if any.
func (a *actor) currentSession() *agentSession {
	var sess *agentSession
	a.do(func() { sess = a.current })
	return sess
}

// TunnelSnapshot is one registered tunnel's live state, for status surfaces.
type TunnelSnapshot struct {
	ID         string
	Name       string
	PublicPort int  // actual bound port
	LocalUp    bool // agent's last reported backend health
	LocalKnown bool
}

// tunnels snapshots every registered tunnel and its bound port, joined with
// the owning session's last reported backend health.
func (a *actor) tunnels() []TunnelSnapshot {
	var out []TunnelSnapshot
	a.do(func() {
		for _, pl := range a.listeners {
			ts := TunnelSnapshot{
				ID:         pl.spec.ID,
				Name:       pl.spec.Name,
				PublicPort: pl.ln.Addr().(*net.TCPAddr).Port,
			}
			if v, ok := pl.owner.health.Load(pl.spec.ID); ok {
				ts.LocalUp, ts.LocalKnown = v.(bool), true
			}
			out = append(out, ts)
		}
	})
	// Map iteration order is random; sort so the GUI list is stable.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// tunnelPort reports the bound port for a tunnel ID.
func (a *actor) tunnelPort(tunnelID string) (int, bool) {
	var (
		port  int
		found bool
	)
	a.do(func() {
		if pl, ok := a.listeners[tunnelID]; ok {
			port = pl.ln.Addr().(*net.TCPAddr).Port
			found = true
		}
	})
	return port, found
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
