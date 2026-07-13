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
// to ~340 bytes, so 150 keeps the full status a comfortable margin under
// MaxFrame. The newest connections win; ConnectionsTruncated /
// ConnectionsTotal tell the GUI what it isn't seeing.
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

	// Agent-side fields.
	LinkUp    bool  `json:"linkUp,omitempty"`
	RTTMillis int64 `json:"rttMillis,omitempty"`

	// Link quality (agent-side; the gateway reports -1/unknown). Jitter and
	// packet loss drive the tunnel health badge alongside RTT and uptime.
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

	// Gateway-side fields.
	AgentConnected bool `json:"agentConnected,omitempty"`

	Tunnels []TunnelStatus `json:"tunnels,omitempty"`

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

// TunnelStatus is one tunnel's live state.
type TunnelStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PublicPort int    `json:"publicPort,omitempty"` // confirmed bound port
	LocalUp    bool   `json:"localUp"`
	LocalKnown bool   `json:"localKnown"`
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
