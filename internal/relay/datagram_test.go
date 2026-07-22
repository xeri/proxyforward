package relay

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// udpPair returns a connected UDP socket and the "server" socket it talks to.
func udpPair(t *testing.T) (client *net.UDPConn, server *net.UDPConn) {
	t.Helper()
	srv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	cli, err := net.DialUDP("udp", nil, srv.LocalAddr().(*net.UDPAddr))
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close(); srv.Close() })
	return cli, srv
}

// byteConn is a Conn over a fixed byte slice, for exercising the deframer
// without a socket. Writes are discarded; only the read path is under test.
type byteConn struct {
	Conn
	r *bytes.Reader
}

func (b *byteConn) Read(p []byte) (int, error)      { return b.r.Read(p) }
func (b *byteConn) Write(p []byte) (int, error)     { return len(p), nil }
func (b *byteConn) SetReadDeadline(time.Time) error { return nil }

// TestDatagramStreamPreservesBoundaries is the whole point of the framing (D1):
// a byte pump across a reliable stream would deliver two back-to-back datagrams
// as one, which no UDP protocol survives — RakNet would see a malformed packet
// and drop the session. Each Read must return exactly one datagram, whatever
// its size and however many are already buffered ahead of it.
func TestDatagramStreamPreservesBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		sizes []int
	}{
		{"single small", []int{1}},
		{"back to back", []int{7, 7, 7}},
		{"mixed sizes", []int{1, 1400, 32, 9000, 2}},
		{"typical game traffic", []int{1200, 1200, 1200, 1200}},
		{"maximum datagram", []int{MaxDatagram}},
		{"max then small", []int{MaxDatagram, 4}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, b := tcpPair(t)
			defer a.Close()
			defer b.Close()
			w := &DatagramStream{Conn: a}
			r := &DatagramStream{Conn: b}

			// Send every datagram first, so they are certainly coalesced in the
			// stream's buffer by the time the reader looks.
			want := make([][]byte, len(tc.sizes))
			go func() {
				for i, n := range tc.sizes {
					payload := bytes.Repeat([]byte{byte(i + 1)}, n)
					want[i] = payload
					if _, err := w.Write(payload); err != nil {
						return
					}
				}
			}()

			buf := make([]byte, BufSize)
			for i, n := range tc.sizes {
				r.SetReadDeadline(time.Now().Add(5 * time.Second))
				got, err := r.Read(buf)
				if err != nil {
					t.Fatalf("datagram %d: read: %v", i, err)
				}
				if got != n {
					t.Fatalf("datagram %d: read %d bytes, want exactly %d (boundary lost)", i, got, n)
				}
				if buf[0] != byte(i+1) {
					t.Fatalf("datagram %d: payload marker %d, want %d (wrong datagram)", i, buf[0], i+1)
				}
			}
		})
	}
}

// TestDatagramStreamResumesAfterDeadlineMidDatagram covers the desync a naive
// deframer has (D2). Splice arms a read deadline on every leg, so it can and
// will fire between a datagram's length prefix and its payload. A reader that
// forgot the partial datagram would parse the payload's first two bytes as the
// next length — and every datagram after that is garbage, permanently, with no
// error to say so. The partial must be retained and resumed.
func TestDatagramStreamResumesAfterDeadlineMidDatagram(t *testing.T) {
	a, b := tcpPair(t)
	defer a.Close()
	defer b.Close()
	r := &DatagramStream{Conn: b}
	payload := []byte("resumed intact")

	// Header only: the reader now knows a datagram is coming but has none of it.
	var hdr [datagramHdr]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(payload)))
	if _, err := a.Write(hdr[:]); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, BufSize)
	r.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, err := r.Read(buf); err == nil {
		t.Fatal("read returned before the payload arrived; a partial datagram must not be delivered")
	} else if ne := net.Error(nil); !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("want a timeout while the datagram is incomplete, got %v", err)
	}

	// The rest of the datagram, plus a whole second one right behind it: if the
	// resume mis-tracked its offset, this is where the stream desynchronises.
	if _, err := a.Write(payload); err != nil {
		t.Fatal(err)
	}
	second := &DatagramStream{Conn: a}
	if _, err := second.Write([]byte("and the next one too")); err != nil {
		t.Fatal(err)
	}

	r.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("resumed read: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("resumed read got %q, want %q", buf[:n], payload)
	}
	n, err = r.Read(buf)
	if err != nil {
		t.Fatalf("following read: %v", err)
	}
	if string(buf[:n]) != "and the next one too" {
		t.Fatalf("stream desynchronised after resume: got %q", buf[:n])
	}
}

// TestDatagramStreamRejectsOversizeLength proves the length is checked before
// the buffer is sized (D3). The 2-byte prefix can express 65535, above
// MaxDatagram, so a peer can always name a number this side must refuse rather
// than allocate for.
func TestDatagramStreamRejectsOversizeLength(t *testing.T) {
	raw := []byte{0xFF, 0xFF} // 65535 > MaxDatagram
	r := &DatagramStream{Conn: &byteConn{r: bytes.NewReader(raw)}}
	if _, err := r.Read(make([]byte, BufSize)); err == nil {
		t.Fatal("oversize length accepted")
	} else if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want an explicit maximum-exceeded error, got %v", err)
	}
}

// TestDatagramStreamSkipsEmptyDatagram pins the documented deviation (D4):
// Splice acts only on n > 0, so a zero-length datagram cannot ride it. It must
// be skipped and the *next* datagram delivered — never mistaken for EOF, which
// would tear down a live session.
func TestDatagramStreamSkipsEmptyDatagram(t *testing.T) {
	var raw []byte
	raw = append(raw, 0x00, 0x00) // empty datagram
	raw = append(raw, 0x00, 0x03) // then a real one
	raw = append(raw, 'a', 'b', 'c')
	r := &DatagramStream{Conn: &byteConn{r: bytes.NewReader(raw)}}

	buf := make([]byte, BufSize)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "abc" {
		t.Fatalf("got %q, want the datagram following the empty one", buf[:n])
	}
}

// TestSplicePreservesDatagramBoundariesEndToEnd is the integration proof (D5):
// the unmodified Splice, with a framed tunnel leg on one side and a real UDP
// socket on the other, reproduces datagram semantics. This is what lets UDP
// tunnels exist without touching the TCP hot path.
func TestSplicePreservesDatagramBoundariesEndToEnd(t *testing.T) {
	// tunnelGW ── framed stream ── tunnelAgent ── Splice ── UDP ── server
	tunnelGW, tunnelAgent := tcpPair(t)
	defer tunnelGW.Close()
	local, server := udpPair(t)

	go func() {
		_ = Splice(&DatagramStream{Conn: tunnelAgent}, DatagramConn{local}, nil, SpliceOpts{})
	}()

	gw := &DatagramStream{Conn: tunnelGW}
	sent := [][]byte{
		[]byte("first"),
		bytes.Repeat([]byte{0xAB}, 1400),
		[]byte("third"),
	}
	for _, p := range sent {
		if _, err := gw.Write(p); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	buf := make([]byte, BufSize)
	for i, want := range sent {
		server.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _, err := server.ReadFrom(buf)
		if err != nil {
			t.Fatalf("datagram %d: %v", i, err)
		}
		if !bytes.Equal(buf[:n], want) {
			t.Fatalf("datagram %d: got %d bytes, want %d (boundaries lost through Splice)", i, n, len(want))
		}
	}
}

// TestSpliceReturnsDatagramsFromTheServer covers the reverse direction (D6):
// the server's replies must arrive on the tunnel leg framed, one per datagram.
func TestSpliceReturnsDatagramsFromTheServer(t *testing.T) {
	tunnelGW, tunnelAgent := tcpPair(t)
	defer tunnelGW.Close()
	local, server := udpPair(t)

	go func() {
		_ = Splice(&DatagramStream{Conn: tunnelAgent}, DatagramConn{local}, nil, SpliceOpts{})
	}()

	// The server can only reply once it knows the client's address, so prime it.
	gw := &DatagramStream{Conn: tunnelGW}
	if _, err := gw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, BufSize)
	server.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, from, err := server.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}

	replies := [][]byte{[]byte("one"), bytes.Repeat([]byte{0xCD}, 900), []byte("three")}
	for _, p := range replies {
		if _, err := server.WriteTo(p, from); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range replies {
		tunnelGW.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := gw.Read(buf)
		if err != nil {
			t.Fatalf("reply %d: %v", i, err)
		}
		if !bytes.Equal(buf[:n], want) {
			t.Fatalf("reply %d: got %d bytes, want %d", i, n, len(want))
		}
	}
}

// TestDatagramConnCloseWriteEndsTheFlow guards the half-close contract (D7).
// Splice calls CloseWrite when one direction reaches EOF and then waits for the
// other to notice. On a stream the FIN is what it notices; a datagram socket has
// no FIN, so CloseWrite must close — otherwise the opposite copy stays blocked
// on a socket nobody will write to again, and Splice never returns. That leak
// costs a goroutine, a socket and a gate slot for every player who quits.
func TestDatagramConnCloseWriteEndsTheFlow(t *testing.T) {
	local, _ := udpPair(t)
	dc := DatagramConn{local}

	blocked := make(chan error, 1)
	go func() {
		_, err := dc.Read(make([]byte, 2048))
		blocked <- err
	}()
	// Let the reader park before closing, so this tests the wake-up and not a
	// read that never started.
	time.Sleep(50 * time.Millisecond)
	if err := dc.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite on a datagram socket: %v", err)
	}
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("CloseWrite left the opposite direction blocked; Splice would never return")
	}
}

// FuzzDatagramStreamRead fuzzes the deframer, which parses length-prefixed
// bytes off the tunnel. Reaching it needs an authenticated peer, but "the peer
// is authenticated" has never been a reason to trust its framing — the same
// rule that gets control.ReadMsg fuzzed. The deframer must never panic and
// never size a buffer past MaxDatagram.
func FuzzDatagramStreamRead(f *testing.F) {
	f.Add([]byte{0x00, 0x03, 'a', 'b', 'c'})
	f.Add([]byte{0xFF, 0xFF})
	f.Add([]byte{0x00, 0x00})
	f.Add([]byte{0x00})
	f.Add([]byte{0x00, 0x05, 'a'})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := &DatagramStream{Conn: &byteConn{r: bytes.NewReader(data)}}
		buf := make([]byte, BufSize)
		for range 64 {
			n, err := r.Read(buf)
			if err != nil {
				if n != 0 {
					t.Fatalf("read reported %d bytes alongside error %v", n, err)
				}
				break
			}
			if n <= 0 || n > MaxDatagram {
				t.Fatalf("read returned %d bytes, outside (0, %d]", n, MaxDatagram)
			}
		}
		if cap(r.rbuf) > MaxDatagram {
			t.Fatalf("payload buffer grew to %d, past the %d maximum", cap(r.rbuf), MaxDatagram)
		}
	})
}
