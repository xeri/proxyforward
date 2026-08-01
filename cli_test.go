package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/engine"
	"proxyforward/internal/gateway"
	"proxyforward/internal/ipc"
	"proxyforward/internal/link"
)

// TestCLIDrivesALiveDaemon below starts a real Engine, which serves the IPC
// pipe; give this test binary a private pipe so it can never collide with the
// engine/ipc/app test packages running in parallel, or with a live daemon on the
// developer's machine (.claude/rules/go-tests.md).
func init() {
	ipc.PipeName = fmt.Sprintf(`\\.\pipe\proxyforward-clitest-%d`, os.Getpid())
}

// Sentinels must never be printed as the numbers they are encoded with: RTT
// carries 0 for "no sample" and jitter/loss/uptime carry -1, and both read as
// plausible measurements if rendered literally. (sentinels)
func TestCLIRendersSentinelsAsUnknown(t *testing.T) {
	cases := []struct {
		name string
		got  string
	}{
		{"rtt no sample", fmtRTT(0)},
		{"rtt negative", fmtRTT(-1)},
		{"jitter unknown", fmtMillis(-1)},
		{"loss unknown", fmtPercent(-1)},
		{"uptime unknown", fmtDur(-1)},
		{"never happened", fmtSince(0)},
		{"empty optional", orDash("")},
	}
	for _, c := range cases {
		if c.got != unknown {
			t.Errorf("%s = %q, want %q", c.name, c.got, unknown)
		}
	}
	// A real measurement still renders as one; the clamp guarantees 1 ms is the
	// smallest a sample can be, so it must not be swallowed as a sentinel.
	if got := fmtRTT(1); got != "1 ms" {
		t.Errorf("fmtRTT(1) = %q, want %q", got, "1 ms")
	}
	if got := fmtPercent(0); got != "0.0%" {
		t.Errorf("fmtPercent(0) = %q, want a real zero", got)
	}
}

// A whole status frame with every quality field unknown must contain no
// rendered -1 and no "0 ms" — the sentinels have to survive the renderer, not
// just the formatters. (sentinels)
func TestStatusRenderKeepsSentinelsUnknown(t *testing.T) {
	var buf bytes.Buffer
	renderStatus(&buf, &ipc.Status{
		Role: string(config.RoleAgent), Version: "v1", PID: 1,
		RTTMillis: 0, JitterMillis: -1, PacketLossPct: -1, HealthScore: "unknown",
	})
	out := buf.String()
	for _, bad := range []string{"-1", "0 ms"} {
		if strings.Contains(out, bad) {
			t.Errorf("status render leaked the sentinel %q:\n%s", bad, out)
		}
	}
	if strings.Count(out, unknown) < 4 {
		t.Errorf("expected RTT, jitter, loss and link-up all unknown:\n%s", out)
	}
}

// A clamped list must report the daemon's real total, never the length of what
// arrived — that is what the Total/Truncated fields on ipc.Status are for, and
// printing len() of a shortened list reports the cap as the fleet size.
// (truncation)
func TestStatusRenderHonoursTruncation(t *testing.T) {
	st := &ipc.Status{
		Role: string(config.RoleGateway), Version: "v1", PID: 1,
		JitterMillis: -1, PacketLossPct: -1,
		// No connection rows survived the clamp, but 412 are live: the count
		// line must report the daemon's number, not the empty list's.
		ConnectionsTotal:     412,
		ConnectionsTruncated: true,
		Tunnels:              []ipc.TunnelStatus{{ID: "tnl_a", Name: "Minecraft", PublicPort: 25565, LocalKnown: true, LocalUp: true}},
		TunnelsTotal:         900,
		TunnelsTruncated:     true,
		Agents:               []ipc.AgentStatus{{AgentID: "agt_a", RTTMillis: 0, HealthScore: "good"}},
		AgentsTotal:          77,
	}
	var buf bytes.Buffer
	renderStatus(&buf, st)
	out := buf.String()
	for _, want := range []string{"0 of 412 shown", "1 of 900 shown", "1 of 77 shown"} {
		if !strings.Contains(out, want) {
			t.Errorf("status render must say %q:\n%s", want, out)
		}
	}

	// A daemon that predates the Total fields sends the flag alone. Saying
	// nothing would present a clamped list as the whole truth.
	if got := listCount(150, 0, true); !strings.Contains(got, "dropped") {
		t.Errorf("listCount with no total = %q, want it to admit the list is short", got)
	}
	if got := listCount(3, 3, false); got != "3" {
		t.Errorf("listCount for a complete list = %q, want %q", got, "3")
	}
}

// The roster's independent flags fold into one word per row, and an offline
// agent's empty live fields render as unknown rather than as blank columns —
// the two-process live exercise cannot reach this state from one machine (one
// pipe, one daemon), so it is pinned here. (roster)
func TestRenderAgentsFoldsStateAndKeepsUnknowns(t *testing.T) {
	var buf bytes.Buffer
	renderAgents(&buf, []gateway.AgentView{
		{AgentID: "agt_on", Nickname: "Basement", Connected: true, Tunnels: 2, Version: "0.1.0", Transport: "quic", RemoteIP: "203.0.113.9"},
		{AgentID: "agt_off"},
		{AgentID: "agt_rev", Revoked: true, Connected: true},
		{AgentID: "agt_pause", Connected: true, PausedAll: true},
	})
	out := buf.String()
	for _, want := range []string{"online", "offline", "revoked", "paused", "Basement", "quic"} {
		if !strings.Contains(out, want) {
			t.Errorf("roster render is missing %q:\n%s", want, out)
		}
	}
	// The offline row has no nickname, version, transport or address.
	if strings.Count(out, unknown) < 4 {
		t.Errorf("an offline agent's empty live fields must render as %q:\n%s", unknown, out)
	}
	// A revoked record whose session has not gone yet must not read as healthy.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "agt_rev") && strings.Contains(line, "online") {
			t.Errorf("a revoked agent rendered as online: %q", line)
		}
	}
}

// The paged roster walk must terminate against a daemon too old to page, which
// ignores the cursor and answers the whole roster every time. Its second answer
// starts at an id we already hold — that is the tell, and without it the loop
// appends forever. (paging)
func TestCollectAgentPagesStopsOnALegacyDaemon(t *testing.T) {
	whole := []gateway.AgentView{{AgentID: "agt_a"}, {AgentID: "agt_b"}}
	calls := 0
	got, err := collectAgentPages(func(string) ([]gateway.AgentView, error) {
		calls++
		if calls > 10 {
			t.Fatal("the walk never terminated against a non-paging daemon")
		}
		return whole, nil
	})
	if err != nil {
		t.Fatalf("collectAgentPages: %v", err)
	}
	if len(got) != 2 || got[0].AgentID != "agt_a" || got[1].AgentID != "agt_b" {
		t.Fatalf("roster = %+v, want exactly one copy of the whole roster", got)
	}
	if calls != 2 {
		t.Errorf("asked %d times, want 2 (one page, one repeat that proves it repeats)", calls)
	}
}

// The happy path: pages are concatenated in order and the cursor advances to the
// last id seen, until an empty page ends the walk. (paging)
func TestCollectAgentPagesWalksTheCursor(t *testing.T) {
	pages := map[string][]gateway.AgentView{
		"":      {{AgentID: "agt_a"}, {AgentID: "agt_b"}},
		"agt_b": {{AgentID: "agt_c"}},
		"agt_c": {},
	}
	got, err := collectAgentPages(func(cursor string) ([]gateway.AgentView, error) {
		page, ok := pages[cursor]
		if !ok {
			t.Fatalf("unexpected cursor %q", cursor)
		}
		return page, nil
	})
	if err != nil {
		t.Fatalf("collectAgentPages: %v", err)
	}
	if len(got) != 3 || got[2].AgentID != "agt_c" {
		t.Fatalf("roster = %+v, want agt_a, agt_b, agt_c", got)
	}
}

// A read error mid-walk must surface, inverting the GUI's convention: app.
// ListAgents answers with an empty roster because it polls again in seconds,
// but this process prints once and exits, and a partial roster shown as a whole
// one says an agent is gone. (read errors)
func TestCollectAgentPagesSurfacesAReadError(t *testing.T) {
	boom := errors.New("the daemon stopped answering")
	_, err := collectAgentPages(func(cursor string) ([]gateway.AgentView, error) {
		if cursor == "" {
			return []gateway.AgentView{{AgentID: "agt_a"}}, nil
		}
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the read error surfaced", err)
	}
}

// `pair issue` is a child of `pair`, which itself takes a positional pairing
// code. Cobra must keep routing a pxf:// argument to the parent — if it ever
// stopped, every existing pairing invocation would break. (routing)
func TestPairIssueDoesNotShadowPairWithACode(t *testing.T) {
	root := newRootCmd("")
	cmd, _, err := root.Find([]string{"pair", "pxf://gw:8474/v1/pair/tkt_x#sha256:abc"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if cmd.Name() != "pair" {
		t.Errorf("a pairing code routed to %q, want the pair command itself", cmd.Name())
	}
	cmd, _, err = root.Find([]string{"pair", "issue"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if cmd.Name() != "issue" {
		t.Errorf("`pair issue` routed to %q, want issue", cmd.Name())
	}
}

// The unknown verb arms exist so a typo is answered with the alternatives rather
// than with silence. (verbs)
func TestUnknownVerbsNameTheAlternatives(t *testing.T) {
	for _, args := range [][]string{{"agents", "delete"}, {"run", "restart"}, {"pause", "maybe"}} {
		err := runCLI(t, &bytes.Buffer{}, args...)
		if err == nil {
			t.Fatalf("%v was accepted", args)
		}
		if !strings.Contains(err.Error(), "want") {
			t.Errorf("%v: error %q does not list the valid actions", args, err)
		}
	}
}

// The whole point of this command group, exercised against a real daemon over a
// real pipe: read status, mint a pairing code, and list the roster on a gateway
// that has no GUI attached. (headless control)
func TestCLIDrivesALiveDaemon(t *testing.T) {
	startTestGateway(t)

	var out bytes.Buffer
	if err := runCLI(t, &out, "status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), string(config.RoleGateway)) {
		t.Errorf("status did not name the role:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Tunnels") {
		t.Errorf("status did not render the tunnel section:\n%s", out.String())
	}

	out.Reset()
	if err := runCLI(t, &out, "pair", "issue", "--label", "cli-test"); err != nil {
		t.Fatalf("pair issue: %v", err)
	}
	code := strings.TrimSpace(out.String())
	if !strings.HasPrefix(code, "pxf://gw.example:") {
		t.Fatalf("issued code = %q, want a finished pxf:// code carrying the configured host", code)
	}
	// What is minted is what an agent pastes, so it must be the current format and
	// must survive the parser — the mint composes the pin and ticket by hand, and a
	// width that stopped matching would produce a code nothing can read.
	if !strings.Contains(code, "/v2/pair/") {
		t.Errorf("issued code is not in the current format: %q", code)
	}
	parsed, perr := link.ParsePairingCode(code)
	if perr != nil {
		t.Fatalf("the daemon issued a code its own parser rejects: %v (%q)", perr, code)
	}
	if !link.IsEnrollTicket(parsed.Token) {
		t.Errorf("issued code carries %q, want a tkt_ enrollment ticket", parsed.Token)
	}
	if !link.IsShortPin(parsed.Fingerprint) {
		t.Errorf("issued code carries the whole fingerprint %q; a v2 code carries the pin", parsed.Fingerprint)
	}

	out.Reset()
	if err := runCLI(t, &out, "agents", "list"); err != nil {
		t.Fatalf("agents list: %v", err)
	}
	if !strings.Contains(out.String(), "No agents are enrolled") {
		t.Errorf("an empty roster needs a written empty state, got:\n%s", out.String())
	}

	// ok:false is the daemon's truthful "no such agent", not a failure: the
	// command must say so in prose and still succeed.
	out.Reset()
	if err := runCLI(t, &out, "agents", "revoke", "agt_ghost"); err != nil {
		t.Fatalf("revoking an unknown agent must not be an error: %v", err)
	}
	if !strings.Contains(out.String(), "not in the roster") {
		t.Errorf("revoke of an unknown agent said %q", out.String())
	}

	// A refusal the daemon *served* (a gateway pause with no agent named) is an
	// error, and its message must reach the operator verbatim rather than as a
	// transport diagnostic.
	out.Reset()
	err := runCLI(t, &out, "pause", "on")
	if err == nil {
		t.Fatal("a gateway pause with no --agent must be refused")
	}
	if !strings.Contains(err.Error(), "names the agent") {
		t.Errorf("refusal = %v, want the daemon's own message", err)
	}
	if strings.Contains(err.Error(), "stopped answering") {
		t.Errorf("a served refusal was reported as a transport failure: %v", err)
	}
}

// --json must emit the daemon's own shape, not a rendering of it. (json)
func TestCLIJSONEmitsTheDaemonsShape(t *testing.T) {
	startTestGateway(t)

	var out bytes.Buffer
	if err := runCLI(t, &out, "status", "--json"); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	if !strings.Contains(out.String(), `"role": "gateway"`) {
		t.Errorf("status --json is not the Status document:\n%s", out.String())
	}

	out.Reset()
	if err := runCLI(t, &out, "pair", "issue", "--json"); err != nil {
		t.Fatalf("pair issue --json: %v", err)
	}
	if !strings.Contains(out.String(), `"code"`) {
		t.Errorf("pair issue --json did not pass the op result through:\n%s", out.String())
	}
}

// runCLI executes one command through the real root, with output captured.
func runCLI(t *testing.T, out *bytes.Buffer, args ...string) error {
	t.Helper()
	root := newRootCmd("")
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(args)
	return root.Execute()
}

// startTestGateway runs a real gateway engine (control listener, allowlist, IPC
// pipe) on a free loopback port for the rest of the test, and returns once its
// pipe answers.
func startTestGateway(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	dir := t.TempDir()
	cfg := config.Default()
	cfg.Role = config.RoleGateway
	cfg.Gateway.BindAddr = "127.0.0.1"
	cfg.Gateway.ControlPort = port
	// A real public host, so an issued code carries one instead of the
	// placeholder — the placeholder path has its own branch in pair issue.
	cfg.Gateway.PublicHost = "gw.example"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	eng, err := engine.New(cfg, dir, filepath.Join(dir, "config.toml"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("the test gateway did not stop after cancel")
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		c, derr := ipc.Dial(200 * time.Millisecond)
		if derr == nil {
			c.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the test gateway's pipe never answered: %v", derr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
