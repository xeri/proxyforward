package e2e

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"proxyforward/internal/conntrack"
	"proxyforward/internal/mc"
)

// mcLoginBytes builds a client preamble: handshake (login intent) + login
// start carrying the given name and a UUID.
func mcLoginBytes(proto int32, name string) []byte {
	hb := mc.AppendVarInt(nil, proto)
	hb = mc.AppendString(hb, "mc.example.com")
	hb = binary.BigEndian.AppendUint16(hb, 25565)
	hb = mc.AppendVarInt(hb, mc.NextStateLogin)
	var buf bytes.Buffer
	mc.WritePacket(&buf, 0x00, hb)

	lb := mc.AppendString(nil, name)
	var uuid [16]byte
	for i := range uuid {
		uuid[i] = byte(i + 1)
	}
	lb = append(lb, uuid[:]...) // proto >= 764 carries a required UUID
	mc.WritePacket(&buf, mc.LoginStartID, lb)
	return buf.Bytes()
}

func hasPlayer(snaps []conntrack.Snapshot, name string) bool {
	for _, s := range snaps {
		if s.PlayerName == name {
			return true
		}
	}
	return false
}

// TestMinecraftLoginSniffed drives a real login handshake through the tunnel
// and asserts both the gateway and the agent attribute the connection to the
// player's name. Both seams sniff, so both registries must see it.
func TestMinecraftLoginSniffed(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarnessWith(t, echoAddr, harnessOpts{mcAware: true})
	addr := h.waitPublicPort()

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(mcLoginBytes(767, "Steve")); err != nil {
		t.Fatalf("write login: %v", err)
	}
	// The echo server sends the bytes back; drain a little so the connection
	// stays healthy while the sniffer runs.
	go func() {
		buf := make([]byte, 512)
		conn.Read(buf)
	}()

	deadline := time.Now().Add(10 * time.Second)
	var gwSeen, agentSeen bool
	for time.Now().Before(deadline) {
		gwSeen = gwSeen || hasPlayer(h.gw.Conns.Snapshot(), "Steve")
		agentSeen = agentSeen || hasPlayer(h.agent.Conns.Snapshot(), "Steve")
		if gwSeen && agentSeen {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("player name not attributed: gateway=%v agent=%v", gwSeen, agentSeen)
}

// TestConnKeyPlumbing drives one connection through the tunnel and asserts
// both registries hold it under the gateway-issued correlation key — the
// invariant behind sessions.conn_key and per-player RTT attribution. The
// gateway issues sequential keys from 1, so the harness's first connection
// is "1" on both ends. Run under -race this is also the regression for the
// old post-publish ConnKey write.
func TestConnKeyPlumbing(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addr := h.waitPublicPort()

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("echo read: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h.gw.Conns.EntryByConnKey("1") != nil && h.agent.Conns.EntryByConnKey("1") != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("conn key not correlated: gateway=%v agent=%v",
		h.gw.Conns.EntryByConnKey("1") != nil, h.agent.Conns.EntryByConnKey("1") != nil)
}

// TestNonMinecraftTunnelDoesNotSniff confirms a plain tunnel never attaches a
// player (the sniffer only runs when MinecraftAware is set).
func TestNonMinecraftTunnelDoesNotSniff(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr) // not MinecraftAware
	addr := h.waitPublicPort()

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write(mcLoginBytes(767, "Herobrine"))
	go func() {
		buf := make([]byte, 512)
		conn.Read(buf)
	}()

	// Give the connection time to register, then assert no player was set.
	time.Sleep(300 * time.Millisecond)
	if hasPlayer(h.gw.Conns.Snapshot(), "Herobrine") {
		t.Fatal("non-Minecraft tunnel sniffed a player name")
	}
}
