package control

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := Hello{ProtocolVersion: 1, Kind: KindControl, AgentID: "a1", Token: "t", AppVersion: "0.1", Capabilities: []string{CapTunnelSync}}
	if err := WriteMsg(&buf, TypeHello, in); err != nil {
		t.Fatal(err)
	}
	env, err := ReadMsg(&buf, MaxFrame)
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != TypeHello {
		t.Fatalf("type: got %q", env.Type)
	}
	got, err := Decode[Hello](env)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, in) {
		t.Fatalf("round trip mismatch: %+v != %+v", *got, in)
	}
}

func TestSyncRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	sync := SyncTunnels{Seq: 7, Tunnels: []TunnelSpec{
		{ID: "t1", Name: "mc", Type: "tcp", PublicPort: 25565},
		{ID: "t2", Name: "eph", Type: "tcp", PublicPort: 0, OfflineMOTD: "brb"},
	}}
	if err := WriteMsg(&buf, TypeSyncTunnels, sync); err != nil {
		t.Fatal(err)
	}
	res := SyncResult{Seq: 7, Results: []SyncTunnelResult{
		{TunnelID: "t1", OK: true, PublicPort: 25565},
		{TunnelID: "t2", OK: false, Code: ErrCodePortInUse, Message: "busy"},
	}}
	if err := WriteMsg(&buf, TypeSyncResult, res); err != nil {
		t.Fatal(err)
	}
	env, err := ReadMsg(&buf, MaxFrame)
	if err != nil {
		t.Fatal(err)
	}
	gotSync, err := Decode[SyncTunnels](env)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*gotSync, sync) {
		t.Fatalf("sync mismatch: %+v != %+v", *gotSync, sync)
	}
	env, err = ReadMsg(&buf, MaxFrame)
	if err != nil {
		t.Fatal(err)
	}
	gotRes, err := Decode[SyncResult](env)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*gotRes, res) {
		t.Fatalf("result mismatch: %+v != %+v", *gotRes, res)
	}
}

// A v1-era hello (no capabilities key) must decode to an empty capability
// set, and marshaling nil capabilities must not emit the key — legacy frames
// stay byte-identical.
func TestLegacyHelloCompat(t *testing.T) {
	legacy := []byte(`{"protocolVersion":1,"kind":"control","agentId":"a1","token":"t","appVersion":"0.1"}`)
	var h Hello
	if err := json.Unmarshal(legacy, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Capabilities) != 0 {
		t.Fatalf("legacy hello grew capabilities: %v", h.Capabilities)
	}
	if NewCapSet(h.Capabilities).Has(CapTunnelSync) {
		t.Fatal("empty cap set claims tunnel-sync")
	}
	out, err := json.Marshal(Hello{ProtocolVersion: 1, Kind: KindControl, AgentID: "a1", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("capabilities")) {
		t.Fatalf("nil capabilities leaked into JSON: %s", out)
	}
	okOut, err := json.Marshal(HelloOK{ProtocolVersion: 1, SessionGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(okOut, []byte("capabilities")) {
		t.Fatalf("nil capabilities leaked into HelloOK JSON: %s", okOut)
	}
}

func TestIntersectCaps(t *testing.T) {
	cases := []struct {
		name               string
		offered, supported []string
		want               []string
	}{
		{"both nil", nil, nil, nil},
		{"offered nil", nil, []string{"a"}, nil},
		{"supported nil", []string{"a"}, nil, nil},
		{"exact", []string{"a"}, []string{"a"}, []string{"a"}},
		{"unknown offered dropped", []string{"a", "future-cap"}, []string{"a"}, []string{"a"}},
		{"supported order preserved", []string{"c", "a", "b"}, []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"disjoint", []string{"x"}, []string{"y"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IntersectCaps(tc.offered, tc.supported)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestCapSet(t *testing.T) {
	s := NewCapSet([]string{CapTunnelSync})
	if !s.Has(CapTunnelSync) {
		t.Fatal("missing tunnel-sync")
	}
	if s.Has("nope") {
		t.Fatal("phantom capability")
	}
	if NewCapSet(nil).Has(CapTunnelSync) {
		t.Fatal("nil set claims capability")
	}
}

func TestSupportedCapabilities(t *testing.T) {
	s := NewCapSet(SupportedCapabilities)
	if !s.Has(CapTunnelSync) || !s.Has(CapConnStats) || !s.Has(CapPerConn) || !s.Has(CapGatewayConfig) {
		t.Fatalf("supported set missing a built-in capability: %v", SupportedCapabilities)
	}
	// tunnel-udp is deliberately NOT advertised: it isn't implemented
	// end-to-end (the gateway rejects udp specs), so offering it would be a
	// protocol lie.
	if s.Has("tunnel-udp") {
		t.Fatal("tunnel-udp must not be advertised until it is implemented")
	}
	// A sync-only peer (older build) must negotiate away conn-stats — an old
	// agent that never offers conn-stats gets no RTT frames.
	got := IntersectCaps(SupportedCapabilities, []string{CapTunnelSync})
	if !reflect.DeepEqual(got, []string{CapTunnelSync}) {
		t.Fatalf("against a sync-only peer: got %v want [tunnel-sync]", got)
	}
}

func TestHashTunnels(t *testing.T) {
	a := []TunnelSpec{
		{ID: "t1", Name: "mc", Type: "tcp", PublicPort: 25565},
		{ID: "t2", Name: "eph", Type: "tcp", OfflineMOTD: "brb"},
	}
	// Order-independent: the same set in a different order hashes identically.
	reversed := []TunnelSpec{a[1], a[0]}
	if HashTunnels(a) != HashTunnels(reversed) {
		t.Fatalf("hash depends on order: %s != %s", HashTunnels(a), HashTunnels(reversed))
	}
	// A non-empty set has a non-empty hash.
	if HashTunnels(a) == "" {
		t.Fatal("hash of a non-empty set must not be empty")
	}
	// Any wire-field change flips the hash.
	changed := append([]TunnelSpec(nil), a...)
	changed[0].PublicPort = 25566
	if HashTunnels(a) == HashTunnels(changed) {
		t.Fatal("hash ignored a PublicPort change")
	}
	// The empty set is stable across nil and empty, and never collides with a
	// populated set.
	if HashTunnels(nil) != HashTunnels([]TunnelSpec{}) {
		t.Fatalf("empty-set hash unstable: %s != %s", HashTunnels(nil), HashTunnels([]TunnelSpec{}))
	}
	if HashTunnels(nil) == HashTunnels(a) {
		t.Fatal("empty and populated sets collide")
	}
	// Deterministic across calls (no map iteration order leaking in).
	if h1, h2 := HashTunnels(a), HashTunnels(a); h1 != h2 {
		t.Fatal("hash is not deterministic")
	}
}

func TestOpenDataRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := OpenData{ConnID: "12345"}
	if err := WriteMsg(&buf, TypeOpenData, in); err != nil {
		t.Fatalf("write: %v", err)
	}
	env, err := ReadMsg(&buf, MaxFrame)
	if err != nil || env.Type != TypeOpenData {
		t.Fatalf("read: %v type=%q", err, env.Type)
	}
	got, err := Decode[OpenData](env)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if *got != in {
		t.Fatalf("round trip: got %+v want %+v", *got, in)
	}
}

func TestConnStatsRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := ConnStats{Entries: []ConnStat{{ConnID: "42", RttMs: 23.5}, {ConnID: "7", RttMs: 101}}}
	if err := WriteMsg(&buf, TypeConnStats, in); err != nil {
		t.Fatalf("write: %v", err)
	}
	env, err := ReadMsg(&buf, MaxFrame)
	if err != nil || env.Type != TypeConnStats {
		t.Fatalf("read: %v type=%q", err, env.Type)
	}
	got, err := Decode[ConnStats](env)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got.Entries, in.Entries) {
		t.Fatalf("round trip mismatch: %+v != %+v", got.Entries, in.Entries)
	}
}

func TestMultipleFramesSequential(t *testing.T) {
	var buf bytes.Buffer
	for seq := uint64(1); seq <= 3; seq++ {
		if err := WriteMsg(&buf, TypePing, Ping{Seq: seq}); err != nil {
			t.Fatal(err)
		}
	}
	for seq := uint64(1); seq <= 3; seq++ {
		env, err := ReadMsg(&buf, MaxFrame)
		if err != nil {
			t.Fatal(err)
		}
		p, err := Decode[Ping](env)
		if err != nil {
			t.Fatal(err)
		}
		if p.Seq != seq {
			t.Fatalf("seq: got %d want %d", p.Seq, seq)
		}
	}
}

func TestReadRejectsOversizeBeforeAllocating(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 1<<30) // 1 GiB claim
	// Reader will EOF after the header — if the size cap works we never try
	// to read (or allocate) the body.
	_, err := ReadMsg(bytes.NewReader(hdr[:]), PreAuthMaxFrame)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("want ErrFrameTooLarge, got %v", err)
	}
}

func TestReadRejectsZeroLength(t *testing.T) {
	_, err := ReadMsg(bytes.NewReader(make([]byte, 4)), MaxFrame)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("want ErrFrameTooLarge for zero frame, got %v", err)
	}
}

func TestReadRejectsJunkJSON(t *testing.T) {
	var buf bytes.Buffer
	body := []byte("{not json")
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	buf.Write(hdr[:])
	buf.Write(body)
	if _, err := ReadMsg(&buf, MaxFrame); err == nil {
		t.Fatal("expected error for junk JSON")
	}
}

func TestReadRejectsMissingType(t *testing.T) {
	var buf bytes.Buffer
	body := []byte(`{"data":{}}`)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	buf.Write(hdr[:])
	buf.Write(body)
	if _, err := ReadMsg(&buf, MaxFrame); err == nil || !strings.Contains(err.Error(), "missing type") {
		t.Fatalf("expected missing-type error, got %v", err)
	}
}

func TestTruncatedFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMsg(&buf, TypePing, Ping{Seq: 9}); err != nil {
		t.Fatal(err)
	}
	trunc := buf.Bytes()[:buf.Len()-3]
	_, err := ReadMsg(bytes.NewReader(trunc), MaxFrame)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want ErrUnexpectedEOF, got %v", err)
	}
}

func FuzzReadMsg(f *testing.F) {
	var seed bytes.Buffer
	WriteMsg(&seed, TypeHello, Hello{ProtocolVersion: 1, AgentID: "x", Token: "y"})
	f.Add(seed.Bytes())
	f.Add([]byte{0, 0, 0, 1, '{'})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		// Must never panic or allocate beyond the cap, whatever the input.
		env, err := ReadMsg(bytes.NewReader(data), PreAuthMaxFrame)
		if err != nil {
			return
		}
		// Decoding into any known type must not panic either.
		_, _ = Decode[Hello](env)
		_, _ = Decode[OpenConn](env)
	})
}
