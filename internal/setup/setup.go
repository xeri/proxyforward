// Package setup implements the .pfsetup portable-setup container: a single
// file carrying everything needed to reproduce a machine's proxyforward
// setup on another install (dual-boot, reinstall, migration) — the config,
// the credential files beside it (the agent's Ed25519 identity key; the
// gateway's TLS identity and its enrollment allowlist), and collected
// statistics.
//
// The container is one JSON document with a plaintext header (so a file can
// be inspected without a passphrase) and either a plaintext files map or an
// AES-256-GCM ciphertext of it, keyed by argon2id over a user passphrase.
package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"proxyforward/internal/config"
	"proxyforward/internal/secureio"
)

// FormatVersion is bumped only for changes an older reader cannot tolerate;
// readers accept any version in [1, FormatVersion].
const (
	FormatVersion = 1
	FileExt       = ".pfsetup"
	magicApp      = "proxyforward-setup"
)

// File names allowed inside a setup container. WriteFiles refuses anything
// else — names come from an untrusted file and must never escape the config
// dir.
const (
	FileConfig = "config.toml"
	FileCert   = "gateway.crt"
	FileKey    = "gateway.key"
	FileStats  = "stats.json"
	// FileAgentKey is the agent's Ed25519 identity (link.LoadOrCreateIdentity)
	// — since the shared token left the config, it is the agent's ONLY
	// credential, so a backup without it could never re-authenticate.
	FileAgentKey = "agent_identity.key"
	// FileAgents is the gateway's enrollment allowlist (gateway.AgentStore) —
	// the entire fleet's trust. A restored gateway without it would refuse
	// every agent it had enrolled.
	FileAgents = "gateway_agents.json"
)

var (
	ErrBadPassphrase      = errors.New("wrong passphrase or corrupted file")
	ErrPassphraseRequired = errors.New("this setup file is encrypted — a passphrase is required")
	ErrNotSetupFile       = errors.New("not a proxyforward setup file")
)

// Manifest is the on-disk container. Header fields stay plaintext so
// Inspect works without a passphrase; in encrypted mode they are bound to
// the ciphertext as GCM additional data, so tampering with the header is
// detected at decrypt time.
type Manifest struct {
	App           string    `json:"app"` // magicApp
	FormatVersion int       `json:"formatVersion"`
	Role          string    `json:"role"` // "gateway" | "agent"
	AppVersion    string    `json:"appVersion"`
	ExportedAt    time.Time `json:"exportedAt"`
	Encrypted     bool      `json:"encrypted"`

	// Plaintext mode.
	Files map[string][]byte `json:"files,omitempty"`

	// Encrypted mode: Ciphertext = AES-256-GCM(json(files)), key derived
	// from the passphrase via argon2id with the pinned KDF parameters.
	KDF        *KDFParams `json:"kdf,omitempty"`
	Nonce      []byte     `json:"nonce,omitempty"`
	Ciphertext []byte     `json:"ciphertext,omitempty"`
}

// Info is the passphrase-free view of a setup file, for confirm dialogs.
type Info struct {
	Role       string
	AppVersion string
	ExportedAt time.Time
	Encrypted  bool
}

// Export builds a setup container for cfg's role. The config is marshaled
// from memory (secrets included — this is a backup, not a diagnostic);
// role-specific files are read from configDir: a gateway must have its
// cert + key (agents pin that identity) and includes its enrollment
// allowlist when one exists; an agent must have its identity key (its only
// credential) unless the config still carries an unspent enrollment ticket.
// Stats are included when present. An empty passphrase produces a plaintext
// container.
func Export(cfg *config.Config, configDir, appVersion, passphrase string) ([]byte, error) {
	if cfg.Role != config.RoleAgent && cfg.Role != config.RoleGateway {
		return nil, errors.New("no role configured — nothing to export")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("refusing to export invalid config: %w", err)
	}
	cfgData, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	files := map[string][]byte{FileConfig: cfgData}

	if cfg.Role == config.RoleGateway {
		for _, name := range []string{FileCert, FileKey} {
			data, err := os.ReadFile(filepath.Join(configDir, name))
			if err != nil {
				return nil, fmt.Errorf("gateway identity file %s: %w (agents pin this certificate — a gateway export without it would break every pairing)", name, err)
			}
			files[name] = data
		}
		// The allowlist is the fleet: absent only when nothing has enrolled yet.
		if data, err := os.ReadFile(filepath.Join(configDir, FileAgents)); err == nil {
			files[FileAgents] = data
		}
	}
	if cfg.Role == config.RoleAgent {
		data, err := os.ReadFile(filepath.Join(configDir, FileAgentKey))
		switch {
		case err == nil:
			files[FileAgentKey] = data
		case cfg.Agent.EnrollTicket != "":
			// Never connected: no key minted yet, but the live ticket in the
			// config can still enroll the restored install's fresh key.
		default:
			return nil, fmt.Errorf("agent identity file %s: %w (it is this agent's only credential — a backup without it could never re-authenticate)", FileAgentKey, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(configDir, FileStats)); err == nil {
		files[FileStats] = data
	} // stats are history, not identity — absence is fine

	m := &Manifest{
		App:           magicApp,
		FormatVersion: FormatVersion,
		Role:          string(cfg.Role),
		AppVersion:    appVersion,
		ExportedAt:    time.Now().UTC().Truncate(time.Second),
		Encrypted:     passphrase != "",
	}
	if passphrase == "" {
		m.Files = files
	} else if err := seal(m, files, passphrase); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal setup file: %w", err)
	}
	return out, nil
}

// Inspect reads the plaintext header so the UI can show what a file
// contains before asking for a passphrase or confirmation.
func Inspect(data []byte) (Info, error) {
	m, err := parseManifest(data)
	if err != nil {
		return Info{}, err
	}
	return Info{Role: m.Role, AppVersion: m.AppVersion, ExportedAt: m.ExportedAt, Encrypted: m.Encrypted}, nil
}

// Decode validates a setup container end to end (format, decryption, config
// syntax and semantics) and returns its role and files, ready for
// WriteFiles. It never touches the filesystem.
func Decode(data []byte, passphrase string) (role string, files map[string][]byte, err error) {
	m, err := parseManifest(data)
	if err != nil {
		return "", nil, err
	}
	if m.Encrypted {
		if passphrase == "" {
			return "", nil, ErrPassphraseRequired
		}
		if files, err = open(m, passphrase); err != nil {
			return "", nil, err
		}
	} else {
		files = m.Files
	}

	cfgData, ok := files[FileConfig]
	if !ok {
		return "", nil, errors.New("setup file is missing " + FileConfig)
	}
	cfg := config.Default()
	if err := toml.Unmarshal(cfgData, cfg); err != nil {
		return "", nil, fmt.Errorf("setup file config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return "", nil, fmt.Errorf("setup file config: %w", err)
	}
	if string(cfg.Role) != m.Role {
		return "", nil, fmt.Errorf("setup file role %q does not match its config role %q", m.Role, cfg.Role)
	}
	if cfg.Role == config.RoleGateway {
		for _, name := range []string{FileCert, FileKey} {
			if len(files[name]) == 0 {
				return "", nil, errors.New("gateway setup file is missing " + name)
			}
		}
	}
	for name := range files {
		if !allowedFile(name) {
			return "", nil, fmt.Errorf("setup file contains unexpected entry %q", name)
		}
	}
	return m.Role, files, nil
}

// WriteFiles lands the decoded files in destDir atomically (temp + rename,
// with the Windows AV-scanner retry). The config is written last so a crash
// mid-import can never leave a config referencing files that are not there.
func WriteFiles(files map[string][]byte, destDir string) error {
	// An imported bundle lands the gateway key and config here; harden the
	// destination before writing them, not after.
	if err := secureio.MkdirAll(destDir); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	order := []string{FileCert, FileKey, FileAgents, FileAgentKey, FileStats, FileConfig}
	for _, name := range order {
		data, ok := files[name]
		if !ok {
			continue
		}
		mode := os.FileMode(0o600) // key material and tokens throughout
		if err := atomicWrite(filepath.Join(destDir, name), data, mode); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

func allowedFile(name string) bool {
	switch name {
	case FileConfig, FileCert, FileKey, FileStats, FileAgentKey, FileAgents:
		return true
	}
	return false
}

func parseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, ErrNotSetupFile
	}
	if m.App != magicApp {
		return nil, ErrNotSetupFile
	}
	if m.FormatVersion < 1 || m.FormatVersion > FormatVersion {
		return nil, fmt.Errorf("setup file format v%d was created by a newer version of proxyforward — update this install first", m.FormatVersion)
	}
	if m.Role != string(config.RoleAgent) && m.Role != string(config.RoleGateway) {
		return nil, fmt.Errorf("setup file has unknown role %q", m.Role)
	}
	return &m, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".setup-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		// One retry: AV scanners briefly hold fresh files on Windows.
		time.Sleep(100 * time.Millisecond)
		if err = os.Rename(tmpName, path); err != nil {
			return err
		}
	}
	return nil
}
