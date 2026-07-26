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
	for _, f := range []string{"hostname", "localIps", "headless", "pooled", "poolSize"} {
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
	h2 := control.Hello{Hostname: "DESKTOP", LocalIPs: []string{"192.168.1.5"}, Headless: true}
	b2, _ := json.Marshal(h2)
	var g2 control.Hello
	if err := json.Unmarshal(b2, &g2); err != nil {
		t.Fatal(err)
	}
	if g2.Hostname != "DESKTOP" || len(g2.LocalIPs) != 1 || g2.LocalIPs[0] != "192.168.1.5" || !g2.Headless {
		t.Errorf("Hello identity round-trip failed: %+v", g2)
	}
}

// The connection-pool fields must be invisible to a peer that does not know
// them: absent from an unpooled frame on both sides, and a grant of 0 (what a
// legacy gateway effectively says) must decode as "no pool" rather than as a
// pool of unspecified size.
func TestPoolFieldsAreInvisibleWithoutThem(t *testing.T) {
	dataHello := control.Hello{ProtocolVersion: 1, Kind: control.KindData, ConnID: "7"}
	b, err := json.Marshal(dataHello)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "pooled") || strings.Contains(string(b), "poolSize") {
		t.Errorf("an ordinary dial-back hello carries pool fields: %s", b)
	}
	ok, err := json.Marshal(control.HelloOK{ProtocolVersion: 1, SessionGeneration: 3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ok), "poolSize") {
		t.Errorf("a hello_ok without a grant carries poolSize: %s", ok)
	}

	// A legacy gateway's hello_ok: no grant key at all reads as no pool.
	var legacy control.HelloOK
	if err := json.Unmarshal([]byte(`{"protocolVersion":1,"generation":1}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.PoolSize != 0 {
		t.Errorf("a legacy hello_ok granted a pool of %d", legacy.PoolSize)
	}

	// And the populated forms round-trip.
	pooled := control.Hello{Kind: control.KindData, Pooled: true}
	var gotPooled control.Hello
	pb, _ := json.Marshal(pooled)
	if err := json.Unmarshal(pb, &gotPooled); err != nil {
		t.Fatal(err)
	}
	if !gotPooled.Pooled || gotPooled.ConnID != "" {
		t.Errorf("pooled data hello round-trip: %+v", gotPooled)
	}
	req := control.Hello{Kind: control.KindControl, PoolSize: 4}
	var gotReq control.Hello
	rb, _ := json.Marshal(req)
	if err := json.Unmarshal(rb, &gotReq); err != nil {
		t.Fatal(err)
	}
	if gotReq.PoolSize != 4 {
		t.Errorf("pool request round-trip: %+v", gotReq)
	}
}

// OpenConn is the first frame on every data leg, so its back-compat matters as
// much as the hello's: a flow that rides its stream — every tcp tunnel, every
// udp tunnel on a transport that cannot carry datagrams, and every flow to a
// peer that never learned the capability — must send the frame a legacy peer
// sends, down to the byte. A flowId of 0 is that same "stream only" statement
// read from the other side.
func TestOpenConnFlowIDIsInvisibleWithoutIt(t *testing.T) {
	stream := control.OpenConn{TunnelID: "tnl_a", ClientAddr: "203.0.113.7:51234", ConnID: "9"}
	b, err := json.Marshal(stream)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "flowId") {
		t.Errorf("a stream-only open_conn carries a flow id: %s", b)
	}

	// A legacy frame decodes as flow 0, never as an unspecified flow.
	var legacy control.OpenConn
	if err := json.Unmarshal([]byte(`{"tunnelId":"tnl_a","clientAddr":"203.0.113.7:51234","connId":"9"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.FlowID != 0 {
		t.Errorf("a legacy open_conn named flow %d", legacy.FlowID)
	}

	// And an allocated flow round-trips, including a value past int32 — flow ids
	// are a full uint32 space.
	fast := control.OpenConn{TunnelID: "tnl_a", ConnID: "9", FlowID: 4294967295}
	fb, _ := json.Marshal(fast)
	var gotFast control.OpenConn
	if err := json.Unmarshal(fb, &gotFast); err != nil {
		t.Fatal(err)
	}
	if gotFast.FlowID != 4294967295 {
		t.Errorf("flow id round-trip: %+v", gotFast)
	}
}

// PauseState is a complete-set message: an empty one must marshal to {} (no
// keys — a legacy peer that somehow receives one sees nothing surprising), and
// a populated one round-trips.
func TestPauseStateOmitsEmpty(t *testing.T) {
	b, err := json.Marshal(control.PauseState{})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}" {
		t.Errorf("empty PauseState marshals to %s, want {}", b)
	}
	b2, _ := json.Marshal(control.PauseState{TunnelIDs: []string{"tnl_a"}, AgentWide: true})
	var g control.PauseState
	if err := json.Unmarshal(b2, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.TunnelIDs) != 1 || g.TunnelIDs[0] != "tnl_a" || !g.AgentWide {
		t.Errorf("PauseState round-trip failed: %+v", g)
	}
}

func TestHelloOKAndPongOmitEmpty(t *testing.T) {
	ok := control.HelloOK{ProtocolVersion: 1, SessionGeneration: 3}
	b, _ := json.Marshal(ok)
	for _, f := range []string{"hostname", "localIps", "observedIp", "assignedAgentId", "gatewayId", "configSeedNeeded", "headless"} {
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

	// An untagged spec must not put the group key on the wire either — a solo
	// tunnel's frame stays byte-identical to a legacy peer's.
	if strings.Contains(string(b), "groupId") {
		t.Errorf("untagged TunnelSpec.GroupID leaked onto the wire: %s", b)
	}

	// A legacy frame (no bandwidth or group fields) decodes with all zero.
	var got control.TunnelSpec
	if err := json.Unmarshal([]byte(`{"id":"t1","name":"web","type":"tcp","publicPort":25565}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.BandwidthLimitMbps != 0 || got.BandwidthLimitScope != "" || got.GroupID != "" {
		t.Errorf("legacy frame produced non-zero additive fields: %+v", got)
	}

	// A group tag round-trips and appears on the wire.
	tagged := control.TunnelSpec{ID: "t1", Name: "web", Type: "tcp", GroupID: "grp_a"}
	bt, _ := json.Marshal(tagged)
	if !strings.Contains(string(bt), `"groupId":"grp_a"`) {
		t.Errorf("set TunnelSpec.GroupID missing from the wire: %s", bt)
	}
	var gt control.TunnelSpec
	if err := json.Unmarshal(bt, &gt); err != nil {
		t.Fatal(err)
	}
	if gt.GroupID != "grp_a" {
		t.Errorf("TunnelSpec.GroupID round-trip failed: %+v", gt)
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
