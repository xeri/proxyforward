package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/gateway"
	"proxyforward/internal/mc"
)

// idServer is a backend that identifies itself: on each accepted connection it
// writes a fixed 4-byte token, then holds the connection open until the peer closes.
// A player routed through a group thus reads the token of the member that served it,
// and the agent's bare-dial health probe still sees the backend as up.
func idServer(t *testing.T, id string) (addr string, closeFn func()) {
	t.Helper()
	if len(id) != 4 {
		t.Fatalf("idServer id %q must be 4 bytes", id)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	conns := map[net.Conn]struct{}{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns[c] = struct{}{}
			mu.Unlock()
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				c.Write([]byte(id))
				io.Copy(io.Discard, c) // hold open until the peer closes
				mu.Lock()
				delete(conns, c)
				mu.Unlock()
				c.Close()
			}(c)
		}
	}()
	return ln.Addr().String(), func() {
		ln.Close()
		mu.Lock()
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	}
}

// freeGroupPort reserves and releases a port so a group can claim a concrete one.
func freeGroupPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

// tagMember tags one tunnel into a group via the gateway's arbitration path. The
// gateway-side agentID is resolved from the live tunnel table (read before the tag
// moves the tunnel off its solo listener).
func tagMember(t *testing.T, h *harness, tunnelID, groupID string) {
	t.Helper()
	agentID := ""
	for _, ts := range h.gw.Tunnels() {
		if ts.ID == tunnelID {
			agentID = ts.AgentID
			break
		}
	}
	if agentID == "" {
		t.Fatalf("no gateway tunnel found for %s (cannot resolve its agent)", tunnelID)
	}
	ok, err := h.gw.SetTunnelGroup(agentID, tunnelID, groupID)
	if err != nil || !ok {
		t.Fatalf("tag %s into %s: ok=%v err=%v", tunnelID, groupID, ok, err)
	}
}

// groupEligibleCount returns how many members of a group are currently eligible, or
// -1 when the group is unknown.
func groupEligibleCount(h *harness, groupID string) int {
	for _, gs := range h.gw.GroupStatuses() {
		if gs.Spec.ID != groupID {
			continue
		}
		n := 0
		for _, m := range gs.Members {
			if m.Eligible {
				n++
			}
		}
		return n
	}
	return -1
}

// waitGroupEligible blocks until a group has at least want eligible members.
func waitGroupEligible(t *testing.T, h *harness, groupID string, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if groupEligibleCount(h, groupID) >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("group %s never reached %d eligible members (have %d)", groupID, want, groupEligibleCount(h, groupID))
}

// dialReadID connects to a group port and returns the open connection plus the
// 4-byte id of the member that served it.
func dialReadID(t *testing.T, addr string) (net.Conn, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial group port: %v", err)
	}
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	id := make([]byte, 4)
	if _, err := io.ReadFull(c, id); err != nil {
		c.Close()
		t.Fatalf("read member id: %v", err)
	}
	return c, string(id)
}

// TestGroupSpreadsAcrossMembers: three members behind one group port; players are
// distributed by least connections with a deterministic round-robin tiebreak, so nine
// sequential held-open players land evenly, three per member. Proves the whole path —
// tag → join → eligible → pick → route through the existing data plane → real backend.
func TestGroupSpreadsAcrossMembers(t *testing.T) {
	addrA, closeA := idServer(t, "srvA")
	defer closeA()
	h := newHarness(t, addrA)
	h.waitPublicPort()

	bTun, cTun := config.NewID(), config.NewID()
	addrB, closeB := idServer(t, "srvB")
	defer closeB()
	agB := h.addAgent(t, config.NewID(), []config.Tunnel{tcpTunnel(bTun, addrB, 0)})
	waitPublicPortOf(t, agB, bTun)
	addrC, closeC := idServer(t, "srvC")
	defer closeC()
	agC := h.addAgent(t, config.NewID(), []config.Tunnel{tcpTunnel(cTun, addrC, 0)})
	waitPublicPortOf(t, agC, cTun)

	groupPort := freeGroupPort(t)
	if err := h.gw.SetGroup(gateway.GroupSpec{ID: "grp_a", Name: "grp", PublicPort: groupPort, Proto: "tcp", Policy: gateway.PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	tagMember(t, h, h.tunnelID, "grp_a")
	tagMember(t, h, bTun, "grp_a")
	tagMember(t, h, cTun, "grp_a")
	waitGroupEligible(t, h, "grp_a", 3)

	groupAddr := fmt.Sprintf("127.0.0.1:%d", groupPort)
	counts := map[string]int{}
	var open []net.Conn
	defer func() {
		for _, c := range open {
			c.Close()
		}
	}()
	for i := 0; i < 9; i++ {
		c, id := dialReadID(t, groupAddr)
		open = append(open, c)
		counts[id]++
	}
	for _, id := range []string{"srvA", "srvB", "srvC"} {
		if counts[id] != 3 {
			t.Fatalf("least-connections spread over 3 members should be even (3 each); got %v", counts)
		}
	}
}

// TestGroupFailoverStandby: under standby every player goes to the lowest-ordinal
// eligible member (the primary); when the primary dies, new players land on the next
// ordinal. No player migration — only new connections move.
func TestGroupFailoverStandby(t *testing.T) {
	addrA, closeA := idServer(t, "srvA")
	defer closeA()
	h := newHarness(t, addrA)
	h.waitPublicPort()

	bTun := config.NewID()
	addrB, closeB := idServer(t, "srvB")
	defer closeB()
	agB := h.addAgent(t, config.NewID(), []config.Tunnel{tcpTunnel(bTun, addrB, 0)})
	waitPublicPortOf(t, agB, bTun)

	groupPort := freeGroupPort(t)
	if err := h.gw.SetGroup(gateway.GroupSpec{ID: "grp_a", Name: "grp", PublicPort: groupPort, Proto: "tcp", Policy: gateway.PolicyStandby}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	tagMember(t, h, h.tunnelID, "grp_a") // A joins first → lowest ordinal → primary
	tagMember(t, h, bTun, "grp_a")       // B joins second
	waitGroupEligible(t, h, "grp_a", 2)

	groupAddr := fmt.Sprintf("127.0.0.1:%d", groupPort)
	// Every player goes to the primary A while it is eligible.
	for i := 0; i < 3; i++ {
		c, id := dialReadID(t, groupAddr)
		c.Close()
		if id != "srvA" {
			t.Fatalf("standby must route all players to the primary A, got %s", id)
		}
	}

	// Kill the primary: stop agent A. The gateway evicts it, dropping it from the
	// group; the next ordinal (B) becomes the primary.
	h.stopAgent()
	waitGroupEligible(t, h, "grp_a", 1) // only B remains

	// New players now land on B — failover moved new connections, not existing ones.
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, id := dialReadID(t, groupAddr)
		c.Close()
		if id == "srvB" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the primary died, new players should land on B, got %s", id)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestGroupListenerSurvivesMemberEviction: evicting one member leaves the shared
// socket up and the surviving members serving — the group form of eviction isolation.
func TestGroupListenerSurvivesMemberEviction(t *testing.T) {
	addrA, closeA := idServer(t, "srvA")
	defer closeA()
	h := newHarness(t, addrA)
	h.waitPublicPort()

	bTun := config.NewID()
	addrB, closeB := idServer(t, "srvB")
	defer closeB()
	agB := h.addAgent(t, config.NewID(), []config.Tunnel{tcpTunnel(bTun, addrB, 0)})
	waitPublicPortOf(t, agB, bTun)

	groupPort := freeGroupPort(t)
	if err := h.gw.SetGroup(gateway.GroupSpec{ID: "grp_a", Name: "grp", PublicPort: groupPort, Proto: "tcp", Policy: gateway.PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	tagMember(t, h, h.tunnelID, "grp_a")
	tagMember(t, h, bTun, "grp_a")
	waitGroupEligible(t, h, "grp_a", 2)

	groupAddr := fmt.Sprintf("127.0.0.1:%d", groupPort)
	// Evict A (stop its agent). The shared socket must stay up and B keep serving.
	h.stopAgent()
	waitGroupEligible(t, h, "grp_a", 1)

	deadline := time.Now().Add(10 * time.Second)
	for {
		c, id := dialReadID(t, groupAddr)
		c.Close()
		if id == "srvB" {
			return // the socket survived the eviction and the survivor serves
		}
		if time.Now().After(deadline) {
			t.Fatalf("surviving member B should serve after A's eviction, got %s", id)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestGroupOfflineWhenNoEligibleMember: a group whose only member's backend is down
// (never eligible) answers the offline MOTD with its socket still bound, instead of a
// dead port.
func TestGroupOfflineWhenNoEligibleMember(t *testing.T) {
	const motd = "the group is offline — back soon"
	h := newHarnessWith(t, reservedDeadAddr(t), harnessOpts{})
	h.waitPublicPort()

	groupPort := freeGroupPort(t)
	if err := h.gw.SetGroup(gateway.GroupSpec{ID: "grp_a", Name: "grp", PublicPort: groupPort, Proto: "tcp", Policy: gateway.PolicySpread, OfflineMOTD: motd}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	tagMember(t, h, h.tunnelID, "grp_a")

	// The member is cold then down → never eligible → the group serves the offline
	// MOTD in a status response. Poll to ride out the health-probe settling window.
	groupAddr := fmt.Sprintf("127.0.0.1:%d", groupPort)
	deadline := time.Now().Add(20 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", groupAddr, 5*time.Second)
		if err == nil {
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			conn.Write(mcStatusRequest(767))
			id, body, rerr := mc.ReadPacket(conn, 32*1024)
			conn.Close()
			if rerr == nil && id == 0x00 && bytes.Contains(body, []byte(motd)) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("group never served the offline MOTD %q", motd)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestGroupBandwidthCapThrottles: a member's bandwidth cap follows it into a group —
// a player routed through the shared group port is throttled to the cap of the
// member they landed on. A group shares a public port, never a budget. (Which leg
// enforces is pinned in internal/gateway TestGroupMemberCapThrottlesAtGateway; here
// the whole tag → join → route → cap path runs against real agents.)
func TestGroupBandwidthCapThrottles(t *testing.T) {
	srcAddr, closeSrc := sourceServer(t, capPayload)
	defer closeSrc()
	h := newHarnessWith(t, srcAddr, harnessOpts{bandwidthMbps: capMbps, bandwidthScope: config.BandwidthScopeCombined})
	h.waitPublicPort()

	groupPort := freeGroupPort(t)
	if err := h.gw.SetGroup(gateway.GroupSpec{ID: "grp_a", Name: "grp", PublicPort: groupPort, Proto: "tcp", Policy: gateway.PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	tagMember(t, h, h.tunnelID, "grp_a")
	waitGroupEligible(t, h, "grp_a", 1)

	mbps, err := downloadMbps(fmt.Sprintf("127.0.0.1:%d", groupPort), capPayload)
	if err != nil {
		t.Fatalf("download through the group port: %v", err)
	}
	checkCapped(t, "grouped member", mbps)
}

// TestFailedBindRecoversWithoutReconnect: a tunnel whose public port was held by
// another process at bind time comes back on its own once that process exits —
// the agent stays connected throughout and simply learns the new port from the
// push. Before the rebind retry the tunnel stayed dark until the agent happened
// to reconnect, which on a healthy link could be hours.
func TestFailedBindRecoversWithoutReconnect(t *testing.T) {
	// One allowed port and nothing to reassign to, so a squatted port is a hard
	// bind failure rather than a silent move to an alternative.
	port := freeGroupPort(t)
	squatter, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("squat port %d: %v", port, err)
	}
	defer squatter.Close()

	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarnessWith(t, echoAddr, harnessOpts{tweakGateway: func(c *config.Config) {
		c.Gateway.PortAllowlist = []int{port}
	}})

	// The agent is connected, but its one tunnel has no listener.
	deadline := time.Now().Add(10 * time.Second)
	for !h.gw.AgentConnected() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !h.gw.AgentConnected() {
		t.Fatal("agent never connected")
	}
	if p, ok := h.gw.TunnelPort(h.tunnelID); ok {
		t.Fatalf("tunnel bound on port %d despite its only allowed port being held", p)
	}

	squatter.Close()
	h.gw.RetryFailedBinds()

	addr := h.waitPublicPort()
	if addr != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("recovered tunnel is at %s, want the freed port %d", addr, port)
	}
	// The proof that matters: a player gets through, on a link the agent never
	// reconnected.
	roundTrip(t, addr, []byte("recovered"))
}
