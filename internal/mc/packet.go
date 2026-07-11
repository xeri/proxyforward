package mc

import (
	"fmt"
	"io"
)

// Packet size caps by phase. Handshakes are tiny in vanilla but BungeeCord /
// Forge stuff extra data into the server-address field (UUID + skin
// properties), so the handshake cap is generous; status-phase requests are
// empty or 8 bytes, so their cap is tight.
const (
	MaxHandshakePacket = 8192
	MaxStatusPacket    = 64
)

// ReadPacket reads one framed packet — VarInt length, then that many bytes of
// VarInt packet ID + body — rejecting lengths beyond maxSize before
// allocating.
func ReadPacket(r io.Reader, maxSize int32) (id int32, body []byte, err error) {
	length, _, err := ReadVarInt(r)
	if err != nil {
		return 0, nil, err
	}
	if length <= 0 || length > maxSize {
		return 0, nil, fmt.Errorf("mc: packet length %d outside (0, %d]", length, maxSize)
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(r, raw); err != nil {
		return 0, nil, err
	}
	rd := &bodyReader{b: raw}
	id = rd.varint()
	if rd.err != nil {
		return 0, nil, fmt.Errorf("mc: bad packet id: %w", rd.err)
	}
	return id, raw[rd.off:], nil
}

// WritePacket frames and writes one packet.
func WritePacket(w io.Writer, id int32, body []byte) error {
	idBytes := AppendVarInt(nil, id)
	frame := AppendVarInt(nil, int32(len(idBytes)+len(body)))
	frame = append(frame, idBytes...)
	frame = append(frame, body...)
	_, err := w.Write(frame)
	return err
}
