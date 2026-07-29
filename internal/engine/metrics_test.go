package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/gateway"
	"proxyforward/internal/ipc"
)

func TestWriteMetricsFormat(t *testing.T) {
	st := ipc.Status{
		Version:            "v1.2.3",
		Role:               "gateway",
		AgentConnected:     true,
		ConnectionsTotal:   5,
		TotalBytesIn:       100,
		TotalBytesOut:      200,
		AllTimeBytesIn:     1000,
		AllTimeBytesOut:    2000,
		LinkBytesIn:        10,
		LinkBytesOut:       20,
		LinkSessions:       3,
		CumulativeUptimeMs: 45000,
		RTTMillis:          -1,  // unknown → must be omitted
		JitterMillis:       2.5, // known
		PacketLossPct:      -1,  // unknown → must be omitted
		Tunnels: []ipc.TunnelStatus{
			{ID: "abc", Name: "mc", LocalUp: true, LocalKnown: true},
			{ID: "xyz", Name: "pending", LocalKnown: false}, // unknown → omitted
		},
	}
	refusals := &gateway.RefusalCounts{
		AuthFailures:     7,
		ConnsGlobalCap:   11,
		ConnsPerIPCap:    13,
		PreAuthFull:      17,
		BindTunnelCap:    19,
		BindNoFreePort:   23,
		BindPortInUse:    29,
		BindListenFailed: 31,
		PortsReassigned:  37,
		TunnelsRecovered: 41,
		GroupsRecovered:  43,
	}
	var buf bytes.Buffer
	writeMetrics(&buf, st, 4, refusals)
	out := buf.String()

	for _, want := range []string{
		`proxyforward_build_info{version="v1.2.3",role="gateway"} 1`,
		"proxyforward_link_up 1",
		"proxyforward_connections 5",
		"proxyforward_players 4",
		`proxyforward_bytes_total{direction="in"} 100`,
		`proxyforward_bytes_total{direction="out"} 200`,
		`proxyforward_alltime_bytes_total{direction="out"} 2000`,
		`proxyforward_link_bytes_total{direction="in"} 10`,
		"proxyforward_link_sessions_total 3",
		"proxyforward_uptime_ms_total 45000",
		"proxyforward_link_jitter_ms 2.5",
		`proxyforward_tunnel_local_up{tunnel_id="abc",name="mc"} 1`,
		"# TYPE proxyforward_bytes_total counter",
		"# TYPE proxyforward_connections gauge",
		// The harm series: every refusal branch, every bind outcome that is not a
		// clean success, and the recoveries that tell a boot race from a squatter.
		"proxyforward_gateway_auth_failures_total 7",
		`proxyforward_gateway_conns_refused_total{reason="global_cap"} 11`,
		`proxyforward_gateway_conns_refused_total{reason="per_ip_cap"} 13`,
		"proxyforward_gateway_preauth_refused_total 17",
		`proxyforward_gateway_bind_failures_total{reason="tunnel_cap"} 19`,
		`proxyforward_gateway_bind_failures_total{reason="no_free_port"} 23`,
		`proxyforward_gateway_bind_failures_total{reason="port_in_use"} 29`,
		`proxyforward_gateway_bind_failures_total{reason="listen_failed"} 31`,
		"proxyforward_gateway_port_reassignments_total 37",
		`proxyforward_gateway_binds_recovered_total{scope="tunnel"} 41`,
		`proxyforward_gateway_binds_recovered_total{scope="group"} 43`,
		"# TYPE proxyforward_gateway_conns_refused_total counter",
		"# HELP proxyforward_gateway_bind_failures_total ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing series %q\n--- output ---\n%s", want, out)
		}
	}

	// -1 sentinels are "no sample": omit the series, never export 0 or -1.
	for _, absent := range []string{"proxyforward_link_rtt_ms", "proxyforward_link_loss_pct"} {
		if strings.Contains(out, absent) {
			t.Errorf("unknown gauge %q should be omitted\n--- output ---\n%s", absent, out)
		}
	}
	// A tunnel with unknown backend health must not appear at all.
	if strings.Contains(out, `tunnel_id="xyz"`) || strings.Contains(out, `name="pending"`) {
		t.Errorf("unknown-health tunnel leaked into output\n--- output ---\n%s", out)
	}
}

func TestWriteMetricsAgentSentinels(t *testing.T) {
	// An agent with a live link and a real RTT but no jitter/loss sample yet.
	st := ipc.Status{
		Version:       "dev",
		Role:          "agent",
		LinkUp:        true,
		RTTMillis:     42,
		JitterMillis:  -1,
		PacketLossPct: -1,
	}
	var buf bytes.Buffer
	writeMetrics(&buf, st, 0, nil) // an agent has no gateway
	out := buf.String()
	if !strings.Contains(out, "proxyforward_link_up 1") {
		t.Errorf("agent link should be up\n%s", out)
	}
	if !strings.Contains(out, "proxyforward_link_rtt_ms 42") {
		t.Errorf("known RTT should be exported\n%s", out)
	}
	if strings.Contains(out, "proxyforward_link_jitter_ms") || strings.Contains(out, "proxyforward_link_loss_pct") {
		t.Errorf("unknown jitter/loss should be omitted\n%s", out)
	}
	// Same contract as the -1 gauges: a process that cannot refuse anything must
	// not export a row of zeroes that reads as "refusing nothing".
	if strings.Contains(out, "proxyforward_gateway_") {
		t.Errorf("gateway refusal series must be absent on an agent\n%s", out)
	}
}

// TestWriteMetricsZeroRefusalsStillRender is the other half of the omit rule: a
// gateway that has refused nothing knows that it has refused nothing, so its
// counters are exported as 0 rather than omitted. Absent means "no gateway";
// zero means "nothing yet", and a dashboard has to be able to tell them apart.
func TestWriteMetricsZeroRefusalsStillRender(t *testing.T) {
	var buf bytes.Buffer
	writeMetrics(&buf, ipc.Status{Role: "gateway", RTTMillis: -1, JitterMillis: -1, PacketLossPct: -1}, 0, &gateway.RefusalCounts{})
	out := buf.String()
	for _, want := range []string{
		"proxyforward_gateway_auth_failures_total 0",
		`proxyforward_gateway_conns_refused_total{reason="global_cap"} 0`,
		`proxyforward_gateway_bind_failures_total{reason="listen_failed"} 0`,
		"proxyforward_gateway_binds_recovered_total{scope=\"group\"} 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing zero series %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestEscapeLabel(t *testing.T) {
	// backslash, then quote, then newline — all must be escaped.
	if got := escapeLabel("a\"b\\c\n"); got != `a\"b\\c\n` {
		t.Errorf("escapeLabel = %q, want %q", got, `a\"b\\c\n`)
	}
}

// TestServeMetricsLifecycle serves the endpoint, scrapes it, stops it, then does
// it again on the SAME port — the second round proves the listener was released
// on ctx cancel (the RestartEngine port-reuse guarantee).
func TestServeMetricsLifecycle(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", borrowPort(t))
	eng := newGatewayEngine(t, true, addr)

	serveAndScrape := func(round int) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { eng.serveMetrics(ctx); close(done) }()

		body := pollMetrics(t, addr)
		if !strings.Contains(body, "proxyforward_build_info") {
			t.Fatalf("round %d: scrape missing build_info:\n%s", round, body)
		}
		if !strings.Contains(body, `role="gateway"`) {
			t.Fatalf("round %d: scrape missing gateway role:\n%s", round, body)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: serveMetrics did not return after cancel (listener leaked?)", round)
		}
	}

	serveAndScrape(1)
	serveAndScrape(2) // same port — only succeeds if round 1 freed it
}

func TestServeMetricsDisabled(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", borrowPort(t))
	eng := newGatewayEngine(t, false, addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { eng.serveMetrics(ctx); close(done) }()

	select {
	case <-done: // must return immediately, having bound nothing
	case <-time.After(2 * time.Second):
		t.Fatal("serveMetrics should return immediately when disabled")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port should be free when metrics are disabled: %v", err)
	}
	ln.Close()
}

// --- helpers ---

func borrowPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func newGatewayEngine(t *testing.T, metricsEnabled bool, metricsAddr string) *Engine {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Role = config.RoleGateway
	cfg.Gateway.BindAddr = "127.0.0.1"
	cfg.Gateway.ControlPort = borrowPort(t)
	cfg.Metrics.PrometheusEnabled = metricsEnabled
	cfg.Metrics.PrometheusAddr = metricsAddr
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	eng, err := New(cfg, dir, filepath.Join(dir, "config.toml"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() {
		if eng.DB != nil {
			_ = eng.DB.Close()
		}
		eng.geo.Close()
	})
	return eng
}

func pollMetrics(t *testing.T, addr string) string {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get("http://" + addr + "/metrics")
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /metrics: status %d", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
				t.Errorf("Content-Type = %q, want text/plain; version=0.0.4", ct)
			}
			b, _ := io.ReadAll(resp.Body)
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("metrics endpoint never came up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
