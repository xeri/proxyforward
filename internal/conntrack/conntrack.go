// Package conntrack is the live-connection registry behind the GUI's
// connections table and bandwidth stats: one entry per proxied client
// connection, byte counters updated lock-free by the splice, snapshots taken
// on demand (the data path never blocks on a reader).
package conntrack

import (
	"sync"
	"sync/atomic"
	"time"

	"proxyforward/internal/relay"
)

// Entry is one live proxied connection. Counter direction is explicit:
// In = client → server bytes, Out = server → client bytes.
type Entry struct {
	ID         uint64
	TunnelID   string
	TunnelName string
	ClientAddr string
	StartedAt  time.Time

	// Counters is handed to relay.Splice. Which atomic maps to In vs Out
	// depends on argument order at the splice site; the opener says so via
	// inIsAToB.
	Counters *relay.Counters
	inIsAToB bool
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
	TunnelID   string `json:"tunnelId"`
	TunnelName string `json:"tunnelName"`
	ClientAddr string `json:"clientAddr"`
	StartedAt  int64  `json:"startedAt"` // unix millis
	BytesIn    int64  `json:"bytesIn"`
	BytesOut   int64  `json:"bytesOut"`
}

// Registry tracks live connections and lifetime byte totals.
type Registry struct {
	mu    sync.Mutex
	next  uint64
	conns map[uint64]*Entry

	// Closed-connection bytes; live bytes are summed from entries on read.
	closedIn  atomic.Int64
	closedOut atomic.Int64

	// Optional observers (the stats store); set before traffic flows.
	onOpen  func(clientAddr string)
	onClose func(clientAddr string, bytesIn, bytesOut int64)
}

func NewRegistry() *Registry {
	return &Registry{conns: make(map[uint64]*Entry)}
}

// SetHooks installs connection observers: onOpen fires when a connection is
// registered, onClose with the final byte counts when it ends. Install before
// traffic flows; hooks run outside the registry lock and must not call back
// into the registry.
func (r *Registry) SetHooks(onOpen func(clientAddr string), onClose func(clientAddr string, bytesIn, bytesOut int64)) {
	r.onOpen, r.onClose = onOpen, onClose
}

// Open registers a connection and returns its entry plus a close func to
// call when the splice ends. inIsAToB declares counter orientation: true
// when the splice's first argument is the client-facing leg.
func (r *Registry) Open(tunnelID, tunnelName, clientAddr string, inIsAToB bool) (*Entry, func()) {
	e := &Entry{
		TunnelID:   tunnelID,
		TunnelName: tunnelName,
		ClientAddr: clientAddr,
		StartedAt:  time.Now(),
		Counters:   &relay.Counters{},
		inIsAToB:   inIsAToB,
	}
	r.mu.Lock()
	r.next++
	e.ID = r.next
	r.conns[e.ID] = e
	r.mu.Unlock()
	if r.onOpen != nil {
		r.onOpen(clientAddr)
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
			delete(r.conns, e.ID)
			r.mu.Unlock()
			if r.onClose != nil {
				r.onClose(e.ClientAddr, in, out)
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
		out = append(out, Snapshot{
			ID:         e.ID,
			TunnelID:   e.TunnelID,
			TunnelName: e.TunnelName,
			ClientAddr: e.ClientAddr,
			StartedAt:  e.StartedAt.UnixMilli(),
			BytesIn:    e.bytesIn(),
			BytesOut:   e.bytesOut(),
		})
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
