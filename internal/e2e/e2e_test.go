// Package e2e runs the full loopback tunnel in-process: real gateway, real
// agent, real TLS + mux + splice — only the WAN is missing. These tests are
// the milestone-2 exit criteria: byte round-trip, rapid agent kill/restart
// (ghost-listener sequencing), duplicate-agent rejection, burst throughput
// with cross-connection latency, and goroutine-leak freedom.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"proxyforward/internal/agent"
	"proxyforward/internal/config"
	"proxyforward/internal/gateway"
)

func TestMain(m *testing.M) {
	// winio starts one process-lifetime IO-completion goroutine on first
	// named-pipe use (attach_test.go) and never stops it — by design, not a
	// leak.
	goleak.VerifyTestMain(m,
		goleak.IgnoreAnyFunction("github.com/Microsoft/go-winio.ioCompletionProcessor"))
}

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(bytes.TrimRight(p, "\n")))
	return len(p), nil
}

// echoServer accepts loopback conns and echoes everything back.
func echoServer(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				io.Copy(c, c)
			}(c)
		}
	}()
	return ln.Addr().String(), func() {
		ln.Close()
		wg.Wait()
	}
}

// harness wires a gateway + agent on loopback with one tunnel to localAddr.
type harness struct {
	t         *testing.T
	gw        *gateway.Gateway
	gwCancel  context.CancelFunc
	agentCfg  *config.Config
	tunnelID  string
	agent     *agent.Agent
	agentCtx  context.Context
	agentStop context.CancelFunc
	agentDone chan error
	offerCaps []string // non-nil overrides the agent's capability offer
}

// harnessOpts tweak the harness before anything starts.
type harnessOpts struct {
	tweakGateway func(*config.Config)
	// offerCaps overrides the agent's hello capability offer; an explicit
	// empty slice simulates a legacy (pre-capability) agent.
	offerCaps []string
	// mcAware marks the tunnel Minecraft-aware so both seams sniff logins.
	mcAware bool
}

func newHarness(t *testing.T, localAddr string) *harness {
	return newHarnessWith(t, localAddr, harnessOpts{})
}

func newHarnessWith(t *testing.T, localAddr string, opts harnessOpts) *harness {
	t.Helper()
	logger := testLogger(t)

	gwCfg := config.Default()
	gwCfg.Role = config.RoleGateway
	gwCfg.Gateway.Token = config.NewToken()
	gwCfg.Gateway.BindAddr = "127.0.0.1"
	gwCfg.Gateway.ControlPort = 0 // ephemeral
	if opts.tweakGateway != nil {
		opts.tweakGateway(gwCfg)
	}

	gwCtx, gwCancel := context.WithCancel(context.Background())
	gw := gateway.New(gwCfg, t.TempDir(), logger.With("side", "gateway"))
	if err := gw.Start(gwCtx); err != nil {
		gwCancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		gwCancel()
		gw.Shutdown()
	})

	tunnelID := config.NewID()
	agentCfg := config.Default()
	agentCfg.Role = config.RoleAgent
	agentCfg.Agent.AgentID = config.NewID()
	agentCfg.Agent.GatewayHost = "127.0.0.1"
	agentCfg.Agent.GatewayPort = gw.ControlAddr().(*net.TCPAddr).Port
	agentCfg.Agent.Token = gwCfg.Gateway.Token
	agentCfg.Agent.CertFingerprint = gw.Fingerprint()
	agentCfg.Agent.Tunnels = []config.Tunnel{{
		ID:         tunnelID,
		Name:       "test",
		Type:       config.TunnelTCP,
		LocalAddr:  localAddr,
		PublicPort: 0, // gateway picks
		Enabled:    true,
		Options:    config.TunnelOptions{MinecraftAware: opts.mcAware},
	}}

	h := &harness{t: t, gw: gw, gwCancel: gwCancel, agentCfg: agentCfg, tunnelID: tunnelID, offerCaps: opts.offerCaps}
	h.startAgent()
	t.Cleanup(h.stopAgent)
	return h
}

func (h *harness) startAgent() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.agentCtx, h.agentStop = ctx, cancel
	h.agent = agent.New(h.agentCfg, testLogger(h.t).With("side", "agent"))
	if h.offerCaps != nil {
		h.agent.SetCapabilityOffer(h.offerCaps)
	}
	h.agentDone = make(chan error, 1)
	done := h.agentDone
	a := h.agent
	go func() { done <- a.Run(ctx) }()
}

func (h *harness) stopAgent() {
	h.t.Helper()
	if h.agentStop == nil {
		return
	}
	h.agentStop()
	select {
	case <-h.agentDone:
	case <-time.After(10 * time.Second):
		h.t.Fatal("agent did not stop within 10s")
	}
	h.agentStop = nil
}

// waitPublicPort polls until the tunnel is registered and returns its
// public address.
func (h *harness) waitPublicPort() string {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if port, ok := h.agent.TunnelPublicPort(h.tunnelID); ok {
			return fmt.Sprintf("127.0.0.1:%d", port)
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatal("tunnel never became live")
	return ""
}

func roundTrip(t *testing.T, addr string, payload []byte) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial public port: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("echo mismatch")
	}
}

// TestHealthPropagates: the agent's local-target probe result reaches the
// gateway over the control stream (the offline responder's data source).
func TestHealthPropagates(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	h.waitPublicPort()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if up, known := h.gw.TunnelLocalUp(h.tunnelID); known {
			if !up {
				t.Fatal("gateway thinks a live backend is down")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("gateway never learned the backend's health")
}

// TestGatewayTunnelsSnapshot: the gateway can enumerate agent-registered
// tunnels for its status surfaces — the GUI's "tunnels" / "public port"
// display — and the list empties again when the agent goes away.
func TestGatewayTunnelsSnapshot(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addr := h.waitPublicPort()
	wantPort := h.agent.MustPublicPort(h.tunnelID)

	tunnels := h.gw.Tunnels()
	if len(tunnels) != 1 {
		t.Fatalf("gateway reports %d tunnels, want 1: %+v", len(tunnels), tunnels)
	}
	ts := tunnels[0]
	if ts.ID != h.tunnelID || ts.Name != "test" || ts.PublicPort != wantPort {
		t.Fatalf("snapshot mismatch: got %+v, want id=%s name=test port=%d", ts, h.tunnelID, wantPort)
	}

	// Health joins in once the agent's first probe report lands.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ts := h.gw.Tunnels()[0]; ts.LocalKnown {
			if !ts.LocalUp {
				t.Fatal("snapshot says a live backend is down")
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	roundTrip(t, addr, []byte("still serving"))

	// Eviction clears the list.
	h.stopAgent()
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.gw.Tunnels()) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tunnels still listed after agent disconnect: %+v", h.gw.Tunnels())
}

// TestConfigHotApply: tunnel edits reach the live session without a
// restart — adds bind, removals unbind, existing tunnels stay untouched.
func TestConfigHotApply(t *testing.T) {
	echoA, closeA := echoServer(t)
	defer closeA()
	echoB, closeB := echoServer(t)
	defer closeB()

	h := newHarness(t, echoA)
	addrA := h.waitPublicPort()
	roundTrip(t, addrA, []byte("tunnel A up"))

	// Add tunnel B alongside A.
	tunnelB := config.Tunnel{
		ID: config.NewID(), Name: "test-b", Type: config.TunnelTCP,
		LocalAddr: echoB, PublicPort: 0, Enabled: true,
	}
	current := h.agent.Tunnels()
	h.agent.ApplyTunnels(append(current, tunnelB))

	var addrB string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if port, ok := h.agent.TunnelPublicPort(tunnelB.ID); ok {
			addrB = fmt.Sprintf("127.0.0.1:%d", port)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if addrB == "" {
		t.Fatal("hot-applied tunnel never became live")
	}
	roundTrip(t, addrB, []byte("tunnel B up"))
	roundTrip(t, addrA, []byte("tunnel A still up"))

	// Remove tunnel A; its public port must actually unbind.
	h.agent.ApplyTunnels([]config.Tunnel{tunnelB})
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addrA, time.Second)
		if err != nil {
			break // unbound
		}
		conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	if conn, err := net.DialTimeout("tcp", addrA, time.Second); err == nil {
		conn.Close()
		t.Fatal("removed tunnel's public port still accepts connections")
	}
	roundTrip(t, addrB, []byte("tunnel B survives A's removal"))
}

// TestSyncIdempotent: re-applying an identical tunnel set is a no-op on the
// gateway — the public port stays the same and connections opened before the
// re-sync keep flowing.
func TestSyncIdempotent(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addr := h.waitPublicPort()
	portBefore := h.agent.MustPublicPort(h.tunnelID)

	// A long-lived client connection straddling the re-sync.
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	echo := func(payload []byte) {
		t.Helper()
		if _, err := conn.Write(payload); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, got); err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("echo mismatch")
		}
	}
	echo([]byte("before re-sync"))

	// Same desired state, twice for good measure.
	h.agent.ApplyTunnels(h.agent.Tunnels())
	h.agent.ApplyTunnels(h.agent.Tunnels())

	// The port must not move and the pre-sync connection must survive.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if port, ok := h.agent.TunnelPublicPort(h.tunnelID); ok && port != portBefore {
			t.Fatalf("idempotent re-sync moved the public port: %d → %d", portBefore, port)
		}
		time.Sleep(20 * time.Millisecond)
	}
	echo([]byte("after re-sync"))
	roundTrip(t, addr, []byte("new connections fine too"))
}

// TestSyncRebindOnChange: changing a live tunnel's public port rebinds — the
// old port stops accepting and the new one serves.
func TestSyncRebindOnChange(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addrOld := h.waitPublicPort()

	// Reserve a distinct free port for the move.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	newPort := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	tunnels := h.agent.Tunnels()
	tunnels[0].PublicPort = newPort
	h.agent.ApplyTunnels(tunnels)

	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("tunnel never moved to the new port")
		}
		if port, ok := h.agent.TunnelPublicPort(h.tunnelID); ok && port == newPort {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	roundTrip(t, fmt.Sprintf("127.0.0.1:%d", newPort), []byte("serving on the new port"))

	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addrOld, time.Second)
		if err != nil {
			return // old port unbound
		}
		conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("old public port still accepts connections after the move")
}

// TestLegacyRegisterFallback: an agent that offers no capabilities (a
// pre-capability build) must still hot-apply via the per-tunnel
// register/unregister path against the new gateway.
func TestLegacyRegisterFallback(t *testing.T) {
	echoA, closeA := echoServer(t)
	defer closeA()
	echoB, closeB := echoServer(t)
	defer closeB()

	h := newHarnessWith(t, echoA, harnessOpts{offerCaps: []string{}})
	addrA := h.waitPublicPort()
	roundTrip(t, addrA, []byte("legacy tunnel A up"))

	tunnelB := config.Tunnel{
		ID: config.NewID(), Name: "test-b", Type: config.TunnelTCP,
		LocalAddr: echoB, PublicPort: 0, Enabled: true,
	}
	h.agent.ApplyTunnels(append(h.agent.Tunnels(), tunnelB))

	var addrB string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if port, ok := h.agent.TunnelPublicPort(tunnelB.ID); ok {
			addrB = fmt.Sprintf("127.0.0.1:%d", port)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if addrB == "" {
		t.Fatal("legacy hot-applied tunnel never became live")
	}
	roundTrip(t, addrB, []byte("legacy tunnel B up"))

	// Removal must unbind through the legacy path too.
	h.agent.ApplyTunnels([]config.Tunnel{tunnelB})
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addrA, time.Second)
		if err != nil {
			roundTrip(t, addrB, []byte("legacy B survives A's removal"))
			return
		}
		conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("legacy removed tunnel's public port still accepts connections")
}

// TestSyncPartialFailure: one invalid tunnel in the desired set must not
// poison the rest — the valid tunnel binds, the disallowed one is rejected
// and the session stays up.
func TestSyncPartialFailure(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarnessWith(t, echoAddr, harnessOpts{tweakGateway: func(cfg *config.Config) {
		cfg.Gateway.PortAllowlist = []int{40000} // port 0 (ephemeral) bypasses the allowlist
	}})
	addrA := h.waitPublicPort()

	badTunnel := config.Tunnel{
		ID: config.NewID(), Name: "forbidden", Type: config.TunnelTCP,
		LocalAddr: echoAddr, PublicPort: 39999, Enabled: true, // not in the allowlist
	}
	h.agent.ApplyTunnels(append(h.agent.Tunnels(), badTunnel))

	// The valid tunnel keeps serving while the bad one never comes up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.agent.TunnelPublicPort(badTunnel.ID); ok {
			t.Fatal("allowlist-violating tunnel became live")
		}
		time.Sleep(20 * time.Millisecond)
	}
	roundTrip(t, addrA, []byte("valid tunnel unharmed by sibling rejection"))
}

func TestTunnelRoundTrip(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addr := h.waitPublicPort()
	roundTrip(t, addr, []byte("hello through the tunnel"))

	// Several concurrent clients over one session.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := make([]byte, 32*1024)
			rand.Read(payload)
			roundTrip(t, addr, payload)
		}(i)
	}
	wg.Wait()
}

// TestPerConnRTTPropagates: the gateway measures each public connection's
// kernel RTT and reports it to the agent over the control link (conn-stats), so
// both sides can attribute a network latency to the connection. tcpinfo is
// best-effort, so a kernel that yields no sample skips rather than fails.
func TestPerConnRTTPropagates(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addr := h.waitPublicPort()

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial public port: %v", err)
	}
	defer conn.Close()

	exchange := func() {
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := conn.Write([]byte("ping")); err != nil {
			return
		}
		io.ReadFull(conn, make([]byte, 4))
	}
	exchange() // prime the kernel's RTT estimate

	// The gateway samples every rttSampleInterval (5s); allow a few cycles for
	// the value to be measured and relayed.
	deadline := time.Now().Add(20 * time.Second)
	var gwOK, agentOK bool
	for time.Now().Before(deadline) && !(gwOK && agentOK) {
		exchange() // keep the connection warm so the kernel keeps a sample
		for _, s := range h.gw.Conns.Snapshot() {
			if s.RttMs >= 0 {
				gwOK = true
			}
		}
		for _, s := range h.agent.Conns.Snapshot() {
			if s.RttMs >= 0 {
				agentOK = true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !gwOK {
		t.Skip("kernel provided no TCP_INFO RTT sample; probe is best-effort")
	}
	if !agentOK {
		t.Fatal("gateway measured RTT but it never reached the agent over conn-stats")
	}
}

// TestAgentRestartRebinds is the ghost-listener test: kill the agent, start
// a new one with the same identity and port, repeatedly and fast. Every
// generation must come back serving traffic.
func TestAgentRestartRebinds(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addr := h.waitPublicPort()
	roundTrip(t, addr, []byte("gen 1"))

	// Pin the public port for subsequent registrations so each restart
	// re-binds the same port — the racy case.
	port := h.agent.MustPublicPort(h.tunnelID)
	h.agentCfg.Agent.Tunnels[0].PublicPort = port

	for i := 0; i < 5; i++ {
		h.stopAgent()
		h.startAgent()
		deadline := time.Now().Add(10 * time.Second)
		var lastErr error
		for {
			if time.Now().After(deadline) {
				t.Fatalf("restart %d: tunnel never came back: %v", i, lastErr)
			}
			if _, ok := h.agent.TunnelPublicPort(h.tunnelID); ok {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		roundTrip(t, fmt.Sprintf("127.0.0.1:%d", port), []byte(fmt.Sprintf("gen %d", i+2)))
	}
}

// TestSecondAgentRejected: a different agent identity with the same token
// must be refused while the first is connected — and the refusal is fatal
// (no retry hammering).
func TestSecondAgentRejected(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	h.waitPublicPort()

	intruderCfg := config.Default()
	*intruderCfg = *h.agentCfg
	intruderCfg.Agent.AgentID = config.NewID() // different identity
	intruderCfg.Agent.Tunnels = nil

	intruder := agent.New(intruderCfg, testLogger(t).With("side", "intruder"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := intruder.Run(ctx)
	if !errors.Is(err, agent.ErrAgentConflict) {
		t.Fatalf("want ErrAgentConflict, got %v", err)
	}
	// The original session must be unharmed.
	roundTrip(t, h.waitPublicPort(), []byte("still alive"))
}

// TestBadTokenRejected: wrong token → fatal ErrBadToken.
func TestBadTokenRejected(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	h.waitPublicPort()

	thiefCfg := config.Default()
	*thiefCfg = *h.agentCfg
	thiefCfg.Agent.AgentID = config.NewID()
	thiefCfg.Agent.Token = config.NewToken() // wrong
	thiefCfg.Agent.Tunnels = nil

	thief := agent.New(thiefCfg, testLogger(t).With("side", "thief"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := thief.Run(ctx)
	if !errors.Is(err, agent.ErrBadToken) {
		t.Fatalf("want ErrBadToken, got %v", err)
	}
}

// TestBurstThroughputAndCrossStreamLatency pushes 64 MiB through one
// connection while a second connection does small echo round-trips; the
// burst must move fast and must not starve the small stream.
func TestBurstThroughputAndCrossStreamLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("burst benchmark skipped in -short")
	}
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addr := h.waitPublicPort()

	const burstSize = 64 << 20
	burstDone := make(chan error, 1)
	start := time.Now()
	go func() {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			burstDone <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(120 * time.Second))
		chunk := make([]byte, 1<<20)
		rand.Read(chunk)
		var wg sync.WaitGroup
		wg.Add(1)
		var readErr error
		go func() { // drain the echo so flow control keeps moving
			defer wg.Done()
			_, readErr = io.CopyN(io.Discard, conn, burstSize)
		}()
		for sent := 0; sent < burstSize; sent += len(chunk) {
			if _, err := conn.Write(chunk); err != nil {
				burstDone <- err
				return
			}
		}
		wg.Wait()
		burstDone <- readErr
	}()

	// Meanwhile: latency probes on a separate connection.
	probeConn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer probeConn.Close()
	var worst time.Duration
	probe := []byte("ping-probe-payload")
	buf := make([]byte, len(probe))
	probes := 0
	for {
		select {
		case err := <-burstDone:
			if err != nil {
				t.Fatalf("burst failed: %v", err)
			}
			elapsed := time.Since(start)
			mbps := float64(burstSize) / (1 << 20) / elapsed.Seconds()
			t.Logf("burst: 64 MiB in %s (%.0f MiB/s round-trip); %d probes, worst cross-stream RTT %s", elapsed.Round(time.Millisecond), mbps, probes, worst.Round(time.Millisecond))
			if mbps < 20 {
				t.Errorf("throughput too low: %.1f MiB/s", mbps)
			}
			if probes > 0 && worst > 500*time.Millisecond {
				t.Errorf("cross-stream latency degraded during burst: worst %s", worst)
			}
			return
		default:
		}
		probeStart := time.Now()
		probeConn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := probeConn.Write(probe); err != nil {
			t.Fatalf("probe write: %v", err)
		}
		if _, err := io.ReadFull(probeConn, buf); err != nil {
			t.Fatalf("probe read: %v", err)
		}
		if rtt := time.Since(probeStart); rtt > worst {
			worst = rtt
		}
		probes++
		time.Sleep(10 * time.Millisecond)
	}
}

// TestFinalBytesThroughTunnel: the disconnect-message property, end to end —
// a server that writes then closes must deliver every byte to the client.
func TestFinalBytesThroughTunnel(t *testing.T) {
	// A "server" that writes a farewell and immediately closes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	farewell := []byte("Disconnected: whitelist enabled")
	var srvWG sync.WaitGroup
	srvWG.Add(1)
	go func() {
		defer srvWG.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write(farewell)
			c.Close() // immediate close behind the payload
		}
	}()
	// Close the listener before waiting: Accept only unblocks once the
	// listener is closed, so the wait must come after (defers run LIFO).
	defer srvWG.Wait()
	defer ln.Close()

	h := newHarness(t, ln.Addr().String())
	addr := h.waitPublicPort()

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, farewell) {
		t.Fatalf("farewell mangled: got %q want %q", got, farewell)
	}
}
