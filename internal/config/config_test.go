package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validAgentConfig() *Config {
	cfg := Default()
	cfg.Role = RoleAgent
	cfg.Agent.AgentID = NewID()
	cfg.Agent.GatewayHost = "gw.example.com"
	cfg.Agent.Token = NewToken()
	cfg.Agent.CertFingerprint = "sha256:" + strings.Repeat("ab", 32)
	cfg.Agent.Tunnels = []Tunnel{{
		ID:         NewID(),
		Name:       "Main server",
		Type:       TunnelTCP,
		LocalAddr:  "127.0.0.1:25565",
		PublicPort: 25565,
		Enabled:    true,
	}}
	return cfg
}

func validGatewayConfig() *Config {
	cfg := Default()
	cfg.Role = RoleGateway
	cfg.Gateway.Token = NewToken()
	return cfg
}

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	for name, cfg := range map[string]*Config{
		"agent":   validAgentConfig(),
		"gateway": validGatewayConfig(),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := cfg.Save(path); err != nil {
				t.Fatalf("save: %v", err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.Role != cfg.Role {
				t.Errorf("role: got %q want %q", got.Role, cfg.Role)
			}
			if got.Agent.Token != cfg.Agent.Token {
				t.Errorf("agent token did not round-trip")
			}
			if len(got.Agent.Tunnels) != len(cfg.Agent.Tunnels) {
				t.Fatalf("tunnels: got %d want %d", len(got.Agent.Tunnels), len(cfg.Agent.Tunnels))
			}
			for i := range cfg.Agent.Tunnels {
				if got.Agent.Tunnels[i] != cfg.Agent.Tunnels[i] {
					t.Errorf("tunnel %d: got %+v want %+v", i, got.Agent.Tunnels[i], cfg.Agent.Tunnels[i])
				}
			}
		})
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope", "config.toml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if cfg.Role != RoleUnset {
		t.Errorf("expected unset role, got %q", cfg.Role)
	}
	if cfg.Gateway.ControlPort != DefaultControlPort {
		t.Errorf("expected default control port %d, got %d", DefaultControlPort, cfg.Gateway.ControlPort)
	}
}

func TestLoadRejectsBadTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("role = [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestValidateCatches(t *testing.T) {
	cases := map[string]struct {
		mutate  func(*Config)
		wantSub string
	}{
		"bad role":            {func(c *Config) { c.Role = "banana" }, "role:"},
		"agent missing host":  {func(c *Config) { c.Agent.GatewayHost = "" }, "gateway_host"},
		"agent missing token": {func(c *Config) { c.Agent.Token = "" }, "agent.token"},
		"bad fingerprint":     {func(c *Config) { c.Agent.CertFingerprint = "md5:zz" }, "cert_fingerprint"},
		"bad transport":       {func(c *Config) { c.Agent.Transport = "carrier-pigeon" }, "transport"},
		"bad tunnel type":     {func(c *Config) { c.Agent.Tunnels[0].Type = "sctp" }, "type"},
		"bad local addr":      {func(c *Config) { c.Agent.Tunnels[0].LocalAddr = "localhost" }, "local_addr"},
		"bad public port":     {func(c *Config) { c.Agent.Tunnels[0].PublicPort = 70000 }, "public_port"},
		"duplicate tunnel id": {func(c *Config) { c.Agent.Tunnels = append(c.Agent.Tunnels, c.Agent.Tunnels[0]) }, "duplicate"},
		"bad log level":       {func(c *Config) { c.Logging.Level = "loud" }, "logging.level"},
		"bad theme":           {func(c *Config) { c.UI.Theme = "hotdog" }, "ui.theme"},
		"udp minecraft-aware": {func(c *Config) {
			c.Agent.Tunnels[0].Type = TunnelUDP
			c.Agent.Tunnels[0].Options.MinecraftAware = true
		}, "minecraft_aware"},
		"udp proxy-protocol": {func(c *Config) {
			c.Agent.Tunnels[0].Type = TunnelUDP
			c.Agent.Tunnels[0].Options.ProxyProtocolV2 = true
		}, "proxy_protocol_v2"},
		"udp offline-motd": {func(c *Config) {
			c.Agent.Tunnels[0].Type = TunnelUDP
			c.Agent.Tunnels[0].Options.OfflineMOTD = "brb"
		}, "offline_motd"},
		"duplicate udp port": {func(c *Config) {
			c.Agent.Tunnels[0].Type = TunnelUDP
			dup := c.Agent.Tunnels[0]
			dup.ID = NewID()
			c.Agent.Tunnels = append(c.Agent.Tunnels, dup)
		}, "already used by another enabled udp tunnel"},
		"retention too short": {func(c *Config) { c.Analytics.RetentionDays = 0 }, "retention_days"},
		"retention too long":  {func(c *Config) { c.Analytics.RetentionDays = 4000 }, "retention_days"},
		"geoip not mmdb":      {func(c *Config) { c.Analytics.GeoIPCityPath = `C:\geo\GeoLite2-City.zip` }, "geoip_city_path"},
		"geoip asn not mmdb":  {func(c *Config) { c.Analytics.GeoIPASNPath = "asn.dat" }, "geoip_asn_path"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validAgentConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestValidateAccepts(t *testing.T) {
	cases := map[string]func(*Config){
		"udp tunnel": func(c *Config) { c.Agent.Tunnels[0].Type = TunnelUDP },
		"tcp and udp share a public port": func(c *Config) {
			udp := c.Agent.Tunnels[0]
			udp.ID = NewID()
			udp.Type = TunnelUDP
			c.Agent.Tunnels = append(c.Agent.Tunnels, udp)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validAgentConfig()
			mutate(cfg)
			if err := cfg.Validate(); err != nil {
				t.Fatalf("expected valid config, got %v", err)
			}
		})
	}
}

func TestValidateGatewayCatches(t *testing.T) {
	cases := map[string]struct {
		mutate  func(*Config)
		wantSub string
	}{
		"missing token":  {func(c *Config) { c.Gateway.Token = "" }, "gateway.token"},
		"bad bind addr":  {func(c *Config) { c.Gateway.BindAddr = "not-an-ip" }, "bind_addr"},
		"bad allowlist":  {func(c *Config) { c.Gateway.PortAllowlist = []int{0} }, "port_allowlist"},
		"zero conn caps": {func(c *Config) { c.Gateway.MaxConnsPerIP = 0 }, "max_conns_per_ip"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validGatewayConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestSaveRefusesInvalid(t *testing.T) {
	cfg := validAgentConfig()
	cfg.Role = "banana"
	if err := cfg.Save(filepath.Join(t.TempDir(), "config.toml")); err == nil {
		t.Fatal("expected save of invalid config to fail")
	}
}

func TestIDsAreUnique(t *testing.T) {
	if NewID() == NewID() {
		t.Error("NewID returned duplicates")
	}
	if len(NewToken()) != 64 {
		t.Errorf("token should be 64 hex chars, got %d", len(NewToken()))
	}
}
