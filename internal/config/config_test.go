package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxyforward/internal/link"
)

func validAgentConfig() *Config {
	cfg := Default()
	cfg.Role = RoleAgent
	cfg.Agent.AgentID = NewID()
	cfg.Agent.GatewayHost = "gw.example.com"
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
			if got.Agent.CertFingerprint != cfg.Agent.CertFingerprint {
				t.Errorf("agent cert fingerprint did not round-trip")
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

// TestLoadKeepsDefaultsForAbsentKeys: Load decodes over Default(), so a config
// written before a field existed reads as the default rather than as the zero
// value. That distinction is the whole opt-out story for pool_size — an
// upgraded agent pools by default, and only an explicit 0 turns it off.
func TestLoadKeepsDefaultsForAbsentKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "role = \"agent\"\n\n[agent]\ngateway_host = \"gw.example.com\"\ngateway_port = 7000\ncert_fingerprint = \"sha256:" + strings.Repeat("ab", 32) + "\"\ntransport = \"per-conn\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agent.PoolSize != PoolSizeDefault {
		t.Errorf("pool_size absent from the file read as %d, want the default %d", cfg.Agent.PoolSize, PoolSizeDefault)
	}

	if err := os.WriteFile(path, []byte(body+"pool_size = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agent.PoolSize != 0 {
		t.Errorf("an explicit pool_size = 0 read as %d: pooling cannot be turned off", cfg.Agent.PoolSize)
	}
}

// TestSchemaVersionIsStampedOnSave: the document on disk carries the shape that
// wrote it, which is the whole point of the field — a build that finds a number
// it does not know can say so instead of guessing. A config assembled in memory
// (or read from a legacy file) is stamped by Save, not left at whatever it held.
func TestSchemaVersionIsStampedOnSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := validAgentConfig()
	cfg.SchemaVersion = 0 // as an unversioned file would have arrived
	if err := cfg.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("schema_version = %d", CurrentSchemaVersion)
	if !strings.Contains(string(raw), want) {
		t.Errorf("saved config does not contain %q:\n%s", want, raw)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("schema version round-tripped as %d, want %d", got.SchemaVersion, CurrentSchemaVersion)
	}
}

// TestLoadRefusesFutureSchema: the mirror of internal/analytics/db.go migrate's
// downgrade check. The refusal must name both numbers, and it must win over
// validation — a future document is likely to trip today's validators for
// reasons that are only symptoms of its age.
func TestLoadRefusesFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	future := CurrentSchemaVersion + 1
	body := fmt.Sprintf("schema_version = %d\nrole = \"hovercraft\"\n", future)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected a config from a newer build to be refused, got nil")
	}
	want := fmt.Sprintf("(schema version %d, this build understands %d)", future, CurrentSchemaVersion)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name both versions as %q", err, want)
	}
	if strings.Contains(err.Error(), "role:") {
		t.Errorf("version refusal lost to a validation symptom: %v", err)
	}
}

// TestLoadAcceptsUnversionedConfig: version 0 is every config written before the
// field existed. It is not an error and never has been a different shape, so it
// loads and is carried to the current version in memory, ready for the next Save
// to stamp.
func TestLoadAcceptsUnversionedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("role = \"gateway\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("a config with no schema_version must load: %v", err)
	}
	if cfg.Role != RoleGateway {
		t.Errorf("role: got %q want %q", cfg.Role, RoleGateway)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("unversioned config loaded at version %d, want it carried to %d", cfg.SchemaVersion, CurrentSchemaVersion)
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
		"missing fingerprint": {func(c *Config) { c.Agent.CertFingerprint = "" }, "cert_fingerprint"},
		"bad fingerprint":     {func(c *Config) { c.Agent.CertFingerprint = "md5:zz" }, "cert_fingerprint"},
		// A pin narrower than the forgery floor must be refused when the config is
		// read, not once per reconnect at the handshake.
		"pin below the floor": {func(c *Config) {
			c.Agent.CertFingerprint = "sha256:" + strings.Repeat("ab", link.MinPinBytes-1)
		}, "cert_fingerprint"},
		"pin half a byte wide": {func(c *Config) {
			c.Agent.CertFingerprint = "sha256:" + strings.Repeat("ab", link.MinPinBytes) + "c"
		}, "cert_fingerprint"},
		"pin wider than the hash": {func(c *Config) {
			c.Agent.CertFingerprint = "sha256:" + strings.Repeat("ab", 33)
		}, "cert_fingerprint"},
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
		"negative pool size": {func(c *Config) { c.Agent.PoolSize = -1 }, "pool_size"},
		"pool size over the ceiling": {func(c *Config) {
			c.Agent.PoolSize = MaxPoolSize + 1
		}, "pool_size"},
		"negative bandwidth": {func(c *Config) {
			c.Agent.Tunnels[0].Options.BandwidthLimitMbps = -1
		}, "bandwidth_limit_mbps"},
		"bad bandwidth scope": {func(c *Config) {
			c.Agent.Tunnels[0].Options.BandwidthLimitMbps = 10
			c.Agent.Tunnels[0].Options.BandwidthLimitScope = "sideways"
		}, "bandwidth_limit_scope"},
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
		"bandwidth cap with explicit scope": func(c *Config) {
			c.Agent.Tunnels[0].Options.BandwidthLimitMbps = 5
			c.Agent.Tunnels[0].Options.BandwidthLimitScope = BandwidthScopePerConnection
		},
		"bandwidth cap with empty scope": func(c *Config) {
			c.Agent.Tunnels[0].Options.BandwidthLimitMbps = 5 // empty scope normalizes to combined
		},
		"bandwidth scope ignored without a cap": func(c *Config) {
			c.Agent.Tunnels[0].Options.BandwidthLimitScope = "sideways" // no cap, so not validated
		},
		"pooling turned off": func(c *Config) { c.Agent.PoolSize = 0 },
		"pooling at the ceiling": func(c *Config) {
			c.Agent.PoolSize = MaxPoolSize
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

// TestQuarantinePreservesAnUnreadableConfig: an unreadable config takes the GUI
// down its defaults path, which presents the first-run wizard; finishing that
// wizard saves, and the atomic rename would otherwise destroy a document the user
// never chose to discard. The copy is the floor under the config-error surface.
func TestQuarantinePreservesAnUnreadableConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	const wreckage = "role = \"agent\"\nthis is not toml\n"
	if err := os.WriteFile(path, []byte(wreckage), 0o600); err != nil {
		t.Fatal(err)
	}
	if !quarantineUnreadableNow(path) {
		t.Fatal("an unreadable config was not copied aside")
	}
	got, err := os.ReadFile(path + ".corrupt")
	if err != nil {
		t.Fatalf("read quarantined copy: %v", err)
	}
	if string(got) != wreckage {
		t.Errorf("quarantined copy = %q, want the original bytes %q", got, wreckage)
	}

	// The first casualty wins: a second, emptier wreck must not overwrite it.
	if err := os.WriteFile(path, []byte("also broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if quarantineUnreadableNow(path) {
		t.Error("a second quarantine overwrote the first copy")
	}
	if got, _ := os.ReadFile(path + ".corrupt"); string(got) != wreckage {
		t.Errorf("first copy was clobbered: %q", got)
	}
}

// TestQuarantineLeavesAGoodConfigAlone: the common path is a config that loads,
// and Save runs on every narrow write (a theme toggle, a pause bit). It must cost
// nothing visible and leave no litter.
func TestQuarantineLeavesAGoodConfigAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := validAgentConfig().Save(path); err != nil {
		t.Fatal(err)
	}
	if quarantineUnreadableNow(path) {
		t.Error("a readable config was copied aside")
	}
	if _, err := os.Stat(path + ".corrupt"); !os.IsNotExist(err) {
		t.Error("quarantine left a copy beside a healthy config")
	}
}

// TestQuarantineIsSilentOnAFirstRun: no file at all is a genuine first run, not
// wreckage — Load returns Default() for it and there is nothing to preserve.
func TestQuarantineIsSilentOnAFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if quarantineUnreadableNow(path) {
		t.Error("a missing config was treated as wreckage")
	}
	if _, err := os.Stat(path + ".corrupt"); !os.IsNotExist(err) {
		t.Error("quarantine invented a copy of a file that never existed")
	}
}

func TestIDsAreUnique(t *testing.T) {
	if a, b := NewID(), NewID(); a == b {
		t.Error("NewID returned duplicates")
	}
}

// TestCloneSharesNoMemory (D7): Clone is what lets a GUI and a running engine each
// own a configuration document instead of racing on one, so a field it copies
// shallowly is a shared write that nothing else will catch. Tunnels in particular
// is mutated element-wise (a pause bit), not only replaced.
func TestCloneSharesNoMemory(t *testing.T) {
	src := Default()
	src.Role = RoleAgent
	src.Agent.Tunnels = []Tunnel{
		{ID: "tnl_a", Name: "a", Type: TunnelTCP, LocalAddr: "127.0.0.1:1", PublicPort: 1, Enabled: true},
		{ID: "tnl_b", Name: "b", Type: TunnelTCP, LocalAddr: "127.0.0.1:2", PublicPort: 2, Enabled: true},
	}
	src.Gateway.PortAllowlist = []int{25565, 25566}

	dst := src.Clone()

	// Element-wise writes on either side must not cross.
	dst.Agent.Tunnels[0].Name = "renamed"
	dst.Agent.Tunnels[0].Paused = true
	dst.Gateway.PortAllowlist[0] = 1
	if src.Agent.Tunnels[0].Name != "a" || src.Agent.Tunnels[0].Paused {
		t.Fatalf("a write to the clone's tunnel reached the source: %+v", src.Agent.Tunnels[0])
	}
	if src.Gateway.PortAllowlist[0] != 25565 {
		t.Fatalf("a write to the clone's allowlist reached the source: %v", src.Gateway.PortAllowlist)
	}
	src.Agent.Tunnels[1].Name = "renamed too"
	if dst.Agent.Tunnels[1].Name != "b" {
		t.Fatal("a write to the source's tunnel reached the clone")
	}
	// Appends must not alias either (same length, spare capacity would share).
	src.Agent.Tunnels = append(src.Agent.Tunnels, Tunnel{ID: "tnl_c"})
	if len(dst.Agent.Tunnels) != 2 {
		t.Fatalf("clone grew to %d tunnels with the source", len(dst.Agent.Tunnels))
	}
	// A nil slice stays nil rather than becoming empty — the marshalled config
	// must not sprout keys a clone round-trip invented.
	bare := &Config{}
	if c := bare.Clone(); c.Agent.Tunnels != nil || c.Gateway.PortAllowlist != nil {
		t.Fatalf("Clone turned nil slices into empty ones: %+v", c)
	}
	if (*Config)(nil).Clone() != nil {
		t.Fatal("Clone of nil must be nil")
	}
}
