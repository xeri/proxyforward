package gateway

import "sync/atomic"

// Refusal and failure accounting.
//
// Every other number this gateway exports describes the happy path: bytes moved,
// players connected, sessions established. None of them change shape when the
// gateway is being flooded or when a tunnel never came up — an operator can see
// that the process is alive and nothing else. These counters are the other half:
// one per branch where the gateway says no, or binds something the agent did not
// ask for.
//
// Nothing in the gateway ever branches on them. They are read by the Prometheus
// endpoint and by tests, and by nothing on any path that decides anything, which
// is why an absent (nil) instance is a legitimate state rather than a bug.

// RefusalCounters counts the refusal and bind-failure branches, cumulative over
// this gateway's lifetime. One instance per Gateway, shared by the limiters, the
// actor and the rebind retry; a nil *RefusalCounters counts nothing, so a
// limiter or actor built without a Gateway (as every unit test that constructs
// one directly does) needs no counters at all.
//
// The increments live *inside* the refusing methods, never at their call sites:
// a refusal reachable from several places would otherwise be counted at some of
// them. udp.go's gate refusal is the proof — it logs nothing and returns
// nothing, so a call-site scheme would have missed it silently.
//
// atomic.Int64 throughout even though authLimiter and connGate already hold
// their mutex when they count, so that "counters are atomic" is one rule rather
// than a per-field judgement; preAuthGate is lock-free and could not use a plain
// field anyway.
type RefusalCounters struct {
	// Refusals: something was turned away.
	AuthFailures   atomic.Int64 // authLimiter.fail — an attempt charged to a network's budget
	ConnsGlobalCap atomic.Int64 // connGate.admit — the gateway-wide connection ceiling
	ConnsPerIPCap  atomic.Int64 // connGate.admit — one source network's ceiling
	PreAuthFull    atomic.Int64 // preAuthGate.admit — the unauthenticated set is full

	// Bind outcomes that are not a clean success. The four failures leave the
	// tunnel down; PortsReassigned is degraded-but-serving and is counted apart,
	// because an operator reading "0 bind failures" must not be reading past a
	// fleet of tunnels answering on ports nobody asked for.
	BindTunnelCap    atomic.Int64 // per-agent listener cap hit
	BindNoFreePort   atomic.Int64 // port 0, and no port this agent may bind was free
	BindPortInUse    atomic.Int64 // requested port taken and no reassignment available
	BindListenFailed atomic.Int64 // listen failed for any other reason
	PortsReassigned  atomic.Int64 // requested port taken, bound elsewhere instead

	// Recovery: what the rebind ticker got back once a squatter left. Paired with
	// the failures above, these are what distinguish a gateway that recovers from
	// one that is merely quiet.
	TunnelsRecovered atomic.Int64
	GroupsRecovered  atomic.Int64
}

func (c *RefusalCounters) authFailed() {
	if c != nil {
		c.AuthFailures.Add(1)
	}
}

func (c *RefusalCounters) connRefusedGlobal() {
	if c != nil {
		c.ConnsGlobalCap.Add(1)
	}
}

func (c *RefusalCounters) connRefusedPerIP() {
	if c != nil {
		c.ConnsPerIPCap.Add(1)
	}
}

func (c *RefusalCounters) preAuthRefused() {
	if c != nil {
		c.PreAuthFull.Add(1)
	}
}

func (c *RefusalCounters) bindTunnelCap() {
	if c != nil {
		c.BindTunnelCap.Add(1)
	}
}

func (c *RefusalCounters) bindNoFreePort() {
	if c != nil {
		c.BindNoFreePort.Add(1)
	}
}

func (c *RefusalCounters) bindPortInUse() {
	if c != nil {
		c.BindPortInUse.Add(1)
	}
}

func (c *RefusalCounters) bindListenFailed() {
	if c != nil {
		c.BindListenFailed.Add(1)
	}
}

func (c *RefusalCounters) portReassigned() {
	if c != nil {
		c.PortsReassigned.Add(1)
	}
}

// tunnelsRecovered takes a count because one retry pass recovers a whole set.
func (c *RefusalCounters) tunnelsRecovered(n int) {
	if c != nil && n > 0 {
		c.TunnelsRecovered.Add(int64(n))
	}
}

func (c *RefusalCounters) groupRecovered() {
	if c != nil {
		c.GroupsRecovered.Add(1)
	}
}

// RefusalCounts is a RefusalCounters snapshot in plain ints, for callers that
// only render it. Each field is loaded independently, so the snapshot is
// consistent per counter and not across them — a scrape is a sample, not a
// transaction, and no consumer compares two of these fields to each other.
type RefusalCounts struct {
	AuthFailures   int64
	ConnsGlobalCap int64
	ConnsPerIPCap  int64
	PreAuthFull    int64

	BindTunnelCap    int64
	BindNoFreePort   int64
	BindPortInUse    int64
	BindListenFailed int64
	PortsReassigned  int64

	TunnelsRecovered int64
	GroupsRecovered  int64
}

// Refusals reports how often this gateway has turned something away or failed to
// bind a listener. Cumulative over this gateway's lifetime, and safe to call on
// one that never started — the counters are a plain field, not built by Start.
func (g *Gateway) Refusals() RefusalCounts {
	c := &g.refusals
	return RefusalCounts{
		AuthFailures:     c.AuthFailures.Load(),
		ConnsGlobalCap:   c.ConnsGlobalCap.Load(),
		ConnsPerIPCap:    c.ConnsPerIPCap.Load(),
		PreAuthFull:      c.PreAuthFull.Load(),
		BindTunnelCap:    c.BindTunnelCap.Load(),
		BindNoFreePort:   c.BindNoFreePort.Load(),
		BindPortInUse:    c.BindPortInUse.Load(),
		BindListenFailed: c.BindListenFailed.Load(),
		PortsReassigned:  c.PortsReassigned.Load(),
		TunnelsRecovered: c.TunnelsRecovered.Load(),
		GroupsRecovered:  c.GroupsRecovered.Load(),
	}
}
