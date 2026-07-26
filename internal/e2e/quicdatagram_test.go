package e2e

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/control"
)

// udpPlayer dials the tunnel's public UDP port and returns a send/receive pair
// with a bounded deadline on every exchange.
func udpPlayer(t *testing.T, public string) net.Conn {
	t.Helper()
	c, err := net.Dial("udp", public)
	if err != nil {
		t.Fatalf("dial public udp port: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// echoOnce sends one datagram and returns what came back, insisting the
// boundary survived — the property every hop of a udp tunnel must preserve.
func echoOnce(t *testing.T, c net.Conn, payload []byte) {
	t.Helper()
	if _, err := c.Write(payload); err != nil {
		t.Fatalf("write %d bytes: %v", len(payload), err)
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 65535)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("read echo of %d bytes: %v", len(payload), err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Fatalf("echoed %d bytes, sent %d — a boundary was lost", n, len(payload))
	}
}

// TestQUICDatagramRoundTrip: over QUIC, a udp tunnel's payload rides the
// connection's unreliable datagrams rather than its reliable, ordered stream —
// which is the whole point, since a lost packet on a stream stalls every
// datagram behind it. Proven by the counter, never by timing: on loopback a
// stream and a datagram are indistinguishable on a stopwatch.
//
// A flow's first datagram takes the stream by design. A flow id reaches the
// peer in the open_conn header, and until the peer has read it and registered,
// a datagram naming that flow would be dropped as unknown — so nothing goes
// unreliably until the peer has proved it is listening. What that buys is the
// property this test also asserts: not one datagram is lost on the way. (udp-datagram)
func TestQUICDatagramRoundTrip(t *testing.T) {
	localAddr, closeEcho := udpEchoServer(t)
	defer closeEcho()
	h := newHarnessWith(t, localAddr, harnessOpts{
		tunnelType: config.TunnelUDP,
		transport:  config.TransportQUIC,
	})
	public := h.waitPublicPort()

	if fast, streamed := h.gw.DatagramCounts(); fast != 0 || streamed != 0 {
		t.Fatalf("datagram counters started at (%d fast, %d streamed)", fast, streamed)
	}
	c := udpPlayer(t, public)
	const exchanges = 6
	for i := 0; i < exchanges; i++ {
		echoOnce(t, c, []byte(fmt.Sprintf("datagram %d", i)))
	}

	fast, streamed := h.gw.DatagramCounts()
	if fast != exchanges-1 {
		t.Errorf("gateway sent %d datagrams on the unreliable path, want %d (streamed %d) — the fast path was not taken", fast, exchanges-1, streamed)
	}
	if streamed != 1 {
		t.Errorf("%d datagrams took the stream, want exactly the one that armed the flow", streamed)
	}
}

// TestQUICDatagramOversizeFallsBack: a QUIC datagram cannot be fragmented, so
// one larger than the path allows must ride the flow's stream instead — and
// arrive whole. Silently dropping it, or splitting it, would corrupt a protocol
// that the tunnel is supposed to carry untouched. (udp-datagram)
func TestQUICDatagramOversizeFallsBack(t *testing.T) {
	localAddr, closeEcho := udpEchoServer(t)
	defer closeEcho()
	h := newHarnessWith(t, localAddr, harnessOpts{
		tunnelType: config.TunnelUDP,
		transport:  config.TransportQUIC,
	})
	public := h.waitPublicPort()
	c := udpPlayer(t, public)

	// 1400 bytes: past what the unreliable path is offered, well inside what a
	// loopback UDP socket carries.
	big := bytes.Repeat([]byte{0x7E}, 1400)
	echoOnce(t, c, big)
	// And a small one still takes the fast path afterwards — the fallback is per
	// datagram, not a latch on the flow.
	echoOnce(t, c, []byte("small again"))

	fast, streamed := h.gw.DatagramCounts()
	if streamed < 1 {
		t.Errorf("no datagram fell back to the stream; a 1400-byte payload cannot have gone unreliably (fast %d)", fast)
	}
	if fast < 1 {
		t.Errorf("the small datagram did not take the fast path after an oversize one (fast %d, streamed %d)", fast, streamed)
	}
}

// TestQUICDatagramLegacyPeerRidesStream: a peer that never learned the
// capability keeps the framed-stream leg it has always used, and the gateway
// must not send it a single datagram — they would be dropped by a peer with no
// flow table, silently losing a player's traffic. (udp-datagram)
func TestQUICDatagramLegacyPeerRidesStream(t *testing.T) {
	localAddr, closeEcho := udpEchoServer(t)
	defer closeEcho()

	// Everything this build offers except the datagram capability.
	var legacy []string
	for _, c := range control.SupportedCapabilities {
		if c != control.CapUDPDatagram {
			legacy = append(legacy, c)
		}
	}
	h := newHarnessWith(t, localAddr, harnessOpts{
		tunnelType: config.TunnelUDP,
		transport:  config.TransportQUIC,
		offerCaps:  legacy,
	})
	public := h.waitPublicPort()
	c := udpPlayer(t, public)

	for i := 0; i < 3; i++ {
		echoOnce(t, c, []byte(fmt.Sprintf("legacy %d", i)))
	}
	if fast, streamed := h.gw.DatagramCounts(); fast != 0 || streamed != 0 {
		t.Errorf("gateway used a flow leg for a peer that never offered the capability: %d fast, %d streamed", fast, streamed)
	}
}

// TestQUICDatagramFlowIsolation: a connection's datagrams are shared by every
// player on it, so the flow id is the only thing keeping them apart. Two players
// on one udp port must each get their own traffic and only their own — crossing
// them would deliver one player's packets to another, which is the failure a
// shared, per-connection datagram space makes easy. (udp-datagram)
func TestQUICDatagramFlowIsolation(t *testing.T) {
	// A server that answers with the source port it heard from, so each player
	// can prove it is talking on its own flow.
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
				_, _ = pc.WriteToUDP([]byte(fmt.Sprintf("%s|%s", buf[:n], from)), from)
			}
			if err != nil {
				return
			}
		}
	}()

	h := newHarnessWith(t, pc.LocalAddr().String(), harnessOpts{
		tunnelType: config.TunnelUDP,
		transport:  config.TransportQUIC,
	})
	public := h.waitPublicPort()

	tags := []string{"alpha", "bravo"}
	conns := make([]net.Conn, len(tags))
	for i := range tags {
		conns[i] = udpPlayer(t, public)
	}

	// Interleave the two players so a mux that ignored the flow id would deliver
	// one's reply to the other.
	seen := map[string]string{}
	buf := make([]byte, 2048)
	for round := 0; round < 3; round++ {
		for i, tag := range tags {
			if _, err := conns[i].Write([]byte(tag)); err != nil {
				t.Fatal(err)
			}
		}
		for i, tag := range tags {
			_ = conns[i].SetReadDeadline(time.Now().Add(10 * time.Second))
			n, err := conns[i].Read(buf)
			if err != nil {
				t.Fatalf("%s round %d: %v", tag, round, err)
			}
			gotTag, srcPort, ok := bytes.Cut(buf[:n], []byte("|"))
			if !ok {
				t.Fatalf("%s: malformed reply %q", tag, buf[:n])
			}
			if string(gotTag) != tag {
				t.Fatalf("%s received %q — a player's datagrams crossed into another's flow", tag, gotTag)
			}
			seen[tag] = string(srcPort)
		}
	}
	if seen["alpha"] == seen["bravo"] {
		t.Fatalf("both players reached the local server from one socket (%s); flows are not separated", seen["alpha"])
	}
	// Six exchanges over two flows, each of which spends its first on the stream
	// while the peer proves it has registered.
	if fast, streamed := h.gw.DatagramCounts(); fast != 4 || streamed != 2 {
		t.Errorf("across 6 exchanges on 2 flows: %d fast, %d streamed; want 4 and 2", fast, streamed)
	}
}
