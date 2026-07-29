// Package config defines proxyforward's on-disk configuration: schema,
// defaults, validation, and atomic load/save.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	toml "github.com/pelletier/go-toml/v2"
	"proxyforward/internal/secureio"
)

type Role string

const (
	RoleUnset   Role = ""
	RoleAgent   Role = "agent"
	RoleGateway Role = "gateway"
)

// Transport selects the agent's data plane. TransportMux multiplexes all
// streams over one TCP control connection (one player's loss stalls all).
// TransportPerConn opens a fresh outbound TCP connection per proxied client
// (kills cross-player head-of-line blocking at the cost of more connections and
// NAT entries). TransportQUIC keeps one connection like mux but flow-controls
// each player's stream independently, so it kills HOL without the extra
// connections — and survives the agent's IP changing (passive QUIC migration).
// TransportAuto prefers QUIC and falls back to per-conn then mux when the QUIC
// (UDP) handshake fails — e.g. an ISP blocks UDP — re-probing on reconnect and
// on network changes; the explicit values force one transport.
const (
	TransportMux     = "mux"
	TransportPerConn = "per-conn"
	TransportQUIC    = "quic"
	TransportAuto    = "auto"
)

const (
	TunnelTCP = "tcp"
	// TunnelUDP relays datagrams for Bedrock/Geyser (RakNet) and voice-chat
	// mods. TCP and UDP hold a port number independently, so a udp tunnel may
	// reuse the number a java tunnel already has.
	TunnelUDP = "udp"
)

// Bandwidth-cap scope values (options.bandwidth_limit_scope), selecting what a
// tunnel's cap applies to. Empty normalizes to combined. Enforcement semantics
// live in internal/bwcap.
const (
	// BandwidthScopeCombined shares one bucket across both directions and all
	// of the tunnel's connections.
	BandwidthScopeCombined = "combined"
	// BandwidthScopePerDirection caps inbound and outbound independently, each
	// summed across the tunnel's connections.
	BandwidthScopePerDirection = "per-direction"
	// BandwidthScopePerConnection gives every connection its own bucket.
	BandwidthScopePerConnection = "per-connection"
)

// Health probe kinds for options.health_probe.
const (
	// HealthProbeTCP dials the local address and closes: a socket that accepts
	// is called up. The default, and all a non-Minecraft backend can support.
	HealthProbeTCP = "tcp"
	// HealthProbeMCStatus performs a Server List Ping, so "accepts connections"
	// and "is serving Minecraft" stop being the same answer. TCP tunnels only.
	HealthProbeMCStatus = "mc-status"
)

type Config struct {
	// SchemaVersion is the shape of this document, stamped by Save. A file
	// written before this field existed carries no key and reads 0; Load treats
	// that as the unversioned original and climbs it, and refuses anything
	// numbered above what this build understands.
	SchemaVersion int             `toml:"schema_version"`
	Role          Role            `toml:"role"`
	Agent         AgentConfig     `toml:"agent"`
	Gateway       GatewayConfig   `toml:"gateway"`
	Engine        EngineConfig    `toml:"engine"`
	Metrics       MetricsConfig   `toml:"metrics"`
	Logging       LoggingConfig   `toml:"logging"`
	UI            UIConfig        `toml:"ui"`
	Analytics     AnalyticsConfig `toml:"analytics"`
}

type AgentConfig struct {
	// AgentID is this agent's identity label; reconnects with the same ID
	// supersede the previous session. The gateway derives the authoritative
	// agt_ id from the agent's Ed25519 key at enrollment.
	AgentID         string   `toml:"agent_id"`
	GatewayHost     string   `toml:"gateway_host"`
	GatewayPort     int      `toml:"gateway_port"`
	CertFingerprint string   `toml:"cert_fingerprint"` // "sha256:<hex>" of the gateway's pinned cert
	Transport       string   `toml:"transport"`        // "auto" | "quic" | "per-conn" | "mux"
	Tunnels         []Tunnel `toml:"tunnels"`
	// EnrollTicket is a one-time pairing ticket carried on the first connect;
	// once the gateway confirms enrollment (hello_ok's assigned identity) the
	// engine persists the assigned agt_ id into AgentID and clears this field
	// (agent.go finishEnrollment). Empty in steady state — the Ed25519 identity
	// authenticates thereafter, and the config holds no live credential.
	EnrollTicket string `toml:"enroll_ticket"`
	// Paused is the agent-wide pause bit: every tunnel keeps its port but
	// turns players away until resumed. Local-only state, not config — it
	// rides the pause message, never config sync or consent.
	Paused bool `toml:"paused"`
	// PoolSize is how many data connections to keep parked at the gateway ahead
	// of any player, so a join costs a header write instead of a TCP and TLS
	// handshake. Meaningful only under the per-conn transport (mux and QUIC open
	// a stream for free), and only a request: the gateway answers with its own
	// grant, which can only be smaller. 0 turns pooling off; the default is
	// PoolSizeDefault, kept low because a parked connection costs a descriptor on
	// both machines and a NAT mapping between them whether or not anyone joins.
	PoolSize int `toml:"pool_size"`
}

type Tunnel struct {
	ID         string `toml:"id"`
	Name       string `toml:"name"`
	Type       string `toml:"type"` // "tcp" | "udp"
	LocalAddr  string `toml:"local_addr"`
	PublicPort int    `toml:"public_port"`
	Enabled    bool   `toml:"enabled"`
	// Paused is this tunnel's own pause bit: still enabled, still registered,
	// still holding its public port — but players are turned away until
	// resumed. Local-only like LocalAddr: it never serializes into TunnelSpec,
	// so it cannot reach HashTunnels or any config/consent path, and flipping
	// it never rebinds the listener.
	Paused  bool          `toml:"paused"`
	Options TunnelOptions `toml:"options"`
}

type TunnelOptions struct {
	// MinecraftAware enables passive username sniffing on this tunnel: the
	// gateway reads the login handshake in passing to attribute connections to
	// players. It does not poll the server — that is HealthProbe below.
	MinecraftAware bool `toml:"minecraft_aware"`
	// ProxyProtocolV2 prepends a PP2 header when dialing the local server so
	// it sees real client IPs. Mutually exclusive with BungeeCord/Velocity
	// IP-forwarding on the same server.
	ProxyProtocolV2 bool `toml:"proxy_protocol_v2"`
	// OfflineMOTD, when non-empty, makes the gateway answer status pings with
	// this message while the agent or local server is down.
	OfflineMOTD string `toml:"offline_motd"`
	// BandwidthLimitMbps caps this tunnel's throughput; 0 = unlimited.
	BandwidthLimitMbps int `toml:"bandwidth_limit_mbps"`
	// BandwidthLimitScope selects what the cap applies to: combined (default),
	// per-direction, or per-connection. Ignored when the cap is 0.
	BandwidthLimitScope string `toml:"bandwidth_limit_scope"`
	// Health-check tuning for this tunnel's local backend. These are
	// deliberately agent-local and never cross the wire (the LocalAddr/PP2
	// precedent): the probe targets the agent's LAN, which is the one thing the
	// gateway cannot see. Every field zero = the shipped defaults, so an upgrade
	// changes no behavior.
	//
	// HealthProbe selects how the backend is checked: "" or HealthProbeTCP is a
	// bare dial (a socket that accepts is called up); HealthProbeMCStatus asks
	// for a Server List Ping, so a process that accepts but will never serve
	// reads down — TCP tunnels only, the status protocol has no UDP form.
	HealthProbe string `toml:"health_probe"`
	// HealthIntervalSec is how often to probe, HealthTimeoutSec how long one
	// probe may take, and HealthMaxFailed how many CONSECUTIVE failures flip the
	// tunnel down (1 = report the first failure; higher debounces a backend that
	// stutters under load). One success always flips it back up and clears the
	// counter.
	HealthIntervalSec int `toml:"health_interval_sec"`
	HealthTimeoutSec  int `toml:"health_timeout_sec"`
	HealthMaxFailed   int `toml:"health_max_failed"`
}

type GatewayConfig struct {
	BindAddr    string `toml:"bind_addr"`
	ControlPort int    `toml:"control_port"`
	// PublicHost is the address players and agents reach this gateway at
	// (ideally a stable DNS name); it is embedded in pairing codes. Optional —
	// codes fall back to a placeholder the user replaces.
	PublicHost string `toml:"public_host"`
	// PortAllowlist restricts which public ports agents may register; empty
	// allows any port not otherwise in use.
	PortAllowlist []int `toml:"port_allowlist"`
	// Abuse limits enforced on public listeners and the control port.
	MaxConnsGlobal     int `toml:"max_conns_global"`
	MaxConnsPerIP      int `toml:"max_conns_per_ip"`
	AuthAttemptsPerMin int `toml:"auth_attempts_per_min"`
	// MaxTunnelsPerAgent bounds how many public listeners one agent may hold at
	// once. Unlike the caps above it limits an *authenticated* peer: listeners are
	// the only gateway resource an agent allocates by asking, and a scope of ports
	// is the only thing that bounded them — which the default (empty = any) does
	// not. Enforced at the bind, in gateway/actor.go bindLocked.
	MaxTunnelsPerAgent int `toml:"max_tunnels_per_agent"`
	// QUICEnabled binds a UDP QUIC control listener alongside the TCP one, on the
	// same port, so agents configured transport="quic" (or "auto") can dial it.
	// Default on; a missing key inherits that (Load overlays onto Default). Set
	// false to serve TCP transports only.
	QUICEnabled bool `toml:"quic_enabled"`
}

// EngineConfig holds the tunnel run's persisted lifecycle. It is a section of
// its own because it is not the agent's or the gateway's — it is the run both
// roles share, and it survives restart and reboot so a deliberate stop is not
// silently undone by the next launch.
type EngineConfig struct {
	// Stopped means the operator turned the tunnel off. The daemon still
	// starts and serves the pipe; only the run stays down until StartEngine.
	// Default false: a fresh install is on.
	Stopped bool `toml:"stopped"`
}

type MetricsConfig struct {
	PrometheusEnabled bool   `toml:"prometheus_enabled"`
	PrometheusAddr    string `toml:"prometheus_addr"`
}

type LoggingConfig struct {
	Level       string `toml:"level"` // debug|info|warn|error
	FileEnabled bool   `toml:"file_enabled"`
}

type UIConfig struct {
	Theme          string `toml:"theme"` // dark|light|system
	MinimizeToTray bool   `toml:"minimize_to_tray"`
	Autostart      bool   `toml:"autostart"`
	// TrayNoticeShown is set after the user has seen the one-time "closing
	// minimizes to the tray" notice; it is never shown twice.
	TrayNoticeShown bool `toml:"tray_notice_shown"`
}

// AnalyticsConfig governs the local analytics store (SQLite next to the
// config) and its external enrichment sources.
type AnalyticsConfig struct {
	// RetentionDays bounds how long connection/session history is kept.
	RetentionDays int `toml:"retention_days"`
	// MojangLookups permits resolving observed player names to Mojang UUIDs
	// (small JSON API calls from the engine process).
	MojangLookups bool `toml:"mojang_lookups"`
	// GeoIPCityPath / GeoIPASNPath point at MaxMind GeoLite2 .mmdb files;
	// empty disables geo enrichment. Whether the file exists is a runtime
	// status, not a config error, so a moved file never blocks startup.
	GeoIPCityPath string `toml:"geoip_city_path"`
	GeoIPASNPath  string `toml:"geoip_asn_path"`
}

const (
	DefaultControlPort = 8474
	DefaultPublicPort  = 25565
)

// CurrentSchemaVersion is the document shape this build writes and understands.
// Shape 1 is the config as it stands today. A file written before the version
// field existed reads 0: the same shape without the stamp, which is why climbing
// 0 to 1 needs no rung on the ladder below.
//
// Raise it only in the same commit as the migration that earns it. The number is
// a promise that Load can turn any lower document into this one.
const CurrentSchemaVersion = 1

// configMigrations is the config's schema ladder: the entry keyed v upgrades a
// document at version v to v+1, in memory, before it is validated. Append only,
// never edit an applied entry. That is the rule internal/analytics/schema.go
// lives by, for the same reason: a machine that already climbed a rung will
// never climb it again, so an edit rewrites history only for the machines that
// have not.
//
// It is empty, and nothing is missing. Every field added so far has been
// additive, and Load decodes over Default(), so a document written by an older
// build already reads as intended (TestLoadKeepsDefaultsForAbsentKeys). A rung
// is needed only when a field changes meaning, or when a default already written
// to disk must change. The first one lands here, with CurrentSchemaVersion
// raised in the same commit.
var configMigrations = map[int]func(*Config){}

// migrate climbs the ladder in memory and stamps the result. A version with no
// entry is a shape that did not change (0 to 1 is exactly that), so it is a
// no-op rather than an error.
//
// Nothing is written here on purpose: the migrated fields and the version that
// describes them reach disk together, in the caller's next Save, whose
// temp+rename makes that one atomic unit. A stamp landing without the change it
// claims would be the one failure a ladder exists to prevent.
func (c *Config) migrate() {
	for v := c.SchemaVersion; v < CurrentSchemaVersion; v++ {
		if up := configMigrations[v]; up != nil {
			up(c)
		}
	}
	c.SchemaVersion = CurrentSchemaVersion
}

// Default returns a config with every field at its documented default.
// Role is unset; the wizard or CLI decides it.
func Default() *Config {
	return &Config{
		SchemaVersion: CurrentSchemaVersion,
		Agent: AgentConfig{
			GatewayPort: DefaultControlPort,
			Transport:   TransportAuto,
			PoolSize:    PoolSizeDefault,
		},
		Gateway: GatewayConfig{
			BindAddr:           "0.0.0.0",
			ControlPort:        DefaultControlPort,
			MaxConnsGlobal:     4096,
			MaxConnsPerIP:      32,
			AuthAttemptsPerMin: 10,
			MaxTunnelsPerAgent: 32,
			QUICEnabled:        true,
		},
		Metrics: MetricsConfig{
			PrometheusAddr: "127.0.0.1:9464",
		},
		Logging: LoggingConfig{
			Level:       "info",
			FileEnabled: true,
		},
		UI: UIConfig{
			Theme:          "dark",
			MinimizeToTray: true,
		},
		Analytics: AnalyticsConfig{
			RetentionDays: 180,
			MojangLookups: true,
		},
	}
}

// NewID returns a 16-byte random hex identifier (agent IDs, tunnel IDs).
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b[:])
}

// Load reads the config at path. A missing file returns Default() and no
// error so first runs work without setup; any other failure (bad TOML, a
// schema version this build does not understand, failed validation) is
// returned to the caller rather than silently replaced with defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// The version gate runs before Validate, because a document from a future
	// build is likely to fail today's validation for reasons that are only
	// symptoms (an enum value this build has never heard of), and "your
	// proxyforward is older than this file" is the one message the user can act
	// on. Refusing also protects the file: an old build that loaded it would
	// drop every key it does not know on the next Save.
	if cfg.SchemaVersion > CurrentSchemaVersion {
		return nil, fmt.Errorf("config %s was written by a newer proxyforward (schema version %d, this build understands %d); install that version, or move the file aside to start fresh", path, cfg.SchemaVersion, CurrentSchemaVersion)
	}
	cfg.migrate()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// Save validates and atomically writes the config: marshal to a temp file in
// the target directory, then rename over the destination. Before the first such
// replacement in this process it copies aside a config it could not load, so a
// write is never the thing that makes an unreadable document unrecoverable
// (quarantineUnreadable).
func (c *Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("refusing to save invalid config: %w", err)
	}
	// Stamp the shape being written, so the version on disk and the fields
	// beside it are always the same write: a document can never claim a version
	// its contents do not match.
	c.SchemaVersion = CurrentSchemaVersion
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	dir := filepath.Dir(path)
	// The config can hold a live enrollment ticket, and the temp+rename below
	// inherits the directory's ACL rather than any mode of its own.
	if err := secureio.MkdirAll(dir); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	quarantineUnreadable(path)
	tmp, err := os.CreateTemp(dir, ".config-*.toml.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// quarantinedOnce bounds the quarantine below to one attempt per process. Save
// runs on every narrow write — a theme toggle, a persisted pause bit, an
// enrollment — so re-reading and re-parsing the file on each call would buy one
// answer we already have at a cost paid forever.
var quarantinedOnce sync.Once

// quarantineUnreadable copies an existing config aside before the first Save of
// this process replaces it, but only when the file cannot be loaded.
//
// The case it exists for: a config that fails to parse or validate takes the GUI
// down its defaults path, which presents the first-run wizard; finishing that
// wizard calls Save, and the atomic rename below then destroys a document the
// user never chose to discard — the gateway address, every tunnel, the scope. The
// blocking config-error surface is the real fix; this is the floor under it, so
// even a version of that surface with a bug cannot make the file unrecoverable.
//
// Deliberately silent and best-effort. It runs on the way to a Save the caller
// asked for, and a failure to preserve the old file is not a reason to refuse the
// new one — the alternative is a GUI that cannot save because of a file the user
// already cannot use. A readable config is left alone: the common path costs one
// read and one parse, once.
func quarantineUnreadable(path string) {
	quarantinedOnce.Do(func() { quarantineUnreadableNow(path) })
}

// quarantineUnreadableNow is the policy without the latch, so a test can run it
// more than once per binary. Reports whether it wrote a copy.
func quarantineUnreadableNow(path string) bool {
	if _, err := Load(path); err == nil {
		return false // loads fine: nothing to preserve, and nothing was lost
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false // missing (a genuine first run) or unreadable; nothing to copy
	}
	// A fixed suffix, not a timestamp: the point is that one copy survives, and a
	// stamp per launch would grow a directory nobody prunes. The first unreadable
	// config wins — a later one is the wreckage of an already-quarantined original,
	// and overwriting the good copy with it is the one outcome worth preventing.
	aside := path + ".corrupt"
	if _, err := os.Stat(aside); err == nil {
		return false
	}
	return os.WriteFile(aside, data, 0o600) == nil
}

// Clone returns a deep copy: every slice gets its own backing array, so the two
// configs share no memory at all.
//
// It exists because a *Config is a document with exactly one owner, and handing
// the same pointer to two of them is how a GUI and a running engine ended up
// writing the same fields under two different locks. `*dst = *src` is not enough
// here — Tunnels is mutated element-wise (a pause bit) as well as replaced, so a
// shallow copy leaves both owners writing one backing array.
//
// Anything added to this struct that is not a scalar or a string must be copied
// here too; a missed slice is a shared write that nothing else will catch.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	out := *c
	if c.Agent.Tunnels != nil {
		out.Agent.Tunnels = append([]Tunnel(nil), c.Agent.Tunnels...)
	}
	if c.Gateway.PortAllowlist != nil {
		out.Gateway.PortAllowlist = append([]int(nil), c.Gateway.PortAllowlist...)
	}
	return &out
}

// Validate checks structural invariants. It validates both role sections only
// as far as they are exercised by the configured role, so a fresh config with
// an unset role passes.
func (c *Config) Validate() error {
	var errs []error

	switch c.Role {
	case RoleUnset, RoleAgent, RoleGateway:
	default:
		errs = append(errs, fmt.Errorf("role: must be %q or %q, got %q", RoleAgent, RoleGateway, c.Role))
	}

	if c.Role == RoleAgent {
		errs = append(errs, c.validateAgent()...)
	}
	if c.Role == RoleGateway {
		errs = append(errs, c.validateGateway()...)
	}

	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("logging.level: unknown level %q", c.Logging.Level))
	}
	switch c.UI.Theme {
	case "dark", "light", "system":
	default:
		errs = append(errs, fmt.Errorf("ui.theme: must be dark, light or system, got %q", c.UI.Theme))
	}
	if c.Metrics.PrometheusEnabled {
		if _, _, err := net.SplitHostPort(c.Metrics.PrometheusAddr); err != nil {
			errs = append(errs, fmt.Errorf("metrics.prometheus_addr: %w", err))
		}
	}
	if r := c.Analytics.RetentionDays; r < 1 || r > 3650 {
		errs = append(errs, fmt.Errorf("analytics.retention_days: must be 1-3650, got %d", r))
	}
	for _, g := range []struct{ name, path string }{
		{"analytics.geoip_city_path", c.Analytics.GeoIPCityPath},
		{"analytics.geoip_asn_path", c.Analytics.GeoIPASNPath},
	} {
		if g.path != "" && !strings.EqualFold(filepath.Ext(g.path), ".mmdb") {
			errs = append(errs, fmt.Errorf("%s: expected a .mmdb file, got %q", g.name, g.path))
		}
	}
	return errors.Join(errs...)
}

func (c *Config) validateAgent() []error {
	var errs []error
	a := &c.Agent
	if a.GatewayHost == "" {
		errs = append(errs, errors.New("agent.gateway_host: required (pair with a gateway first)"))
	}
	if err := validPort(a.GatewayPort); err != nil {
		errs = append(errs, fmt.Errorf("agent.gateway_port: %w", err))
	}
	if a.CertFingerprint == "" {
		errs = append(errs, errors.New("agent.cert_fingerprint: required (pair with a gateway first)"))
	} else if !strings.HasPrefix(a.CertFingerprint, "sha256:") {
		errs = append(errs, fmt.Errorf("agent.cert_fingerprint: must start with \"sha256:\", got %q", a.CertFingerprint))
	}
	switch a.Transport {
	case TransportMux, TransportPerConn, TransportQUIC, TransportAuto:
	default:
		errs = append(errs, fmt.Errorf("agent.transport: must be one of %q, %q, %q, %q, got %q", TransportAuto, TransportQUIC, TransportPerConn, TransportMux, a.Transport))
	}
	if a.PoolSize < 0 || a.PoolSize > MaxPoolSize {
		errs = append(errs, fmt.Errorf("agent.pool_size: must be 0 (off) to %d, got %d", MaxPoolSize, a.PoolSize))
	}
	seenID := map[string]bool{}
	// Ports are per protocol: a TCP and a UDP tunnel may share a number.
	seenPort := map[string]bool{}
	for i := range a.Tunnels {
		t := &a.Tunnels[i]
		where := fmt.Sprintf("agent.tunnels[%d] (%s)", i, t.Name)
		if t.ID == "" {
			errs = append(errs, fmt.Errorf("%s: id required", where))
		} else if seenID[t.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate tunnel id %q", where, t.ID))
		}
		seenID[t.ID] = true
		if t.Type != TunnelTCP && t.Type != TunnelUDP {
			errs = append(errs, fmt.Errorf("%s: type: must be %q or %q, got %q", where, TunnelTCP, TunnelUDP, t.Type))
		}
		if _, portStr, err := net.SplitHostPort(t.LocalAddr); err != nil {
			errs = append(errs, fmt.Errorf("%s: local_addr: %w", where, err))
		} else if p, err := strconv.Atoi(portStr); err != nil || validPort(p) != nil {
			errs = append(errs, fmt.Errorf("%s: local_addr: invalid port %q", where, portStr))
		}
		portKey := t.Type + "/" + strconv.Itoa(t.PublicPort)
		if err := validPort(t.PublicPort); err != nil {
			errs = append(errs, fmt.Errorf("%s: public_port: %w", where, err))
		} else if t.Enabled && seenPort[portKey] {
			errs = append(errs, fmt.Errorf("%s: public_port %d already used by another enabled %s tunnel", where, t.PublicPort, t.Type))
		}
		if t.Enabled {
			seenPort[portKey] = true
		}
		if t.Type == TunnelUDP {
			if t.Options.MinecraftAware {
				errs = append(errs, fmt.Errorf("%s: options.minecraft_aware: not supported on udp tunnels (the Java status protocol is TCP)", where))
			}
			if t.Options.ProxyProtocolV2 {
				errs = append(errs, fmt.Errorf("%s: options.proxy_protocol_v2: not supported on udp tunnels (PP2 is a stream preamble)", where))
			}
			if t.Options.OfflineMOTD != "" {
				errs = append(errs, fmt.Errorf("%s: options.offline_motd: not supported on udp tunnels (status pings are TCP)", where))
			}
		}
		if t.Options.BandwidthLimitMbps < 0 {
			errs = append(errs, fmt.Errorf("%s: bandwidth_limit_mbps: must be >= 0", where))
		} else if t.Options.BandwidthLimitMbps > 0 && !validBandwidthScope(t.Options.BandwidthLimitScope) {
			// Scope is meaningless without a cap, so it is only checked when one
			// is set; the gateway normalizes whatever arrives on the wire.
			errs = append(errs, fmt.Errorf("%s: bandwidth_limit_scope: must be %q, %q, or %q (empty = %q), got %q",
				where, BandwidthScopeCombined, BandwidthScopePerDirection, BandwidthScopePerConnection, BandwidthScopeCombined, t.Options.BandwidthLimitScope))
		}
		errs = append(errs, validateHealthOptions(where, t)...)
	}
	return errs
}

// PoolSizeDefault is how many connections a per-conn agent parks at the gateway
// unless told otherwise, and MaxPoolSize the most an operator may ask for. Two
// covers the ordinary case — a player joining, and the next one arriving while
// the first is being refilled — without holding open sockets nobody is using.
// The ceiling is this side's sanity bound only: the gateway answers every
// request with its own grant, and that grant is what binds.
const (
	PoolSizeDefault = 2
	MaxPoolSize     = 8
)

// maxHealthIntervalSec / maxHealthTimeoutSec bound the tuning an operator can
// ask for. The ceilings are not policy so much as a guard against a typo turning
// health reporting off: a probe every few hours would leave a dead backend
// looking up for that long, and the gateway routes players on that answer.
const (
	maxHealthIntervalSec = 300
	maxHealthTimeoutSec  = 60
	maxHealthMaxFailed   = 10
)

// validateHealthOptions checks one tunnel's health-probe tuning. Zero means "use
// the default" for every field, so an untouched config never errors here.
func validateHealthOptions(where string, t *Tunnel) []error {
	var errs []error
	o := t.Options
	switch o.HealthProbe {
	case "", HealthProbeTCP:
	case HealthProbeMCStatus:
		if t.Type == TunnelUDP {
			errs = append(errs, fmt.Errorf("%s: options.health_probe: %q needs tcp (the Java status protocol has no udp form)", where, HealthProbeMCStatus))
		}
	default:
		errs = append(errs, fmt.Errorf("%s: options.health_probe: must be %q or %q (empty = %q), got %q",
			where, HealthProbeTCP, HealthProbeMCStatus, HealthProbeTCP, o.HealthProbe))
	}
	if o.HealthIntervalSec < 0 || o.HealthIntervalSec > maxHealthIntervalSec {
		errs = append(errs, fmt.Errorf("%s: options.health_interval_sec: must be 0 (default) to %d, got %d", where, maxHealthIntervalSec, o.HealthIntervalSec))
	}
	if o.HealthTimeoutSec < 0 || o.HealthTimeoutSec > maxHealthTimeoutSec {
		errs = append(errs, fmt.Errorf("%s: options.health_timeout_sec: must be 0 (default) to %d, got %d", where, maxHealthTimeoutSec, o.HealthTimeoutSec))
	}
	// A timeout longer than the interval would queue probes behind each other and
	// make the interval a lie; the checker skips a tunnel whose probe is running.
	if o.HealthIntervalSec > 0 && o.HealthTimeoutSec > o.HealthIntervalSec {
		errs = append(errs, fmt.Errorf("%s: options.health_timeout_sec (%d) must not exceed health_interval_sec (%d)", where, o.HealthTimeoutSec, o.HealthIntervalSec))
	}
	if o.HealthMaxFailed < 0 || o.HealthMaxFailed > maxHealthMaxFailed {
		errs = append(errs, fmt.Errorf("%s: options.health_max_failed: must be 0 (default) to %d, got %d", where, maxHealthMaxFailed, o.HealthMaxFailed))
	}
	return errs
}

// validBandwidthScope reports whether s is an accepted options.bandwidth_limit_scope
// value. Empty is accepted (normalizes to combined at enforcement time).
func validBandwidthScope(s string) bool {
	switch s {
	case "", BandwidthScopeCombined, BandwidthScopePerDirection, BandwidthScopePerConnection:
		return true
	default:
		return false
	}
}

func (c *Config) validateGateway() []error {
	var errs []error
	g := &c.Gateway
	if err := validPort(g.ControlPort); err != nil {
		errs = append(errs, fmt.Errorf("gateway.control_port: %w", err))
	}
	if g.BindAddr != "" && net.ParseIP(g.BindAddr) == nil {
		errs = append(errs, fmt.Errorf("gateway.bind_addr: not an IP address: %q", g.BindAddr))
	}
	for _, p := range g.PortAllowlist {
		if err := validPort(p); err != nil {
			errs = append(errs, fmt.Errorf("gateway.port_allowlist: %w", err))
		}
	}
	if g.MaxConnsGlobal < 1 {
		errs = append(errs, errors.New("gateway.max_conns_global: must be >= 1"))
	}
	if g.MaxConnsPerIP < 1 {
		errs = append(errs, errors.New("gateway.max_conns_per_ip: must be >= 1"))
	}
	if g.AuthAttemptsPerMin < 1 {
		errs = append(errs, errors.New("gateway.auth_attempts_per_min: must be >= 1"))
	}
	if g.MaxTunnelsPerAgent < 1 {
		errs = append(errs, errors.New("gateway.max_tunnels_per_agent: must be >= 1"))
	}
	return errs
}

func validPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("port must be 1-65535, got %d", p)
	}
	return nil
}
