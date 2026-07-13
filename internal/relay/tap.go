package relay

// TapConn wraps a Conn and hands each inbound Read's payload to a tap
// function until the tap signals it is finished, after which reads revert to
// zero overhead. The bytes themselves pass through unchanged — the tap only
// observes — so a splice over a TapConn is byte-for-byte identical to one over
// the raw conn. This is how the Minecraft sniffer watches the client's login
// packets without altering the stream.
type TapConn struct {
	Conn
	tap  func([]byte) bool // returns true when it wants no more bytes
	done bool
}

// NewTap returns c wrapped so that tap sees each chunk this side reads until
// tap returns true. tap must not retain or mutate the slice it is handed
// beyond the call; the sniffer copies what it needs.
func NewTap(c Conn, tap func([]byte) bool) *TapConn {
	return &TapConn{Conn: c, tap: tap}
}

// Read reads from the underlying conn and, while tapping is active, shows the
// freshly read bytes to the tap. It never changes n, the bytes, or err.
func (t *TapConn) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)
	if !t.done && n > 0 {
		if t.tap(p[:n]) {
			t.done = true
		}
	}
	return n, err
}
