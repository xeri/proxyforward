package mc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

// Client-side Server List Ping. This is the only place in the tree that speaks
// the Minecraft protocol as a *client*: everything else either sniffs a client's
// bytes in passing (sniffer.go) or answers as a server (status.go). It exists so
// a tunnel's health probe can be an actual question — "are you a Minecraft server
// and are you serving?" — instead of a bare TCP dial, which a listening socket
// that will never answer passes just as well as a healthy server.
const (
	// maxStatusResponse bounds the JSON document a server may return, checked
	// before the buffer is sized (ReadPacket's contract). Vanilla responses are a
	// few hundred bytes; a large modded server with a base64 favicon can reach
	// tens of KiB. The cap exists because the peer is untrusted — a tunnel's local
	// address is whatever the operator typed.
	maxStatusResponse = 256 << 10
	// statusPingProtocol is the protocol version the handshake claims. -1 is the
	// conventional "just pinging, not joining" value: the server echoes its own
	// version either way, and no server refuses a status handshake over it.
	statusPingProtocol = -1
)

// StatusInfo is what a server told us about itself. PlayersOnline/PlayersMax are
// -1 when the server omitted them — the repo's unknown sentinel, never a fake 0.
type StatusInfo struct {
	MOTD          string
	PlayersOnline int
	PlayersMax    int
	Version       string
	Protocol      int
}

// statusDoc is the subset of the status JSON we read. Everything else in the
// document (favicon, sample player list, mod metadata) is deliberately ignored:
// this is a health probe, not a server browser.
type statusDoc struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players *struct {
		Max    int `json:"max"`
		Online int `json:"online"`
	} `json:"players"`
	Description description `json:"description"`
}

// description flattens the three shapes a MOTD arrives in — a bare string, a
// chat component with "text", or one with nested "extra" parts — into plain
// text. A server that sends a shape we cannot read is not unhealthy, so this
// never fails the probe; it yields "".
type description struct{ Text string }

func (d *description) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		d.Text = s
		return nil
	}
	var c chatComponent
	if err := json.Unmarshal(b, &c); err != nil {
		return nil // unreadable MOTD: not an error, just nothing to show
	}
	d.Text = c.flatten()
	return nil
}

type chatComponent struct {
	Text  string          `json:"text"`
	Extra []chatComponent `json:"extra"`
}

func (c chatComponent) flatten() string {
	var sb strings.Builder
	sb.WriteString(c.Text)
	for _, e := range c.Extra {
		sb.WriteString(e.flatten())
	}
	return sb.String()
}

// stripFormatting removes legacy §-codes. The MOTD is display text and this is
// the one place that knows its encoding, so callers never each invent their own.
func stripFormatting(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '§' {
			i++ // and the code character with it; a trailing bare § is malformed
			continue
		}
		sb.WriteRune(runes[i])
	}
	return sb.String()
}

// StatusPing asks a Minecraft server for its status: handshake (next state
// "status") then a status request, reading the JSON response. timeout bounds the
// whole exchange, dial included — a probe that hangs is a probe that never
// reports, and the health checker is on a tick.
//
// An error means "this is not a Minecraft server answering right now", which is
// exactly what a health probe wants to know: a socket that accepts and says
// nothing fails here where a bare TCP dial would call it healthy.
func StatusPing(ctx context.Context, addr string, timeout time.Duration) (*StatusInfo, error) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	// One deadline on the whole exchange, not per read: the caller's budget is
	// for an answer, and a server that dribbles bytes forever is not answering.
	_ = conn.SetDeadline(time.Now().Add(timeout))

	host, port, err := splitHostPort(addr)
	if err != nil {
		return nil, err
	}
	hs := AppendVarInt(nil, statusPingProtocol)
	hs = AppendString(hs, host)
	hs = append(hs, byte(port>>8), byte(port))
	hs = AppendVarInt(hs, NextStateStatus)
	if err := WritePacket(conn, 0x00, hs); err != nil {
		return nil, fmt.Errorf("mc: send handshake: %w", err)
	}
	if err := WritePacket(conn, 0x00, nil); err != nil { // status request
		return nil, fmt.Errorf("mc: send status request: %w", err)
	}

	id, body, err := ReadPacket(conn, maxStatusResponse)
	if err != nil {
		return nil, fmt.Errorf("mc: read status response: %w", err)
	}
	if id != 0x00 {
		return nil, fmt.Errorf("mc: status response has packet id 0x%02x, want 0x00", id)
	}
	rd := &bodyReader{b: body}
	doc := rd.string(maxStatusResponse)
	if rd.err != nil {
		return nil, fmt.Errorf("mc: bad status response: %w", rd.err)
	}

	return parseStatusDoc(doc)
}

// parseStatusDoc turns the server's JSON into a StatusInfo. Split out because it
// is the one parser here that reads bytes a *server* chose, and the peer is
// whatever is listening on the operator-typed local address — so it is fuzzed
// (FuzzStatusPingResponse) like every other untrusted parser in this package.
func parseStatusDoc(doc string) (*StatusInfo, error) {
	var parsed statusDoc
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		return nil, fmt.Errorf("mc: status response is not the expected JSON: %w", err)
	}
	info := &StatusInfo{
		MOTD:          stripFormatting(parsed.Description.Text),
		PlayersOnline: -1,
		PlayersMax:    -1,
		Version:       parsed.Version.Name,
		Protocol:      parsed.Version.Protocol,
	}
	if parsed.Players != nil {
		info.PlayersOnline, info.PlayersMax = parsed.Players.Online, parsed.Players.Max
	}
	return info, nil
}

// splitHostPort pulls the handshake's declared address apart. The server echoes
// nothing back from it, but proxies (Velocity, Bungee) route on it, so sending
// the address actually dialled is what makes this work behind one.
func splitHostPort(addr string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("mc: bad address %q: %w", addr, err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("mc: bad port in %q", addr)
	}
	return host, uint16(port), nil
}
