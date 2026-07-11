package mc

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"unicode/utf16"
)

// OfflineInfo is what the offline responder tells clients while a tunnel's
// backend is unreachable.
type OfflineInfo struct {
	// MOTD is shown in the client's server list (and as the disconnect reason
	// if someone tries to join).
	MOTD string
	// VersionName appears in the version column; the responder always reports
	// protocol -1 so every client renders it as "incompatible" text rather
	// than attempting to join silently.
	VersionName string
}

func (o OfflineInfo) motd() string {
	if o.MOTD == "" {
		return "Server offline"
	}
	return o.MOTD
}

func (o OfflineInfo) versionName() string {
	if o.VersionName == "" {
		return "offline"
	}
	return o.VersionName
}

// chat is the minimal JSON chat component.
type chat struct {
	Text string `json:"text"`
}

// StatusResponse is the server-list JSON document.
type StatusResponse struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int `json:"max"`
		Online int `json:"online"`
	} `json:"players"`
	Description chat `json:"description"`
}

// ServeOffline speaks just enough server-side protocol on conn to answer a
// status ping with the offline MOTD, reject a join attempt with the same
// message, or answer a legacy (pre-1.7, 0xFE) ping. The caller owns conn's
// deadlines and closing; ServeOffline returns once the exchange is complete
// or the peer misbehaves.
func ServeOffline(conn net.Conn, info OfflineInfo) error {
	br := bufio.NewReaderSize(conn, 512)
	first, err := br.Peek(1)
	if err != nil {
		return err
	}
	if first[0] == 0xFE {
		return serveLegacyPing(conn, info)
	}

	id, body, err := ReadPacket(br, MaxHandshakePacket)
	if err != nil {
		return err
	}
	h, err := ParseHandshake(id, body)
	if err != nil {
		return err
	}

	switch h.NextState {
	case NextStateStatus:
		return serveStatusPhase(conn, br, info)
	default:
		// Login and transfer both accept a login-phase Disconnect (0x00 with a
		// JSON chat reason) at any point before success.
		reason, err := json.Marshal(chat{Text: info.motd()})
		if err != nil {
			return err
		}
		return WritePacket(conn, 0x00, AppendString(nil, string(reason)))
	}
}

// serveStatusPhase answers one status request and one ping, in either order,
// then returns. Vanilla sends request → ping; some server-list crawlers skip
// the request.
func serveStatusPhase(conn net.Conn, br *bufio.Reader, info OfflineInfo) error {
	for range 2 {
		id, body, err := ReadPacket(br, MaxStatusPacket)
		if err != nil {
			return nil // peer hung up after the response — normal
		}
		switch id {
		case 0x00: // status request
			var resp StatusResponse
			resp.Version.Name = info.versionName()
			resp.Version.Protocol = -1
			resp.Description = chat{Text: info.motd()}
			doc, err := json.Marshal(resp)
			if err != nil {
				return err
			}
			if err := WritePacket(conn, 0x00, AppendString(nil, string(doc))); err != nil {
				return err
			}
		case 0x01: // ping — echo the 8-byte payload back as pong
			if len(body) != 8 {
				return fmt.Errorf("mc: ping payload is %d bytes, want 8", len(body))
			}
			return WritePacket(conn, 0x01, body)
		default:
			return fmt.Errorf("mc: unexpected status-phase packet 0x%02x", id)
		}
	}
	return nil
}

// serveLegacyPing answers a pre-1.7 ping (first byte 0xFE) with the 0xFF
// kick packet in the post-1.4 "§1" format: null-separated fields encoded as
// UTF-16BE. The client renders the MOTD and shows the server as outdated.
func serveLegacyPing(conn net.Conn, info OfflineInfo) error {
	// Legacy MOTDs are plain text with no framing for \0 — strip separators.
	motd := strings.ReplaceAll(info.motd(), "\x00", " ")
	payload := strings.Join([]string{
		"§1", "127", info.versionName(), motd, "0", "0",
	}, "\x00")
	units := utf16.Encode([]rune(payload))
	out := make([]byte, 3+2*len(units))
	out[0] = 0xFF
	binary.BigEndian.PutUint16(out[1:3], uint16(len(units)))
	for i, u := range units {
		binary.BigEndian.PutUint16(out[3+2*i:], u)
	}
	_, err := conn.Write(out)
	return err
}
