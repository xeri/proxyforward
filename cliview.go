package main

// CLI rendering — the one place that decides how a headless surface looks.
//
// The repo has no tabwriter and no shared formatter, so without a single home
// three commands would invent three ways to align a column and three ways to
// print an unknown value. Everything the subcommands in cli.go print goes
// through here.
//
// Two rules this file exists to keep:
//
//   - A sentinel is never rendered as the number it is encoded with. RTT uses 0
//     for "no sample" (the wire clamps a real measurement to ≥ 1 ms precisely so
//     0 stays free); jitter, loss and uptime use -1. Printed literally, those
//     read as a perfect link and an impossible loss rate — convincing lies, which
//     are worse than a blank.
//   - A clamped list says how many rows really exist. ipc.Status ships
//     ConnectionsTotal / TunnelsTotal / AgentsTotal for exactly this reason:
//     printing len() of a list the daemon shortened reports the cap as the truth.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"proxyforward/internal/config"
	"proxyforward/internal/engine"
	"proxyforward/internal/gateway"
	"proxyforward/internal/ipc"
)

// unknown is what every sentinel and every empty optional field renders as. One
// glyph, everywhere — the same one the GUI uses for "no sample" (state.ts
// hasRtt), so the two surfaces describe the same absence the same way.
const unknown = "—"

// placeholderAddr is what engine.pairingCodeFor substitutes when the gateway has
// no public address configured. Matched rather than parsed: the code is a URL
// this side deliberately does not re-parse.
const placeholderAddr = "YOUR-PUBLIC-ADDRESS"

func placeholderHost(code string) bool { return strings.Contains(code, placeholderAddr) }

// table lays out fixed-width columns. The whole thing is buffered because a
// column's width is not known until the last row is added. Columns are separated
// by two spaces and the final one is never padded, so one wide trailing value (a
// config path, a pxf:// code) cannot push every line into wrap on its own.
type table struct {
	indent string
	head   []string
	rows   [][]string
}

func (t *table) row(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) render(w io.Writer) {
	widths := t.widths()
	if len(t.head) > 0 {
		t.writeRow(w, t.head, widths)
	}
	for _, r := range t.rows {
		t.writeRow(w, r, widths)
	}
}

// widths measures in runes, not bytes: the unknown glyph is three bytes wide and
// one column wide, and padding by length would splay every column holding one.
func (t *table) widths() []int {
	var widths []int
	measure := func(cells []string) {
		for i, c := range cells {
			for i >= len(widths) {
				widths = append(widths, 0)
			}
			if n := utf8.RuneCountInString(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	measure(t.head)
	for _, r := range t.rows {
		measure(r)
	}
	return widths
}

func (t *table) writeRow(w io.Writer, cells []string, widths []int) {
	var b strings.Builder
	b.WriteString(t.indent)
	for i, c := range cells {
		b.WriteString(c)
		if i == len(cells)-1 {
			break
		}
		b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)+2))
	}
	fmt.Fprintln(w, b.String())
}

// listCount describes a possibly-clamped list. total is 0 from a daemon that
// predates the field, so it is trusted only when it is larger than what arrived;
// the truncated flag on its own still gets said out loud, because "150" with no
// qualifier is a claim about the fleet.
func listCount(shown, total int, truncated bool) string {
	switch {
	case total > shown:
		return fmt.Sprintf("%d of %d shown", shown, total)
	case truncated:
		return fmt.Sprintf("%d shown, the rest were dropped to fit", shown)
	default:
		return strconv.Itoa(shown)
	}
}

// orDash renders an empty optional string as unknown rather than as a blank
// column, so a missing value is visibly missing.
func orDash(s string) string {
	if s == "" {
		return unknown
	}
	return s
}

// fmtRTT renders a round-trip time. 0 is the wire's "no sample" — the agent
// clamps a real measurement to ≥ 1 ms so that stays true — and a negative value
// cannot happen but is treated as the same absence rather than printed.
func fmtRTT(ms int64) string {
	if ms <= 0 {
		return unknown
	}
	return strconv.FormatInt(ms, 10) + " ms"
}

// fmtMillis renders jitter, whose unknown sentinel is -1.
func fmtMillis(ms float64) string {
	if ms < 0 {
		return unknown
	}
	return fmt.Sprintf("%.1f ms", ms)
}

// fmtPercent renders packet loss and uptime, whose unknown sentinel is -1.
func fmtPercent(pct float64) string {
	if pct < 0 {
		return unknown
	}
	return fmt.Sprintf("%.1f%%", pct)
}

// fmtBytes renders a byte count in binary units.
func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// fmtDur renders a duration at two units of precision — enough to answer "how
// long has this been up" without pretending to a second's accuracy on a value
// measured in days.
func fmtDur(d time.Duration) string {
	if d < 0 {
		return unknown
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// fmtSince renders how long ago a Unix-millisecond stamp was. 0 means the event
// never happened (the link never came up, the run never started), which is an
// absence, not an epoch timestamp.
func fmtSince(unixMs int64) string {
	if unixMs <= 0 {
		return unknown
	}
	return fmtDur(time.Since(time.UnixMilli(unixMs)))
}

// runStateText reads an empty run state as running. The field arrived with the
// off switch, so a daemon that omits it has no way to be stopped — its tunnel is
// running for the same reason (ipc.Status RunState).
func runStateText(s string) string {
	if s == "" {
		return string(engine.RunRunning)
	}
	return s
}

// linkText answers "is the tunnel carrying anything", which each role knows
// under a different field: the agent has one link and reports LinkUp, the
// gateway has many and reports whether any agent is attached.
func linkText(st *ipc.Status) string {
	up := st.LinkUp
	if st.Role == string(config.RoleGateway) {
		up = st.AgentConnected
	}
	if up {
		return "up"
	}
	return "down"
}

// printJSON writes the daemon's own result bytes, indented and otherwise
// untouched. Re-marshalling through a Go struct here would quietly drop any
// field this build does not know about — which is exactly what a --json consumer
// talking to a newer daemon needs to see.
func printJSON(w io.Writer, raw json.RawMessage) error {
	if len(raw) == 0 {
		fmt.Fprintln(w, "null") // an op that answered with no body at all
		return nil
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return fmt.Errorf("the daemon's reply is not valid JSON: %w", err)
	}
	fmt.Fprintln(w, buf.String())
	return nil
}

// printJSONValue is the same for a reply that never arrived as one block of raw
// bytes: Status is its own typed message, and the agent roster is concatenated
// from several pages. Both re-marshal structs whose json tags are the daemon's.
func printJSONValue(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(b))
	return nil
}

// renderStatus prints one snapshot as an operator's dashboard: who this daemon
// is, then whether the link is healthy, then what is actually serving.
func renderStatus(w io.Writer, st *ipc.Status) {
	id := &table{}
	id.row("Role", orDash(st.Role))
	id.row("Version", orDash(st.Version))
	id.row("PID", strconv.Itoa(st.PID))
	id.row("Run state", runStateText(st.RunState))
	id.row("Process up", fmtSince(st.ProcessStartMs))
	if st.LocalHostname != "" {
		id.row("Hostname", st.LocalHostname)
	}
	if st.PublicIP != "" {
		id.row("Public IP", st.PublicIP)
	}
	if st.ConfigPath != "" {
		id.row("Config", st.ConfigPath)
	}
	id.render(w)

	fmt.Fprintln(w, "\nLink")
	lk := &table{indent: "  "}
	lk.row("State", linkText(st))
	if st.PeerAddr != "" {
		lk.row("Peer", st.PeerAddr)
	}
	if st.PeerHostname != "" {
		lk.row("Peer host", st.PeerHostname)
	}
	if st.Transport != "" {
		lk.row("Transport", st.Transport)
	}
	lk.row("RTT", fmtRTT(st.RTTMillis))
	lk.row("Jitter", fmtMillis(st.JitterMillis))
	lk.row("Packet loss", fmtPercent(st.PacketLossPct))
	lk.row("Health", orDash(st.HealthScore))
	lk.row("Up for", fmtSince(st.LinkUpSinceMs))
	lk.render(w)

	if st.Role == string(config.RoleGateway) {
		fmt.Fprintf(w, "\nAgents (%s)\n", listCount(len(st.Agents), st.AgentsTotal, false))
		if len(st.Agents) == 0 {
			fmt.Fprintln(w, "  none connected")
		} else {
			t := &table{indent: "  ", head: []string{"AGENT ID", "HOST", "TUNNELS", "PLAYERS", "RTT", "HEALTH", "ADDRESS"}}
			for _, a := range st.Agents {
				t.row(a.AgentID, orDash(a.Hostname), strconv.Itoa(a.Tunnels), strconv.Itoa(a.Players),
					fmtRTT(a.RTTMillis), orDash(a.HealthScore), orDash(a.RemoteIP))
			}
			t.render(w)
		}
	}

	fmt.Fprintf(w, "\nTunnels (%s)\n", listCount(len(st.Tunnels), st.TunnelsTotal, st.TunnelsTruncated))
	if len(st.Tunnels) == 0 {
		fmt.Fprintln(w, "  none registered")
	} else {
		renderTunnels(w, st.Tunnels)
	}

	fmt.Fprintln(w, "\nTraffic")
	tr := &table{indent: "  "}
	tr.row("Connections", listCount(len(st.Connections), st.ConnectionsTotal, st.ConnectionsTruncated))
	// In and out are the wire's own directions, deliberately not re-labelled
	// download/upload: that mapping is defined once for the GUI
	// (frontend/src/history.ts) and is role-relative, so a second spelling of it
	// here would be a third convention to keep in step.
	tr.row("This session", fmtBytes(st.TotalBytesIn)+" in / "+fmtBytes(st.TotalBytesOut)+" out")
	tr.row("Control link", fmtBytes(st.LinkBytesIn)+" in / "+fmtBytes(st.LinkBytesOut)+" out")
	tr.row("All time", fmtBytes(st.AllTimeBytesIn)+" in / "+fmtBytes(st.AllTimeBytesOut)+" out")
	tr.row("Link sessions", strconv.FormatInt(st.LinkSessions, 10))
	tr.row("Cumulative up", fmtDur(time.Duration(st.CumulativeUptimeMs)*time.Millisecond))
	tr.render(w)
}

// renderTunnels prints the flat tunnel list. The agent column appears only when
// some row carries one (the gateway's list spans agents; an agent's does not), so
// the agent role is not given a column of dashes.
func renderTunnels(w io.Writer, tunnels []ipc.TunnelStatus) {
	withAgent := false
	for _, t := range tunnels {
		if t.AgentID != "" {
			withAgent = true
			break
		}
	}
	head := []string{"PORT", "NAME", "LOCAL", "PAUSED"}
	if withAgent {
		head = []string{"PORT", "NAME", "AGENT", "LOCAL", "PAUSED"}
	}
	t := &table{indent: "  ", head: head}
	for _, tn := range tunnels {
		port := unknown
		if tn.PublicPort > 0 {
			port = strconv.Itoa(tn.PublicPort)
		}
		cells := []string{port, orDash(tn.Name)}
		if withAgent {
			cells = append(cells, orDash(tn.AgentID))
		}
		t.row(append(cells, localText(tn), pauseText(tn))...)
	}
	t.render(w)
}

// localText says what the tunnel's local end is doing. "Never pointed at one" is
// a different problem from "the server is down" — only one of them is fixed by
// starting Minecraft — so the two must not share a word (ipc.TunnelStatus
// NeedsLocalAddr). A udp tunnel's health is never probed, so it reports unknown.
func localText(t ipc.TunnelStatus) string {
	switch {
	case t.NeedsLocalAddr:
		return "no local address set"
	case !t.LocalKnown:
		return unknown
	case t.LocalUp:
		return "up"
	default:
		return "down"
	}
}

// pauseText names whose bit covers the tunnel, because a resume can only clear
// this side's own — "paused by them" is not something this daemon can undo.
func pauseText(t ipc.TunnelStatus) string {
	if !t.Paused {
		return "no"
	}
	if t.PausedBy == "" {
		return "yes"
	}
	return "yes (" + t.PausedBy + ")"
}

// renderAgents prints the roster from `agents list`.
func renderAgents(w io.Writer, agents []gateway.AgentView) {
	if len(agents) == 0 {
		fmt.Fprintln(w, "No agents are enrolled. Mint a code for one with: proxyforward pair issue")
		return
	}
	t := &table{head: []string{"AGENT ID", "NAME", "STATE", "TUNNELS", "VERSION", "TRANSPORT", "ADDRESS"}}
	for _, a := range agents {
		t.row(a.AgentID, orDash(a.Nickname), agentState(a), strconv.Itoa(a.Tunnels),
			orDash(a.Version), orDash(a.Transport), orDash(a.RemoteIP))
	}
	t.render(w)
}

// agentState folds the roster's independent flags into the one word an operator
// acts on. Revoked outranks connected: revocation evicts the session, so a row
// that is somehow both is on its way out and must not read as healthy.
func agentState(a gateway.AgentView) string {
	switch {
	case a.Revoked:
		return "revoked"
	case !a.Connected:
		return "offline"
	case a.PausedAll:
		return "paused"
	default:
		return "online"
	}
}
