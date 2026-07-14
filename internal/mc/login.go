package mc

import (
	"fmt"
	"regexp"
)

// Login-state serverbound packet ids.
const LoginStartID = 0x00

// nameRe rejects bodies that decoded to a non-username: a real misparse (or a
// non-Minecraft client that happened to frame like login) never matches, so a
// bad decode fails closed rather than inventing a player.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)

// LoginStart is what we extract from the client's first login packet: the
// username (always present) and, on protocols that carry it, the client's
// self-declared UUID (authoritative only in online mode; treat as a hint).
type LoginStart struct {
	Name    string
	UUID    [16]byte
	HasUUID bool
}

// ParseLoginStart decodes a login-state packet 0x00 body for the handshake's
// protocol version. This packet is always plaintext and uncompressed — it is
// the first thing sent after the handshake switches the connection to the
// login state, before Set Compression or the Encryption Request.
//
// Field layout by protocol (see https://minecraft.wiki/w/Java_Edition_protocol):
//
//	≤758 (≤1.18.2):        Name
//	 759 (1.19):           Name + optional signature data
//	 760 (1.19.1/1.19.2):  Name + optional signature data + optional UUID
//	761–763 (1.19.3–1.20.1):Name + optional UUID
//	≥764 (≥1.20.2):        Name + UUID
//
// The username is the only field we require; UUID extraction is best-effort,
// so a layout quirk in a modded client never costs us the name.
func ParseLoginStart(protocol int32, body []byte) (*LoginStart, error) {
	rd := &bodyReader{b: body}
	name := rd.string(16)
	if rd.err != nil {
		return nil, fmt.Errorf("mc: bad login start: %w", rd.err)
	}
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("mc: login start name %q is not a valid username", name)
	}
	ls := &LoginStart{Name: name}

	switch {
	case protocol >= 764:
		// Name + required UUID.
		ls.readUUID(rd)
	case protocol >= 761:
		// Name + optional UUID (boolean-prefixed).
		if rd.boolean() {
			ls.readUUID(rd)
		}
	case protocol == 760:
		// Name + optional signature data + optional UUID.
		skipSignatureData(rd)
		if rd.boolean() {
			ls.readUUID(rd)
		}
	case protocol == 759:
		// Name + optional signature data (no UUID field).
		skipSignatureData(rd)
	default:
		// ≤758: name only.
	}
	return ls, nil
}

// readUUID reads a 16-byte UUID if the reader has one; a truncated or absent
// UUID leaves HasUUID false rather than erroring (the name already succeeded).
func (ls *LoginStart) readUUID(rd *bodyReader) {
	if u, ok := rd.uuid(); ok {
		ls.UUID, ls.HasUUID = u, true
	}
}

// skipSignatureData consumes the 1.19–1.19.2 chat-signing block:
// Boolean hasSig, and when set { Long expiry, ByteArray publicKey, ByteArray
// signature }. Best-effort — on any anomaly the reader's sticky error stops
// further reads and the caller keeps just the name.
func skipSignatureData(rd *bodyReader) {
	if !rd.boolean() {
		return
	}
	rd.skip(8)                    // expiry timestamp (Long)
	rd.skipByteArray(maxSigField) // public key
	rd.skipByteArray(maxSigField) // signature
}

// maxSigField caps the signed-chat public key / signature byte arrays. Real
// values are a few hundred bytes; the cap only bounds a hostile length.
const maxSigField = 8192
