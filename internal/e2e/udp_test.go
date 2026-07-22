package e2e

import (
	"bytes"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// udpEchoServer echoes every datagram back to its sender, preserving
// boundaries. It is the UDP twin of echoServer.
func udpEchoServer(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFromUDP(buf)
			if n > 0 {
				// Copy before writing: the next read reuses buf.
				out := make([]byte, n)
				copy(out, buf[:n])
				pc.WriteToUDP(out, from)
			}
			if err != nil {
				return
			}
		}
	}()
	return pc.LocalAddr().String(), func() {
		pc.Close()
		wg.Wait()
	}
}

// TestUDPTunnelPreservesDatagrams is the end-to-end proof that a udp tunnel
// works and that message boundaries survive the whole path (U8): player socket →
// gateway UDP listener → framed tunnel stream → agent's connected UDP socket →
// local server, and all the way back. Boundaries are the thing that breaks if
// any hop treats the flow as a byte stream, and nothing about the traffic would
// look wrong until a game parsed it.
func TestUDPTunnelPreservesDatagrams(t *testing.T) {
	localAddr, closeEcho := udpEchoServer(t)
	defer closeEcho()

	h := newHarnessWith(t, localAddr, harnessOpts{tunnelType: config.TunnelUDP})
	public := h.waitPublicPort()

	conn, err := net.Dial("udp", public)
	if err != nil {
		t.Fatalf("dial public udp port: %v", err)
	}
	defer conn.Close()

	// Distinct sizes, sent back to back: if any hop coalesced them, the reply
	// sizes would not line up one for one.
	payloads := [][]byte{
		[]byte("hello"),
		bytes.Repeat([]byte{0x5A}, 1200),
		[]byte("x"),
		bytes.Repeat([]byte{0xC3}, 700),
	}
	buf := make([]byte, 65535)
	for i, want := range payloads {
		if _, err := conn.Write(want); err != nil {
			t.Fatalf("datagram %d: write: %v", i, err)
		}
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("datagram %d (%d bytes): read: %v", i, len(want), err)
		}
		if !bytes.Equal(buf[:n], want) {
			t.Fatalf("datagram %d: echoed %d bytes, sent %d — a boundary was lost", i, n, len(want))
		}
	}
}

// TestUDPTunnelSeparatesClients (U9): two player sockets on one public UDP port
// are two sessions, each with its own tunnel stream and its own socket to the
// local server. Crossing them would deliver one player's traffic to another —
// the failure a shared-socket design makes easy and this one must not have.
func TestUDPTunnelSeparatesClients(t *testing.T) {
	// A server that answers with the port it heard from, so each client can
	// prove it is talking to its own flow.
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFromUDP(buf)
			if n > 0 {
				pc.WriteToUDP([]byte(fmt.Sprintf("%s|%s", buf[:n], from.String())), from)
			}
			if err != nil {
				return
			}
		}
	}()

	h := newHarnessWith(t, pc.LocalAddr().String(), harnessOpts{tunnelType: config.TunnelUDP})
	public := h.waitPublicPort()

	type client struct {
		conn net.Conn
		tag  string
	}
	clients := []client{{tag: "alpha"}, {tag: "bravo"}}
	for i := range clients {
		c, err := net.Dial("udp", public)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		clients[i].conn = c
	}

	seen := map[string]string{}
	buf := make([]byte, 2048)
	for _, c := range clients {
		if _, err := c.conn.Write([]byte(c.tag)); err != nil {
			t.Fatal(err)
		}
		c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, err := c.conn.Read(buf)
		if err != nil {
			t.Fatalf("%s: %v", c.tag, err)
		}
		reply := string(buf[:n])
		tag, srcPort, ok := bytes.Cut([]byte(reply), []byte("|"))
		if !ok {
			t.Fatalf("%s: malformed reply %q", c.tag, reply)
		}
		if string(tag) != c.tag {
			t.Fatalf("%s received %q — a client's traffic crossed into another's flow", c.tag, tag)
		}
		seen[c.tag] = string(srcPort)
	}
	if seen["alpha"] == seen["bravo"] {
		t.Fatalf("both players reached the local server from the same socket (%s); sessions are not separated", seen["alpha"])
	}
}

// TestUDPTunnelRefusedForLegacyAgent is the capability's whole point (U10). An
// agent that cannot serve datagrams must not get a UDP port bound on its behalf:
// the gateway would accept players and hand the agent a framed stream it would
// splice as raw bytes, corrupting every packet silently. The tunnel must simply
// never come up.
func TestUDPTunnelRefusedForLegacyAgent(t *testing.T) {
	localAddr, closeEcho := udpEchoServer(t)
	defer closeEcho()

	// Offer every capability except tunnel-udp — a build from before UDP existed.
	var legacy []string
	for _, c := range control.SupportedCapabilities {
		if c != control.CapTunnelUDP {
			legacy = append(legacy, c)
		}
	}
	h := newHarnessWith(t, localAddr, harnessOpts{tunnelType: config.TunnelUDP, offerCaps: legacy})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if port, ok := h.agent.TunnelPublicPort(h.tunnelID); ok {
			t.Fatalf("gateway bound udp port %d for an agent that cannot serve datagrams", port)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
