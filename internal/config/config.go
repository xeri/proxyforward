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

	toml "github.com/pelletier/go-toml/v2"
)

type Role string

const (
	RoleUnset   Role = ""
	RoleAgent   Role = "agent"
	RoleGateway Role = "gateway"
)

// TransportMux multiplexes all data streams over the single control
// connection; TransportPerConn opens a fresh outbound data connection per
// proxied client (avoids TCP head-of-line blocking at the cost of more
// connections).
const (
	TransportMux     = "mux"
	TransportPerConn = "per-conn"
)

const (
	TunnelTCP = "tcp"
	// TunnelUDP is reserved in the protocol for Bedrock/Geyser relay; not
	// implemented in v1.
	TunnelUDP = "udp"
)

type Config struct {
	Role    Role          `toml:"role"`
	Agent   AgentConfig   `toml:"agent"`
	Gateway GatewayConfig `toml:"gateway"`
	Metrics MetricsConfig `toml:"metrics"`
	Logging LoggingConfig `toml:"logging"`
	UI      UIConfig      `toml:"ui"`
}

type AgentConfig struct {
	// AgentID is a persistent random identity; reconnects with the same ID
	// supersede the previous session, a different ID on the same token is
	// rejected while one is connected.
	AgentID         string   `toml:"agent_id"`
	GatewayHost     string   `toml:"gateway_host"`
	GatewayPort     int      `toml:"gateway_port"`
	Token           string   `toml:"token"`
	CertFingerprint string   `toml:"cert_fingerprint"` // "sha256:<hex>" of the gateway's pinned cert
	Transport       string   `toml:"transport"`        // "mux" | "per-conn"
	Tunnels         []Tunnel `toml:"tunnels"`
}

type Tunnel struct {
	ID         string        `toml:"id"`
	Name       string        `toml:"name"`
	Type       string        `toml:"type"` // "tcp" ("udp" reserved)
	LocalAddr  string        `toml:"local_addr"`
	PublicPort int           `toml:"public_port"`
	Enabled    bool          `toml:"enabled"`
	Options    TunnelOptions `toml:"options"`
}

type TunnelOptions struct {
	// MinecraftAware enables status polling (authoritative MOTD/player data)
	// and passive username sniffing for this tunnel.
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
}

type GatewayConfig struct {
	BindAddr    string `toml:"bind_addr"`
	ControlPort int    `toml:"control_port"`
	Token       string `toml:"token"`
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
}

const (
	DefaultControlPort = 8474
	DefaultPublicPort  = 25565
)

// Default returns a config with every field at its documented default.
// Role is unset; the wizard or CLI decides it.
func Default() *Config {
	return &Config{
		Agent: AgentConfig{
			GatewayPort: DefaultControlPort,
			Transport:   TransportMux,
		},
		Gateway: GatewayConfig{
			BindAddr:           "0.0.0.0",
			ControlPort:        DefaultControlPort,
			MaxConnsGlobal:     4096,
			MaxConnsPerIP:      32,
			AuthAttemptsPerMin: 10,
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

// NewToken returns a 32-byte random hex token for gateway auth.
func NewToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b[:])
}

// Load reads the config at path. A missing file returns Default() and no
// error so first runs work without setup; any other failure (bad TOML,
// failed validation) is returned to the caller rather than silently
// replaced with defaults.
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
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// Save validates and atomically writes the config: marshal to a temp file in
// the target directory, then rename over the destination.
func (c *Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("refusing to save invalid config: %w", err)
	}
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
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
	if a.Token == "" {
		errs = append(errs, errors.New("agent.token: required (pair with a gateway first)"))
	}
	if a.CertFingerprint != "" && !strings.HasPrefix(a.CertFingerprint, "sha256:") {
		errs = append(errs, fmt.Errorf("agent.cert_fingerprint: must start with \"sha256:\", got %q", a.CertFingerprint))
	}
	if a.Transport != TransportMux && a.Transport != TransportPerConn {
		errs = append(errs, fmt.Errorf("agent.transport: must be %q or %q, got %q", TransportMux, TransportPerConn, a.Transport))
	}
	seenID := map[string]bool{}
	seenPort := map[int]bool{}
	for i := range a.Tunnels {
		t := &a.Tunnels[i]
		where := fmt.Sprintf("agent.tunnels[%d] (%s)", i, t.Name)
		if t.ID == "" {
			errs = append(errs, fmt.Errorf("%s: id required", where))
		} else if seenID[t.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate tunnel id %q", where, t.ID))
		}
		seenID[t.ID] = true
		if t.Type != TunnelTCP {
			errs = append(errs, fmt.Errorf("%s: type: only %q is supported in this version, got %q", where, TunnelTCP, t.Type))
		}
		if _, portStr, err := net.SplitHostPort(t.LocalAddr); err != nil {
			errs = append(errs, fmt.Errorf("%s: local_addr: %w", where, err))
		} else if p, err := strconv.Atoi(portStr); err != nil || validPort(p) != nil {
			errs = append(errs, fmt.Errorf("%s: local_addr: invalid port %q", where, portStr))
		}
		if err := validPort(t.PublicPort); err != nil {
			errs = append(errs, fmt.Errorf("%s: public_port: %w", where, err))
		} else if t.Enabled && seenPort[t.PublicPort] {
			errs = append(errs, fmt.Errorf("%s: public_port %d already used by another enabled tunnel", where, t.PublicPort))
		}
		if t.Enabled {
			seenPort[t.PublicPort] = true
		}
		if t.Options.BandwidthLimitMbps < 0 {
			errs = append(errs, fmt.Errorf("%s: bandwidth_limit_mbps: must be >= 0", where))
		}
	}
	return errs
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
	if g.Token == "" {
		errs = append(errs, errors.New("gateway.token: required (generated on first gateway start)"))
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
	return errs
}

func validPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("port must be 1-65535, got %d", p)
	}
	return nil
}
