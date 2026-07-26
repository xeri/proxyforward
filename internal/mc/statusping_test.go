package mc

import (
	"context"
	"net"
	"testing"
	"time"
)

// servePeer accepts exactly one connection and hands it to serve, so a test can
// stand up a fake backend without leaking a goroutine past the test.
func servePeer(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		serve(c)
	}()
	t.Cleanup(func() {
		ln.Close()
		<-done
	})
	return ln.Addr().String()
}

// TestStatusPingRoundTrip: the client half talks to this package's own server
// half. ServeOffline answers a real status request, so a green round trip proves
// handshake framing, the request, the response read, and the JSON shape all
// agree — with no external server involved.
func TestStatusPingRoundTrip(t *testing.T) {
	addr := servePeer(t, func(c net.Conn) {
		_ = ServeOffline(c, OfflineInfo{MOTD: "§aDown for maintenance", VersionName: "offline"})
	})

	info, err := StatusPing(context.Background(), addr, 5*time.Second)
	if err != nil {
		t.Fatalf("StatusPing: %v", err)
	}
	// §-codes are stripped here, once, so no caller has to know the encoding.
	if info.MOTD != "Down for maintenance" {
		t.Fatalf("MOTD = %q, want the formatting stripped", info.MOTD)
	}
	if info.Version != "offline" || info.Protocol != -1 {
		t.Fatalf("version = %q/%d, want offline/-1", info.Version, info.Protocol)
	}
	if info.PlayersOnline != 0 || info.PlayersMax != 0 {
		t.Fatalf("players = %d/%d, want the responder's 0/0", info.PlayersOnline, info.PlayersMax)
	}
}

// TestStatusPingReadsPlayersAndNestedMOTD: a real server's MOTD is a chat
// component whose text is spread across "extra" parts, and its player counts are
// what the GUI shows. Both must survive the parse.
func TestStatusPingReadsPlayersAndNestedMOTD(t *testing.T) {
	const doc = `{"version":{"name":"1.21.4","protocol":769},` +
		`"players":{"max":20,"online":3},` +
		`"description":{"text":"Hello ","extra":[{"text":"there"},{"text":"!"}]}}`
	addr := servePeer(t, func(c net.Conn) { serveStatusDoc(t, c, doc) })

	info, err := StatusPing(context.Background(), addr, 5*time.Second)
	if err != nil {
		t.Fatalf("StatusPing: %v", err)
	}
	if info.MOTD != "Hello there!" {
		t.Fatalf("MOTD = %q, want the flattened chat component", info.MOTD)
	}
	if info.PlayersOnline != 3 || info.PlayersMax != 20 {
		t.Fatalf("players = %d/%d, want 3/20", info.PlayersOnline, info.PlayersMax)
	}
	if info.Version != "1.21.4" || info.Protocol != 769 {
		t.Fatalf("version = %q/%d", info.Version, info.Protocol)
	}
}

// TestStatusPingPlayersAbsentStaysUnknown: a server that omits the players
// object must read as unknown (-1), never as an empty server — the GUI renders
// the sentinel as "—" and a fake 0 would be a convincing lie.
func TestStatusPingPlayersAbsentStaysUnknown(t *testing.T) {
	addr := servePeer(t, func(c net.Conn) {
		serveStatusDoc(t, c, `{"version":{"name":"1.21.4","protocol":769},"description":"hi"}`)
	})
	info, err := StatusPing(context.Background(), addr, 5*time.Second)
	if err != nil {
		t.Fatalf("StatusPing: %v", err)
	}
	if info.PlayersOnline != -1 || info.PlayersMax != -1 {
		t.Fatalf("players = %d/%d, want the unknown sentinel -1/-1", info.PlayersOnline, info.PlayersMax)
	}
	if info.MOTD != "hi" {
		t.Fatalf("MOTD = %q, want the bare-string form read", info.MOTD)
	}
}

// TestStatusPingRejectsOversizedResponse: the probe dials whatever local address
// the operator typed, so the peer is untrusted. A response claiming a huge length
// must be refused on the declared length, before anything that size is allocated.
func TestStatusPingRejectsOversizedResponse(t *testing.T) {
	addr := servePeer(t, func(c net.Conn) {
		// A frame header claiming 64 MiB, and then nothing. If the cap were
		// checked after allocation this would cost 64 MiB before failing.
		_, _ = c.Write(AppendVarInt(nil, 64<<20))
		_, _ = c.Write([]byte{0x00})
	})
	if _, err := StatusPing(context.Background(), addr, 2*time.Second); err == nil {
		t.Fatal("StatusPing accepted a response far over the size cap")
	}
}

// TestStatusPingNonMinecraftBackendFails: this is the whole point of probing with
// a status ping instead of a bare dial. A socket that accepts and never speaks —
// a hung server, the wrong service on the port — passes a TCP dial and must fail
// here.
func TestStatusPingNonMinecraftBackendFails(t *testing.T) {
	addr := servePeer(t, func(c net.Conn) { time.Sleep(600 * time.Millisecond) })
	start := time.Now()
	if _, err := StatusPing(context.Background(), addr, 300*time.Millisecond); err == nil {
		t.Fatal("StatusPing called a silent socket a healthy Minecraft server")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("StatusPing took %v; the timeout must bound the whole exchange", elapsed)
	}
}

// TestStatusPingGarbageResponseFails: a backend that answers with something that
// is not the status JSON is not a Minecraft server.
func TestStatusPingGarbageResponseFails(t *testing.T) {
	addr := servePeer(t, func(c net.Conn) { serveStatusDoc(t, c, "not json at all") })
	if _, err := StatusPing(context.Background(), addr, 2*time.Second); err == nil {
		t.Fatal("StatusPing accepted a non-JSON status response")
	}
}

// serveStatusDoc reads the client's handshake + request and replies with doc.
func serveStatusDoc(t *testing.T, c net.Conn, doc string) {
	t.Helper()
	for range 2 { // handshake, then status request
		if _, _, err := ReadPacket(c, MaxHandshakePacket); err != nil {
			return
		}
	}
	_ = WritePacket(c, 0x00, AppendString(nil, doc))
}
