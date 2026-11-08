package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxyforward/internal/config"
	"proxyforward/internal/ipc"
	"proxyforward/internal/link"
	"proxyforward/internal/setup"
)

// Import starts a real engine, which serves the IPC pipe; give this test
// binary a private pipe so it can never collide with the ipc/engine test
// packages running in parallel or with a live daemon on the machine.
func init() {
	ipc.PipeName = ipc.PrivatePipeName("apptest")
}

// newTestAppDir builds an App rooted in a temp config dir.
func newTestAppDir(t *testing.T, cfg *config.Config) *App {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(filepath.Join(t.TempDir(), "config.toml"), cfg, nil, logger)
}

func testAgentConfig() *config.Config {
	cfg := config.Default()
	cfg.Role = config.RoleAgent
	cfg.Agent.AgentID = config.NewID()
	cfg.Agent.GatewayHost = "127.0.0.1"
	cfg.Agent.GatewayPort = 1 // nothing listens; the agent just retries
	cfg.Agent.Token = config.NewToken()
	return cfg
}

// TestImportSetupRoundTrip is the dual-boot simulation: export one install's
// agent setup, import it into a second App with a fresh config dir, and the
// second install must carry the identical identity.
func TestImportSetupRoundTrip(t *testing.T) {
	src := newTestAppDir(t, testAgentConfig())
	exported := filepath.Join(t.TempDir(), "backup"+setup.FileExt)
	if err := src.exportSetupToPath(exported, "swordfish"); err != nil {
		t.Fatal(err)
	}

	dst := newTestAppDir(t, config.Default()) // fresh install, wizard state
	if err := dst.importSetupFromPath(exported, "swordfish"); err != nil {
		t.Fatal(err)
	}
	defer dst.Shutdown(context.Background())

	if dst.cfg.Role != config.RoleAgent {
		t.Fatalf("imported role: got %q", dst.cfg.Role)
	}
	if dst.cfg.Agent.AgentID != src.cfg.Agent.AgentID || dst.cfg.Agent.Token != src.cfg.Agent.Token {
		t.Fatal("imported identity differs from the exported one")
	}
	if _, err := os.Stat(dst.configPath); err != nil {
		t.Fatalf("imported config not on disk: %v", err)
	}
	// The imported config must survive an independent reload.
	loaded, err := config.Load(dst.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.AgentID != src.cfg.Agent.AgentID {
		t.Fatal("reloaded config lost the imported identity")
	}
	if dst.mode != ModeEngine {
		t.Fatalf("engine did not start after import: mode=%q", dst.mode)
	}
}

func TestImportSetupWrongPassphrase(t *testing.T) {
	src := newTestAppDir(t, testAgentConfig())
	exported := filepath.Join(t.TempDir(), "backup"+setup.FileExt)
	if err := src.exportSetupToPath(exported, "right"); err != nil {
		t.Fatal(err)
	}
	dst := newTestAppDir(t, config.Default())
	if err := dst.importSetupFromPath(exported, "wrong"); err == nil {
		t.Fatal("import with wrong passphrase succeeded")
	}
	if dst.mode != ModeWizard {
		t.Fatalf("failed import changed mode to %q", dst.mode)
	}
}

func TestImportRefusedInAttachedMode(t *testing.T) {
	src := newTestAppDir(t, testAgentConfig())
	exported := filepath.Join(t.TempDir(), "backup"+setup.FileExt)
	if err := src.exportSetupToPath(exported, ""); err != nil {
		t.Fatal(err)
	}
	dst := newTestAppDir(t, config.Default())
	dst.mu.Lock()
	dst.mode = ModeAttached
	dst.mu.Unlock()
	err := dst.importSetupFromPath(exported, "")
	if err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Fatalf("want attached-mode refusal, got %v", err)
	}
}

func TestExportRefusedWithoutRole(t *testing.T) {
	a := newTestAppDir(t, config.Default())
	err := a.exportSetupToPath(filepath.Join(t.TempDir(), "x"+setup.FileExt), "")
	if err == nil || !strings.Contains(err.Error(), "no role") {
		t.Fatalf("want no-role refusal, got %v", err)
	}
}

// TestSetupAgentWithEnrollmentTicket: the wizard's default pairing code carries a
// tkt_ enrollment ticket and no shared token. SetupAgent must save and start
// that config — Validate once demanded agent.token, so every ticket pairing
// failed with "refusing to save invalid config".
func TestSetupAgentWithEnrollmentTicket(t *testing.T) {
	ticket, err := link.NewEnrollTicket()
	if err != nil {
		t.Fatal(err)
	}
	code := link.PairingCode{
		Host:        "127.0.0.1",
		Port:        1, // nothing listens; the agent just retries
		Token:       ticket,
		Fingerprint: "sha256:" + strings.Repeat("ab", 32),
	}.String()

	a := newTestAppDir(t, config.Default()) // fresh install, wizard state
	if err := a.SetupAgent(code, "", 0); err != nil {
		t.Fatalf("SetupAgent with a ticket: %v", err)
	}
	defer a.Shutdown(context.Background())

	if a.cfg.Agent.EnrollTicket != ticket || a.cfg.Agent.Token != "" {
		t.Fatalf("ticket routed wrong: enroll_ticket=%q token=%q", a.cfg.Agent.EnrollTicket, a.cfg.Agent.Token)
	}
	saved, err := config.Load(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Agent.EnrollTicket != ticket {
		t.Fatalf("saved enroll_ticket = %q, want the pasted ticket", saved.Agent.EnrollTicket)
	}
}
