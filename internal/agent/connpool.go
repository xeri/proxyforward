package agent

import (
	"context"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/link"
	"proxyforward/internal/relay"
)

// poolRedialFloor is how long a parked connection must survive before the
// gateway dropping it counts as normal churn rather than a refusal. Below it,
// the maintainer paces the next attempt through its backoff: a gateway that
// closes parked connections the instant they arrive (its session evicted, its
// pool already drained) would otherwise be answered with a dial loop — the dial
// and the hello both succeed, so nothing else in this loop would notice.
const poolRedialFloor = time.Second

// parkOutcome is how a parked connection reports back to the maintainer when it
// stops being parked: taken by a player, or gone. lived is how long it waited,
// which is what tells a served pool apart from a refused one.
type parkOutcome struct {
	taken bool
	lived time.Duration
}

// startPoolMaintainer starts the connection-pool maintainer for this session and
// returns a channel closed when it has exited. It is a no-op — an
// already-closed channel — unless pooling was negotiated and granted, so every
// other session pays nothing.
//
// Called after registerTunnels: the tunnel is live before the first connection
// is parked, because a pool that is slow to fill must never delay readiness.
func (s *session) startPoolMaintainer(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	grant := int(s.agent.poolGrant.Load())
	if grant <= 0 || !s.Has(control.CapConnPool) {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		s.maintainPool(ctx, grant)
	}()
	return done
}

// maintainPool keeps grant connections parked at the gateway for the session's
// lifetime. There are no wire messages in either direction: parking is a data
// hello that says so, and a parked connection announces it has been taken by
// receiving the open_conn header a player triggers. So the pool refills itself
// from what the connections themselves report, and a gateway that stops taking
// them costs nothing but the parked sockets.
func (s *session) maintainPool(ctx context.Context, grant int) {
	// Whatever is still parked when the session ends has no player and no splice
	// to close it, and its reader waits on no deadline.
	defer s.closeParked()

	// Buffered to the grant: at most that many connections are outstanding, each
	// reporting exactly once, so a report never blocks even after this loop has
	// returned — which is what keeps teardown from stranding one of them.
	freed := make(chan parkOutcome, grant)
	backoff := &link.Backoff{}
	parked, blocked := 0, false
	for {
		for parked < grant && !blocked {
			rc, err := s.dialData("", true)
			if err != nil {
				s.agent.logger.Debug("parking a data conn failed", "err", err)
				blocked = true
				break
			}
			parked++
			go s.parkAndServe(ctx, rc, func(out parkOutcome) { freed <- out })
		}

		var retry <-chan time.Time
		var timer *time.Timer
		if blocked {
			timer = time.NewTimer(backoff.Next())
			retry = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case out := <-freed:
			parked--
			// A connection that served a player is proof the pool works: start the
			// next failure from the top of the curve. One dropped before it could
			// wait out poolRedialFloor is the gateway refusing, and refilling
			// straight into that is the loop this guards against.
			switch {
			case out.taken:
				backoff.Reset()
				blocked = false
			case out.lived < poolRedialFloor:
				blocked = true
			default:
				blocked = false
			}
		case <-retry:
			blocked = false
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// parkAndServe leaves one authenticated connection at the gateway and waits on
// it for as long as it takes — no deadline, because nothing is late until a
// player arrives. When one does, the gateway writes the open_conn header and
// this connection becomes an ordinary data leg, served by exactly the code a
// dial-back would have run.
//
// It reports back the moment it stops being parked, before serving anything, so
// the pool refills while this player is still connected rather than after they
// leave.
func (s *session) parkAndServe(ctx context.Context, rc relay.Conn, done func(parkOutcome)) {
	key := s.parkSeq.Add(1)
	s.parked.Store(key, rc)
	// The session may have ended between the dial and that store, in which case
	// closeParked has already run and would never see this entry — nothing else
	// would ever close it, and its reader below would wait forever.
	if ctx.Err() != nil {
		s.parked.Delete(key)
		rc.Close()
		done(parkOutcome{})
		return
	}

	parkedAt := time.Now()
	oc, err := readOpenConn(rc, 0)
	s.parked.Delete(key)
	if err != nil {
		// Teardown closed it, or the gateway would not hold it. Which of the two
		// is the maintainer's business, not this goroutine's.
		rc.Close()
		done(parkOutcome{lived: time.Since(parkedAt)})
		return
	}
	done(parkOutcome{taken: true, lived: time.Since(parkedAt)})
	defer rc.Close()
	s.serveOpenConn(rc, oc)
}

// closeParked closes every connection still waiting for a player, which is what
// unblocks their readers — they hold no deadline by design.
func (s *session) closeParked() {
	s.parked.Range(func(k, v any) bool {
		s.parked.Delete(k)
		v.(relay.Conn).Close()
		return true
	})
}
