package relay

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// TestTapPassthroughFidelity splices a TapConn and asserts the tapped side's
// bytes arrive byte-for-byte and that the tap saw the leading bytes.
func TestTapPassthroughFidelity(t *testing.T) {
	// client -> a  spliced to  b -> server
	clientC, a := tcpPair(t)
	b, serverC := tcpPair(t)

	var mu sync.Mutex
	var seen []byte
	tapped := NewTap(a, func(p []byte) bool {
		mu.Lock()
		seen = append(seen, p...)
		n := len(seen)
		mu.Unlock()
		return n >= 8 // stop tapping after the first 8 bytes
	})

	go Splice(tapped, b, nil, SpliceOpts{})

	payload := make([]byte, 200*1024)
	rand.Read(payload)

	got := make(chan []byte, 1)
	go func() {
		buf, _ := io.ReadAll(serverC)
		got <- buf
	}()
	go func() {
		clientC.Write(payload)
		clientC.CloseWrite()
	}()

	select {
	case received := <-got:
		if !bytes.Equal(received, payload) {
			t.Fatalf("payload corrupted: got %d bytes, want %d", len(received), len(payload))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("splice did not complete")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 8 || !bytes.Equal(seen, payload[:len(seen)]) {
		t.Fatalf("tap saw %d bytes that do not match the stream head", len(seen))
	}
}

// chunkConn is a relay.Conn whose reads deliver a fixed sequence of chunks,
// one per Read — the shape a real TCP stream hands the tap.
type chunkConn struct {
	net.Conn // nil; only Read and the no-op methods below are used
	chunks   [][]byte
}

func (c *chunkConn) Read(p []byte) (int, error) {
	for len(c.chunks) > 0 {
		next := c.chunks[0]
		if len(next) == 0 {
			c.chunks = c.chunks[1:]
			continue // empty split — a zero-byte read never reaches callers
		}
		n := copy(p, next)
		if n < len(next) {
			c.chunks[0] = next[n:]
		} else {
			c.chunks = c.chunks[1:]
		}
		return n, nil
	}
	return 0, io.EOF
}
func (c *chunkConn) Close() error      { return nil }
func (c *chunkConn) CloseWrite() error { return nil }

// splitmix64/seededChunks mirror the sniffer fuzzer's chunker (internal/mc):
// a tiny seeded PRNG cutting data into 1–8 orderly chunks.
func splitmix64(state *uint64) uint64 {
	*state += 0x9e3779b97f4a7c15
	z := *state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func seededChunks(data []byte, seed uint64) [][]byte {
	n := int(splitmix64(&seed)%8) + 1
	cuts := make([]int, 0, n+1)
	cuts = append(cuts, 0)
	for i := 1; i < n; i++ {
		if len(data) > 0 {
			cuts = append(cuts, int(splitmix64(&seed)%uint64(len(data)+1)))
		}
	}
	cuts = append(cuts, len(data))
	for i := 1; i < len(cuts); i++ {
		for j := i; j > 0 && cuts[j-1] > cuts[j]; j-- {
			cuts[j-1], cuts[j] = cuts[j], cuts[j-1]
		}
	}
	out := make([][]byte, 0, len(cuts)-1)
	for i := 1; i < len(cuts); i++ {
		out = append(out, data[cuts[i-1]:cuts[i]])
	}
	return out
}

// FuzzTapChunked drives seed-chosen chunk sequences through a TapConn: the
// reassembled stream must be byte-perfect whatever the chunking, the tap must
// see exactly the stream head up to where it signalled done, and it must
// never be called again after that (the zero-overhead revert).
func FuzzTapChunked(f *testing.F) {
	f.Add([]byte("the quick brown fox jumps over the lazy dog"), uint64(1), uint16(8))
	f.Add([]byte{}, uint64(2), uint16(0))
	f.Add(bytes.Repeat([]byte{0xab}, 4096), uint64(3), uint16(1024))
	f.Fuzz(func(t *testing.T, data []byte, seed uint64, doneAt uint16) {
		conn := &chunkConn{chunks: seededChunks(data, seed)}
		var seen []byte
		callsAfterDone := 0
		done := false
		tapped := NewTap(conn, func(p []byte) bool {
			if done {
				callsAfterDone++
			}
			seen = append(seen, p...)
			if len(seen) >= int(doneAt) {
				done = true
				return true
			}
			return false
		})

		got, err := io.ReadAll(tapped)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("passthrough corrupted: got %d bytes, want %d (seed=%d)", len(got), len(data), seed)
		}
		if !bytes.Equal(seen, data[:len(seen)]) {
			t.Fatalf("tap saw %d bytes that are not the stream head (seed=%d)", len(seen), seed)
		}
		if callsAfterDone > 0 {
			t.Fatalf("tap called %d times after signalling done", callsAfterDone)
		}
	})
}

// TestTapRevertsAfterDone confirms the tap function is not called once it has
// returned true, so post-login bytes incur no observation overhead.
func TestTapRevertsAfterDone(t *testing.T) {
	clientC, a := tcpPair(t)
	defer clientC.Close()
	defer a.Close()

	var mu sync.Mutex
	calls := 0
	tapped := NewTap(a, func(p []byte) bool {
		mu.Lock()
		calls++
		mu.Unlock()
		return true // done immediately
	})

	go func() {
		clientC.Write([]byte("first"))
		time.Sleep(20 * time.Millisecond)
		clientC.Write([]byte("second"))
		time.Sleep(20 * time.Millisecond)
		clientC.Write([]byte("third"))
		clientC.CloseWrite()
	}()

	io.ReadAll(tapped)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("tap called %d times, want exactly 1 after signaling done", calls)
	}
}
