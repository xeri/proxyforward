// Package ipc is the GUI↔daemon control channel: a named-pipe JSON-RPC
// carried in the same length-prefixed framing as the wire protocol
// (internal/control). The daemon (service or headless run) serves it; a GUI
// that finds the pipe attaches as a thin client instead of starting its own
// engine, so exactly one process ever owns ports and config.
package ipc

import (
	"encoding/json"
	"errors"

	"proxyforward/internal/conntrack"
	"proxyforward/internal/control"
	"proxyforward/internal/stats"
)

// PipeName is the daemon's well-known local endpoint. It is a var only so
// test packages that run a real engine can point it at a private name —
// parallel test binaries (and a developer's live daemon) must never fight
// over the production pipe.
var PipeName = `\\.\pipe\proxyforward`

// Message types (control.Envelope.Type values on the pipe).
const (
	TypeStatusReq   = "ipc_status_req"
	TypeStatusResp  = "ipc_status_resp"
	TypePing        = "ipc_ping"
	TypePong        = "ipc_pong"
	TypeHistoryReq  = "ipc_history_req"
	TypeHistoryResp = "ipc_history_resp"
	TypePeersReq    = "ipc_peers_req"
	TypePeersResp   = "ipc_peers_resp"
	// The analytics envelope carries every historical/player/geo query as a
	// named op with JSON in and out, so new read endpoints need no new message
	// type — one dispatch entry on the daemon and one typed method in the GUI.
	TypeAnalyticsReq  = "ipc_analytics_req"
	TypeAnalyticsResp = "ipc_analytics_resp"
)

// AnalyticsReq is a generic analytics query: Op names the query, Body is its
// JSON-encoded arguments.
type AnalyticsReq struct {
	Op   string          `json:"op"`
	Body json.RawMessage `json:"body,omitempty"`
}

// AnalyticsResp carries the JSON-encoded result or an error string.
type AnalyticsResp struct {
	Err  string          `json:"err,omitempty"`
	Body json.RawMessage `json:"body,omitempty"`
}

// OpError is a served analytics error: the daemon answered the request and
// reported a failure. It is transient by nature (the pipe and the protocol
// both work), so callers must not treat it like a transport failure — in
// particular it must never latch "analytics unsupported".
type OpError struct {
	Op  string
	Msg string
}

func (e *OpError) Error() string { return "ipc analytics " + e.Op + ": " + e.Msg }

// maxIPCEntries clamps history buckets and peer rows per response so the
// reply always fits control.MaxFrame (64 KiB). Never raise the frame cap;
// clamp the payload instead.
const maxIPCEntries = 300

// MaxStatusConns clamps Status.Connections the same way. A snapshot with
// every field populated (player identity, bracketed IPv6 address) marshals
// to ~340 bytes. The newest connections win; ConnectionsTruncated /
// ConnectionsTotal tell the GUI what it isn't seeing.
//
// It is a per-list ceiling, not the thing that makes the frame fit: three
// lists grow independently here, and no single ceiling can bound their sum.
// FitStatus is what guarantees the frame, so these are chosen for what each
// surface wants to show rather than by dividing MaxFrame three ways.
const MaxStatusConns = 150

// HistoryReq asks for the trailing windowMs of bandwidth history (0 = all)
// aggregated to at most MaxBuckets buckets.
type HistoryReq struct {
	WindowMs   int64 `json:"windowMs"`
	MaxBuckets int   `json:"maxBuckets"`
}

// PeersResp carries the daemon's per-client lifetime records.
type PeersResp struct {
	Peers []stats.PeerStat `json:"peers"`
}

// ErrUnsupported is returned on platforms without named pipes.
var ErrUnsupported = errors.New("ipc: only supported on Windows")

// Status is the daemon's self-description, polled by status surfaces.
type Status struct {
	Role    string `json:"role"`
	Version string `json:"version"`
	// PID lets a GUI distinguish "my own engine" from a foreign daemon.
	PID int `json:"pid"`

	// RunState is the tunnel run's lifecycle — running | stopping | stopped —
	// which is a separate axis from the role and from Mode: a stopped run
	// still answers here, because the daemon serving this pipe outlives it.
	// Empty from a daemon too old to have an off switch, which reads as
	// "running" for the same reason its tunnel is.
	RunState string `json:"runState,omitempty"`

	// Agent-side fields.
	LinkUp    bool  `json:"linkUp,omitempty"`
	RTTMillis int64 `json:"rttMillis,omitempty"`
	// Transport is the data plane the live agent session settled on ("quic" |
	// "per-conn" | "mux"); empty while down or on the gateway role. Lets the GUI
	// show what the auto ladder actually connected over.
	Transport string `json:"transport,omitempty"`

	// Link quality, reported by both roles: each runs its own heartbeat toward
	// the other, so the gateway measures these too (`Engine.Status` reads
	// `Gateway.JitterMillis`/`PacketLossPct`). -1 means unknown — no link,
	// or not enough samples yet. Jitter and packet loss drive the tunnel health
	// badge alongside RTT and uptime.
	JitterMillis  float64 `json:"jitterMillis"`
	PacketLossPct float64 `json:"packetLossPct"`
	// HealthScore is the green/yellow/red rollup: good|warn|bad|unknown.
	HealthScore string `json:"healthScore,omitempty"`

	// Identity of this machine and the peer, for the GUI's identity badges.
	// LocalHostname is always this machine; the Peer* fields populate once the
	// link is up (empty against a legacy peer that sent no hostname).
	LocalHostname string   `json:"localHostname,omitempty"`
	PeerHostname  string   `json:"peerHostname,omitempty"`
	LocalLANIPs   []string `json:"localLanIps,omitempty"`
	PeerLANIPs    []string `json:"peerLanIps,omitempty"`
	// PublicIP is this machine's public address; PeerPublicIP is the other
	// end's. On the agent, PublicIP is the gateway-observed source address and
	// PeerPublicIP is the configured gateway host; on the gateway they swap.
	PublicIP     string `json:"publicIp,omitempty"`
	PeerPublicIP string `json:"peerPublicIp,omitempty"`
	// PeerHeadless reports the peer said no GUI is attached on its side
	// (Hello/HelloOK Headless), so nobody may be watching for a consent
	// proposal there. Informational; false against a legacy peer.
	PeerHeadless bool `json:"peerHeadless,omitempty"`

	// Gateway-side fields.
	AgentConnected bool `json:"agentConnected,omitempty"`

	// Agents is the per-agent link state on a gateway (empty on the agent
	// role). Sorted by AgentID so the flat legacy Peer* fields (derived from
	// Agents[0] when there is exactly one) are deterministic. Tunnels and
	// Connections stay flat and carry agentId, so the GUI groups them per agent
	// without duplicating them here — keeping Status well under MaxFrame.
	// AgentsTotal is the real count when Agents is clamped to MaxStatusAgents,
	// for the same reason ConnectionsTotal exists below: a GUI that counts the
	// list it was sent reports the cap as the fleet size. Zero from a daemon
	// that predates the field, so callers fall back to len(Agents).
	Agents      []AgentStatus `json:"agents,omitempty"`
	AgentsTotal int           `json:"agentsTotal,omitempty"`

	// Tunnels is every registered tunnel of every agent (the gateway role's
	// list is the fleet's, not one agent's, so it grows as agents ×
	// tunnels-per-agent). Clamped to MaxStatusTunnels with TunnelsTruncated /
	// TunnelsTotal, for the reason Connections is: a list the GUI renders
	// short must say it is short, or it reads as the whole set.
	Tunnels          []TunnelStatus `json:"tunnels,omitempty"`
	TunnelsTruncated bool           `json:"tunnelsTruncated,omitempty"`
	TunnelsTotal     int            `json:"tunnelsTotal,omitempty"`

	// Live proxied connections and lifetime byte totals (both roles).
	// Connections holds at most MaxStatusConns (the newest); when clamped,
	// ConnectionsTruncated is set and ConnectionsTotal carries the real count.
	Connections          []conntrack.Snapshot `json:"connections,omitempty"`
	ConnectionsTruncated bool                 `json:"connectionsTruncated,omitempty"`
	ConnectionsTotal     int                  `json:"connectionsTotal,omitempty"`
	TotalBytesIn         int64                `json:"totalBytesIn"`
	TotalBytesOut        int64                `json:"totalBytesOut"`

	// Control-link/session metadata (both roles). PeerAddr is the other end
	// of the tunnel link: gateway host:port on the agent, agent IP on the
	// gateway.
	LinkUpSinceMs  int64  `json:"linkUpSinceMs"`
	ProcessStartMs int64  `json:"processStartMs"`
	PeerAddr       string `json:"peerAddr,omitempty"`
	LinkBytesIn    int64  `json:"linkBytesIn"`
	LinkBytesOut   int64  `json:"linkBytesOut"`

	// Lifetime aggregates from the persistent stats store.
	AllTimeBytesIn     int64 `json:"allTimeBytesIn"`
	AllTimeBytesOut    int64 `json:"allTimeBytesOut"`
	CumulativeUptimeMs int64 `json:"cumulativeUptimeMs"`
	LinkSessions       int64 `json:"linkSessions"`

	// ConfigPath tells an attached GUI where the daemon's config lives.
	ConfigPath string `json:"configPath,omitempty"`
}

// MaxStatusAgents bounds the per-agent link records in a Status frame, and
// MaxStatusTunnels the flat tunnel list. Both are far beyond the "several
// friends" topology; excess rows are dropped from the status view (they keep
// serving — only the dashboard row is elided), and the Total fields say so.
const (
	MaxStatusAgents  = 24
	MaxStatusTunnels = 128
)

// Worst-case marshalled size of one row of each variable-length list, rounded
// up from a fully-populated example (bracketed IPv6 client address, player
// identity, a long MOTD). They exist only to answer "could this frame
// possibly be too big?" without marshalling on the 2 Hz path, so they must
// over-estimate: an under-estimate would skip the exact check that FitStatus
// relies on. A field added to any of these structs makes them stale in the
// safe direction, and the exact check behind them catches the rest.
const (
	statusBaseBytes   = 1024
	statusConnBytes   = 352
	statusAgentBytes  = 352
	statusTunnelBytes = 448
)

// statusFrameBudget is how much of control.MaxFrame one Status may occupy.
// The slack under the cap covers the envelope WriteMsg wraps it in and leaves
// room for a field landing on Status between releases.
const statusFrameBudget = control.MaxFrame - 2048

// FitStatus clamps a Status so its frame always fits control.MaxFrame, and is
// the single place that promise is kept. Producers build the full picture and
// call this last.
//
// Three lists grow here on independent axes — live players, fleet size, and
// tunnels per agent — so no combination of per-list ceilings bounds their sum;
// dividing MaxFrame three ways only moves the arithmetic into a comment, where
// the next field added to any of these structs silently invalidates it. (That
// is exactly what happened: the ceilings were sized one list at a time, and a
// gateway with a busy public port and a handful of agents produced a status
// frame WriteMsg refused, which tore down the pipe connection on every poll —
// the GUI went blank on precisely the gateway that needed watching.)
//
// So: per-list ceilings for what each surface wants to show, then an exact
// size check that sheds rows until the frame fits. The ceilings are the common
// case and cost one comparison; the exact path marshals only when the
// (deliberately pessimistic) estimate says the frame might not fit.
func FitStatus(s *Status) {
	s.ConnectionsTotal = len(s.Connections)
	if len(s.Connections) > MaxStatusConns {
		// The newest connections win: the snapshot is ID-ascending, and a live
		// list is most useful showing who just arrived.
		s.Connections = s.Connections[len(s.Connections)-MaxStatusConns:]
		s.ConnectionsTruncated = true
	}
	s.TunnelsTotal = len(s.Tunnels)
	if len(s.Tunnels) > MaxStatusTunnels {
		// Tunnels and agents arrive sorted, so keeping the head keeps a stable
		// prefix — the same rows survive poll to poll instead of flickering.
		s.Tunnels = s.Tunnels[:MaxStatusTunnels]
		s.TunnelsTruncated = true
	}
	s.AgentsTotal = len(s.Agents)
	if len(s.Agents) > MaxStatusAgents {
		s.Agents = s.Agents[:MaxStatusAgents]
	}

	if statusEstimate(s) <= statusFrameBudget {
		return // cannot be too large; skip the marshal entirely
	}
	for statusSize(s) > statusFrameBudget && shedStatus(s) {
	}
}

// statusEstimate is the pessimistic upper bound on a Status's marshalled size.
func statusEstimate(s *Status) int {
	return statusBaseBytes +
		len(s.Connections)*statusConnBytes +
		len(s.Agents)*statusAgentBytes +
		len(s.Tunnels)*statusTunnelBytes
}

// statusSize is the exact marshalled size. A failed marshal reports the frame
// as over budget, so the caller sheds rather than trusting a number it could
// not compute.
func statusSize(s *Status) int {
	b, err := json.Marshal(s)
	if err != nil {
		return statusFrameBudget + 1
	}
	return len(b)
}

// shedStatus drops rows from every list at once, so an over-budget frame
// degrades all three surfaces a little rather than blanking one to spare
// another. It reports false when there is nothing left to drop.
func shedStatus(s *Status) bool {
	shed := false
	if n := len(s.Connections); n > 0 {
		s.Connections = s.Connections[n-keepAfterShed(n):]
		s.ConnectionsTruncated = true
		shed = true
	}
	if n := len(s.Tunnels); n > 0 {
		s.Tunnels = s.Tunnels[:keepAfterShed(n)]
		s.TunnelsTruncated = true
		shed = true
	}
	if n := len(s.Agents); n > 0 {
		s.Agents = s.Agents[:keepAfterShed(n)]
		shed = true
	}
	return shed
}

// keepAfterShed is how much of a list survives one shedding round: seven
// eighths, and always at least one row fewer, so the loop terminates.
func keepAfterShed(n int) int {
	if k := n * 7 / 8; k < n {
		return k
	}
	return n - 1
}

// AgentStatus is one connected agent's link state on a gateway: identity, link
// quality, and tunnel/player counts. The tunnel and connection lists live flat
// on Status (keyed by agentId) rather than nested here.
type AgentStatus struct {
	AgentID       string   `json:"agentId"`
	Hostname      string   `json:"hostname,omitempty"`
	LANIPs        []string `json:"lanIps,omitempty"`
	RemoteIP      string   `json:"remoteIp,omitempty"`
	LinkUpSinceMs int64    `json:"linkUpSinceMs,omitempty"`
	RTTMillis     int64    `json:"rttMillis"`
	JitterMillis  float64  `json:"jitterMillis"`
	PacketLossPct float64  `json:"packetLossPct"`
	HealthScore   string   `json:"healthScore"`
	LinkBytesIn   int64    `json:"linkBytesIn"`
	LinkBytesOut  int64    `json:"linkBytesOut"`
	Tunnels       int      `json:"tunnels"` // count of registered tunnels
	Players       int      `json:"players"` // count of identified players
}

// TunnelStatus is one tunnel's live state. The engine's status tick fills it
// from gateway.TunnelSnapshot (gateway role) or the agent's tunnel specs (agent
// role) — field-by-field in both cases, so a new field lands here, in that copy,
// and in its source together or not at all.
type TunnelStatus struct {
	AgentID    string `json:"agentId,omitempty"` // owning agent (gateway role)
	ID         string `json:"id"`
	Name       string `json:"name"`
	PublicPort int    `json:"publicPort,omitempty"` // confirmed bound port
	LocalUp    bool   `json:"localUp"`
	LocalKnown bool   `json:"localKnown"`
	// NeedsLocalAddr distinguishes "the local server is down" from "this tunnel
	// was never pointed at one" — a tunnel the gateway created by id, which the
	// wire has no LocalAddr field to create with. Both report LocalUp=false, but
	// only one of them is fixed by starting a server, so the UI must not offer
	// the same answer for both. Agent role only: the gateway learns health from
	// a bool on the wire and cannot tell the two apart. omitempty, so a legacy
	// daemon's frames are unchanged and read as "not this case".
	NeedsLocalAddr bool `json:"needsLocalAddr,omitempty"`
	// BandwidthLimitMbps/Scope surface a tunnel's configured cap read-only (0 =
	// unlimited); omitempty keeps uncapped tunnels' frames unchanged.
	BandwidthLimitMbps  int    `json:"bandwidthLimitMbps,omitempty"`
	BandwidthLimitScope string `json:"bandwidthLimitScope,omitempty"`
	// Paused reports the tunnel turns players away while holding its port;
	// PausedBy names whose bits cover it from the reporting side's view:
	// "mine" | "theirs" | "both". Both omitempty — a legacy daemon's frames
	// carry neither and the UI's pause controls stay disabled.
	Paused   bool   `json:"paused,omitempty"`
	PausedBy string `json:"pausedBy,omitempty"`
	// Server* carry what the local Minecraft server reported to the agent's
	// mc-status health probe. ServerStatusKnown is the guard, mirroring
	// LocalUp/LocalKnown: without it the counts below are meaningless, and a
	// daemon that predates this field sends neither — so an absent report reads
	// as unknown ("—" in the UI) rather than as an empty server.
	ServerStatusKnown   bool   `json:"serverStatusKnown,omitempty"`
	ServerMOTD          string `json:"serverMotd,omitempty"`
	ServerPlayersOnline int    `json:"serverPlayersOnline,omitempty"`
	ServerPlayersMax    int    `json:"serverPlayersMax,omitempty"`
	ServerVersion       string `json:"serverVersion,omitempty"`
}

// StatusSource produces the current Status snapshot for each request.
type StatusSource func() Status

// Sources bundles everything the pipe can serve. History, Peers, and
// Analytics may be nil (the server answers with empty results).
type Sources struct {
	Status    StatusSource
	History   func(windowMs int64, maxBuckets int) stats.HistoryResult
	Peers     func() []stats.PeerStat
	Analytics func(AnalyticsReq) AnalyticsResp
}
