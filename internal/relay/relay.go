// Package relay implements the bidirectional splice at the heart of the
// proxy, with the shutdown semantics that keep Minecraft disconnects clean:
//
//   - EOF on one leg propagates as CloseWrite (FIN) on the other, and the
//     opposite direction keeps draining — a disconnect message written just
//     before close still arrives intact instead of becoming a raw reset.
//   - Every write refreshes a progress deadline, so a peer that stops
//     draining can never park the splice goroutines forever.
//   - A splice that moves no bytes in *either* direction for IdleTimeout is
//     reaped, so neither can a peer that simply never speaks.
//   - Copies use pooled 128 KiB buffers (io.Copy's default 32 KiB throttles
//     chunk-load bursts on fat pipes).
package relay

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
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

// DefaultIdleTimeout is how long a splice may move no bytes in *either*
// direction before it is reaped. It bounds what a peer that connects and then
// says nothing can hold: without it such a peer is free, permanent, and
// amplified — the agent dials the local server on open_conn, before any client
// byte, so one silent socket costs a Minecraft connection slot too.
//
// Calibrated for Minecraft, which this tunnel exists to carry: an in-game
// connection exchanges a KeepAlive every ~15 s (the client is kicked at 30 s of
// silence), so this is over 30× the longest silence the protocol permits, and
// still far more generous than a typical reverse proxy's read timeout. A tunnel
// carrying some other protocol that idles for hours by design (an open RCON or
// SSH session) would be cut; SpliceOpts.IdleTimeout is the per-splice override,
// and no caller sets it today.
//
// It is not a slowloris cure on its own — a peer dribbling one byte per window
// stays alive — it makes holding a connection cost something. The per-network
// conn cap (gateway `limits.go connGate`) is what bounds how many they get.
const DefaultIdleTimeout = 10 * time.Minute

// ErrIdle is what a reaped splice reports, so the log names the cause instead of
// showing a bare i/o timeout that reads like a network fault.
var ErrIdle = errors.New("relay: no bytes in either direction within the idle timeout")

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
	// IdleTimeout overrides DefaultIdleTimeout for this splice. Zero means the
	// default — never "no timeout", because a caller that forgets the field must
	// end up protected, not exposed.
	IdleTimeout time.Duration
}

// splice is the state the two directions share. progress is the whole design:
// idleness is a property of the *connection*, not of a direction. A client
// downloading a modpack sends nothing for the length of the transfer, so a
// per-direction read deadline would cut off every large download; one clock that
// either direction refreshes keeps that connection alive and still reaps one
// where nothing at all is happening.
type splice struct {
	idle     time.Duration
	progress atomic.Int64 // UnixNano of the last byte moved in either direction
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

	sp := &splice{idle: opts.IdleTimeout}
	if sp.idle <= 0 {
		sp.idle = DefaultIdleTimeout
	}
	sp.progress.Store(time.Now().UnixNano())

	wg.Add(2)
	go func() {
		defer wg.Done()
		errA = sp.copyHalf(b, a, cA, ctx, opts.LimitAToB) // a -> b
		if errA != nil {
			cancel()
		}
	}()
	go func() {
		defer wg.Done()
		errB = sp.copyHalf(a, b, cB, ctx, opts.LimitBToA) // b -> a
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
func (s *splice) copyHalf(dst, src Conn, count *atomic.Int64, ctx context.Context, lim Limiter) error {
	bufp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufp)
	buf := *bufp

	// Armed once, not before every Read. The deadline is absolute, so a busy
	// direction never trips it (its Reads return long first) and a quiet one
	// trips it at most once per idle window and re-arms in stillLive. That keeps
	// the read loop free of per-iteration deadline churn on the hot path.
	src.SetReadDeadline(time.Now().Add(s.idle))

	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			s.progress.Store(time.Now().UnixNano())
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
			if live, rearmed := s.stillLive(src, rerr); rearmed {
				continue // quiet in this direction only; the splice is working
			} else if !live {
				dst.CloseWrite()
				return ErrIdle
			}
			// Read error: tear down the write side of dst too — there is
			// nothing more to forward and the conn is likely dead.
			dst.CloseWrite()
			return rerr
		}
	}
}

// stillLive interprets a read error against the shared progress clock.
//
// rearmed: the error was our own idle deadline, but the opposite direction has
// moved bytes inside the window, so this direction is merely quiet. The deadline
// is pushed to one window past the last byte and the caller reads on. Both halves
// re-arm off the same clock, so when traffic does stop they converge and reap
// together.
//
// live=false: our idle deadline fired and nothing moved either way for the whole
// window. Nothing is coming; reap.
//
// Anything else is a real read error and is not ours to interpret.
func (s *splice) stillLive(src Conn, rerr error) (live, rearmed bool) {
	var ne net.Error
	// Every transport under this reports a read deadline as a net.Error with
	// Timeout() true: the stdlib's os.ErrDeadlineExceeded, yamux's *NetError,
	// quic-go's *deadlineError. Misreading one as a hard error would kill healthy
	// connections every window, so accept the interface, not a concrete type.
	if !errors.As(rerr, &ne) || !ne.Timeout() {
		if !errors.Is(rerr, os.ErrDeadlineExceeded) {
			return true, false
		}
	}
	last := time.Unix(0, s.progress.Load())
	if time.Since(last) >= s.idle {
		return false, false
	}
	src.SetReadDeadline(last.Add(s.idle))
	return true, true
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
