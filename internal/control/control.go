// Package control defines proxyforward's wire protocol: length-prefixed JSON
// frames carrying typed messages. The same framing is used for the pre-auth
// hello exchange, the in-mux control stream, the per-stream OpenConn header,
// and (later) the GUI↔daemon IPC pipe.
package control

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ProtocolVersion is negotiated in the hello exchange; incompatible peers are
// rejected with ErrCodeVersion before any tunnel state is created. New wire
// features ride on capabilities, not version bumps — bump this only for
// changes that break the hello exchange itself.
const ProtocolVersion = 1

// Capability names. Negotiation rules:
//   - The agent offers its capabilities in Hello; the gateway replies in
//     HelloOK with (offered ∩ supported). Both sides act only on that
//     negotiated set — a capability in HelloOK means the gateway supports
//     and accepted it.
//   - Unknown capability strings MUST be ignored, never treated as an error.
//   - A missing or empty capabilities field means a legacy peer: no
//     capabilities.
const (
	// CapTunnelSync: the peer understands TypeSyncTunnels/TypeSyncResult —
	// full-set desired-state tunnel sync instead of per-tunnel
	// register/unregister frames.
	CapTunnelSync = "tunnel-sync"
	// CapConnStats: the agent accepts TypeConnStats frames carrying the
	// gateway's per-connection RTT measurements (keyed by OpenConn.ConnID) so
	// the agent's GUI and analytics can attribute a real network RTT to each
	// player. Gateway → agent only; a legacy agent that never offers it simply
	// receives no frames.
	CapConnStats = "conn-stats"
)

// SupportedCapabilities is everything this build implements, both roles.
var SupportedCapabilities = []string{CapTunnelSync, CapConnStats}

// IntersectCaps returns offered ∩ supported, preserving supported's order.
// Nil-safe on both arguments; unknown offered strings are simply dropped.
func IntersectCaps(offered, supported []string) []string {
	if len(offered) == 0 || len(supported) == 0 {
		return nil
	}
	offer := make(map[string]struct{}, len(offered))
	for _, c := range offered {
		offer[c] = struct{}{}
	}
	var out []string
	for _, c := range supported {
		if _, ok := offer[c]; ok {
			out = append(out, c)
		}
	}
	return out
}

// CapSet is a fast membership wrapper for a negotiated capability list.
type CapSet map[string]struct{}

func NewCapSet(caps []string) CapSet {
	s := make(CapSet, len(caps))
	for _, c := range caps {
		s[c] = struct{}{}
	}
	return s
}

func (c CapSet) Has(cap string) bool {
	_, ok := c[cap]
	return ok
}

// Frame size limits. PreAuthMaxFrame applies to bytes read from a peer that
// has not authenticated yet (internet scanners hit the control port); frames
// never allocate more than the applicable cap.
const (
	MaxFrame        = 64 * 1024
	PreAuthMaxFrame = 4 * 1024
)

// Message type tags.
const (
	TypeHello      = "hello"
	TypeHelloOK    = "hello_ok"
	TypeHelloErr   = "hello_err"
	TypeRegister   = "register_tunnel"
	TypeRegisterOK = "register_ok"
	TypeRegErr     = "register_err"
	TypeUnregister = "unregister_tunnel"
	TypePing       = "ping"
	TypePong       = "pong"
	TypeHealth     = "health"
	TypeOpenConn   = "open_conn"
	// Desired-state tunnel sync (requires CapTunnelSync).
	TypeSyncTunnels = "sync_tunnels" // agent → gateway: full desired set
	TypeSyncResult  = "sync_result"  // gateway → agent: per-tunnel outcomes
	// Per-connection RTT report, gateway → agent (requires CapConnStats).
	TypeConnStats = "conn_stats"
)

// Hello error codes.
const (
	ErrCodeBadToken      = "bad_token"
	ErrCodeAgentConflict = "agent_conflict"
	ErrCodeVersion       = "version"
)

// Register error codes.
const (
	ErrCodePortInUse      = "port_in_use"
	ErrCodePortNotAllowed = "port_not_allowed"
	ErrCodeBadTunnel      = "bad_tunnel"
)

// Connection kinds carried in Hello. KindData is reserved for the per-conn
// transport mode where each proxied connection dials the control port itself.
const (
	KindControl = "control"
	KindData    = "data"
)

type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Hello is sent by the agent immediately after TLS, before any multiplexing.
type Hello struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Kind            string `json:"kind"` // control | data
	AgentID         string `json:"agentId"`
	Token           string `json:"token"`
	AppVersion      string `json:"appVersion"`
	// ConnID correlates a per-conn-mode data connection with its OpenConn
	// offer; empty on control connections.
	ConnID string `json:"connId,omitempty"`
	// Capabilities the agent offers (see the capability rules above).
	// omitempty keeps frames to legacy gateways byte-identical to v1.
	Capabilities []string `json:"capabilities,omitempty"`
	// Hostname / LocalIPs identify the agent's machine for the GUI's identity
	// badges. Purely informational; both omitempty so frames to/from legacy
	// peers stay byte-identical to v1.
	Hostname string   `json:"hostname,omitempty"`
	LocalIPs []string `json:"localIps,omitempty"`
}

type HelloOK struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Generation      uint64 `json:"generation"`
	AppVersion      string `json:"appVersion"`
	// Capabilities is the negotiated set: offered ∩ gateway-supported.
	Capabilities []string `json:"capabilities,omitempty"`
	// Hostname / LocalIPs identify the gateway's machine for the agent's GUI.
	Hostname string   `json:"hostname,omitempty"`
	LocalIPs []string `json:"localIps,omitempty"`
	// ObservedIP is the agent's public IP as the gateway sees it (the source
	// address of this connection) — the agent has no other way to learn it.
	ObservedIP string `json:"observedIp,omitempty"`
}

type HelloErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// TunnelSpec is what the gateway needs to know about a tunnel; agent-side
// options (PP2, Minecraft awareness, bandwidth caps) stay on the agent.
type TunnelSpec struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // tcp | udp; udp not yet implemented (gateway rejects it)
	// PublicPort 0 asks the gateway to pick an ephemeral port.
	PublicPort int `json:"publicPort"`
	// OfflineMOTD, when set, keeps the public port answering Minecraft status
	// pings with this message while the tunnel's backend is unavailable.
	OfflineMOTD string `json:"offlineMotd,omitempty"`
	// MinecraftAware lets the gateway passively sniff the login handshake to
	// attribute connections to player names. omitempty keeps frames to legacy
	// gateways byte-identical to v1.
	MinecraftAware bool `json:"minecraftAware,omitempty"`
}

type Register struct {
	Tunnel TunnelSpec `json:"tunnel"`
}

type RegisterOK struct {
	TunnelID string `json:"tunnelId"`
	// PublicPort echoes the actual bound port (differs from the request when
	// the agent asked for 0).
	PublicPort int `json:"publicPort"`
}

type RegisterErr struct {
	TunnelID string `json:"tunnelId"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type Unregister struct {
	TunnelID string `json:"tunnelId"`
}

// SyncTunnels replaces the gateway's entire tunnel set for this session with
// the given desired state (CapTunnelSync). Seq correlates the SyncResult; the
// agent ignores results older than its latest sync.
type SyncTunnels struct {
	Seq     uint64       `json:"seq"`
	Tunnels []TunnelSpec `json:"tunnels"`
}

// SyncTunnelResult is one tunnel's outcome within a SyncResult. Code reuses
// the register error codes.
type SyncTunnelResult struct {
	TunnelID string `json:"tunnelId"`
	OK       bool   `json:"ok"`
	// PublicPort echoes the actual bound port when OK (differs from the
	// request when the agent asked for 0).
	PublicPort int    `json:"publicPort,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

type SyncResult struct {
	Seq     uint64             `json:"seq"`
	Results []SyncTunnelResult `json:"results"`
}

type Ping struct {
	Seq          uint64 `json:"seq"`
	SentUnixNano int64  `json:"sentUnixNano"`
}

type Pong struct {
	Seq          uint64 `json:"seq"`
	SentUnixNano int64  `json:"sentUnixNano"` // echoed from the ping
	// RecvUnixNano is the gateway's clock when it received the ping. It lets
	// the agent estimate per-direction one-way latency (up = recv−sent,
	// down = now−recv), which is only meaningful when both clocks are
	// NTP-synced. omitempty keeps legacy pongs (which never set it) identical.
	RecvUnixNano int64 `json:"recvUnixNano,omitempty"`
}

// Health reports the agent's view of a tunnel's local backend; the gateway
// uses it to drive the offline responder.
type Health struct {
	TunnelID string `json:"tunnelId"`
	LocalUp  bool   `json:"localUp"`
}

// OpenConn is the first frame on every data stream, gateway → agent.
type OpenConn struct {
	TunnelID   string `json:"tunnelId"`
	ClientAddr string `json:"clientAddr"`
	// ConnID identifies this proxied connection. It is set in per-conn
	// transport mode so the agent's data dial can be matched to this offer,
	// and always set otherwise so the agent can correlate later TypeConnStats
	// RTT reports (stored as the entry's ConnKey).
	ConnID string `json:"connId,omitempty"`
}

// ConnStat is one connection's measured round-trip time. ConnID matches the
// OpenConn.ConnID the gateway issued for that connection.
type ConnStat struct {
	ConnID string  `json:"c"`
	RttMs  float64 `json:"r"`
}

// ConnStats carries a batch of per-connection RTT measurements, gateway →
// agent (CapConnStats). The gateway chunks reports to at most
// MaxConnStatsPerFrame entries so a frame never approaches MaxFrame.
type ConnStats struct {
	Entries []ConnStat `json:"entries"`
}

// MaxConnStatsPerFrame bounds one conn_stats frame. Each entry is a short id
// plus a number (well under 64 bytes), so 200 keeps the frame far below
// MaxFrame's 64 KiB.
const MaxConnStatsPerFrame = 200

var (
	ErrFrameTooLarge = errors.New("control: frame exceeds size limit")
	ErrUnknownType   = errors.New("control: unknown message type")
)

// WriteMsg frames and writes one message: 4-byte big-endian length + JSON
// envelope.
func WriteMsg(w io.Writer, msgType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("control: marshal %s: %w", msgType, err)
	}
	env, err := json.Marshal(Envelope{Type: msgType, Data: data})
	if err != nil {
		return fmt.Errorf("control: marshal envelope: %w", err)
	}
	if len(env) > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4+len(env))
	binary.BigEndian.PutUint32(buf, uint32(len(env)))
	copy(buf[4:], env)
	_, err = w.Write(buf)
	return err
}

// ReadMsg reads one framed envelope, rejecting frames larger than maxFrame
// before allocating.
func ReadMsg(r io.Reader, maxFrame int) (*Envelope, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > uint32(maxFrame) {
		return nil, ErrFrameTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	var env Envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		return nil, fmt.Errorf("control: bad envelope: %w", err)
	}
	if env.Type == "" {
		return nil, fmt.Errorf("control: envelope missing type")
	}
	return &env, nil
}

// Decode unmarshals an envelope's payload into out.
func Decode[T any](env *Envelope) (*T, error) {
	out := new(T)
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return nil, fmt.Errorf("control: bad %s payload: %w", env.Type, err)
		}
	}
	return out, nil
}
