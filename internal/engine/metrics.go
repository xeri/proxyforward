package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"proxyforward/internal/gateway"
	"proxyforward/internal/ipc"
)

const (
	// Bounds on a single scrape so a slow or hostile client can't pin the
	// endpoint, and how long to wait for a graceful shutdown on ctx cancel.
	metricsReadTimeout  = 5 * time.Second
	metricsWriteTimeout = 10 * time.Second
	metricsShutdownWait = 3 * time.Second
	// defaultMetricsAddr matches config.Default(); used only if an enabled
	// endpoint somehow reaches us with an empty address.
	defaultMetricsAddr = "127.0.0.1:9464"
)

// serveMetrics runs the Prometheus /metrics endpoint for the lifetime of ctx.
// It is best-effort and never fatal: proxying is the core job, so a bind
// failure is logged and swallowed rather than killing the engine (unlike the
// IPC pipe, which is fatal by design). It returns only after the listener is
// closed, so a restarting engine can re-bind the same address without an
// "address already in use" race.
func (e *Engine) serveMetrics(ctx context.Context) {
	mc := e.cfg.Metrics
	if !mc.PrometheusEnabled {
		return
	}
	addr := mc.PrometheusAddr
	if addr == "" {
		addr = defaultMetricsAddr
	}
	warnIfMetricsPublic(e.logger, addr)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		e.logger.Warn("metrics: listen failed, endpoint disabled", "addr", addr, "err", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writeMetrics(w, e.Status(), e.conns().PlayerCount(), e.gatewayRefusals())
	})
	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  metricsReadTimeout,
		WriteTimeout: metricsWriteTimeout,
	}

	// Shut the listener when ctx dies; wait for that goroutine before returning
	// so no goroutine outlives Run (goleak-clean) and the port is provably free.
	shutDone := make(chan struct{})
	go func() {
		defer close(shutDone)
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), metricsShutdownWait)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	e.logger.Info("metrics endpoint listening", "addr", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		e.logger.Warn("metrics: server error", "err", err)
	}
	<-shutDone
}

// warnIfMetricsPublic logs a warning when the endpoint binds to anything other
// than loopback, since the payload exposes traffic and player counts. It is a
// soft warning, not a hard error: an operator may deliberately front it with a
// reverse proxy or firewall.
func warnIfMetricsPublic(logger *slog.Logger, addr string) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}
	switch {
	case host == "": // empty host binds all interfaces
	case host == "localhost":
		return
	default:
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return
		}
	}
	logger.Warn("metrics: endpoint is not loopback-only; it exposes traffic and player counts — bind to 127.0.0.1 or firewall it", "addr", addr)
}

// gatewayRefusals snapshots the gateway's refusal counters, or nil on an agent —
// which has no gateway, so those series are absent rather than a row of zeroes.
// Zeroes would read as "nothing is being refused" on a process that cannot
// refuse anything, the same lie the -1 gauges are omitted to avoid.
func (e *Engine) gatewayRefusals() *gateway.RefusalCounts {
	if e.Gateway == nil {
		return nil
	}
	c := e.Gateway.Refusals()
	return &c
}

// writeMetrics renders the engine status as Prometheus text-format metrics. All
// series come from a single Status snapshot (plus the player count and, on a
// gateway, its refusal counters), so a scrape adds no work to the data path.
// Unknown gauges (-1 sentinels) are omitted entirely rather than exported as 0,
// preserving the honest-unknown contract. No player names or peer IPs appear as
// labels (privacy charter) — which is why the refusal counters are totals with
// coarse reason labels and never per-source series.
//
// refusals is nil for a role that has no gateway.
func writeMetrics(w io.Writer, st ipc.Status, players int, refusals *gateway.RefusalCounts) {
	linkUp := 0
	if st.LinkUp || st.AgentConnected {
		linkUp = 1
	}

	metric(w, "proxyforward_build_info", "gauge", "Build version and role; value is always 1.",
		fmt.Sprintf("proxyforward_build_info{version=%q,role=%q} 1", escapeLabel(st.Version), escapeLabel(st.Role)))
	metric(w, "proxyforward_link_up", "gauge", "1 when the control link to the peer is up, else 0.",
		fmt.Sprintf("proxyforward_link_up %d", linkUp))
	metric(w, "proxyforward_connections", "gauge", "Currently tracked proxied connections.",
		fmt.Sprintf("proxyforward_connections %d", st.ConnectionsTotal))
	metric(w, "proxyforward_players", "gauge", "Currently connected distinct players.",
		fmt.Sprintf("proxyforward_players %d", players))

	metric(w, "proxyforward_bytes_total", "counter", "Bytes relayed this engine run, by direction.",
		fmt.Sprintf("proxyforward_bytes_total{direction=\"in\"} %d\nproxyforward_bytes_total{direction=\"out\"} %d", st.TotalBytesIn, st.TotalBytesOut))
	metric(w, "proxyforward_alltime_bytes_total", "counter", "Bytes relayed over all runs, by direction.",
		fmt.Sprintf("proxyforward_alltime_bytes_total{direction=\"in\"} %d\nproxyforward_alltime_bytes_total{direction=\"out\"} %d", st.AllTimeBytesIn, st.AllTimeBytesOut))
	metric(w, "proxyforward_link_bytes_total", "counter", "Bytes over the control link this session, by direction.",
		fmt.Sprintf("proxyforward_link_bytes_total{direction=\"in\"} %d\nproxyforward_link_bytes_total{direction=\"out\"} %d", st.LinkBytesIn, st.LinkBytesOut))
	metric(w, "proxyforward_link_sessions_total", "counter", "Control-link sessions established over all runs.",
		fmt.Sprintf("proxyforward_link_sessions_total %d", st.LinkSessions))
	metric(w, "proxyforward_uptime_ms_total", "counter", "Cumulative engine uptime in milliseconds.",
		fmt.Sprintf("proxyforward_uptime_ms_total %d", st.CumulativeUptimeMs))

	// Link quality: -1 means "no sample" — omit rather than lie with a zero.
	if st.RTTMillis >= 0 {
		metric(w, "proxyforward_link_rtt_ms", "gauge", "Control-link round-trip time in milliseconds.",
			fmt.Sprintf("proxyforward_link_rtt_ms %d", st.RTTMillis))
	}
	if st.JitterMillis >= 0 {
		metric(w, "proxyforward_link_jitter_ms", "gauge", "Control-link jitter in milliseconds.",
			"proxyforward_link_jitter_ms "+formatFloat(st.JitterMillis))
	}
	if st.PacketLossPct >= 0 {
		metric(w, "proxyforward_link_loss_pct", "gauge", "Control-link packet loss percentage.",
			"proxyforward_link_loss_pct "+formatFloat(st.PacketLossPct))
	}

	writeRefusalMetrics(w, refusals)

	// Per-tunnel backend health, only when known (unknown stays absent).
	printed := false
	for _, t := range st.Tunnels {
		if !t.LocalKnown {
			continue
		}
		if !printed {
			fmt.Fprint(w, "# HELP proxyforward_tunnel_local_up 1 when the tunnel's local backend is reachable, else 0.\n# TYPE proxyforward_tunnel_local_up gauge\n")
			printed = true
		}
		up := 0
		if t.LocalUp {
			up = 1
		}
		fmt.Fprintf(w, "proxyforward_tunnel_local_up{tunnel_id=%q,name=%q} %d\n", escapeLabel(t.ID), escapeLabel(t.Name), up)
	}
}

// writeRefusalMetrics renders the gateway's refusal and bind-failure counters —
// the only series here that describe harm rather than health, and the ones an
// operator alerts on. Nothing is rendered for c == nil (an agent): a gateway
// with nothing to refuse and a process incapable of refusing must not look the
// same on a dashboard.
//
// Reasons are labels on one metric per family, not a metric each, so a rate()
// over the family is a single query and a new reason breaks nobody's dashboard.
// Every label is a fixed word from this file: a reason label built from a peer's
// address would be both unbounded cardinality and the privacy leak the charter
// forbids.
func writeRefusalMetrics(w io.Writer, c *gateway.RefusalCounts) {
	if c == nil {
		return
	}
	metric(w, "proxyforward_gateway_auth_failures_total", "counter", "Failed authentication attempts charged to a source network's budget.",
		fmt.Sprintf("proxyforward_gateway_auth_failures_total %d", c.AuthFailures))
	metric(w, "proxyforward_gateway_conns_refused_total", "counter", "Public connections refused by the connection gate, by the cap that was hit.",
		fmt.Sprintf("proxyforward_gateway_conns_refused_total{reason=\"global_cap\"} %d\nproxyforward_gateway_conns_refused_total{reason=\"per_ip_cap\"} %d",
			c.ConnsGlobalCap, c.ConnsPerIPCap))
	metric(w, "proxyforward_gateway_preauth_refused_total", "counter", "Connections refused because the unauthenticated set was full.",
		fmt.Sprintf("proxyforward_gateway_preauth_refused_total %d", c.PreAuthFull))
	metric(w, "proxyforward_gateway_bind_failures_total", "counter", "Public listeners that could not be bound, by reason; each leaves a tunnel down.",
		fmt.Sprintf("proxyforward_gateway_bind_failures_total{reason=\"tunnel_cap\"} %d\nproxyforward_gateway_bind_failures_total{reason=\"no_free_port\"} %d\nproxyforward_gateway_bind_failures_total{reason=\"port_in_use\"} %d\nproxyforward_gateway_bind_failures_total{reason=\"listen_failed\"} %d",
			c.BindTunnelCap, c.BindNoFreePort, c.BindPortInUse, c.BindListenFailed))
	metric(w, "proxyforward_gateway_port_reassignments_total", "counter", "Tunnels bound to a port other than the one requested because it was in use.",
		fmt.Sprintf("proxyforward_gateway_port_reassignments_total %d", c.PortsReassigned))
	metric(w, "proxyforward_gateway_binds_recovered_total", "counter", "Listeners the rebind retry brought back after an earlier failure, by scope.",
		fmt.Sprintf("proxyforward_gateway_binds_recovered_total{scope=\"tunnel\"} %d\nproxyforward_gateway_binds_recovered_total{scope=\"group\"} %d",
			c.TunnelsRecovered, c.GroupsRecovered))
}

// metric writes a HELP/TYPE header followed by one or more pre-rendered series
// lines (body may contain several \n-separated series sharing the name).
func metric(w io.Writer, name, typ, help, body string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s\n", name, help, name, typ, body)
}

// escapeLabel escapes a Prometheus label value: backslash, double-quote, newline.
func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// formatFloat renders a float without scientific notation, shortest round-trip.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
