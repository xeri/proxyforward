package mc

import (
	"errors"
	"fmt"
)

// Next-state values from the handshake packet.
const (
	NextStateStatus   = 1
	NextStateLogin    = 2
	NextStateTransfer = 3 // 1.20.5+ server transfer
)

// maxServerAddress is deliberately larger than the vanilla 255-char limit:
// BungeeCord IP-forwarding and Forge (FML markers) append \0-separated extra
// data to the address field, and rejecting those would break modded clients
// behind proxies.
const maxServerAddress = 4096

// Handshake is the first packet of every modern connection (packet 0x00 in
// the handshaking state).
type Handshake struct {
	ProtocolVersion int32
	ServerAddress   string
	ServerPort      uint16
	NextState       int32
}

var ErrNotHandshake = errors.New("mc: packet is not a handshake")

// ParseHandshake decodes a handshake from a packet's id + body.
func ParseHandshake(id int32, body []byte) (*Handshake, error) {
	if id != 0x00 {
		return nil, fmt.Errorf("%w: packet id 0x%02x", ErrNotHandshake, id)
	}
	rd := &bodyReader{b: body}
	h := &Handshake{
		ProtocolVersion: rd.varint(),
		ServerAddress:   rd.string(maxServerAddress),
		ServerPort:      rd.uint16(),
		NextState:       rd.varint(),
	}
	if rd.err != nil {
		return nil, fmt.Errorf("mc: bad handshake: %w", rd.err)
	}
	if rd.remaining() != 0 {
		return nil, fmt.Errorf("mc: bad handshake: %d trailing bytes", rd.remaining())
	}
	switch h.NextState {
	case NextStateStatus, NextStateLogin, NextStateTransfer:
	default:
		return nil, fmt.Errorf("mc: bad handshake: unknown next state %d", h.NextState)
	}
	return h, nil
}
