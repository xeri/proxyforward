package mc

// Sniffer passively reconstructs the login handshake from the client→server
// byte stream to learn a player's name, without ever altering or blocking the
// bytes. It is a pure push parser: Feed it each chunk as it flows past, and it
// buffers only until it reaches a verdict, an inspection cap, or a parse
// anomaly — after which it goes inert (Feed returns done and does nothing).
//
// Design contract for the splice tap:
//   - read-only: it copies bytes into its own buffer; the caller forwards the
//     originals untouched.
//   - bounded: at most maxSniffBytes are ever buffered.
//   - fail-open: any malformed or non-Minecraft stream ends the sniff with no
//     outcome; the connection keeps flowing normally.
type Sniffer struct {
	buf   []byte
	state sniffState
	proto int32
	out   SniffOutcome
	haveH bool
}

type sniffState int

const (
	sniffHandshake sniffState = iota // waiting for the handshake packet
	sniffLogin                       // handshake said login; waiting for login start
	sniffDone                        // verdict reached or given up
)

// maxSniffBytes caps total buffering. One generous handshake plus one login
// start (name + signed-chat key/sig) fits comfortably; beyond it we give up.
const maxSniffBytes = MaxHandshakePacket + 4096

// SniffOutcome is what a completed sniff learned. Login is nil when the
// connection was a status ping (NextState 1) or the login packet never
// parsed.
type SniffOutcome struct {
	Handshake Handshake
	Login     *LoginStart
}

// NewSniffer returns a sniffer awaiting the first client bytes.
func NewSniffer() *Sniffer { return &Sniffer{} }

// Feed consumes one client→server chunk. It returns true once the sniffer is
// finished — whether it found a player, saw a non-login handshake, hit the
// byte cap, or gave up on a parse error. Once done, further Feed calls are
// no-ops returning true.
func (s *Sniffer) Feed(p []byte) (done bool) {
	if s.state == sniffDone {
		return true
	}
	// Only buffer up to the cap; excess bytes are irrelevant to the handshake
	// and login packets, which come first.
	if room := maxSniffBytes - len(s.buf); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		s.buf = append(s.buf, p...)
	}
	s.advance()
	if s.state != sniffDone && len(s.buf) >= maxSniffBytes {
		// Buffer full without a verdict: this isn't a login we understand.
		s.give()
	}
	return s.state == sniffDone
}

// Outcome returns what the sniff learned and whether it is meaningful (a
// completed handshake was parsed). Call after Feed reports done.
func (s *Sniffer) Outcome() (SniffOutcome, bool) {
	return s.out, s.haveH
}

// advance parses as far as the buffered bytes allow, without consuming beyond
// complete frames.
func (s *Sniffer) advance() {
	for s.state != sniffDone {
		body, consumed, st := splitFrame(s.buf, MaxHandshakePacket)
		switch st {
		case frameIncomplete:
			return // wait for more bytes
		case frameBad:
			s.give()
			return
		}
		s.buf = s.buf[consumed:]

		id, payload, ok := packetID(body)
		if !ok {
			s.give()
			return
		}
		switch s.state {
		case sniffHandshake:
			h, err := ParseHandshake(id, payload)
			if err != nil {
				s.give()
				return
			}
			s.out.Handshake = *h
			s.haveH = true
			s.proto = h.ProtocolVersion
			if h.NextState != NextStateLogin && h.NextState != NextStateTransfer {
				// Status ping (or unknown intent): nothing more to learn.
				s.state = sniffDone
				return
			}
			s.state = sniffLogin
		case sniffLogin:
			if id != LoginStartID {
				s.give()
				return
			}
			if ls, err := ParseLoginStart(s.proto, payload); err == nil {
				s.out.Login = ls
			}
			s.state = sniffDone
			return
		}
	}
}

// give abandons the sniff, keeping any handshake already parsed but recording
// no further outcome.
func (s *Sniffer) give() {
	s.state = sniffDone
	s.buf = nil
}

type frameStatus int

const (
	frameOK frameStatus = iota
	frameIncomplete
	frameBad
)

// splitFrame returns the body of the first complete length-prefixed packet in
// buf (the bytes after the VarInt length), how many bytes it spans, and a
// status: OK, incomplete (need more bytes), or bad (malformed / over cap).
func splitFrame(buf []byte, maxSize int32) (body []byte, consumed int, st frameStatus) {
	length, n, ok := readVarIntPrefix(buf)
	if !ok {
		if n < 0 {
			return nil, 0, frameBad // over 5 bytes: not a valid VarInt
		}
		return nil, 0, frameIncomplete // ran out of bytes mid-VarInt
	}
	if length <= 0 || length > maxSize {
		return nil, 0, frameBad
	}
	total := n + int(length)
	if len(buf) < total {
		return nil, 0, frameIncomplete
	}
	return buf[n:total], total, frameOK
}

// readVarIntPrefix decodes a VarInt from the front of buf. ok is true on a
// complete value; on failure n is -1 for an over-length (invalid) VarInt and
// the bytes-consumed-so-far for a truncated one (need more input).
func readVarIntPrefix(buf []byte) (val int32, n int, ok bool) {
	var v uint32
	for i := 0; ; i++ {
		if i >= len(buf) {
			return 0, i, false // truncated: caller should wait for more
		}
		b := buf[i]
		v |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return int32(v), i + 1, true
		}
		if i == 4 {
			return 0, -1, false // exceeds 5 bytes: invalid
		}
	}
}

// packetID splits a packet body into its VarInt id and the remaining payload.
func packetID(body []byte) (id int32, payload []byte, ok bool) {
	rd := &bodyReader{b: body}
	id = rd.varint()
	if rd.err != nil {
		return 0, nil, false
	}
	return id, body[rd.off:], true
}
