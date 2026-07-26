package e2e

import (
	"io"
	"net"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// poolHarness brings up a per-conn agent through a counting proxy, so the
// agent→gateway connections the pool parks are visible from outside both
// processes: nothing but a real parked connection can move that number before
// any player exists.
func poolHarness(t *testing.T, echoAddr string, tweak func(*harnessOpts)) (*harness, *connCountingProxy) {
	t.Helper()
	var proxy *connCountingProxy
	opts := harnessOpts{
		transport: config.TransportPerConn,
		interpose: func(gwAddr string) string {
			proxy = newConnCountingProxy(t, gwAddr)
			t.Cleanup(proxy.close)
			return proxy.addr()
		},
	}
	if tweak != nil {
		tweak(&opts)
	}
	h := newHarnessWith(t, echoAddr, opts)
	return h, proxy
}

// waitLinkConns polls until at least n agent→gateway connections are open at
// once, or fails. The pool fills asynchronously and deliberately does not delay
// the tunnel coming up, so every pooling assertion starts here.
func waitLinkConns(t *testing.T, p *connCountingProxy, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p.concurrent() >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: only %d agent→gateway connections open, want %d", what, p.concurrent(), n)
}

// holdPlayer opens a player connection and confirms an echo, so its data leg is
// established before the caller asserts anything. The connection stays open
// until the returned close runs.
func holdPlayer(t *testing.T, addr, msg string) (net.Conn, func()) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		c.Close()
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		c.Close()
		t.Fatalf("player %q never echoed: %v", msg, err)
	}
	if string(got) != msg {
		c.Close()
		t.Fatalf("player %q got %q back", msg, got)
	}
	return c, func() { c.Close() }
}

// TestConnPoolServesPlayer: with pooling negotiated, a joining player is served
// on a connection the agent had already dialled, handshaked and authenticated —
// no control round trip, no TCP handshake, no TLS handshake on the join. The
// counter is the proof, not the timing: a fast dial-back would look identical
// on a loopback stopwatch. (conn-pool)
func TestConnPoolServesPlayer(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h, proxy := poolHarness(t, echoAddr, nil)
	addr := h.waitPublicPort()

	// Control conn + the default pool of two, all before any player exists.
	want := 1 + config.PoolSizeDefault
	waitLinkConns(t, proxy, want, "pool never filled")

	if hits := h.gw.PooledLegs(); hits != 0 {
		t.Fatalf("%d pooled legs before any player joined", hits)
	}
	_, done := holdPlayer(t, addr, "pooled")
	defer done()
	if hits := h.gw.PooledLegs(); hits != 1 {
		t.Errorf("player served with %d pooled legs, want 1 — the join paid for a dial-back with a connection parked and waiting", hits)
	}
}

// TestConnPoolRefillsAfterUse: taking a connection out of the pool is what
// triggers its replacement, and that happens while the player who took it is
// still connected — not after they leave. Otherwise the pool would be empty for
// exactly as long as anyone is playing, which is when the next player arrives.
// (conn-pool)
func TestConnPoolRefillsAfterUse(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h, proxy := poolHarness(t, echoAddr, nil)
	addr := h.waitPublicPort()
	waitLinkConns(t, proxy, 1+config.PoolSizeDefault, "pool never filled")

	_, done := holdPlayer(t, addr, "first")
	defer done()
	// Control + the first player's leg + a refilled pool of two.
	waitLinkConns(t, proxy, 2+config.PoolSizeDefault, "pool never refilled while a player was connected")

	_, done2 := holdPlayer(t, addr, "second")
	defer done2()
	if hits := h.gw.PooledLegs(); hits != 2 {
		t.Errorf("%d pooled legs after two players, want 2 — the second join did not find a refilled pool", hits)
	}
}

// TestConnPoolEvictionDrains: eviction closes the connections nobody is using.
// A parked connection has no player and no splice to end it, and its reader
// waits on no deadline, so if closeAll skipped it, it would outlive the session
// on both machines with nothing left to notice. (conn-pool)
func TestConnPoolEvictionDrains(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h, proxy := poolHarness(t, echoAddr, nil)
	h.waitPublicPort()
	waitLinkConns(t, proxy, 1+config.PoolSizeDefault, "pool never filled")

	// Revoking evicts the live session; every connection it holds — control,
	// data, parked — must go with it.
	if !h.gw.RevokeAgent(enrolledAgentID(t, h)) {
		t.Fatal("RevokeAgent refused")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if proxy.concurrent() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%d agent→gateway connections survived eviction: parked connections were not drained", proxy.concurrent())
}

// TestLegacyPeerNoPool: an agent that never learned the capability parks
// nothing and is served exactly as before — the dial-back path, with the
// gateway granting no pool. Pooling is opt-in on the wire in both directions,
// so a mixed-version pair loses the optimization and nothing else. (conn-pool)
func TestLegacyPeerNoPool(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h, proxy := poolHarness(t, echoAddr, func(o *harnessOpts) {
		// Per-conn transport, but a capability offer from before pooling existed.
		o.offerCaps = []string{control.CapTunnelSync, control.CapConnStats, control.CapPerConn}
	})
	addr := h.waitPublicPort()

	// Nothing is ever parked, so the link holds one connection until a player
	// arrives. Give the pool maintainer every chance to misbehave first.
	time.Sleep(500 * time.Millisecond)
	if n := proxy.concurrent(); n != 1 {
		t.Errorf("%d agent→gateway connections with pooling un-negotiated, want 1 (the control conn)", n)
	}

	_, done := holdPlayer(t, addr, "legacy")
	defer done()
	if hits := h.gw.PooledLegs(); hits != 0 {
		t.Errorf("%d pooled legs against a peer that never offered the capability", hits)
	}
}

// TestConnPoolOptOutParksNothing: pool_size = 0 is the operator's off switch,
// and it is honoured before a single connection is parked — an agent that opts
// out looks exactly like one that never learned the capability. (conn-pool)
func TestConnPoolOptOutParksNothing(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h, proxy := poolHarness(t, echoAddr, func(o *harnessOpts) {
		o.tweakAgent = func(c *config.Config) { c.Agent.PoolSize = 0 }
	})
	addr := h.waitPublicPort()

	time.Sleep(500 * time.Millisecond)
	if n := proxy.concurrent(); n != 1 {
		t.Errorf("%d agent→gateway connections with pool_size = 0, want 1 (the control conn)", n)
	}
	_, done := holdPlayer(t, addr, "opted out")
	defer done()
	if hits := h.gw.PooledLegs(); hits != 0 {
		t.Errorf("%d pooled legs with pooling turned off in the agent's config", hits)
	}
}
