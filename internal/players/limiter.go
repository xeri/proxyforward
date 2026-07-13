package players

import (
	"context"
	"sync"
	"time"
)

// limiter is a minimal token bucket: refills at rate tokens/sec up to burst,
// and wait blocks until one token is available. A dedicated type avoids a new
// dependency for the resolver's single, low-rate use.
type limiter struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newLimiter(ratePerSec float64, burst int) *limiter {
	return &limiter{
		rate:   ratePerSec,
		burst:  float64(burst),
		tokens: float64(burst),
		now:    time.Now,
	}
}

// wait blocks until a token is available or ctx is cancelled.
func (l *limiter) wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := l.now()
		if l.last.IsZero() {
			l.last = now
		}
		l.tokens += now.Sub(l.last).Seconds() * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
		if l.tokens >= 1 {
			l.tokens -= 1
			l.mu.Unlock()
			return nil
		}
		deficit := 1 - l.tokens
		wait := time.Duration(deficit / l.rate * float64(time.Second))
		l.mu.Unlock()
		if wait <= 0 {
			wait = time.Millisecond
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
