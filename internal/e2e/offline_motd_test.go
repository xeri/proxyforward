package e2e

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"proxyforward/internal/mc"
)

// TestOfflineMOTDStatusServed: when the agent reports the local backend down, a
// player who queries the tunnel's public port gets the tunnel's offline MOTD in
// the server-list status response instead of a dead socket.
func TestOfflineMOTDStatusServed(t *testing.T) {
	const motd = "Server is offline — back soon"
	h := newHarnessWith(t, reservedDeadAddr(t), harnessOpts{offlineMOTD: motd})
	addr := h.waitPublicPort()
	waitBackendDown(t, h)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial public port: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(mcStatusRequest(767)); err != nil {
		t.Fatalf("write status request: %v", err)
	}
	id, body, err := mc.ReadPacket(conn, 32*1024)
	if err != nil {
		t.Fatalf("read status response: %v", err)
	}
	if id != 0x00 {
		t.Fatalf("status response id = %#x, want 0x00", id)
	}
	if !bytes.Contains(body, []byte(motd)) {
		t.Fatalf("status response does not carry the offline MOTD %q; body=%q", motd, body)
	}
}

// TestOfflineMOTDLoginDisconnect: a player who tries to JOIN a tunnel whose
// backend is down is disconnected with the offline MOTD as the reason.
func TestOfflineMOTDLoginDisconnect(t *testing.T) {
	const motd = "Be right back"
	h := newHarnessWith(t, reservedDeadAddr(t), harnessOpts{offlineMOTD: motd})
	addr := h.waitPublicPort()
	waitBackendDown(t, h)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial public port: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(mcLoginBytes(767, "Steve")); err != nil {
		t.Fatalf("write login: %v", err)
	}
	id, body, err := mc.ReadPacket(conn, 32*1024)
	if err != nil {
		t.Fatalf("read disconnect: %v", err)
	}
	if id != 0x00 {
		t.Fatalf("disconnect id = %#x, want 0x00", id)
	}
	if !bytes.Contains(body, []byte(motd)) {
		t.Fatalf("disconnect reason does not carry the offline MOTD %q; body=%q", motd, body)
	}
}

// TestOfflineMOTDDisabledCleanClose: with no OfflineMOTD configured, a
// backend-down tunnel drops the connection (no MOTD emitted) — the pre-existing
// behavior the empty-string guard preserves.
func TestOfflineMOTDDisabledCleanClose(t *testing.T) {
	h := newHarnessWith(t, reservedDeadAddr(t), harnessOpts{}) // no offlineMOTD
	addr := h.waitPublicPort()
	waitBackendDown(t, h)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial public port: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(mcStatusRequest(767)); err != nil {
		// A write may already fail if the gateway closed first; that is fine.
		return
	}
	// The gateway serves nothing and closes; the read must not yield a packet.
	if _, _, err := mc.ReadPacket(conn, 32*1024); err == nil {
		t.Fatal("expected a clean close with no MOTD, but got a response packet")
	}
}

// mcStatusRequest builds a status-intent handshake followed by the empty status
// request packet.
func mcStatusRequest(proto int32) []byte {
	hb := mc.AppendVarInt(nil, proto)
	hb = mc.AppendString(hb, "mc.example.com")
	hb = binary.BigEndian.AppendUint16(hb, 25565)
	hb = mc.AppendVarInt(hb, mc.NextStateStatus)
	var buf bytes.Buffer
	mc.WritePacket(&buf, 0x00, hb)
	mc.WritePacket(&buf, 0x00, nil) // status request
	return buf.Bytes()
}

// reservedDeadAddr returns a 127.0.0.1 address that refuses connections: it
// binds a port, learns its number, then frees it so a dial gets refused and the
// agent's health probe reports the backend down.
func reservedDeadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// waitBackendDown blocks until the gateway has learned (over the control link)
// that the tunnel's local backend is unreachable — the offline responder's
// trigger condition.
func waitBackendDown(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if up, known := h.gw.TunnelLocalUp(h.tunnelID); known && !up {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("gateway never learned the backend was down")
}
