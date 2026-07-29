package main

// The headless control surface: subcommands that drive an *already running*
// daemon over the IPC pipe instead of starting one of their own.
//
// This is the half a gateway on a desktop-less VPS could not otherwise reach.
// `RunStarted` (`internal/gateway/gateway.go`) prints a bootstrap pairing code
// only while the roster is empty, by design — so once the first agent enrolls,
// an operator with no GUI had no way to mint a second code, revoke an agent, or
// even read status. The daemon served all of it; nothing could ask.
//
// Everything here rides ipc.Client.Analytics, which despite its name is the
// generic op call: engine.analyticsOp routes the lifecycle, pause, and
// agent-administration ops before it ever looks at the analytics store, so these
// commands answer even on a daemon whose database failed to open. Nothing new is
// added to internal/ipc for the same reason the GUI adds nothing: every method
// there has to be mirrored in ipc/stub_other.go for the non-Windows builds CI
// compiles.
//
// Request and result shapes are re-declared here as anonymous structs with
// explicit json tags, exactly as app/agents.go declares its own. The two sides
// never share a type: the wire contract is the tags, and a shared struct would
// silently couple a CLI field rename to the daemon's.
//
// Two conventions run the opposite way from the GUI's, and both are deliberate:
//
//   - Read errors are surfaced, never swallowed into an empty state. The GUI can
//     render nothing and try again in half a second; this process prints once and
//     exits. An empty table and exit 0 would tell an operator the fleet is empty.
//     So every RunE returns its error and main() exits 1.
//   - ok:false is not an error. It is the daemon's truthful "no such agent" or
//     "that cannot land right now" — a well-formed question, answered. It prints
//     as prose and exits 0.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"proxyforward/internal/engine"
	"proxyforward/internal/gateway"
	"proxyforward/internal/ipc"
	"proxyforward/internal/wincon"
)

// cliDialTimeout bounds the connect. The pipe is local: the daemon answers
// immediately or is not there at all, so a longer wait only delays the
// diagnostic that tells the operator to start one.
const cliDialTimeout = 2 * time.Second

// daemon is one command's connection to the running engine. A CLI process runs
// a single op (or one paged walk) and exits, so there is no pooling and no
// concurrency to guard — unlike the GUI, which holds a connection for its life.
type daemon struct{ c *ipc.Client }

// dialDaemon opens the pipe, telling the two ways it can fail apart. "Nothing
// answered" means no daemon is running and the fix is to start one. "Something
// answered but it is not trusted" is the opposite problem: a process is
// squatting the pipe name, and attaching anyway would mean taking this
// gateway's pairing codes from whoever won that race (ipc/security_windows.go
// verifyServer).
func dialDaemon() (*daemon, error) {
	c, err := ipc.Dial(cliDialTimeout)
	if err != nil {
		if untrustedPipe(err) {
			return nil, fmt.Errorf("%w\nsomething is squatting %s — find and stop that process before running this again", err, ipc.PipeName)
		}
		return nil, fmt.Errorf("no proxyforward daemon is answering %s: %w\nstart one with `proxyforward gateway`, `proxyforward agent`, or `proxyforward service start`", ipc.PipeName, err)
	}
	return &daemon{c: c}, nil
}

func (d *daemon) close() { _ = d.c.Close() }

// op runs one engine op and returns the daemon's own result bytes.
//
// The two failure classes are different diagnoses and must never share a
// message. *ipc.OpError means the daemon received the request, ran it, and
// refused: the pipe and the protocol both work, and its message is written for a
// human, so it is the whole answer and nothing should be wrapped around it.
// Anything else is transport — the daemon died mid-request, or is an older build
// that does not serve this op at all and therefore never replies, which surfaces
// as the client's read deadline rather than as a refusal.
func (d *daemon) op(op string, req any) (json.RawMessage, error) {
	var body json.RawMessage
	if req != nil {
		b, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		body = b
	}
	raw, err := d.c.Analytics(op, body)
	if err == nil {
		return raw, nil
	}
	var opErr *ipc.OpError
	if errors.As(err, &opErr) {
		return nil, errors.New(opErr.Msg)
	}
	return nil, fmt.Errorf("the daemon stopped answering (op %s): %w\nit may have shut down, or be an older build that does not serve this command", op, err)
}

// opInto is op plus unmarshalling, for the callers that do not need the raw
// bytes because --json was not asked for.
func (d *daemon) opInto(op string, req, out any) error {
	raw, err := d.op(op, req)
	if err != nil || out == nil || len(raw) == 0 {
		return err
	}
	return json.Unmarshal(raw, out)
}

// jsonFlag registers --json on cmd. Every command carries it: the other half of
// headless is being scriptable, and a fixed-width table is not a data format.
func jsonFlag(cmd *cobra.Command, into *bool) {
	cmd.Flags().BoolVar(into, "json", false, "emit the daemon's reply as JSON instead of a table")
}

// newStatusCmd reads the daemon's snapshot — the same one the GUI's 2 Hz tick
// renders, printed once.
func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the running daemon's status (role, link quality, tunnels, traffic)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			d, err := dialDaemon()
			if err != nil {
				return err
			}
			defer d.close()
			st, err := d.c.Status()
			if err != nil {
				// Status is its own message type, not an op, so there is no
				// OpError class here — any failure is the transport.
				return fmt.Errorf("the daemon did not answer with a status: %w", err)
			}
			if asJSON {
				// Status arrives typed rather than as raw bytes, so this
				// re-marshals the very struct the daemon marshalled — the tags
				// are the same ones, so the shape is the daemon's, not a view.
				return printJSONValue(cmd.OutOrStdout(), st)
			}
			renderStatus(cmd.OutOrStdout(), st)
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

// newAgentsCmd is the gateway's roster administration. Verb as a positional and
// one switch, matching `service` and `firewall`; arity is checked per verb
// because `rename` takes two operands where the others take one, which
// cobra.ExactArgs cannot express for the group.
func newAgentsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "agents <list|revoke|rename>",
		Short: "Inspect and administer the gateway's agent roster",
		Long: "Inspect and administer the gateway's agent roster.\n\n" +
			"  agents list                        every enrolled agent, online or not\n" +
			"  agents revoke <agent-id>           drop it from the allowlist and evict its session\n" +
			"  agents rename <agent-id> <name>    set its display nickname",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			out := cmd.OutOrStdout()
			// The switch resolves the verb and its operands into the work to
			// do, and only then is the pipe dialled: a typo must be answered
			// with the alternatives, not with "no daemon is running" from a
			// machine where one is.
			var work func(*daemon) error
			switch args[0] {
			case "list":
				work = func(d *daemon) error {
					roster, err := d.listAgents()
					if err != nil {
						return err
					}
					if asJSON {
						// Reassembled from several pages, so there are no
						// single daemon bytes to pass through — the rows are
						// still the daemon's, only concatenated.
						return printJSONValue(out, roster)
					}
					renderAgents(out, roster)
					return nil
				}
			case "revoke":
				if len(args) != 2 {
					return fmt.Errorf("agents revoke takes one agent id (list them with: proxyforward agents list)")
				}
				work = func(d *daemon) error {
					return d.agentMutate(out, asJSON, engine.OpRevokeAgent, struct {
						AgentID string `json:"agentId"`
					}{args[1]}, "Revoked. Its live session is evicted and the next connect is refused.")
				}
			case "rename":
				if len(args) != 3 {
					return fmt.Errorf("agents rename takes an agent id and a nickname")
				}
				work = func(d *daemon) error {
					return d.agentMutate(out, asJSON, engine.OpRenameAgent, struct {
						AgentID  string `json:"agentId"`
						Nickname string `json:"nickname"`
					}{args[1], args[2]}, "Renamed.")
				}
			default:
				return fmt.Errorf("unknown agents action %q (want list, revoke, or rename)", args[0])
			}
			d, err := dialDaemon()
			if err != nil {
				return err
			}
			defer d.close()
			return work(d)
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

// listAgents returns the whole roster, walking the daemon's pages.
//
// The roster outgrows one IPC frame at ~120 agents and, because revoked records
// stay on the allowlist, never shrinks back under it (gateway.maxAgentsPerPoll),
// so the op is cursored. Reassembly happens here for the same reason it happens
// in app.ListAgents: the paging is an artifact of the frame cap, and no caller
// above this line should have to know a cursor exists.
func (d *daemon) listAgents() ([]gateway.AgentView, error) {
	return collectAgentPages(func(cursor string) ([]gateway.AgentView, error) {
		var page []gateway.AgentView
		req := struct {
			AfterAgentID string `json:"afterAgentId"`
		}{cursor}
		if err := d.opInto(engine.OpListAgents, req, &page); err != nil {
			return nil, err
		}
		return page, nil
	})
}

// collectAgentPages walks next's cursor until the roster runs out. Split from
// the op call so the two ways this loop can go wrong are testable without a
// daemon: a legacy peer that never advances, and an error mid-walk.
func collectAgentPages(next func(cursor string) ([]gateway.AgentView, error)) ([]gateway.AgentView, error) {
	out := []gateway.AgentView{}
	cursor := ""
	for {
		page, err := next(cursor)
		if err != nil {
			// Inverted from app.ListAgents, which answers with an empty roster
			// here because its next poll is seconds away. This process exits: a
			// partial roster printed as a whole one says an agent is gone.
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		// A daemon too old to page ignores the cursor and answers the whole
		// roster every time. Its second answer starts at an id we already hold,
		// which is the tell — stop there rather than appending forever. That
		// leaves exactly the roster such a daemon could always serve.
		if page[0].AgentID <= cursor {
			return out, nil
		}
		out = append(out, page...)
		cursor = out[len(out)-1].AgentID
	}
}

// agentMutate runs a revoke/rename and reports the outcome. done is the sentence
// printed on success.
//
// ok:false is not a failure: the daemon looked, and that agent id is not in the
// allowlist. The question was well formed and truthfully answered, so it prints
// as prose and the command still exits 0 — only a refusal (an OpError, e.g. this
// daemon is not a gateway) or a broken pipe is an error.
func (d *daemon) agentMutate(w io.Writer, asJSON bool, op string, req any, done string) error {
	raw, err := d.op(op, req)
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(w, raw)
	}
	var res struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if !res.OK {
		fmt.Fprintln(w, "That agent is not in the roster — it may already have been removed.")
		return nil
	}
	fmt.Fprintln(w, done)
	return nil
}

// newPairIssueCmd mints a pairing code from the running gateway. It is a child
// of `pair` rather than a verb inside it because the two take different
// operands and different flags; cobra routes a first argument that names no
// child (a pxf:// code) to the parent's own RunE, so `pair <code>` is untouched.
func newPairIssueCmd() *cobra.Command {
	var (
		asJSON   bool
		reusable bool
		ttl      time.Duration
		label    string
	)
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Mint a new pairing code from the running gateway",
		Long: "Mint a new pairing code from the running gateway.\n\n" +
			"The gateway prints a bootstrap code on startup only while no agent is enrolled, so\n" +
			"this is how every code after the first one is created. Run `proxyforward pair <code>`\n" +
			"with the printed code on the machine that should become an agent.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			// The daemon takes whole seconds, so anything under one would
			// truncate to zero — which means "never expires". Refuse rather
			// than silently issuing an immortal credential.
			if ttl > 0 && ttl < time.Second {
				return fmt.Errorf("--ttl must be at least 1s (a shorter one would round to zero, which means never expires)")
			}
			d, err := dialDaemon()
			if err != nil {
				return err
			}
			defer d.close()
			req := struct {
				Reusable bool   `json:"reusable"`
				TTLSecs  int64  `json:"ttlSecs"`
				Label    string `json:"label"`
			}{reusable, int64(ttl / time.Second), label}
			raw, err := d.op(engine.OpIssuePairing, req)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, raw)
			}
			var res struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return err
			}
			// Printed exactly as issued. The daemon already composed host, port,
			// ticket and certificate fingerprint into this string
			// (engine.pairingCodeFor); reassembling it here would be a second
			// implementation of the pairing format, free to drift from the first.
			fmt.Fprintln(out, res.Code)
			if placeholderHost(res.Code) {
				// The gateway substitutes a placeholder when it has no public
				// address configured. The ticket is real, so print the code and
				// say what is missing rather than withholding it.
				fmt.Fprintln(out)
				fmt.Fprintln(out, "This gateway has no public address configured, so the code above carries a")
				fmt.Fprintln(out, "placeholder host. Set public_host under [gateway] in the config, restart, and")
				fmt.Fprintln(out, "issue another one — or hand-edit the host in the code before pasting it.")
			}
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	cmd.Flags().BoolVar(&reusable, "reusable", false, "let the code enroll more than one agent (default: single use)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "expire the code after this long (default: never)")
	cmd.Flags().StringVar(&label, "label", "", "name this invite in the gateway's roster")
	return cmd
}

// newRunCmd drives the tunnel run itself. The daemon's pipe outlives the run in
// both directions, so a stopped daemon still answers `status` and can be started
// again from here — that is the whole point of the supervisor/run split.
func newRunCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "run <start|stop>",
		Short: "Start or stop the daemon's tunnel run (it keeps serving the pipe either way)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			var op string
			switch args[0] {
			case "start":
				op = engine.OpStartRun
			case "stop":
				op = engine.OpStopRun
			default:
				return fmt.Errorf("unknown run action %q (want start or stop)", args[0])
			}
			d, err := dialDaemon()
			if err != nil {
				return err
			}
			defer d.close()
			raw, err := d.op(op, nil)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, raw)
			}
			var res struct {
				RunState string `json:"runState"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return err
			}
			if op == engine.OpStopRun {
				// engine.lifecycleOp calls StopRun(true) unconditionally — the
				// op's Force field is not read, because the request/response
				// cycle cannot afford to wait out a clean unwind. So there is no
				// graceful stop to advertise and no --wait flag to offer: live
				// players are dropped now. The state that comes back is
				// therefore usually "stopping", because the unwind outlives the
				// reply — hence "requested" rather than a claim it is done.
				fmt.Fprintf(out, "Stop requested (state: %s). Connected players were dropped immediately.\n", res.RunState)
				fmt.Fprintln(out, "The daemon keeps serving the pipe and the stop persists across restarts;")
				fmt.Fprintln(out, "`proxyforward status` says when it has settled, and `run start` brings it back.")
				return nil
			}
			fmt.Fprintf(out, "Run started (state: %s).\n", res.RunState)
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	return cmd
}

// newPauseCmd flips this daemon's own pause bit. Pause has two owners: each side
// holds its own bit and may clear only that one, so `pause off` here can never
// re-expose a tunnel the other side is holding down.
func newPauseCmd() *cobra.Command {
	var (
		asJSON     bool
		agentID    string
		tunnelID   string
		disconnect bool
	)
	cmd := &cobra.Command{
		Use:   "pause <on|off>",
		Short: "Pause or resume tunnels (the port stays bound; players are turned away)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			var paused bool
			switch args[0] {
			case "on":
				paused = true
			case "off":
				paused = false
			default:
				return fmt.Errorf("unknown pause action %q (want on or off)", args[0])
			}
			d, err := dialDaemon()
			if err != nil {
				return err
			}
			defer d.close()
			// --agent is required on a gateway and ignored on an agent; the
			// daemon knows its own role, so it makes that call and names the
			// problem rather than this side guessing at a role it has not read.
			req := struct {
				AgentID    string `json:"agentId"`
				TunnelID   string `json:"tunnelId"`
				Paused     bool   `json:"paused"`
				Disconnect bool   `json:"disconnect"`
			}{agentID, tunnelID, paused, disconnect}
			raw, err := d.op(engine.OpSetPause, req)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, raw)
			}
			target := "every tunnel"
			if tunnelID != "" {
				target = "tunnel " + tunnelID
			}
			if agentID != "" {
				target += " of agent " + agentID
			}
			if paused {
				fmt.Fprintf(out, "Paused %s. The public port stays bound and new players are turned away.\n", target)
				return nil
			}
			fmt.Fprintf(out, "Resumed %s — this side's pause bit is cleared.\n", target)
			fmt.Fprintln(out, "If the other side has also paused it, it stays paused until they resume too.")
			return nil
		},
	}
	jsonFlag(cmd, &asJSON)
	cmd.Flags().StringVar(&agentID, "agent", "", "which agent to pause (required on a gateway; ignored on an agent)")
	cmd.Flags().StringVar(&tunnelID, "tunnel", "", "which tunnel to pause (default: all of them)")
	cmd.Flags().BoolVar(&disconnect, "disconnect", false, "also drop the players already connected")
	return cmd
}
