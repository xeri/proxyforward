package app

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxyforward/internal/config"
)

func sampleConfig() *config.Config {
	cfg := config.Default()
	cfg.Role = config.RoleAgent
	cfg.Agent.AgentID = "AGENTIDSECRET"
	cfg.Agent.Token = "AGENTTOKENSECRET"
	cfg.Agent.GatewayHost = "gw.secret.example.com"
	cfg.Agent.CertFingerprint = "sha256:deadbeefsecret"
	cfg.Agent.Tunnels = []config.Tunnel{{ID: "t1", Name: "mc", LocalAddr: "10.9.8.7:25565"}}
	cfg.Gateway.Token = "GWTOKENSECRET"
	cfg.Gateway.PublicHost = "public.secret.example.com"
	cfg.Gateway.BindAddr = "192.168.50.1"
	cfg.Metrics.PrometheusAddr = "127.0.0.99:9464"
	return cfg
}

func TestRedactConfigMasksEverySecret(t *testing.T) {
	cfg := sampleConfig()
	orig := *cfg
	r := redactConfig(cfg)

	secrets := []string{
		"AGENTIDSECRET", "AGENTTOKENSECRET", "gw.secret.example.com",
		"deadbeefsecret", "10.9.8.7", "GWTOKENSECRET",
		"public.secret.example.com", "192.168.50.1", "127.0.0.99",
	}
	blob := r.Agent.AgentID + r.Agent.Token + r.Agent.GatewayHost + r.Agent.CertFingerprint +
		r.Gateway.Token + r.Gateway.PublicHost + r.Gateway.BindAddr + r.Metrics.PrometheusAddr
	for _, t2 := range r.Agent.Tunnels {
		blob += t2.LocalAddr
	}
	for _, s := range secrets {
		if strings.Contains(blob, s) {
			t.Errorf("redacted config still contains secret %q", s)
		}
	}
	// The original config must be untouched (tunnel slice copied, not aliased).
	if cfg.Agent.Tunnels[0].LocalAddr != orig.Agent.Tunnels[0].LocalAddr {
		t.Error("redactConfig mutated the caller's tunnel slice")
	}
}

func TestRedactStatsJSONHashesPeerIPs(t *testing.T) {
	in := []byte(`{"v":3,"lifetime":{"bytesIn":10},"peers":[{"ip":"203.0.113.9","totalConns":4},{"ip":"198.51.100.2","totalConns":1}]}`)
	out := redactStatsJSON(in)
	s := string(out)
	if strings.Contains(s, "203.0.113.9") || strings.Contains(s, "198.51.100.2") {
		t.Fatalf("peer IPs survived redaction: %s", s)
	}
	if !strings.Contains(s, "ip-") {
		t.Fatalf("expected hashed ip- pseudonyms, got: %s", s)
	}
	// Non-PII fields are preserved.
	if !strings.Contains(s, "\"totalConns\"") || !strings.Contains(s, "bytesIn") {
		t.Fatalf("redaction dropped non-PII fields: %s", s)
	}
}

func TestWriteDiagnosticsNoLeaks(t *testing.T) {
	dir := t.TempDir()
	// Seed a stats.json with a client IP that must not leak.
	os.WriteFile(filepath.Join(dir, "stats.json"),
		[]byte(`{"v":3,"peers":[{"ip":"203.0.113.77","totalConns":2}]}`), 0o600)

	out := filepath.Join(dir, "diag.zip")
	cfg := sampleConfig()
	if err := writeDiagnostics(out, cfg, dir, "health: good\n", nil); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	names := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = string(b)
	}

	for _, want := range []string{"version.txt", "health.txt", "config.redacted.toml", "stats.redacted.json"} {
		if _, ok := names[want]; !ok {
			t.Errorf("bundle missing %s (have %v)", want, keys(names))
		}
	}
	all := strings.Join(values(names), "\n")
	for _, secret := range []string{"AGENTTOKENSECRET", "GWTOKENSECRET", "gw.secret.example.com", "203.0.113.77", "deadbeefsecret"} {
		if strings.Contains(all, secret) {
			t.Errorf("diagnostics bundle leaked %q", secret)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
