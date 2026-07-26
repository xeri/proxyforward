package app

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxyforward/internal/analytics"
	"proxyforward/internal/config"
	"proxyforward/internal/ipc"
	"proxyforward/internal/logging"
)

func sampleConfig() *config.Config {
	cfg := config.Default()
	cfg.Role = config.RoleAgent
	cfg.Agent.AgentID = "AGENTIDSECRET"
	cfg.Agent.GatewayHost = "gw.secret.example.com"
	cfg.Agent.CertFingerprint = "sha256:deadbeefsecret"
	cfg.Agent.EnrollTicket = "tkt_ENROLLTICKETSECRET"
	cfg.Agent.Tunnels = []config.Tunnel{{ID: "t1", Name: "mc", LocalAddr: "10.9.8.7:25565"}}
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
		"AGENTIDSECRET", "gw.secret.example.com",
		"deadbeefsecret", "10.9.8.7",
		"public.secret.example.com", "192.168.50.1", "127.0.0.99",
		"ENROLLTICKETSECRET",
	}
	blob := r.Agent.AgentID + r.Agent.GatewayHost + r.Agent.CertFingerprint +
		r.Agent.EnrollTicket +
		r.Gateway.PublicHost + r.Gateway.BindAddr + r.Metrics.PrometheusAddr
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
	pseudo, err := newPseudonymiser()
	if err != nil {
		t.Fatal(err)
	}
	out := redactStatsJSON(in, pseudo)
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

// TestIPPseudonymIsNotJustAnEncoding is the difference between a pseudonym and a
// reversible encoding of the address. IPv4 is 2^32 addresses: an unsalted digest
// of one is a table lookup, so "ip-3f2a…" told anyone who received a bundle
// exactly which addresses had connected — and bundles are made to be handed to
// strangers. Two bundles must therefore disagree about the same peer.
func TestIPPseudonymIsNotJustAnEncoding(t *testing.T) {
	const peer = "203.0.113.9"
	a, err := newPseudonymiser()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newPseudonymiser()
	if err != nil {
		t.Fatal(err)
	}
	if a.ip(peer) == b.ip(peer) {
		t.Error("the same IP pseudonymises identically across bundles: the mapping is a fixed function of the address, so anyone can invert it by enumerating IPv4")
	}

	// Within one bundle it must still be stable, or a support reader cannot follow
	// a peer through the file, which is the only reason to keep any token at all.
	// Both calls must run; they are deliberately the same expression.
	first, again := a.ip(peer), a.ip(peer)
	if first != again {
		t.Error("the same IP pseudonymises differently within one bundle: repeated appearances are no longer correlatable")
	}
	// And it must still tell peers apart.
	if a.ip(peer) == a.ip("198.51.100.2") {
		t.Error("two different peers share a pseudonym")
	}
	// A hostname and an IP that spell the same string must not read as the same
	// thing, or the "ip-"/"host-" prefix on a label is a claim the label cannot
	// back — and a reader correlating the two would be correlating nothing.
	const same = "203.0.113.9"
	if a.ip(same) == a.host(same) {
		t.Error("an ip and a hostname with the same spelling share a pseudonym: the domains are not separated")
	}
}

// TestDiagnosticsHealthPseudonymisesHostnames: health.txt printed both hostnames
// verbatim under a comment calling them safe to share. A machine name is
// routinely a person's name or an employer's internal naming, the same function
// already refuses to print LAN addresses, and config.redacted.toml masks
// GatewayHost one file over. Bundles are made to be handed to strangers.
func TestDiagnosticsHealthPseudonymisesHostnames(t *testing.T) {
	p, err := newPseudonymiser()
	if err != nil {
		t.Fatal(err)
	}
	out := diagnosticsHealth(ipc.Status{
		Role:          "gateway",
		LocalHostname: "alices-laptop",
		PeerHostname:  "prod-mc-01.corp.internal",
		RTTMillis:     42,
		JitterMillis:  -1,
		PacketLossPct: -1,
		LocalLANIPs:   []string{"192.168.1.5", "10.0.0.9"},
		PeerLANIPs:    []string{"192.168.9.9"},
	}, p)

	for _, secret := range []string{"alices-laptop", "prod-mc-01", "corp.internal", "192.168.1.5", "10.0.0.9", "192.168.9.9"} {
		if strings.Contains(out, secret) {
			t.Errorf("health.txt leaks %q:\n%s", secret, out)
		}
	}
	// It must stay a health report: the metrics are the reason the file exists.
	for _, want := range []string{"host-", "42", "gateway", "local LAN IPs:  2"} {
		if !strings.Contains(out, want) {
			t.Errorf("health.txt lost %q — redaction by shipping nothing is not redaction:\n%s", want, out)
		}
	}
	// An absent hostname is a diagnostic answer, not a name to hide: it must not
	// pseudonymise the empty string into a plausible-looking machine.
	empty := diagnosticsHealth(ipc.Status{Role: "agent", JitterMillis: -1, PacketLossPct: -1}, p)
	if !strings.Contains(empty, "local hostname: —") {
		t.Errorf("an unset hostname should read as unset:\n%s", empty)
	}
}

// TestWriteDiagnosticsNoLeaks drives every channel writeDiagnostics ships, because a
// bundle is only as shareable as its leakiest file. It previously passed ring=nil
// into an empty dir, so the two *unredacted* channels — the GUI ring and the on-disk
// log files — were never populated and the secret sweep below ran over an empty set.
// That blind spot is how a logged pairing code (which embeds a live enrollment
// ticket verbatim) shipped in cleartext while config.redacted.toml sat right beside
// it masking the same credential. The logger here is the real production fan-out, so
// the ring and the rotating file are filled the way they are in a live process.
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
		"pxf://"+cfg.Gateway.PublicHost+":8474/v1/pair/"+cfg.Agent.EnrollTicket+"#"+cfg.Agent.CertFingerprint)
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
	if !strings.Contains(string(onDisk), cfg.Agent.EnrollTicket) {
		t.Fatal("setup bug: the seeded log does not contain the ticket, so this test would pass vacuously")
	}

	// health.txt used to be handed in ready-made — the test passed the literal
	// "health: good\n" — so the sweep below never saw a byte diagnosticsHealth
	// produced, and the hostnames it printed verbatim went unnoticed. Seed it like
	// every other channel: a real Status, carrying the names a real one carries.
	status := &ipc.Status{
		Role:          "agent",
		LocalHostname: "alices-laptop",
		PeerHostname:  "gw.secret.example.com",
		RTTMillis:     42,
		JitterMillis:  -1,
		PacketLossPct: -1,
		LocalLANIPs:   []string{"10.9.8.7"},
	}

	// The analytics summary is the newest channel into the bundle. It is numbers
	// only by construction, which is exactly the claim worth testing: seed it
	// like the rest so the sweep below runs over real bytes it produced.
	summary := &analytics.Summary{
		BytesIn: 4096, BytesOut: 8192, Sessions: 3, UniquePlayers: 2,
		PeakPlayers: 2, PeakPlayersAt: 1_700_000_000_000,
		AvgRttMs: 42, AvgLossPct: -1, LinkUptimePct: 99.5,
		LifetimeBytesIn: 1 << 30, LifetimeBytesOut: 1 << 31, LinkSessions: 7,
	}

	out := filepath.Join(dir, "diag.zip")
	if err := writeDiagnostics(out, cfg, dir, status, summary, ring); err != nil {
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
	for _, want := range []string{"version.txt", "health.txt", "config.redacted.toml", "stats.summary.json", "stats.redacted.json", "logs-recent.txt", "proxyforward.log"} {
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
		"10.9.8.7", "127.0.0.99", "203.0.113.77", "alices-laptop",
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

// failAfterWriter accepts n bytes and then fails every write — the shape of a full
// disk or a dying network share partway through a bundle.
type failAfterWriter struct{ left int }

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.left <= 0 {
		return 0, io.ErrShortWrite
	}
	if len(p) > w.left {
		n := w.left
		w.left = 0
		return n, io.ErrShortWrite
	}
	w.left -= len(p)
	return len(p), nil
}

// TestWriteDiagnosticsReportsArchiveFailure: a bundle whose bytes did not all land
// must be reported as a failure, not as a file. A zip's central directory is written
// by Close, so swallowing that error hands the user a path to something that is not
// an archive at all while the GUI says the export succeeded.
func TestWriteDiagnosticsReportsArchiveFailure(t *testing.T) {
	dir := t.TempDir()
	if err := writeDiagnosticsTo(&failAfterWriter{left: 64}, sampleConfig(), dir, nil, nil, nil); err == nil {
		t.Fatal("writeDiagnosticsTo reported success for a bundle that could not be written")
	}
}

// TestWriteDiagnosticsBundleIsComplete: the happy path still produces an archive
// whose central directory is intact and readable.
func TestWriteDiagnosticsBundleIsComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.zip")
	if err := writeDiagnostics(path, sampleConfig(), dir, nil, nil, nil); err != nil {
		t.Fatalf("writeDiagnostics: %v", err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("bundle is not a readable zip: %v", err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if len(names) == 0 {
		t.Fatal("bundle has no entries")
	}
	if !strings.Contains(strings.Join(names, ","), "version.txt") {
		t.Fatalf("bundle entries = %v, want version.txt among them", names)
	}
}
