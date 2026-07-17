package gateway

import (
	"container/list"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"
)

// authFailNets bounds how many source networks the auth limiter remembers at
// once. It is a hard cap, not a cleanup trigger: the table cannot outgrow it, so
// no caller ever pays to walk it.
const authFailNets = 4096

// sourceNet reduces a source address to the unit of abuse accounting: the
// smallest network one party cannot cheaply multiply.
//
// Per-address is right for IPv4, where every address costs money. It is
// meaningless for IPv6, where the smallest block anyone is handed is a /64 (RFC
// 6177 puts a *site* at /56 or /48): one host sources from 2^64 addresses that
// cost nothing and share no state. Keyed per address, every limit underneath —
// the auth failure budget, the per-IP connection cap — is bypassed by
// incrementing the low bits, and any table keyed by them grows 2^64 wide.
//
// So IPv6 accounts per /64: the floor of what a single subscriber controls.
// Aggregating further (/48) would also catch a subscriber holding many /64s, but
// starts pooling unrelated customers of the same ISP into one budget, which turns
// a limit into collateral damage.
//
// This lives inside the limiters rather than at their call sites on purpose: a
// caller that reduced the address itself would eventually forget to (and
// remoteIP's other callers — the GUI's link card, the cloned-identity warning —
// legitimately want the whole address).
func sourceNet(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip // an exotic transport's opaque address: account for it whole
	}
	if addr = addr.Unmap(); addr.Is4() {
		return addr.String()
	}
	p, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return p.String()
}

// authLimiter rate-limits failed authentication attempts per source network
// (fail2ban semantics: successes never count, so a legitimately flapping
// agent reconnecting through backoff is never locked out, but a scanner
// brute-forcing tokens is).
//
// Each network gets a token bucket, and the table of them is LRU-capped. Both
// properties are load-bearing, because every honest agent's admission waits on
// this one mutex — allow() is the first thing handleControlConn does. Holding it
// for work proportional to the table's size hands an attacker the gateway.
//
// The bucket replaces a per-network []time.Time, which grew in two directions at
// once (networks, and failures remembered per network) and whose cleanup made
// that worse rather than better: it triggered on size (>4096 keys) but deleted
// only *fully expired* entries, so a flood that keeps its entries fresh frees
// nothing, grows past the cap without limit, and re-scans the entire map on every
// insert with the lock held. A scalar that refills is O(1) to store and O(1) to
// update; the cap makes the table's size a constant.
type authLimiter struct {
	mu     sync.Mutex
	perMin int
	window time.Duration
	maxNet int
	lru    *list.List // *authBudget, most recently touched at the front
	at     map[string]*list.Element
	now    func() time.Time
}

// authBudget is one network's token bucket: it holds the failures still
// affordable, refilling at perMin per window, so it is both the count and the
// clock. An absent bucket is a full one, which is why honest traffic costs no
// memory.
type authBudget struct {
	net      string
	tokens   float64
	refilled time.Time
}

func newAuthLimiter(perMin int) *authLimiter {
	return &authLimiter{
		perMin: perMin,
		window: time.Minute,
		maxNet: authFailNets,
		lru:    list.New(),
		at:     make(map[string]*list.Element),
		now:    time.Now,
	}
}

// allow reports whether ip's network can afford another attempt. It records
// nothing: an address that has never failed leaves no trace, so the table only
// ever holds abusers.
func (l *authLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	el, ok := l.at[sourceNet(ip)]
	if !ok {
		return true
	}
	b := el.Value.(*authBudget)
	if l.refillLocked(b) {
		// Recovered in full, and so indistinguishable from never having failed.
		// Dropping it keeps the table proportional to live abuse, not to history.
		l.dropLocked(el)
		return true
	}
	l.lru.MoveToFront(el)
	// A whole token, not any token: refill is continuous, so admitting on a
	// fraction would end every block the instant it began.
	return b.tokens >= 1
}

// fail charges one failed attempt to ip's network.
func (l *authLimiter) fail(ip string) {
	key := sourceNet(ip)
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.at[key]; ok {
		b := el.Value.(*authBudget)
		l.refillLocked(b)
		// The floor bounds how long a block can last: without it a long burst
		// buys a ban proportional to the burst, and the bystander sharing a
		// NAT'd address with the attacker serves it.
		if b.tokens--; b.tokens < 0 {
			b.tokens = 0
		}
		l.lru.MoveToFront(el)
		return
	}
	l.at[key] = l.lru.PushFront(&authBudget{net: key, tokens: float64(l.perMin) - 1, refilled: l.now()})
	// One insert, at most one eviction. The least recently touched entry is also
	// the most refilled, so eviction takes whichever network is closest to
	// forgiven anyway — and rotating addresses cannot evict your own block,
	// because hammering keeps your own entry at the front.
	for l.lru.Len() > l.maxNet {
		l.dropLocked(l.lru.Back())
	}
}

// refillLocked credits the tokens owed since the last touch and reports whether
// the bucket came back full (nothing left worth remembering).
func (l *authLimiter) refillLocked(b *authBudget) (full bool) {
	burst := float64(l.perMin)
	now := l.now()
	if d := now.Sub(b.refilled); d > 0 {
		b.tokens += burst * float64(d) / float64(l.window)
		b.refilled = now
	}
	if b.tokens >= burst {
		b.tokens = burst
		return true
	}
	return false
}

func (l *authLimiter) dropLocked(el *list.Element) {
	delete(l.at, l.lru.Remove(el).(*authBudget).net)
}

// connGate enforces concurrent-connection caps on public listeners: a global
// ceiling protecting the gateway and a per-network ceiling protecting it from any
// single client. Per-network, not per-address, for the reason sourceNet gives —
// otherwise one IPv6 subscriber takes the global cap 32 conns at a time.
type connGate struct {
	mu        sync.Mutex
	maxGlobal int
	maxPerIP  int
	global    int
	perNet    map[string]int
}

func newConnGate(maxGlobal, maxPerIP int) *connGate {
	return &connGate{maxGlobal: maxGlobal, maxPerIP: maxPerIP, perNet: make(map[string]int)}
}

// admit reserves a slot for ip; the caller must release(ip) when the
// connection ends iff admit returned true. perNet is bounded by maxGlobal — an
// entry exists only while a connection it counts is live.
func (g *connGate) admit(ip string) bool {
	key := sourceNet(ip)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.global >= g.maxGlobal || g.perNet[key] >= g.maxPerIP {
		return false
	}
	g.global++
	g.perNet[key]++
	return true
}

func (g *connGate) release(ip string) {
	key := sourceNet(ip)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.global--
	if g.perNet[key] <= 1 {
		delete(g.perNet, key)
	} else {
		g.perNet[key]--
	}
}

// preAuthHeadroom is what the pre-auth gate allows *above* the public
// connection cap. Concurrent legitimate pre-auth work is bounded by that cap —
// under the per-conn data plane every player conn causes at most one dial-back,
// and each holds its slot only for a handshake — so this covers the rest: agent
// control conns, and reconnect attempts racing a session that has not yet been
// reaped. Generous on purpose. The gate exists to make the unauthenticated set a
// constant, not to be a tight limit; authLimiter is what punishes an abuser, and
// it needs ~preAuthTimeout to notice one.
const preAuthHeadroom = 256

// preAuthGate bounds how many connections may sit in the pre-auth prologue at
// once. Without it the unauthenticated set is unbounded for preAuthTimeout at a
// stretch: authLimiter.allow only refuses a network once its failures have
// landed, and a connection that stalls does not fail until the deadline fires.
// So a peer that opens sockets and says nothing spends ~10 s buying a goroutine,
// a descriptor and a TLS handshake buffer each, for free — enough to exhaust
// descriptors, which is exactly what makes Accept start erroring.
//
// A counted semaphore, not a connGate: this cannot be keyed per network. The
// per-conn data plane dials back from the agent's own address once per player,
// so a per-network cap tight enough to matter would throttle the data plane, and
// one loose enough not to would not be a cap.
type preAuthGate struct {
	slots chan struct{}
}

func newPreAuthGate(max int) *preAuthGate {
	return &preAuthGate{slots: make(chan struct{}, max)}
}

// admit takes a slot, reporting false when the gate is full.
func (p *preAuthGate) admit() bool {
	select {
	case p.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// release returns a slot. Never call it without a matching admit — a slot freed
// twice raises the ceiling permanently, which is worse than no gate, because it
// looks like one. Use releaser at call sites with more than one exit.
func (p *preAuthGate) release() { <-p.slots }

// releaser returns a release func that is safe to call any number of times, for
// the pre-auth path: it must run on every failure path (defer) *and* the moment
// auth succeeds, so that an authenticated session — which may live for weeks —
// stops occupying a slot meant for handshakes.
func (p *preAuthGate) releaser() func() { return sync.OnceFunc(p.release) }

// isListenerClosed reports whether an Accept error means the listener is gone,
// which is the only reason to stop accepting. Everything else — the OS out of
// descriptors or buffers, a peer resetting between SYN and accept — is transient
// and must be retried: returning instead leaves the process up, the public ports
// still serving players, and no agent able to reconnect, forever.
func isListenerClosed(err error) bool {
	return err != nil && errors.Is(err, net.ErrClosed)
}

// remoteIP extracts the bare IP from a net.Conn's remote address, falling
// back to the whole string for exotic transports.
func remoteIP(conn net.Conn) string { return ipFromAddr(conn.RemoteAddr()) }

// ipFromAddr extracts the bare IP from any net.Addr (a QUIC session reports a
// *net.UDPAddr), falling back to the whole string for exotic transports.
func ipFromAddr(addr net.Addr) string {
	if host, _, err := net.SplitHostPort(addr.String()); err == nil {
		return host
	}
	return addr.String()
}
