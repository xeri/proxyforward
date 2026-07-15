// Package conntrack is the live-connection registry behind the GUI's
// connections table and bandwidth stats: one entry per proxied client
// connection, byte counters updated lock-free by the splice, snapshots taken
// on demand (the data path never blocks on a reader).
package conntrack

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"proxyforward/internal/relay"
)

// PlayerInfo is the identity sniffed from a Minecraft login handshake. UUID
// is the client's self-declared id (authoritative only in online mode) and
// may be empty; the identity service resolves the authoritative UUID later.
type PlayerInfo struct {
	Name     string
	UUID     string
	Protocol int32
}

// Entry is one live proxied connection. Counter direction is explicit:
// In = client → server bytes, Out = server → client bytes.
type Entry struct {
	ID uint64
	// AgentID owns this connection's tunnel. On a multi-agent gateway two
	// agents may serve the same TunnelID, so attribution needs both. Fixed at
	// Open, before the entry is published. Empty on legacy/single-agent paths.
	AgentID    string
	TunnelID   string
	TunnelName string
	ClientAddr string
	StartedAt  time.Time

	// ConnKey correlates this connection with per-connection reports that
	// arrive over the control link (e.g. gateway-measured RTT); empty when
	// the transport does not assign one. Immutable after Open — it is read
	// unlocked by EntryByConnKey callers and snapshotted by open hooks.
	ConnKey string

	// player holds the sniffed Minecraft identity once the login handshake is
	// parsed; nil until then. Read/written lock-free from the splice tap.
	player atomic.Pointer[PlayerInfo]

	// rttBits holds the last measured round-trip time in milliseconds as
	// float64 bits; -1 means unknown. On the gateway it is the kernel's
	// TCP_INFO estimate for the public leg; on the agent it is that value
	// relayed over the control link. Read/written lock-free.
	rttBits atomic.Uint64

	// Counters is handed to relay.Splice. Which atomic maps to In vs Out
	// depends on argument order at the splice site; the opener says so via
	// inIsAToB.
	Counters *relay.Counters
	inIsAToB bool

	reg *Registry // for SetPlayer to fire the registry's onPlayer hook
}

// SetPlayer records the sniffed identity and fires the registry's player
// hook. Safe to call from the splice goroutine; the hook runs outside the
// registry lock.
func (e *Entry) SetPlayer(p PlayerInfo) {
	e.player.Store(&p)
	if e.reg != nil && e.reg.onPlayer != nil {
		e.reg.onPlayer(e)
	}
}

// Player returns the sniffed identity, or nil if none has been seen.
func (e *Entry) Player() *PlayerInfo { return e.player.Load() }

// SetRTT records a fresh round-trip measurement (milliseconds) and fires the
// registry's RTT hook. Safe to call from the sampler / control goroutine; the
// hook runs outside the registry lock.
func (e *Entry) SetRTT(ms float64) {
	e.rttBits.Store(math.Float64bits(ms))
	if e.reg != nil && e.reg.onRTT != nil {
		e.reg.onRTT(e)
	}
}

// RTT returns the last measured round-trip time in milliseconds, or -1 when
// none has been recorded.
func (e *Entry) RTT() float64 {
	b := e.rttBits.Load()
	if b == 0 {
		return -1 // never stored (Open seeds -1, so this is a defensive default)
	}
	return math.Float64frombits(b)
}

func (e *Entry) bytesIn() int64 {
	if e.inIsAToB {
		return e.Counters.AToB.Load()
	}
	return e.Counters.BToA.Load()
}

func (e *Entry) bytesOut() int64 {
	if e.inIsAToB {
		return e.Counters.BToA.Load()
	}
	return e.Counters.AToB.Load()
}

// Snapshot is the GUI-facing view of one connection.
type Snapshot struct {
	ID         uint64 `json:"id"`
	AgentID    string `json:"agentId,omitempty"`
	TunnelID   string `json:"tunnelId"`
	TunnelName string `json:"tunnelName"`
	ClientAddr string `json:"clientAddr"`
	StartedAt  int64  `json:"startedAt"` // unix millis
	BytesIn    int64  `json:"bytesIn"`
	BytesOut   int64  `json:"bytesOut"`
	// Player identity, present once the login handshake was sniffed.
	PlayerName string `json:"playerName,omitempty"`
	PlayerUUID string `json:"playerUuid,omitempty"`
	Protocol   int32  `json:"protocol,omitempty"`
	// RttMs is the last measured round-trip time in milliseconds; -1 unknown.
	RttMs float64 `json:"rttMs"`
}

// Registry tracks live connections and lifetime byte totals.
type Registry struct {
	mu    sync.Mutex
	next  uint64
	conns map[uint64]*Entry

	// Closed-connection bytes; live bytes are summed from entries on read.
	closedIn  atomic.Int64
	closedOut atomic.Int64

	// closedByAgent accumulates closed-connection bytes per owning agent so
	// AgentTotals stays monotonic across connection churn (a closing conn moves
	// its bytes here, not out of the per-agent total). Guarded by mu; keyed by
	// AgentID ("" on single-agent/agent-side paths).
	closedByAgent map[string][2]int64

	// Optional observers; set before traffic flows. Hooks run outside the
	// registry lock and must not call back into the registry.
	onOpen   func(e *Entry)
	onClose  func(e *Entry, bytesIn, bytesOut int64)
	onPlayer func(e *Entry)
	onRTT    func(e *Entry)
}

func NewRegistry() *Registry {
	return &Registry{
		conns:         make(map[uint64]*Entry),
		closedByAgent: make(map[string][2]int64),
	}
}

// SetHooks installs connection observers: onOpen fires when a connection is
// registered, onClose with the final byte counts when it ends, onPlayer when
// a sniffed player identity is attached, onRTT when a fresh round-trip
// measurement lands. Any may be nil. Install before traffic flows; hooks run
// outside the registry lock and must not call back into the registry.
func (r *Registry) SetHooks(onOpen func(e *Entry), onClose func(e *Entry, bytesIn, bytesOut int64), onPlayer, onRTT func(e *Entry)) {
	r.onOpen, r.onClose, r.onPlayer, r.onRTT = onOpen, onClose, onPlayer, onRTT
}

// Open registers a connection and returns its entry plus a close func to
// call when the splice ends. agentID is the owning agent ("" on the agent
// side / single-agent legacy paths). connKey is the control-link correlation
// id ("" when the transport assigns none) and is fixed here, before the entry
// is published, so it is never written after another goroutine can see it.
// inIsAToB declares counter orientation: true when the splice's first
// argument is the client-facing leg.
func (r *Registry) Open(agentID, tunnelID, tunnelName, clientAddr, connKey string, inIsAToB bool) (*Entry, func()) {
	e := &Entry{
		AgentID:    agentID,
		TunnelID:   tunnelID,
		TunnelName: tunnelName,
		ClientAddr: clientAddr,
		ConnKey:    connKey,
		StartedAt:  time.Now(),
		Counters:   &relay.Counters{},
		inIsAToB:   inIsAToB,
		reg:        r,
	}
	e.rttBits.Store(math.Float64bits(-1)) // unknown until first measurement
	r.mu.Lock()
	r.next++
	e.ID = r.next
	r.conns[e.ID] = e
	r.mu.Unlock()
	if r.onOpen != nil {
		r.onOpen(e)
	}

	var once sync.Once
	return e, func() {
		once.Do(func() {
			// Fold the final counters into the closed totals under the same
			// lock as the map delete: a concurrent Totals must never observe
			// the entry as neither live nor closed (the total would dip).
			r.mu.Lock()
			in, out := e.bytesIn(), e.bytesOut()
			r.closedIn.Add(in)
			r.closedOut.Add(out)
			cb := r.closedByAgent[e.AgentID]
			cb[0] += in
			cb[1] += out
			r.closedByAgent[e.AgentID] = cb
			delete(r.conns, e.ID)
			r.mu.Unlock()
			if r.onClose != nil {
				r.onClose(e, in, out)
			}
		})
	}
}

// Snapshot lists live connections, newest last.
func (r *Registry) Snapshot() []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Snapshot, 0, len(r.conns))
	for _, e := range r.conns {
		s := Snapshot{
			ID:         e.ID,
			AgentID:    e.AgentID,
			TunnelID:   e.TunnelID,
			TunnelName: e.TunnelName,
			ClientAddr: e.ClientAddr,
			StartedAt:  e.StartedAt.UnixMilli(),
			BytesIn:    e.bytesIn(),
			BytesOut:   e.bytesOut(),
			RttMs:      e.RTT(),
		}
		if p := e.player.Load(); p != nil {
			s.PlayerName, s.PlayerUUID, s.Protocol = p.Name, p.UUID, p.Protocol
		}
		out = append(out, s)
	}
	// Map order is random; stable output keeps the GUI table from jumping.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].ID > out[j].ID; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Count reports the number of live connections.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.conns)
}

// PlayerCount reports how many distinct players have a sniffed identity on a
// live connection — the "players online" gauge, distinct from raw
// connections. Deduped by identity key (handshake UUID, else lowercased
// name), so one player with two connections counts once.
func (r *Registry) PlayerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]struct{}, len(r.conns))
	for _, e := range r.conns {
		p := e.player.Load()
		if p == nil {
			continue
		}
		key := p.UUID
		if key == "" {
			key = "name:" + strings.ToLower(p.Name)
		}
		seen[key] = struct{}{}
	}
	return len(seen)
}

// EntryByConnKey returns the live entry whose ConnKey matches, or nil. Used
// to route per-connection reports (e.g. RTT) that arrive over the control
// link keyed by the gateway-issued connection id.
func (r *Registry) EntryByConnKey(key string) *Entry {
	if key == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.conns {
		if e.ConnKey == key {
			return e
		}
	}
	return nil
}

// AgentTraffic is one agent's monotonic byte totals plus its current live
// connection and identified-player counts — the per-agent inputs to the
// bandwidth-history sampler.
type AgentTraffic struct {
	BytesIn  int64
	BytesOut int64
	Conns    int
	Players  int
}

// AgentTotals groups traffic by owning agent: monotonic bytes (closed + live)
// so the sampler's deltas never dip on a connection close, plus live conn and
// deduped-player counts. Keyed by AgentID; the "" key covers the single-agent /
// agent-side paths. O(live conns) under one lock hold — called at the sample
// cadence, not per byte.
func (r *Registry) AgentTotals() map[string]AgentTraffic {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]AgentTraffic, len(r.closedByAgent)+1)
	for id, cb := range r.closedByAgent {
		out[id] = AgentTraffic{BytesIn: cb[0], BytesOut: cb[1]}
	}
	seen := make(map[string]map[string]struct{})
	for _, e := range r.conns {
		at := out[e.AgentID]
		at.BytesIn += e.bytesIn()
		at.BytesOut += e.bytesOut()
		at.Conns++
		out[e.AgentID] = at
		if p := e.player.Load(); p != nil {
			key := p.UUID
			if key == "" {
				key = "name:" + strings.ToLower(p.Name)
			}
			s := seen[e.AgentID]
			if s == nil {
				s = make(map[string]struct{})
				seen[e.AgentID] = s
			}
			s[key] = struct{}{}
		}
	}
	for id, s := range seen {
		at := out[id]
		at.Players = len(s)
		out[id] = at
	}
	return out
}

// Totals reports lifetime bytes (closed + live), for bandwidth graphs that
// sample and diff.
func (r *Registry) Totals() (in, out int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	in, out = r.closedIn.Load(), r.closedOut.Load()
	for _, e := range r.conns {
		in += e.bytesIn()
		out += e.bytesOut()
	}
	return in, out
}
