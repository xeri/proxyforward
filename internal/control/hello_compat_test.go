package control_test

import (
	"encoding/json"
	"strings"
	"testing"

	"proxyforward/internal/control"
)

// A legacy peer sends no hostname/localIps/observedIp/recvUnixNano. New structs
// must encode identically (omitempty) and decode legacy frames without error.
func TestHelloWireBackCompat(t *testing.T) {
	// Empty identity fields must not appear on the wire.
	h := control.Hello{ProtocolVersion: 1, Kind: control.KindControl, AgentID: "a", Token: "t", AppVersion: "v"}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hostname", "localIps"} {
		if strings.Contains(string(b), f) {
			t.Errorf("empty Hello.%s leaked onto the wire: %s", f, b)
		}
	}

	// A legacy frame decodes cleanly, leaving the new fields zero.
	var got control.Hello
	if err := json.Unmarshal([]byte(`{"protocolVersion":1,"kind":"control","agentId":"a","token":"t"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Hostname != "" || got.LocalIPs != nil {
		t.Errorf("legacy frame produced non-zero new fields: %+v", got)
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
	ok := control.HelloOK{ProtocolVersion: 1, Generation: 3}
	b, _ := json.Marshal(ok)
	for _, f := range []string{"hostname", "localIps", "observedIp"} {
		if strings.Contains(string(b), f) {
			t.Errorf("empty HelloOK.%s leaked onto the wire: %s", f, b)
		}
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
