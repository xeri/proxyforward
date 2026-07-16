package gateway

import (
	"net"
	"sync"
	"time"
)

// authLimiter rate-limits failed authentication attempts per source IP
// (fail2ban semantics: successes never count, so a legitimately flapping
// agent reconnecting through backoff is never locked out, but a scanner
// brute-forcing tokens is).
type authLimiter struct {
	mu     sync.Mutex
	perMin int
	window time.Duration
	fails  map[string][]time.Time
	now    func() time.Time
}

func newAuthLimiter(perMin int) *authLimiter {
	return &authLimiter{
		perMin: perMin,
		window: time.Minute,
		fails:  make(map[string][]time.Time),
		now:    time.Now,
	}
}

// allow reports whether ip is under the failure limit.
func (l *authLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pruneLocked(ip)) < l.perMin
}

// fail records one failed attempt from ip.
func (l *authLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.pruneLocked(ip), l.now())
	// Keep the map bounded even under a spoofed-source flood.
	if len(l.fails) > 4096 {
		for k := range l.fails {
			if len(l.pruneLocked(k)) == 0 {
				delete(l.fails, k)
			}
		}
	}
}

// pruneLocked drops entries older than the window and returns what remains.
func (l *authLimiter) pruneLocked(ip string) []time.Time {
	cutoff := l.now().Add(-l.window)
	ts := l.fails[ip]
	keep := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, ip)
		return nil
	}
	l.fails[ip] = keep
	return keep
}

// connGate enforces concurrent-connection caps on public listeners: a global
// ceiling protecting the gateway and a per-IP ceiling protecting it from any
// single client.
type connGate struct {
	mu        sync.Mutex
	maxGlobal int
	maxPerIP  int
	global    int
	perIP     map[string]int
}

func newConnGate(maxGlobal, maxPerIP int) *connGate {
	return &connGate{maxGlobal: maxGlobal, maxPerIP: maxPerIP, perIP: make(map[string]int)}
}

// admit reserves a slot for ip; the caller must release(ip) when the
// connection ends iff admit returned true.
func (g *connGate) admit(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.global >= g.maxGlobal || g.perIP[ip] >= g.maxPerIP {
		return false
	}
	g.global++
	g.perIP[ip]++
	return true
}

func (g *connGate) release(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.global--
	if g.perIP[ip] <= 1 {
		delete(g.perIP, ip)
	} else {
		g.perIP[ip]--
	}
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
