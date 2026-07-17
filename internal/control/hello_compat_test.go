package control_test

import (
	"encoding/json"
	"strings"
	"testing"

	"proxyforward/internal/control"
)

// Optional hello fields ride omitempty, and frames from older builds must decode
// without error — including a pre-removal frame still carrying agentId/token,
// whose unknown keys are simply ignored (auth then fails honestly downstream).
func TestHelloWireBackCompat(t *testing.T) {
	// Empty optional fields must not appear on the wire.
	h := control.Hello{ProtocolVersion: 1, Kind: control.KindControl, AppVersion: "v"}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hostname", "localIps"} {
		if strings.Contains(string(b), f) {
			t.Errorf("empty Hello.%s leaked onto the wire: %s", f, b)
		}
	}

	// A pre-removal frame — self-asserted agentId and a bearer token — decodes
	// cleanly; the removed keys are ignored rather than an error, so an old build
	// reaches the auth path and gets its honest bad_token there.
	var got control.Hello
	if err := json.Unmarshal([]byte(`{"protocolVersion":1,"kind":"control","agentId":"a","token":"t"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Hostname != "" || got.LocalIPs != nil {
		t.Errorf("pre-removal frame produced non-zero optional fields: %+v", got)
	}

	// New fields round-trip.
	h2 := control.Hello{Hostname: "DESKTOP", LocalIPs: []string{"192.168.1.5"}}
	b2, _ := json.Marshal(h2)
	var g2 control.Hello
	if err := json.Unmarshal(b2, &g2); err != nil {
		t.Fatal(err)
	}
	if g2.Hostname != "DESKTOP" || len(g2.LocalIPs) != 1 || g2.LocalIPs[0] != "192.168.1.5" {
		t.Errorf("Hello identity round-trip failed: %+v", g2)
	}
}

func TestHelloOKAndPongOmitEmpty(t *testing.T) {
	ok := control.HelloOK{ProtocolVersion: 1, SessionGeneration: 3}
	b, _ := json.Marshal(ok)
	for _, f := range []string{"hostname", "localIps", "observedIp", "assignedAgentId", "gatewayId", "configSeedNeeded"} {
		if strings.Contains(string(b), f) {
			t.Errorf("empty HelloOK.%s leaked onto the wire: %s", f, b)
		}
	}
	// SessionGeneration keeps the legacy json tag so frames stay byte-identical to
	// a peer that predates the rename.
	if !strings.Contains(string(b), `"generation":3`) || strings.Contains(string(b), "sessionGeneration") {
		t.Errorf("SessionGeneration must marshal as \"generation\": %s", b)
	}
	// The enrollment identity fields round-trip when set.
	ok2 := control.HelloOK{AssignedAgentID: "agt_k7q2f9m3", GatewayID: "gw_3h8xd0p5"}
	b2, _ := json.Marshal(ok2)
	var g2 control.HelloOK
	if err := json.Unmarshal(b2, &g2); err != nil {
		t.Fatal(err)
	}
	if g2.AssignedAgentID != "agt_k7q2f9m3" || g2.GatewayID != "gw_3h8xd0p5" {
		t.Errorf("HelloOK identity round-trip failed: %+v", g2)
	}

	// A legacy pong (no gateway receive time) must omit recvUnixNano so the
	// agent can detect the absence and skip one-way math.
	p := control.Pong{Seq: 7, SentUnixNano: 123}
	bp, _ := json.Marshal(p)
	if strings.Contains(string(bp), "recvUnixNano") {
		t.Errorf("empty Pong.RecvUnixNano leaked onto the wire: %s", bp)
	}
	p2 := control.Pong{Seq: 7, SentUnixNano: 123, RecvUnixNano: 456}
	bp2, _ := json.Marshal(p2)
	if !strings.Contains(string(bp2), "recvUnixNano") {
		t.Errorf("set Pong.RecvUnixNano missing from the wire: %s", bp2)
	}
}

// The enrollment + gateway-config hello fields ride omitempty: a minimal hello
// (a data-kind dial-back, or any hello with nothing to report) must encode
// without them, an old frame decodes them as zero, and a populated hello must
// round-trip. Byte-minimal frames are the invariant.
func TestHelloEnrollmentFieldsBackCompat(t *testing.T) {
	minimal := control.Hello{ProtocolVersion: 1, Kind: control.KindControl, AppVersion: "v"}
	b, err := json.Marshal(minimal)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"agentPubKey", "agentSig", "enrollTicket", "configHash", "configGeneration"} {
		if strings.Contains(string(b), f) {
			t.Errorf("empty Hello.%s leaked onto the wire: %s", f, b)
		}
	}

	// An old build's frame decodes with every unknown/absent field zero.
	var got control.Hello
	if err := json.Unmarshal([]byte(`{"protocolVersion":1,"kind":"control","agentId":"a","token":"t"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.AgentPubKey != nil || got.AgentSig != nil || got.EnrollTicket != "" || got.ConfigHash != "" || got.ConfigGeneration != 0 {
		t.Errorf("old frame produced non-zero enrollment fields: %+v", got)
	}

	// A populated enrollment hello round-trips.
	full := control.Hello{
		AgentPubKey: []byte{1, 2, 3}, AgentSig: []byte{4, 5, 6}, EnrollTicket: "tkt",
		ConfigHash: "h", ConfigGeneration: 9,
	}
	fb, _ := json.Marshal(full)
	var fg control.Hello
	if err := json.Unmarshal(fb, &fg); err != nil {
		t.Fatal(err)
	}
	if len(fg.AgentPubKey) != 3 || len(fg.AgentSig) != 3 || fg.EnrollTicket != "tkt" || fg.ConfigHash != "h" || fg.ConfigGeneration != 9 {
		t.Errorf("enrollment field round-trip failed: %+v", fg)
	}
}

// The bandwidth-cap fields are additive: an uncapped spec must encode exactly
// like a legacy one, a legacy frame must decode with both fields zero, and a
// set cap must round-trip.
func TestTunnelSpecWireBackCompat(t *testing.T) {
	// An uncapped spec must not put either bandwidth key on the wire.
	uncapped := control.TunnelSpec{ID: "t1", Name: "web", Type: "tcp", PublicPort: 25565}
	b, err := json.Marshal(uncapped)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"bandwidthLimitMbps", "bandwidthLimitScope"} {
		if strings.Contains(string(b), f) {
			t.Errorf("uncapped TunnelSpec.%s leaked onto the wire: %s", f, b)
		}
	}

	// A legacy frame (no bandwidth fields) decodes with both zero.
	var got control.TunnelSpec
	if err := json.Unmarshal([]byte(`{"id":"t1","name":"web","type":"tcp","publicPort":25565}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.BandwidthLimitMbps != 0 || got.BandwidthLimitScope != "" {
		t.Errorf("legacy frame produced non-zero bandwidth fields: %+v", got)
	}

	// A set cap round-trips and appears on the wire.
	capped := control.TunnelSpec{ID: "t1", Name: "web", Type: "tcp", BandwidthLimitMbps: 5, BandwidthLimitScope: "per-direction"}
	b2, _ := json.Marshal(capped)
	for _, f := range []string{"bandwidthLimitMbps", "bandwidthLimitScope"} {
		if !strings.Contains(string(b2), f) {
			t.Errorf("set TunnelSpec.%s missing from the wire: %s", f, b2)
		}
	}
	var g2 control.TunnelSpec
	if err := json.Unmarshal(b2, &g2); err != nil {
		t.Fatal(err)
	}
	if g2.BandwidthLimitMbps != 5 || g2.BandwidthLimitScope != "per-direction" {
		t.Errorf("TunnelSpec bandwidth round-trip failed: %+v", g2)
	}
}
