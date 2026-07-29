package agent

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// localServer stands in for the Minecraft server a tunnel fronts: a loopback
// listener whose accepted connection the test inspects. It reports the
// connection the agent dialled, so a test can assert what happened to it.
func localServer(t *testing.T) (addr string, accepted <-chan net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(ch)
			return
		}
		t.Cleanup(func() { c.Close() })
		ch <- c
	}()
	return ln.Addr().String(), ch
}

// serveOpenConn dials the local server before it can know the leg will be
// served, so every path from that dial to relay.Splice — which is what
// finally owns both legs — has to close it. The PROXY v2 header is the one
// step in between that can fail, and its input is oc.ClientAddr: a wire field
// the gateway supplies. A leak there costs a descriptor AND a Minecraft
// connection slot per player, unbounded, driven by the peer.
func TestServeOpenConnClosesLocalDialWhenProxyHeaderFails(t *testing.T) {
	localAddr, accepted := localServer(t)

	cfg := &config.Config{}
	cfg.Agent.AgentID = "agt_test"
	cfg.Agent.Tunnels = []config.Tunnel{{
		ID:        "tnl_mc",
		Name:      "mc",
		Type:      config.TunnelTCP,
		LocalAddr: localAddr,
		Enabled:   true,
		Options:   config.TunnelOptions{ProxyProtocolV2: true},
	}}
	a := New(cfg, t.TempDir(), slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &session{agent: a, ctx: ctx}

	// A hostname where an IP literal belongs: writeProxyHeader refuses it, and
	// refusing is correct — the header must carry the real client address or
	// nothing. What must not happen is the dial surviving the refusal.
	s.serveOpenConn(nil, &control.OpenConn{
		TunnelID:   "tnl_mc",
		ClientAddr: "gateway.example:25565",
		ConnID:     "1",
	})

	var local net.Conn
	select {
	case local = <-accepted:
		if local == nil {
			t.Fatal("local server never accepted the agent's dial")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local server never accepted the agent's dial")
	}

	// The agent's end is gone iff the server's end reads EOF. A leaked
	// connection instead blocks here until the deadline.
	_ = local.SetReadDeadline(time.Now().Add(5 * time.Second))
	var b [1]byte
	if _, err := local.Read(b[:]); err != io.EOF {
		t.Fatalf("local conn read = %v, want io.EOF — the agent leaked the dial after refusing the header", err)
	}
}

// tcpPair returns both ends of a real TCP connection. net.Pipe would not do: a
// data leg is a relay.Conn, which half-closes.
func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type accept struct {
		c   net.Conn
		err error
	}
	ch := make(chan accept, 1)
	go func() {
		c, err := ln.Accept()
		ch <- accept{c, err}
	}()
	dialed, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatal(got.err)
	}
	t.Cleanup(func() { dialed.Close(); got.c.Close() })
	return dialed.(*net.TCPConn), got.c.(*net.TCPConn)
}

// A panic serving one player is contained to that player — and on the pooled
// path it must also leave the pool's accounting alone. parkAndServe reports its
// outcome exactly once, BEFORE serving, so the maintainer refills while this
// player is still connected; a recover placed in parkAndServe rather than in
// serveOpenConn would report a second time on a panic, and maintainPool would
// credit a slot that is still in use — over-filling the pool, permanently. The
// legs the panic interrupted must still be released: the parked conn and the
// local dial.
func TestParkAndServeReportsOnceThroughAPanic(t *testing.T) {
	localAddr, accepted := localServer(t)

	cfg := &config.Config{}
	cfg.Agent.AgentID = "agt_test"
	cfg.Agent.Tunnels = []config.Tunnel{{
		ID:        "tnl_mc",
		Name:      "mc",
		Type:      config.TunnelTCP,
		LocalAddr: localAddr,
		Enabled:   true,
	}}
	a := New(cfg, t.TempDir(), slog.New(slog.DiscardHandler))
	// The fault injection. serveOpenConn opens a conntrack entry once it has
	// dialled the local server, so a nil registry faults exactly there: inside
	// the frame the guard covers, and after a resource the unwind must release.
	a.Conns = nil

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &session{agent: a, ctx: ctx}

	gwEnd, agentEnd := tcpPair(t)
	outcomes := make(chan parkOutcome, 4)
	served := make(chan struct{})
	go func() {
		defer close(served)
		s.parkAndServe(ctx, agentEnd, func(out parkOutcome) { outcomes <- out })
	}()

	// A player arrives: the gateway writes the header onto the parked conn.
	if err := control.WriteMsg(gwEnd, control.TypeOpenConn, control.OpenConn{
		TunnelID:   "tnl_mc",
		ClientAddr: "203.0.113.9:52000",
		ConnID:     "7",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case out := <-outcomes:
		if !out.taken {
			t.Fatalf("outcome %+v, want taken — the open_conn header was written", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("parkAndServe never reported its slot")
	}
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("parkAndServe never returned — the panic was not recovered")
	}
	select {
	case extra := <-outcomes:
		t.Fatalf("parkAndServe reported twice (%+v); maintainPool would credit a slot still in use", extra)
	default:
	}

	// Both legs end, exactly as they would on a normal disconnect.
	_ = gwEnd.SetReadDeadline(time.Now().Add(5 * time.Second))
	var b [1]byte
	if _, err := gwEnd.Read(b[:]); err != io.EOF {
		t.Fatalf("parked conn read = %v, want io.EOF — the recovered panic left the leg open", err)
	}
	var local net.Conn
	select {
	case local = <-accepted:
		if local == nil {
			t.Fatal("local server never accepted the agent's dial")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local server never accepted the agent's dial")
	}
	_ = local.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := local.Read(b[:]); err != io.EOF {
		t.Fatalf("local conn read = %v, want io.EOF — the recovered panic leaked the local dial", err)
	}
}
