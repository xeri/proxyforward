package app

import (
	"archive/zip"
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"proxyforward/internal/analytics"
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
		_ = exec.Command("explorer", dir).Start()
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

// writeDiagnostics builds the support bundle: version, a fully redacted config, a
// health summary, the recent in-memory log lines, the persisted stats (peer IPs
// pseudonymized), and every on-disk log file (rotated + crash + wails). Everything
// that could identify a host, network, or client is masked so the bundle is safe to
// share — logs included, via logScrubber: they are shipped, so redacting only the
// config would leave the same secrets in cleartext one file over.
// status is the live link snapshot for health.txt, or nil when no engine is
// running in this process; stats is the analytics store's summary, or nil when
// it could not answer.
// Leak-tested by TestWriteDiagnosticsNoLeaks, which seeds every channel here.
func writeDiagnostics(path string, cfg *config.Config, configDir string, status *ipc.Status, stats *analytics.Summary, ring *logging.Ring) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	// The file's own Close is the last flush; report it when nothing worse went
	// wrong, so a bundle that did not fully land is never announced as written.
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	return writeDiagnosticsTo(f, cfg, configDir, status, stats, ring)
}

// writeDiagnosticsTo writes the bundle archive to w. Split from writeDiagnostics so
// the finalisation contract below is testable against a writer that fails partway.
func writeDiagnosticsTo(w io.Writer, cfg *config.Config, configDir string, status *ipc.Status, stats *analytics.Summary, ring *logging.Ring) error {
	// The salt every pseudonym in this bundle is minted under, before anything is
	// written. Everything else here is best-effort, but this fails the whole
	// bundle: the alternative to a salt is not "a weaker salt", it is shipping the
	// addresses, and a bundle exists to be handed to strangers.
	pseudo, err := newPseudonymiser()
	if err != nil {
		return err
	}

	zw := zip.NewWriter(w)

	// version.txt
	if w, err := zw.Create("version.txt"); err == nil {
		fmt.Fprintln(w, version.String())
	}

	// health.txt — live link quality snapshot. Rendered here rather than handed in
	// ready-made, so the one function that owns this bundle's redaction owns
	// everything the bundle says.
	if status != nil {
		if w, err := zw.Create("health.txt"); err == nil {
			_, _ = io.WriteString(w, diagnosticsHealth(*status, pseudo))
		}
	}

	// config.redacted.toml — secrets, hosts, IPs, and identities masked;
	// structure intact.
	redacted := redactConfig(cfg)
	if data, err := toml.Marshal(&redacted); err == nil {
		if w, err := zw.Create("config.redacted.toml"); err == nil {
			_, _ = w.Write(data)
		}
	}

	// stats.summary.json — the lifetime and all-time figures from the analytics
	// store: counts, byte totals, peaks and their timestamps. analytics.Summary
	// is numbers only — no address, name or id reaches it — so there is nothing
	// here to pseudonymise. Omitted entirely when the store could not answer
	// (detached, old daemon, failed open): a page of zeros would read as "this
	// machine has never moved a byte", which is the lie the sentinels exist to
	// prevent.
	if stats != nil {
		if data, err := json.MarshalIndent(stats, "", "  "); err == nil {
			if w, err := zw.Create("stats.summary.json"); err == nil {
				_, _ = w.Write(data)
			}
		}
	}

	// stats.redacted.json — the pre-SQLite stats file, with peer IPs
	// pseudonymised. Only present before the first engine start after a legacy
	// restore: ImportLegacyStats renames stats.json away once it has imported it
	// (analytics/importjson.go), and everything it held is in the summary above
	// from then on.
	if raw, err := os.ReadFile(filepath.Join(configDir, "stats.json")); err == nil {
		if w, err := zw.Create("stats.redacted.json"); err == nil {
			_, _ = w.Write(redactStatsJSON(raw, pseudo))
		}
	}

	// Logs ship with the config's own secrets masked. Without this the redaction
	// above is theatre: config.redacted.toml hides Agent.EnrollTicket while a log
	// line three files over spells it out.
	scrub := newLogScrubber(cfg)

	// logs-recent.txt — the GUI ring (what the user was just looking at).
	if ring != nil {
		if w, err := zw.Create("logs-recent.txt"); err == nil {
			for _, e := range ring.EntriesSince(0) {
				line := fmt.Sprintf("%s %-5s %s %s\n", time.UnixMilli(e.TimeMs).Format(time.RFC3339), e.Level, e.Msg, e.Attrs)
				_, _ = io.WriteString(w, scrub.clean(line))
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
		copyScrubbedIntoZip(zw, filepath.Base(p), p, scrub)
	}
	// Close writes the central directory — without it the bytes are not an archive
	// at all. Every entry above is best-effort (a missing log file just isn't in the
	// bundle), but this one error is the bundle's success signal.
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finish diagnostics archive: %w", err)
	}
	return nil
}

// logScrubber replaces the exact secret values this bundle already knows — the ones
// redactConfig masks in config.redacted.toml — wherever they appear in shipped log
// text. It is the reason the bundle can claim to be shareable at all: redacting the
// config is worth nothing if the same token is sitting in a log line two files over,
// which is exactly how the pairing code used to escape (gateway.go RunStarted).
//
// Exact-value replacement, never pattern-guessing: every entry is a literal read out
// of the live config, so this cannot pass off a guess as a guarantee. It is a net,
// not a licence — secrets still must not be logged in the first place, because this
// only knows the values config holds. Values shorter than minScrubLen are skipped:
// they carry little entropy and would smear over unrelated log text.
//
// The one exception is the pairing code, and it is not a guess: a pxf:// link is a
// *syntactic* form that is always a credential, whoever minted it. It needs its own
// rule because a v2 code packs the ticket and the pin into base32 rather than
// spelling them out — so the exact values above no longer appear in it, and a
// logged code would sail straight past a scrubber that only knows this machine's
// config. That is the exact escape route this scrubber exists to close
// (gateway.go RunStarted keeps codes off slog in the first place).
type logScrubber struct{ pairs []string } // old1, new1, old2, new2 … for strings.NewReplacer

// pairingCodeRe matches a pxf:// link of any version, up to the first character
// that cannot appear in one. Deliberately greedy about the tail: half a redacted
// code is still a code someone can finish guessing.
var pairingCodeRe = regexp.MustCompile(`pxf://[^\s"'` + "`" + `,)\]}<>]+`)

// minScrubLen is the shortest value worth exact-matching in log text. Below this a
// "secret" is more likely to collide with ordinary words than to be the secret.
const minScrubLen = 4

func newLogScrubber(cfg *config.Config) *logScrubber {
	const secret = "[redacted]"
	const host = "[redacted-host]"
	s := &logScrubber{}
	add := func(val, with string) {
		if len(val) >= minScrubLen {
			s.pairs = append(s.pairs, val, with)
		}
	}
	// Mirrors redactConfig field for field: whatever is a secret in the config is a
	// secret in the logs. If a field is added there, add it here.
	add(cfg.Agent.AgentID, secret)
	add(cfg.Agent.EnrollTicket, secret)
	add(cfg.Agent.CertFingerprint, secret)
	add(cfg.Gateway.PublicHost, host)
	add(cfg.Gateway.BindAddr, host)
	add(cfg.Agent.GatewayHost, host)
	add(cfg.Metrics.PrometheusAddr, host)
	for _, t := range cfg.Agent.Tunnels {
		add(t.LocalAddr, host)
	}
	return s
}

// clean returns line with every known secret replaced, and any pairing code
// removed whole.
func (s *logScrubber) clean(line string) string {
	line = pairingCodeRe.ReplaceAllString(line, "[redacted-pairing-code]")
	if len(s.pairs) == 0 {
		return line
	}
	return strings.NewReplacer(s.pairs...).Replace(line)
}

// copyScrubbedIntoZip streams src into the archive under nameInZip with every known
// secret masked. Line-oriented, so memory stays bounded on a rotated log and a secret
// (which never spans a newline) can't slip through a chunk boundary. Missing files
// are skipped without error — a diagnostics bundle is best-effort.
func copyScrubbedIntoZip(zw *zip.Writer, nameInZip, src string, s *logScrubber) {
	lf, err := os.Open(src)
	if err != nil {
		return
	}
	defer func() { _ = lf.Close() }()
	w, err := zw.Create(nameInZip)
	if err != nil {
		return
	}
	br := bufio.NewReader(lf)
	for {
		// ReadString, not bufio.Scanner: a single pathological log line must not hit
		// Scanner's 64 KiB token cap and silently truncate the rest of the file.
		line, err := br.ReadString('\n')
		if line != "" {
			_, _ = io.WriteString(w, s.clean(line))
		}
		if err != nil {
			return
		}
	}
}

// redactConfig returns a copy of cfg with every secret, host, IP, and identity
// masked. Structure is preserved so the shape of the config stays diagnosable.
func redactConfig(cfg *config.Config) config.Config {
	const secret = "[redacted]"
	const host = "[redacted-host]"
	r := *cfg
	if r.Gateway.PublicHost != "" {
		r.Gateway.PublicHost = host
	}
	if r.Gateway.BindAddr != "" {
		r.Gateway.BindAddr = host
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
func redactStatsJSON(raw []byte, pseudo *pseudonymiser) []byte {
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
		if b, err := json.Marshal(pseudo.ip(ip)); err == nil {
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

// pseudonymiser maps the identifiers that would otherwise name a machine — peer
// IPs, hostnames — to labels that are stable within one bundle and meaningless
// outside it.
//
// The salt is the whole point, and it was missing. A bare sha256 of an IP is not
// a pseudonym: IPv4 is 2^32 addresses, so every possible digest fits in a table
// anyone can build in minutes and query instantly — "ip-3f2a…" was a reversible
// encoding of the address wearing a hash's clothes, and the bundles it protects
// are the ones users post in issues and hand to strangers for support. IPv6 is
// only nominally better, since the addresses that matter come from a handful of
// guessable prefixes. Hostnames are worse still: a digest of "DESKTOP-7A1B2C" or
// "alices-laptop" is a dictionary away from the name it hides.
//
// A fresh random salt per bundle keeps exactly the property the redaction is for
// — the same peer reads as the same pseudonym throughout one bundle, so a support
// reader can still follow it — and drops the one nobody asked for: correlating a
// peer across bundles, or back to an address. The salt is never written anywhere;
// it dies with the bundle that used it.
type pseudonymiser struct{ salt []byte }

func newPseudonymiser() (*pseudonymiser, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("diagnostics: no randomness for the redaction salt: %w", err)
	}
	return &pseudonymiser{salt: salt}, nil
}

// label pseudonymises one value within a named domain. HMAC rather than
// sha256(salt || v): it is the construction actually specified for keyed hashing,
// and it costs nothing here. The domain is mixed in so a host and an IP that
// happen to spell the same string do not read as the same thing — the prefix on
// the label is then a fact about it rather than decoration.
func (p *pseudonymiser) label(kind, v string) string {
	mac := hmac.New(sha256.New, p.salt)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(v))
	return fmt.Sprintf("%s-%x", kind, mac.Sum(nil)[:6])
}

func (p *pseudonymiser) ip(v string) string   { return p.label("ip", v) }
func (p *pseudonymiser) host(v string) string { return p.label("host", v) }

// diagnosticsHealth renders the live link snapshot for health.txt: quality
// metrics as measured, hostnames pseudonymised, and only the *count* of LAN IPs.
//
// The hostnames used to be printed verbatim, under a comment calling them safe to
// share. They are the opposite: a machine name is routinely a person's name
// ("alices-laptop") or an employer's internal naming, this file's own next line
// already refuses to print LAN addresses, and config.redacted.toml two files over
// masks GatewayHost for precisely this reason. A bundle is made to be handed to
// strangers, so health.txt was the one file undoing the rest.
func diagnosticsHealth(s ipc.Status, p *pseudonymiser) string {
	msOrNA := func(v float64) string {
		if v < 0 {
			return "n/a"
		}
		return fmt.Sprintf("%.1f ms", v)
	}
	// A hostname is either absent — which is itself the diagnostic answer — or it
	// is a name, and a name gets a pseudonym.
	host := func(v string) string {
		if v == "" {
			return "—"
		}
		return p.host(v)
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
	fmt.Fprintf(&b, "local hostname: %s\n", host(s.LocalHostname))
	fmt.Fprintf(&b, "peer hostname:  %s\n", host(s.PeerHostname))
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
