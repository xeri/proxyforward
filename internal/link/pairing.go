package link

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// PairingCode is the single line a gateway shows and an agent pastes:
//
//	pxf://host:8474/v2/pair/k3m7q-x2vn8-p4rt6-9wzhd-2xkb4-m7q3n
//
// The /v2/ segment versions the code's shape independently of the wire protocol
// (bumped only when the layout itself changes), and the /pair/ segment is a role
// marker so a pasted code — or a pxf:// deep link the OS hands the app — is
// recognizably a gateway pairing invite; a wrong-role paste or an unknown pxf://
// link kind fails with a clear message instead of half-parsing. Host may be a DNS
// name (preferred — survives gateway IP changes) or an IP; IPv6 literals are
// bracketed.
//
// The v2 payload carries the cert pin, the enrollment ticket and a checksum in one
// grouped base32 field — see v2 payload below for the widths and why each is what
// it is. The fields here stay the *rendered* forms both ends already store
// ("sha256:<hex>", "tkt_<hex>"), so the struct remains comparable and every caller
// is unchanged; the wire form follows from their widths.
//
// v1 — a 64-hex fingerprint in a URL fragment and a free-form token — still parses,
// because a v1 gateway's codes may be live when this app meets one.
type PairingCode struct {
	Host        string
	Port        int
	Token       string
	Fingerprint string // "sha256:<hex>"
}

const (
	// pairingScheme is the URI scheme; it doubles as the OS deep-link protocol, so
	// a clicked pxf:// link opens the app straight into pairing.
	pairingScheme = "pxf"
	// pairingFormat versions the pxf:// path shape, independent of the wire
	// ProtocolVersion — see the version-axes table in the identity design. v2 is
	// what this app emits; v1 is still understood.
	pairingFormat   = "v2"
	pairingFormatV1 = "v1"
	// pairingKindPair marks a pxf:// link as a gateway→agent pairing invite, told
	// apart from any future link kind by the path's role segment.
	pairingKindPair = "pair"
	// maxPairingCodeLen bounds the input before any parsing. A pxf:// deep link is
	// attacker-reachable (a web page can fire one); a real code is well under this.
	maxPairingCodeLen = 512
)

// The v2 payload: 150 bits, rendered as 30 Crockford base32 symbols in six
// hyphenated groups of five — the unit a human reads aloud, types, and checks off.
// Widths are the security parameters of the pairing act, and each is sized against
// the attack that actually applies to it:
//
//   - The pin is 80 bits of sha256(cert DER). Trust is a pin, so the attacker must
//     find a *second* certificate whose hash agrees with the gateway's on those
//     bits. The target is fixed — the gateway generates its own cert, and nothing
//     an attacker sends influences it — so this is a preimage search at 2^80, not
//     a birthday collision at 2^40, and every trial costs a P-256 keygen and a
//     self-sign rather than a bare hash. That is the same bar as
//     agentFingerprintBytes, reached the same way. The pin also only has to hold
//     until the first handshake: the agent learns the full fingerprint there and
//     stores that (see AgentTLSConfig / PinnedPeer).
//   - The ticket is 56 bits. Unlike the pin it can only be *guessed*, online,
//     against a gateway that rate-limits per source network (limits.go
//     authFailNets) and bounds the unauthenticated set outright (preAuthGate).
//     At an absurd thousand guesses a second that is millions of years.
//   - The checksum is 14 bits over the whole code, host and port included. It buys
//     nothing against an attacker — it is for the human: a mistyped character is
//     a verdict in the paste box instead of a connection that fails a minute later
//     somewhere else. Roughly one damaged code in 16384 slips past it, and that
//     one fails at the gateway as it always did.
const (
	v2PinBytes    = 10
	v2TicketBytes = 7
	v2SumBits     = 14
	// v2Symbols is (80 + 56 + 14) / 5 — the payload is a whole number of base32
	// symbols by construction, and a whole number of groups.
	v2Symbols  = (v2PinBytes*8 + v2TicketBytes*8 + v2SumBits) / 5
	v2GroupLen = 5
)

// IsPairingURL reports whether s carries the pxf:// pairing scheme, a cheap sniff
// (no full validation) used to route an OS deep-link launch into the pairing UI —
// which then surfaces any parse error itself, rather than the link being silently
// dropped.
func IsPairingURL(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), pairingScheme+"://")
}

// v2Parts reports whether this code's rendered parts are exactly the widths the v2
// payload packs, and returns their raw bytes. The widths are the discriminator: a
// v1 fingerprint is 32 bytes, never 10, so no code is ambiguous and String needs no
// version field to keep the round trip a fixed point.
func (p PairingCode) v2Parts() (pin, ticket []byte, ok bool) {
	if !strings.HasPrefix(p.Fingerprint, "sha256:") || !strings.HasPrefix(p.Token, enrollTicketPrefix) {
		return nil, nil, false
	}
	rawPin, err := hex.DecodeString(strings.TrimPrefix(p.Fingerprint, "sha256:"))
	if err != nil || len(rawPin) != v2PinBytes {
		return nil, nil, false
	}
	rawTicket, err := hex.DecodeString(strings.TrimPrefix(p.Token, enrollTicketPrefix))
	if err != nil || len(rawTicket) != v2TicketBytes {
		return nil, nil, false
	}
	return rawPin, rawTicket, true
}

func (p PairingCode) String() string {
	format, path := pairingFormatV1, p.Token
	fragment := p.Fingerprint
	if pin, ticket, ok := p.v2Parts(); ok {
		format, path, fragment = pairingFormat, v2Payload(p.Host, p.Port, pin, ticket), ""
	}
	u := url.URL{
		Scheme:   pairingScheme,
		Host:     net.JoinHostPort(p.Host, strconv.Itoa(p.Port)),
		Path:     "/" + format + "/" + pairingKindPair + "/" + path,
		Fragment: fragment,
	}
	return u.String()
}

// ParsePairingCode parses and fully validates a pxf:// pairing code, v2 or v1.
func ParsePairingCode(s string) (PairingCode, error) {
	s = strings.TrimSpace(s)
	if len(s) > maxPairingCodeLen {
		return PairingCode{}, fmt.Errorf("pairing code is implausibly long (%d bytes) — re-copy it from the gateway", len(s))
	}
	u, err := url.Parse(s)
	if err != nil {
		return PairingCode{}, fmt.Errorf("not a valid pairing code: %w", err)
	}
	if u.Scheme != pairingScheme {
		// No scheme at all is the common paste error — a fragment of the code, or
		// the wrong clipboard entirely. url.Parse reports that as an empty scheme,
		// and quoting "" back at someone who typed something is a riddle, not a
		// diagnosis. Name the shape they need instead.
		if u.Scheme == "" {
			return PairingCode{}, fmt.Errorf("that does not look like a pairing code — paste the whole %s://… line the gateway printed", pairingScheme)
		}
		return PairingCode{}, fmt.Errorf("not a proxyforward pairing code (expected %s://, got %q)", pairingScheme, u.Scheme)
	}
	format, payload, err := partsFromPath(u.Path)
	if err != nil {
		return PairingCode{}, err
	}

	host := u.Hostname()
	if host == "" {
		return PairingCode{}, fmt.Errorf("pairing code is missing the gateway host")
	}
	if !pairingHostOK(host) {
		return PairingCode{}, fmt.Errorf("pairing code has an invalid gateway host %q — write an IPv6 address in brackets, as pxf://[2001:db8::1]:8474/…", host)
	}
	portStr := u.Port()
	if portStr == "" {
		return PairingCode{}, fmt.Errorf("pairing code is missing the gateway control port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return PairingCode{}, fmt.Errorf("pairing code has an invalid port %q", portStr)
	}

	if format == pairingFormat {
		pin, ticket, err := parseV2Payload(host, port, payload)
		if err != nil {
			return PairingCode{}, err
		}
		return PairingCode{
			Host:        host,
			Port:        port,
			Token:       enrollTicketPrefix + hex.EncodeToString(ticket),
			Fingerprint: "sha256:" + hex.EncodeToString(pin),
		}, nil
	}

	fp := u.Fragment
	if !strings.HasPrefix(fp, "sha256:") || len(fp) != len("sha256:")+64 {
		return PairingCode{}, fmt.Errorf("pairing code is missing a valid sha256 certificate fingerprint")
	}
	for _, c := range fp[len("sha256:"):] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return PairingCode{}, fmt.Errorf("pairing code fingerprint contains non-hex characters")
		}
	}
	return PairingCode{Host: host, Port: port, Token: payload, Fingerprint: strings.ToLower(fp)}, nil
}

// pairingHostOK reports whether host is something a pairing code may carry — and
// therefore also something String can put back, because a PairingCode is a value
// the app stores, displays and hands on, and one that decays on re-emission is
// one that could name a different gateway on the next hop than the one shown.
//
// Two shapes qualify. An IP literal: String brackets a v6 one and url.Parse
// accepts it back. A *zoned* one does not — fe80::1%eth0 names an interface on
// the machine that minted the code, which is meaningless on the machine that
// pastes it, and netip's zone is not carried through a URL authority anyway.
//
// Otherwise a DNS name, spelled with the characters DNS names are spelled with.
// That set is not arbitrary tidiness: it is exactly what url.URL.String leaves
// unescaped in an authority and does not read as a host/port separator, which is
// what makes parse∘String a fixed point. An internationalized name travels in its
// punycode (xn--) form, which is ASCII and reads the same on both machines —
// unlike a raw unicode host, which a human is being asked to eyeball as the trust
// anchor for a whole gateway.
//
// Without this check url.Parse's lenient authority split had the last word, and
// it cuts an unbracketed IPv6 literal at its final colon: pxf://2001:db8::1/…
// parsed as host "2001:db8:" on port 1, and pxf://::1/… as host ":" on port 1.
// Neither is the gateway the operator copied, and neither said so.
// Found by FuzzParsePairingCode; regression TestPairingRejectsAHostItCannotReEmit.
func pairingHostOK(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == ""
	}
	for _, c := range host {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '.', c == '_':
		default:
			return false
		}
	}
	return true
}

// partsFromPath validates the /<version>/<kind>/<payload> path of a pxf:// code and
// returns the version and payload. A wrong version or role marker fails loudly
// rather than silently accepting a stale-format or wrong-kind link.
func partsFromPath(path string) (format, payload string, err error) {
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segs) != 3 {
		return "", "", fmt.Errorf("pairing code path must be /%s/%s/<code>", pairingFormat, pairingKindPair)
	}
	if segs[0] != pairingFormat && segs[0] != pairingFormatV1 {
		return "", "", fmt.Errorf("unsupported pairing code version %q (this app understands %q and %q) — update the app or re-copy the code", segs[0], pairingFormat, pairingFormatV1)
	}
	if segs[1] != pairingKindPair {
		return "", "", fmt.Errorf("not a pairing invite (expected a %q link, got %q)", pairingKindPair, segs[1])
	}
	if segs[2] == "" {
		return "", "", fmt.Errorf("pairing code is missing the code itself")
	}
	return segs[0], segs[2], nil
}

// v2Payload renders pin ‖ ticket ‖ checksum as 30 Crockford base32 symbols in
// groups of five. The alphabet is the one agt_/gw_ ids already wear (cred.go
// crockfordLower): no i, l, o or u, so nothing a human transcribes is ambiguous.
func v2Payload(host string, port int, pin, ticket []byte) string {
	var (
		out strings.Builder
		acc uint32
		n   uint
	)
	out.Grow(v2Symbols + v2Symbols/v2GroupLen)
	emit := func(v uint32, bits uint) {
		acc = acc<<bits | v
		for n += bits; n >= 5; {
			n -= 5
			if out.Len() > 0 && (out.Len()+1)%(v2GroupLen+1) == 0 {
				out.WriteByte('-')
			}
			out.WriteByte(crockfordLower[(acc>>n)&31])
		}
	}
	for _, b := range pin {
		emit(uint32(b), 8)
	}
	for _, b := range ticket {
		emit(uint32(b), 8)
	}
	emit(uint32(v2Checksum(host, port, pin, ticket)), v2SumBits)
	return out.String()
}

// parseV2Payload undoes v2Payload: normalize what was typed, unpack the bits, and
// hold the checksum against the host and port the code arrived with. A checksum
// failure is its own error — "you mistyped it" and "that is not a pairing code"
// are different problems with different fixes, and the paste box says which.
func parseV2Payload(host string, port int, payload string) (pin, ticket []byte, err error) {
	sym := normalizeV2Payload(payload)
	if len(sym) != v2Symbols {
		return nil, nil, fmt.Errorf("pairing code should be %d characters in %d groups of %d; this one has %d — re-copy the whole line", v2Symbols, v2Symbols/v2GroupLen, v2GroupLen, len(sym))
	}
	var (
		raw = make([]byte, 0, v2PinBytes+v2TicketBytes+1)
		acc uint32
		n   uint
	)
	for _, c := range sym {
		v := strings.IndexRune(crockfordLower, c)
		if v < 0 {
			return nil, nil, fmt.Errorf("pairing code contains %q, which is not one of its characters — re-copy it from the gateway", string(c))
		}
		acc = acc<<5 | uint32(v)
		if n += 5; n >= 8 {
			n -= 8
			raw = append(raw, byte(acc>>n))
		}
	}
	// 150 bits is 18 whole bytes and 6 left over: the last emitted byte holds the
	// checksum's top 8 bits and the remainder holds its low 6.
	pin, ticket = raw[:v2PinBytes], raw[v2PinBytes:v2PinBytes+v2TicketBytes]
	sum := uint16(raw[len(raw)-1])<<6 | uint16(acc&0x3f)
	if sum != v2Checksum(host, port, pin, ticket) {
		return nil, nil, fmt.Errorf("that pairing code has a typo — one character is off, or the address was edited; re-copy the whole line from the gateway")
	}
	return pin, ticket, nil
}

// normalizeV2Payload folds the ways a human retypes a code back onto the one the
// gateway emitted: any case, any grouping, and Crockford's confusable letters
// (i and l are 1, o is 0). The fold is lossless because the alphabet excludes
// exactly those letters, so nothing legitimate is rewritten.
func normalizeV2Payload(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	for _, c := range strings.ToLower(s) {
		switch c {
		case '-', ' ', '\t', '_':
		case 'i', 'l':
			out.WriteByte('1')
		case 'o':
			out.WriteByte('0')
		default:
			out.WriteRune(c)
		}
	}
	return out.String()
}

// v2Checksum is the top v2SumBits of a hash over the whole code — host and port
// included, so a code whose address was mistyped or edited fails locally instead
// of dialing a machine the gateway never named. The host folds to lowercase
// because DNS does, and a code typed in a different case is the same code.
func v2Checksum(host string, port int, pin, ticket []byte) uint16 {
	h := sha256.New()
	h.Write([]byte("proxyforward-pair-v2\x00"))
	h.Write([]byte(strings.ToLower(host)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(port)))
	h.Write([]byte{0})
	h.Write(pin)
	h.Write(ticket)
	sum := h.Sum(nil)
	return (uint16(sum[0])<<6 | uint16(sum[1])>>2) & (1<<v2SumBits - 1)
}
