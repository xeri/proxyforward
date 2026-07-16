package gateway

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"proxyforward/internal/config"
)

// TestRunStartedKeepsThePairingCodeOutOfLogs pins the pairing code to the console.
//
// The code embeds the shared gateway token, and slog fans out to three places that
// outlive the moment it is useful: the rotating log file, the GUI ring, and — since
// app/tools.go ships both verbatim — every diagnostics bundle. Bundles exist to be
// handed to someone else, so a logged pairing code hands over agent-level access to
// the gateway; redactConfig masking Gateway.Token there buys nothing while the same
// token rides along in cleartext inside a logged code. (security)
func TestRunStartedKeepsThePairingCodeOutOfLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	cfg := config.Default()
	cfg.Gateway.BindAddr = "127.0.0.1" // loopback: no firewall prompt on a test bind
	cfg.Gateway.ControlPort = 0        // ephemeral
	cfg.Gateway.QUICEnabled = false
	cfg.Gateway.Token = "GWTOKENSECRET"

	// An already-cancelled ctx: RunStarted still starts, mints the code and emits it,
	// then returns at its own <-ctx.Done(). Deterministic, and no sleep.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunStarted(ctx, New(cfg, t.TempDir(), logger), cfg, logger); err != nil {
		t.Fatal(err)
	}

	got := logs.String()
	if got == "" {
		t.Fatal("RunStarted logged nothing at all — this test would pass vacuously")
	}
	if strings.Contains(got, cfg.Gateway.Token) {
		t.Errorf("the gateway token reached the logs, and therefore diagnostics bundles:\n%s", got)
	}
	if strings.Contains(got, "pxf://") {
		t.Errorf("the pairing code reached the logs:\n%s", got)
	}
}
