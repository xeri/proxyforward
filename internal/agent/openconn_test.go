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
