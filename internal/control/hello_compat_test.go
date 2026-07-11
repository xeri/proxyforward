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
