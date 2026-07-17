package gateway

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"proxyforward/internal/config"
)

// TestRunStartedKeepsThePairingCodeOutOfLogs pins the bootstrap pairing code to
// the console.
//
// On an empty roster RunStarted mints a single-use enrollment ticket and prints
// the code carrying it, and slog fans out to three places that outlive the moment
// it is useful: the rotating log file, the GUI ring, and — since app/tools.go
// ships both verbatim — every diagnostics bundle. Bundles exist to be handed to
// someone else, so a logged pairing code hands over agent-level access to the
// gateway; redacting the config there buys nothing while the same credential
// rides along in cleartext inside a logged code. (security)
func TestRunStartedKeepsThePairingCodeOutOfLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	cfg := config.Default()
	cfg.Gateway.BindAddr = "127.0.0.1" // loopback: no firewall prompt on a test bind
	cfg.Gateway.ControlPort = 0        // ephemeral
	cfg.Gateway.QUICEnabled = false

	// An already-cancelled ctx: RunStarted still starts, mints the bootstrap code
	// and emits it, then returns at its own <-ctx.Done(). Deterministic, no sleep.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunStarted(ctx, New(cfg, t.TempDir(), logger), cfg, logger); err != nil {
		t.Fatal(err)
	}

	got := logs.String()
	if got == "" {
		t.Fatal("RunStarted logged nothing at all — this test would pass vacuously")
	}
	if strings.Contains(got, "tkt_") {
		t.Errorf("the enrollment ticket reached the logs, and therefore diagnostics bundles:\n%s", got)
	}
	if strings.Contains(got, "pxf://") {
		t.Errorf("the pairing code reached the logs:\n%s", got)
	}
}
