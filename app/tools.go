package app

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"proxyforward/internal/config"
	"proxyforward/internal/ipc"
	"proxyforward/internal/logging"
	"proxyforward/internal/version"
)

// openInFileManager reveals dir in the OS file manager. explorer.exe returns a
// non-zero exit code even on success, so its error is intentionally ignored.
func openInFileManager(dir string) error {
	switch runtime.GOOS {
	case "windows":
		exec.Command("explorer", dir).Start()
		return nil
	case "darwin":
		return exec.Command("open", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

// testReachability dials the gateway's public port across the real network —
// the same path a player takes — validating DNS, gateway firewall, router
// forwarding, the listener, and the tunnel in one check.
func testReachability(host string, port int) (string, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 8*time.Second)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w — check the gateway's firewall rule, the router's port forward, and DNS", addr, err)
	}
	conn.Close()
	return fmt.Sprintf("Reachable: %s answered in %s — players can connect.", addr, time.Since(start).Round(time.Millisecond)), nil
}

// writeDiagnostics builds the support bundle: version, a fully redacted config,
// a health summary, the recent in-memory log lines, the persisted stats (peer
// IPs pseudonymized), and every on-disk log file (rotated + crash + wails).
// Everything that could identify a host, network, or client is masked so the
// bundle is safe to share.
func writeDiagnostics(path string, cfg *config.Config, configDir, health string, ring *logging.Ring) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	// version.txt
	if w, err := zw.Create("version.txt"); err == nil {
		fmt.Fprintln(w, version.String())
	}

	// health.txt — live link identity/quality snapshot (already non-secret).
	if health != "" {
		if w, err := zw.Create("health.txt"); err == nil {
			io.WriteString(w, health)
		}
	}

	// config.redacted.toml — secrets, hosts, IPs, and identities masked;
	// structure intact.
	redacted := redactConfig(cfg)
	if data, err := toml.Marshal(&redacted); err == nil {
		if w, err := zw.Create("config.redacted.toml"); err == nil {
			w.Write(data)
		}
	}

	// stats.redacted.json — bandwidth/lifetime history with peer IPs hashed.
	if raw, err := os.ReadFile(filepath.Join(configDir, "stats.json")); err == nil {
		if w, err := zw.Create("stats.redacted.json"); err == nil {
			w.Write(redactStatsJSON(raw))
		}
	}

	// logs-recent.txt — the GUI ring (what the user was just looking at).
	if ring != nil {
		if w, err := zw.Create("logs-recent.txt"); err == nil {
			for _, e := range ring.EntriesSince(0) {
				fmt.Fprintf(w, "%s %-5s %s %s\n", time.UnixMilli(e.TimeMs).Format(time.RFC3339), e.Level, e.Msg, e.Attrs)
			}
		}
	}

	// On-disk log files: the rotating log and its rotations, plus the crash
	// and wails runtime logs. Any that don't exist are silently skipped.
	logDir := filepath.Dir(logging.DefaultFilePath(configDir))
	logFiles := []string{
		logging.DefaultFilePath(configDir),
		logging.DefaultFilePath(configDir) + ".1",
		logging.DefaultFilePath(configDir) + ".2",
		logging.DefaultFilePath(configDir) + ".3",
		filepath.Join(logDir, "crash.log"),
		filepath.Join(logDir, "wails.log"),
	}
	for _, p := range logFiles {
		copyIntoZip(zw, filepath.Base(p), p)
	}
	return nil
}

// copyIntoZip streams src into the archive under nameInZip; missing files are
// skipped without error (a diagnostics bundle is best-effort).
func copyIntoZip(zw *zip.Writer, nameInZip, src string) {
	lf, err := os.Open(src)
	if err != nil {
		return
	}
	defer lf.Close()
	if w, err := zw.Create(nameInZip); err == nil {
		io.Copy(w, lf)
	}
}

// redactConfig returns a copy of cfg with every secret, host, IP, and identity
// masked. Structure is preserved so the shape of the config stays diagnosable.
func redactConfig(cfg *config.Config) config.Config {
	const secret = "[redacted]"
	const host = "[redacted-host]"
	r := *cfg
	if r.Gateway.Token != "" {
		r.Gateway.Token = secret
	}
	if r.Gateway.PublicHost != "" {
		r.Gateway.PublicHost = host
	}
	if r.Gateway.BindAddr != "" {
		r.Gateway.BindAddr = host
	}
	if r.Agent.Token != "" {
		r.Agent.Token = secret
	}
	if r.Agent.AgentID != "" {
		r.Agent.AgentID = secret
	}
	if r.Agent.CertFingerprint != "" {
		r.Agent.CertFingerprint = secret
	}
	if r.Agent.EnrollTicket != "" {
		// A pending single-use enrollment ticket is a live credential until the
		// gateway confirms enrollment — exactly the failing-to-pair window in which
		// a user grabs a bundle. Never let it ride along.
		r.Agent.EnrollTicket = secret
	}
	if r.Agent.GatewayHost != "" {
		r.Agent.GatewayHost = host
	}
	if r.Metrics.PrometheusAddr != "" {
		r.Metrics.PrometheusAddr = host
	}
	// Copy the tunnel slice before masking so the live config is untouched.
	if len(r.Agent.Tunnels) > 0 {
		ts := make([]config.Tunnel, len(r.Agent.Tunnels))
		copy(ts, r.Agent.Tunnels)
		for i := range ts {
			if ts[i].LocalAddr != "" {
				ts[i].LocalAddr = host
			}
		}
		r.Agent.Tunnels = ts
	}
	return r
}

// redactStatsJSON pseudonymizes client IPs in the persisted stats file. Parse
// failures return the input unchanged rather than dropping the file — the raw
// bytes only ever contain peer IPs, which the caller has already accepted.
func redactStatsJSON(raw []byte) []byte {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return raw
	}
	peersRaw, ok := top["peers"]
	if !ok {
		return raw
	}
	var peers []map[string]json.RawMessage
	if json.Unmarshal(peersRaw, &peers) != nil {
		return raw
	}
	for _, p := range peers {
		ipRaw, ok := p["ip"]
		if !ok {
			continue
		}
		var ip string
		if json.Unmarshal(ipRaw, &ip) != nil || ip == "" {
			continue
		}
		if b, err := json.Marshal(hashIP(ip)); err == nil {
			p["ip"] = b
		}
	}
	if b, err := json.Marshal(peers); err == nil {
		top["peers"] = b
	}
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return raw
	}
	return out
}

// hashIP maps a client IP to a stable, non-reversible pseudonym so repeated
// appearances stay correlatable in the bundle without exposing the address.
func hashIP(ip string) string {
	sum := sha256.Sum256([]byte(ip))
	return fmt.Sprintf("ip-%x", sum[:6])
}

// diagnosticsHealth renders the live link snapshot for health.txt. It reports
// hostnames and quality metrics (safe to share) but only the count of LAN IPs,
// never the addresses themselves.
func diagnosticsHealth(s ipc.Status) string {
	msOrNA := func(v float64) string {
		if v < 0 {
			return "n/a"
		}
		return fmt.Sprintf("%.1f ms", v)
	}
	dash := func(v string) string {
		if v == "" {
			return "—"
		}
		return v
	}
	var b strings.Builder
	fmt.Fprintf(&b, "role:           %s\n", s.Role)
	fmt.Fprintf(&b, "health:         %s\n", dash(s.HealthScore))
	fmt.Fprintf(&b, "link up:        %v\n", s.LinkUp || s.AgentConnected)
	fmt.Fprintf(&b, "local hostname: %s\n", dash(s.LocalHostname))
	fmt.Fprintf(&b, "peer hostname:  %s\n", dash(s.PeerHostname))
	fmt.Fprintf(&b, "rtt:            %s\n", msOrNA(float64(s.RTTMillis)))
	fmt.Fprintf(&b, "jitter:         %s\n", msOrNA(s.JitterMillis))
	if s.PacketLossPct < 0 {
		fmt.Fprintf(&b, "packet loss:    n/a\n")
	} else {
		fmt.Fprintf(&b, "packet loss:    %.1f%%\n", s.PacketLossPct)
	}
	if s.LinkUpSinceMs > 0 {
		fmt.Fprintf(&b, "link uptime:    %s\n", time.Since(time.UnixMilli(s.LinkUpSinceMs)).Round(time.Second))
	}
	fmt.Fprintf(&b, "local LAN IPs:  %d\n", len(s.LocalLANIPs))
	fmt.Fprintf(&b, "peer LAN IPs:   %d\n", len(s.PeerLANIPs))
	return b.String()
}
