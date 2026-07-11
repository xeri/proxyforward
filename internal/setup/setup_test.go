package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxyforward/internal/config"
)

func agentConfig() *config.Config {
	cfg := config.Default()
	cfg.Role = config.RoleAgent
	cfg.Agent.AgentID = config.NewID()
	cfg.Agent.GatewayHost = "gw.example.com"
	cfg.Agent.GatewayPort = 8474
	cfg.Agent.Token = config.NewToken()
	cfg.Agent.CertFingerprint = "sha256:" + strings.Repeat("ab", 32)
	cfg.Agent.Tunnels = []config.Tunnel{{
		ID: config.NewID(), Name: "mc", Type: config.TunnelTCP,
		LocalAddr: "127.0.0.1:25565", PublicPort: 25565, Enabled: true,
	}}
	return cfg
}

func gatewayConfig() *config.Config {
	cfg := config.Default()
	cfg.Role = config.RoleGateway
	cfg.Gateway.Token = config.NewToken()
	return cfg
}

// gatewayDir seeds a config dir with the files a live gateway would have.
func gatewayDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{
		FileCert:  "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n",
		FileKey:   "-----BEGIN EC PRIVATE KEY-----\nfake\n-----END EC PRIVATE KEY-----\n",
		FileStats: `{"v":2}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		cfg        *config.Config
		dir        func(*testing.T) string
		passphrase string
		wantFiles  []string
	}{
		{"agent plaintext no stats", agentConfig(), func(t *testing.T) string { return t.TempDir() }, "", []string{FileConfig}},
		{"agent encrypted", agentConfig(), func(t *testing.T) string {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, FileStats), []byte(`{"v":2}`), 0o600)
			return dir
		}, "hunter2", []string{FileConfig, FileStats}},
		{"gateway plaintext", gatewayConfig(), gatewayDir, "", []string{FileConfig, FileCert, FileKey, FileStats}},
		{"gateway encrypted", gatewayConfig(), gatewayDir, "correct horse battery staple", []string{FileConfig, FileCert, FileKey, FileStats}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := tc.dir(t)
			data, err := Export(tc.cfg, srcDir, "1.2.3", tc.passphrase)
			if err != nil {
				t.Fatal(err)
			}

			info, err := Inspect(data)
			if err != nil {
				t.Fatal(err)
			}
			if info.Role != string(tc.cfg.Role) || info.AppVersion != "1.2.3" || info.Encrypted != (tc.passphrase != "") {
				t.Fatalf("inspect mismatch: %+v", info)
			}

			role, files, err := Decode(data, tc.passphrase)
			if err != nil {
				t.Fatal(err)
			}
			if role != string(tc.cfg.Role) {
				t.Fatalf("role: got %q want %q", role, tc.cfg.Role)
			}
			for _, name := range tc.wantFiles {
				if len(files[name]) == 0 {
					t.Fatalf("missing %s in decoded files: %v", name, keys(files))
				}
			}

			destDir := t.TempDir()
			if err := WriteFiles(files, destDir); err != nil {
				t.Fatal(err)
			}
			loaded, err := config.Load(filepath.Join(destDir, FileConfig))
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Role != tc.cfg.Role {
				t.Fatalf("imported role: got %q want %q", loaded.Role, tc.cfg.Role)
			}
			if tc.cfg.Role == config.RoleAgent {
				if loaded.Agent.AgentID != tc.cfg.Agent.AgentID || loaded.Agent.Token != tc.cfg.Agent.Token {
					t.Fatal("agent identity did not survive the round trip")
				}
			} else if loaded.Gateway.Token != tc.cfg.Gateway.Token {
				t.Fatal("gateway token did not survive the round trip")
			}
			// Source files land byte-identical.
			for _, name := range tc.wantFiles {
				if name == FileConfig {
					continue
				}
				src, _ := os.ReadFile(filepath.Join(srcDir, name))
				dst, _ := os.ReadFile(filepath.Join(destDir, name))
				if !bytes.Equal(src, dst) {
					t.Fatalf("%s changed in transit", name)
				}
			}
			// No temp residue.
			entries, _ := os.ReadDir(destDir)
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".tmp") {
					t.Fatalf("temp residue: %s", e.Name())
				}
			}
		})
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestWrongPassphrase(t *testing.T) {
	data, err := Export(agentConfig(), t.TempDir(), "1.2.3", "right")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Decode(data, "wrong"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("want ErrBadPassphrase, got %v", err)
	}
	if _, _, err := Decode(data, ""); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("want ErrPassphraseRequired, got %v", err)
	}
}

func TestTamperedCiphertext(t *testing.T) {
	data, err := Export(agentConfig(), t.TempDir(), "1.2.3", "pass")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m.Ciphertext[len(m.Ciphertext)/2] ^= 0x01
	flipped, _ := json.Marshal(&m)
	if _, _, err := Decode(flipped, "pass"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("want ErrBadPassphrase after bit flip, got %v", err)
	}
}

// Swapping plaintext header fields on an encrypted file must break GCM open
// — the header is bound as additional data.
func TestTamperedHeader(t *testing.T) {
	dir := gatewayDir(t)
	data, err := Export(gatewayConfig(), dir, "1.2.3", "pass")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m.Role = string(config.RoleAgent)
	swapped, _ := json.Marshal(&m)
	if _, _, err := Decode(swapped, "pass"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("want ErrBadPassphrase after header swap, got %v", err)
	}
}

func TestNewerFormatRejected(t *testing.T) {
	data, err := Export(agentConfig(), t.TempDir(), "1.2.3", "")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	json.Unmarshal(data, &m)
	m.FormatVersion = FormatVersion + 1
	newer, _ := json.Marshal(&m)
	if _, err := Inspect(newer); err == nil || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("want newer-version error, got %v", err)
	}
}

func TestNotASetupFile(t *testing.T) {
	for _, junk := range [][]byte{[]byte("not json"), []byte(`{"app":"other"}`), []byte(`{}`)} {
		if _, err := Inspect(junk); !errors.Is(err, ErrNotSetupFile) {
			t.Fatalf("want ErrNotSetupFile for %q, got %v", junk, err)
		}
	}
}

func TestMissingConfigRejected(t *testing.T) {
	data, err := Export(agentConfig(), t.TempDir(), "1.2.3", "")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	json.Unmarshal(data, &m)
	delete(m.Files, FileConfig)
	broken, _ := json.Marshal(&m)
	if _, _, err := Decode(broken, ""); err == nil || !strings.Contains(err.Error(), FileConfig) {
		t.Fatalf("want missing-config error, got %v", err)
	}
}

// A container listing a path-traversal name must be rejected before any
// filesystem write.
func TestTraversalNameRejected(t *testing.T) {
	data, err := Export(agentConfig(), t.TempDir(), "1.2.3", "")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	json.Unmarshal(data, &m)
	m.Files["..\\evil.exe"] = []byte("boom")
	evil, _ := json.Marshal(&m)
	if _, _, err := Decode(evil, ""); err == nil || !strings.Contains(err.Error(), "unexpected entry") {
		t.Fatalf("want unexpected-entry error, got %v", err)
	}
}

func TestGatewayExportRequiresIdentity(t *testing.T) {
	// Empty dir: no gateway.crt/gateway.key.
	if _, err := Export(gatewayConfig(), t.TempDir(), "1.2.3", ""); err == nil || !strings.Contains(err.Error(), FileCert) {
		t.Fatalf("want missing-identity error, got %v", err)
	}
}

func TestUnsetRoleRefused(t *testing.T) {
	if _, err := Export(config.Default(), t.TempDir(), "1.2.3", ""); err == nil || !strings.Contains(err.Error(), "no role") {
		t.Fatalf("want no-role error, got %v", err)
	}
}

// Crafted KDF parameters must not turn Decode into a memory bomb.
func TestOutOfRangeKDFRejected(t *testing.T) {
	data, err := Export(agentConfig(), t.TempDir(), "1.2.3", "pass")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	json.Unmarshal(data, &m)
	m.KDF.MemoryKiB = 8 * 1024 * 1024 // 8 GiB
	bomb, _ := json.Marshal(&m)
	if _, _, err := Decode(bomb, "pass"); err == nil || !strings.Contains(err.Error(), "key-derivation") {
		t.Fatalf("want KDF-range error, got %v", err)
	}
}
