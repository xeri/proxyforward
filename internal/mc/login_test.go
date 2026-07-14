package mc

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildLoginStart encodes a login-start body for the given protocol using the
// fields a real client of that version would send. withUUID controls whether
// the optional/required UUID is present.
func buildLoginStart(proto int32, name string, uuid [16]byte, withUUID bool) []byte {
	body := AppendString(nil, name)
	switch {
	case proto >= 764:
		body = append(body, uuid[:]...) // required UUID, no prefix
	case proto >= 761:
		if withUUID {
			body = append(body, 1)
			body = append(body, uuid[:]...)
		} else {
			body = append(body, 0)
		}
	case proto == 760:
		body = append(body, 0) // no signature data
		if withUUID {
			body = append(body, 1)
			body = append(body, uuid[:]...)
		} else {
			body = append(body, 0)
		}
	case proto == 759:
		body = append(body, 0) // no signature data
	}
	return body
}

func sampleUUID() [16]byte {
	var u [16]byte
	for i := range u {
		u[i] = byte(i * 7)
	}
	return u
}

func TestParseLoginStartByVersion(t *testing.T) {
	uuid := sampleUUID()
	cases := []struct {
		name     string
		proto    int32
		player   string
		withUUID bool
		wantUUID bool
	}{
		{"1.8", 47, "Notch", false, false},
		{"1.16", 754, "jeb_", false, false},
		{"1.18.2", 758, "Dinnerbone", false, false},
		{"1.19 sig", 759, "Player_1", false, false},
		{"1.19.2 with uuid", 760, "Grumm", true, true},
		{"1.19.2 no uuid", 760, "Grumm", false, false},
		{"1.19.3 with uuid", 761, "Steve", true, true},
		{"1.20.1 no uuid", 763, "Alex", false, false},
		{"1.20.2 required", 764, "Herobrine", true, true},
		{"1.21 required", 767, "xX_Miner_Xx", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := buildLoginStart(tc.proto, tc.player, uuid, tc.withUUID)
			ls, err := ParseLoginStart(tc.proto, body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if ls.Name != tc.player {
				t.Fatalf("name = %q, want %q", ls.Name, tc.player)
			}
			if ls.HasUUID != tc.wantUUID {
				t.Fatalf("HasUUID = %v, want %v", ls.HasUUID, tc.wantUUID)
			}
			if tc.wantUUID && ls.UUID != uuid {
				t.Fatalf("UUID = %x, want %x", ls.UUID, uuid)
			}
		})
	}
}

func TestParseLoginStartWithSignatureData(t *testing.T) {
	// 1.19.1 client sending chat-signing data before the (optional) UUID.
	uuid := sampleUUID()
	body := AppendString(nil, "SignedPlayer")
	body = append(body, 1)                            // has signature data
	body = binary.BigEndian.AppendUint64(body, 1<<40) // expiry
	body = AppendVarInt(body, 4)                      // pubkey len
	body = append(body, 0xDE, 0xAD, 0xBE, 0xEF)       // pubkey
	body = AppendVarInt(body, 3)                      // sig len
	body = append(body, 0x01, 0x02, 0x03)             // signature
	body = append(body, 1)                            // has UUID
	body = append(body, uuid[:]...)

	ls, err := ParseLoginStart(760, body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ls.Name != "SignedPlayer" {
		t.Fatalf("name = %q", ls.Name)
	}
	if !ls.HasUUID || ls.UUID != uuid {
		t.Fatalf("UUID not parsed past signature data: has=%v uuid=%x", ls.HasUUID, ls.UUID)
	}
}

func TestParseLoginStartRejectsGarbageName(t *testing.T) {
	// A string with control characters is not a username.
	body := AppendString(nil, "bad\x00name!!")
	if _, err := ParseLoginStart(767, body); err == nil {
		t.Fatal("accepted an invalid username")
	}
	// Empty body: truncated string.
	if _, err := ParseLoginStart(767, nil); err == nil {
		t.Fatal("accepted an empty login start")
	}
	// Over-long name (17 chars).
	body = AppendString(nil, "seventeencharsxxx")
	if _, err := ParseLoginStart(767, body); err == nil {
		t.Fatal("accepted a 17-char username")
	}
}

func TestParseLoginStartMissingRequiredUUIDKeepsName(t *testing.T) {
	// A ≥764 client that (wrongly) omitted the UUID still yields the name; we
	// prioritize the username over the optional identity hint.
	body := AppendString(nil, "NoUUID")
	ls, err := ParseLoginStart(767, body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ls.Name != "NoUUID" || ls.HasUUID {
		t.Fatalf("got name=%q hasUUID=%v", ls.Name, ls.HasUUID)
	}
}

// loginFrames builds a full client preamble: handshake (login intent) followed
// by the login-start packet, each length-prefixed.
func loginFrames(proto int32, name string, uuid [16]byte, withUUID bool) []byte {
	out := handshakeBytes(proto, "mc.example.com", 25565, NextStateLogin)
	var lp bytes.Buffer
	WritePacket(&lp, LoginStartID, buildLoginStart(proto, name, uuid, withUUID))
	return append(out, lp.Bytes()...)
}

func TestSnifferWholeStream(t *testing.T) {
	uuid := sampleUUID()
	sn := NewSniffer()
	if done := sn.Feed(loginFrames(767, "Player123", uuid, true)); !done {
		t.Fatal("sniffer not done after full login")
	}
	out, ok := sn.Outcome()
	if !ok || out.Login == nil {
		t.Fatalf("no login outcome: ok=%v out=%+v", ok, out)
	}
	if out.Login.Name != "Player123" {
		t.Fatalf("name = %q", out.Login.Name)
	}
	if out.Handshake.NextState != NextStateLogin {
		t.Fatalf("next state = %d", out.Handshake.NextState)
	}
}

func TestSnifferChunkedByteByByte(t *testing.T) {
	uuid := sampleUUID()
	stream := loginFrames(764, "SplitMe", uuid, true)
	sn := NewSniffer()
	var done bool
	for i := 0; i < len(stream) && !done; i++ {
		done = sn.Feed(stream[i : i+1])
	}
	if !done {
		t.Fatal("byte-by-byte feed never completed")
	}
	out, ok := sn.Outcome()
	if !ok || out.Login == nil || out.Login.Name != "SplitMe" {
		t.Fatalf("chunked sniff wrong: ok=%v out=%+v", ok, out)
	}
}

func TestSnifferStatusPingHasNoLogin(t *testing.T) {
	sn := NewSniffer()
	sn.Feed(handshakeBytes(767, "mc.example.com", 25565, NextStateStatus))
	// Status handshake alone is a complete verdict: state resolved, no login.
	out, ok := sn.Outcome()
	if !ok {
		t.Fatal("status handshake should still yield a parsed handshake")
	}
	if out.Login != nil {
		t.Fatal("status ping produced a login outcome")
	}
}

func TestSnifferFailsOpenOnGarbage(t *testing.T) {
	for _, in := range [][]byte{
		{0xFE, 0x01},                         // legacy 1.6 ping
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // invalid VarInt length
		[]byte("GET / HTTP/1.1\r\n\r\n"),     // an HTTP scanner
	} {
		sn := NewSniffer()
		// Feed may or may not report "done" on the first chunk; either is fine.
		// What must never happen is a bogus login outcome.
		sn.Feed(in)
		if out, ok := sn.Outcome(); ok && out.Login != nil {
			t.Fatalf("garbage %x produced a login outcome: %+v", in, out.Login)
		}
	}
}

func TestSnifferCapsBuffering(t *testing.T) {
	// A valid handshake claiming login, then a flood of never-completing bytes:
	// the sniffer must give up at the cap rather than buffer forever.
	sn := NewSniffer()
	sn.Feed(handshakeBytes(767, "mc.example.com", 25565, NextStateLogin))
	flood := make([]byte, maxSniffBytes+1024)
	// A huge VarInt length prefix that never satisfies, followed by filler.
	done := sn.Feed(flood)
	if !done {
		t.Fatal("sniffer did not give up at the byte cap")
	}
}
