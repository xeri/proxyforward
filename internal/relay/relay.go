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
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// BufSize is the pooled copy-buffer size, and thus the largest chunk handed to a
// single Write (io.Copy's default 32 KiB throttles chunk-load bursts on fat
// pipes). A rate limiter's burst is sized to this so WaitN(n) with n ≤ BufSize
// never exceeds the burst.
const BufSize = 128 * 1024

// WriteStallTimeout is how long a single Write may make no progress before
// the splice declares the peer dead. Generous: a healthy but slow client
// drains something well within this.
const WriteStallTimeout = 2 * time.Minute

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, BufSize)
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

// Limiter throttles a copy direction: WaitN blocks until n tokens (bytes) are
// available or ctx is done. *golang.org/x/time/rate.Limiter satisfies this
// structurally; a nil Limiter is the uncapped fast path. Kept as an interface so
// relay imports no rate library.
type Limiter interface {
	WaitN(ctx context.Context, n int) error
}

// SpliceOpts carries the optional bandwidth cap for one splice. The zero value
// (nil Ctx, nil limiters) reproduces the uncapped fast path exactly. LimitAToB
// throttles the a→b direction, LimitBToA the b→a direction (either may be nil);
// Ctx is the parent whose cancellation (e.g. agent eviction) unblocks a parked
// WaitN.
type SpliceOpts struct {
	Ctx       context.Context
	LimitAToB Limiter
	LimitBToA Limiter
}

// Splice pumps bytes both ways until both directions have finished, then
// fully closes both conns. It returns the first error that wasn't a normal
// EOF/close (nil for a clean shutdown). opts optionally rate-limits each
// direction; the zero value is the uncapped path and touches no context.
func Splice(a, b Conn, counters *Counters, opts SpliceOpts) error {
	var (
		wg   sync.WaitGroup
		errA error
		errB error
	)
	var cA, cB *atomic.Int64
	if counters != nil {
		cA, cB = &counters.AToB, &counters.BToA
	}

	// Only capped splices need a cancellable context. The child cancels when a
	// half returns an *error* (so an abnormal teardown promptly unblocks the
	// other half's throttled WaitN), but never on a clean EOF — the opposite
	// direction must keep draining (final bytes arrive intact). The parent ctx
	// cancels on eviction, unblocking both.
	ctx := opts.Ctx
	cancel := func() {}
	if opts.LimitAToB != nil || opts.LimitBToA != nil {
		parent := ctx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel = context.WithCancel(parent)
		defer cancel()
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		errA = copyHalf(b, a, cA, ctx, opts.LimitAToB) // a -> b
		if errA != nil {
			cancel()
		}
	}()
	go func() {
		defer wg.Done()
		errB = copyHalf(a, b, cB, ctx, opts.LimitBToA) // b -> a
		if errB != nil {
			cancel()
		}
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
// sees EOF while the opposite direction keeps flowing. When lim is non-nil it
// waits for n tokens before each write; a cancelled wait (teardown) tears the
// half down like a write error.
func copyHalf(dst, src Conn, count *atomic.Int64, ctx context.Context, lim Limiter) error {
	bufp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufp)
	buf := *bufp

	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if lim != nil {
				// Throttle before writing. n ≤ BufSize = the limiter's burst, so
				// WaitN only blocks for the rate delay, never errors on burst.
				if werr := lim.WaitN(ctx, n); werr != nil {
					src.Close()
					return werr
				}
			}
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
// Read, reset-by-peer when the far side vanished abruptly (a killed Minecraft
// client), and a cancelled WaitN (the splice's own child ctx on abnormal
// teardown, or the agent-session ctx on eviction). Write stalls (deadline
// exceeded, a net timeout — distinct from context.DeadlineExceeded) are NOT
// expected: they indicate a parked peer and are worth surfacing.
func isExpectedCloseErr(err error) bool {
	return err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
