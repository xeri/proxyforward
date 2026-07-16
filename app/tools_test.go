package app

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxyforward/internal/config"
	"proxyforward/internal/logging"
)

func sampleConfig() *config.Config {
	cfg := config.Default()
	cfg.Role = config.RoleAgent
	cfg.Agent.AgentID = "AGENTIDSECRET"
	cfg.Agent.Token = "AGENTTOKENSECRET"
	cfg.Agent.GatewayHost = "gw.secret.example.com"
	cfg.Agent.CertFingerprint = "sha256:deadbeefsecret"
	cfg.Agent.EnrollTicket = "tkt_ENROLLTICKETSECRET"
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
		"ENROLLTICKETSECRET",
	}
	blob := r.Agent.AgentID + r.Agent.Token + r.Agent.GatewayHost + r.Agent.CertFingerprint +
		r.Agent.EnrollTicket +
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

// TestWriteDiagnosticsNoLeaks drives every channel writeDiagnostics ships, because a
// bundle is only as shareable as its leakiest file. It previously passed ring=nil
// into an empty dir, so the two *unredacted* channels — the GUI ring and the on-disk
// log files — were never populated and the secret sweep below ran over an empty set.
// That blind spot is how a logged pairing code (which embeds Gateway.Token verbatim)
// shipped in cleartext while config.redacted.toml sat right beside it masking the
// same token. The logger here is the real production fan-out, so the ring and the
// rotating file are filled the way they are in a live process.
func TestWriteDiagnosticsNoLeaks(t *testing.T) {
	dir := t.TempDir()
	// Seed a stats.json with a client IP that must not leak.
	os.WriteFile(filepath.Join(dir, "stats.json"),
		[]byte(`{"v":3,"peers":[{"ip":"203.0.113.77","totalConns":2}]}`), 0o600)

	cfg := sampleConfig()

	// Fill the ring and the on-disk log through the real logger, with lines that
	// carry the secrets the way a careless call site would.
	ring := logging.NewRing(64)
	logger, closeLog, err := logging.New(logging.Options{
		FilePath: logging.DefaultFilePath(dir),
		Ring:     ring,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("pairing code", "code",
		"pxf://"+cfg.Gateway.PublicHost+":8474/v1/pair/"+cfg.Gateway.Token+"#"+cfg.Agent.CertFingerprint)
	logger.Info("agent connected", "agentId", cfg.Agent.AgentID, "gateway", cfg.Agent.GatewayHost)
	logger.Warn("dial failed", "local", cfg.Agent.Tunnels[0].LocalAddr, "bind", cfg.Gateway.BindAddr)
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	// Guard against a vacuous pass: the on-disk log must really hold the secret, or
	// this test proves nothing about the path that ships it.
	onDisk, err := os.ReadFile(logging.DefaultFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), cfg.Gateway.Token) {
		t.Fatal("setup bug: the seeded log does not contain the token, so this test would pass vacuously")
	}

	out := filepath.Join(dir, "diag.zip")
	if err := writeDiagnostics(out, cfg, dir, "health: good\n", ring); err != nil {
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

	// The log channels must actually be in the bundle — if they silently stopped
	// shipping, the sweep below would pass for the wrong reason.
	for _, want := range []string{"version.txt", "health.txt", "config.redacted.toml", "stats.redacted.json", "logs-recent.txt", "proxyforward.log"} {
		if _, ok := names[want]; !ok {
			t.Errorf("bundle missing %s (have %v)", want, keys(names))
		}
	}
	// Both log files must still be diagnostically useful after scrubbing — masking by
	// shipping nothing would pass the sweep and defeat the point of the bundle.
	for _, name := range []string{"logs-recent.txt", "proxyforward.log"} {
		if !strings.Contains(names[name], "pairing code") || !strings.Contains(names[name], "dial failed") {
			t.Errorf("%s lost its log lines to scrubbing:\n%s", name, names[name])
		}
	}

	all := strings.Join(values(names), "\n")
	for _, secret := range []string{
		"AGENTTOKENSECRET", "GWTOKENSECRET", "AGENTIDSECRET", "deadbeefsecret",
		"gw.secret.example.com", "public.secret.example.com", "192.168.50.1",
		"10.9.8.7", "127.0.0.99", "203.0.113.77",
	} {
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
