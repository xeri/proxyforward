// Package relay implements the bidirectional splice at the heart of the
// proxy, with the shutdown semantics that keep Minecraft disconnects clean:
//
//   - EOF on one leg propagates as CloseWrite (FIN) on the other, and the
//     opposite direction keeps draining — a disconnect message written just
//     before close still arrives intact instead of becoming a raw reset.
//   - Every write refreshes a progress deadline, so a peer that stops
//     draining can never park the splice goroutines forever.
//   - Copies use pooled 128 KiB buffers (io.Copy's default 32 KiB throttles
//     chunk-load bursts on fat pipes).
package relay

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const bufSize = 128 * 1024

// WriteStallTimeout is how long a single Write may make no progress before
// the splice declares the peer dead. Generous: a healthy but slow client
// drains something well within this.
const WriteStallTimeout = 2 * time.Minute

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, bufSize)
		return &b
	},
}

// Conn is what Splice needs from each leg: a net.Conn that can half-close
// its write side. *net.TCPConn, *tls.Conn and transport.Stream all qualify.
type Conn interface {
	net.Conn
	CloseWrite() error
}

// Counters receives live byte totals; may be nil. Updated with atomic adds
// so a GUI/metrics reader can snapshot mid-flight.
type Counters struct {
	AToB atomic.Int64
	BToA atomic.Int64
}

// Splice pumps bytes both ways until both directions have finished, then
// fully closes both conns. It returns the first error that wasn't a normal
// EOF/close (nil for a clean shutdown).
func Splice(a, b Conn, counters *Counters) error {
	var (
		wg   sync.WaitGroup
		errA error
		errB error
	)
	var cA, cB *atomic.Int64
	if counters != nil {
		cA, cB = &counters.AToB, &counters.BToA
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		errA = copyHalf(b, a, cA) // a -> b
	}()
	go func() {
		defer wg.Done()
		errB = copyHalf(a, b, cB) // b -> a
	}()
	wg.Wait()
	a.Close()
	b.Close()

	if isExpectedCloseErr(errA) {
		errA = nil
	}
	if isExpectedCloseErr(errB) {
		errB = nil
	}
	return errors.Join(errA, errB)
}

// copyHalf copies src → dst until EOF, then half-closes dst so its reader
// sees EOF while the opposite direction keeps flowing.
func copyHalf(dst, src Conn, count *atomic.Int64) error {
	bufp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufp)
	buf := *bufp

	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			dst.SetWriteDeadline(time.Now().Add(WriteStallTimeout))
			wn, werr := dst.Write(buf[:n])
			if count != nil && wn > 0 {
				count.Add(int64(wn))
			}
			if werr != nil {
				// The writer is gone; unblock the opposite copy immediately
				// rather than letting it drain to a dead peer.
				src.Close()
				return werr
			}
			if wn < n {
				src.Close()
				return io.ErrShortWrite
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				dst.SetWriteDeadline(time.Time{})
				return dst.CloseWrite()
			}
			// Read error: tear down the write side of dst too — there is
			// nothing more to forward and the conn is likely dead.
			dst.CloseWrite()
			return rerr
		}
	}
}

// isExpectedCloseErr filters the errors a normal teardown produces: EOFs,
// "use of closed network connection" from our own Close racing a blocked
// Read, and reset-by-peer when the far side vanished abruptly (a killed
// Minecraft client). Write stalls (deadline exceeded) are NOT expected —
// they indicate a parked peer and are worth surfacing.
func isExpectedCloseErr(err error) bool {
	return err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED)
}
