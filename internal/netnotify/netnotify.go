// Package netnotify turns "the network probably changed" moments — adapter
// up/down, address changes, resume from sleep — into ticks on a channel, so
// the agent can retry immediately instead of waiting out its backoff timer.
package netnotify

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	// resumeCheckInterval is how often the resume detector samples the wall
	// clock.
	resumeCheckInterval = 10 * time.Second
	// resumeSlack is how far beyond the interval the wall clock must jump
	// before we call it a suspend/resume (or a big clock adjustment — both
	// are good reasons to re-dial).
	resumeSlack = 30 * time.Second
)

// Subscribe returns a channel that ticks (coalesced, buffered) whenever the
// host's network configuration changes or the machine resumes from sleep,
// plus a wait func that blocks until every watcher goroutine has exited
// after ctx is cancelled.
func Subscribe(ctx context.Context, logger *slog.Logger) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		watchResume(ctx, resumeCheckInterval, resumeSlack, time.Now, ch)
	}()
	go func() {
		defer wg.Done()
		watchNetChange(ctx, logger, ch) // per-platform
	}()
	return ch, wg.Wait
}

// tick delivers a coalesced notification: if one is already pending, the new
// one merges into it.
func tick(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// watchResume detects suspend/resume portably: Go timers run on awake time,
// so a ticker that oversleeps badly in wall-clock terms means the machine
// was suspended (or the clock jumped) in between.
func watchResume(ctx context.Context, interval, slack time.Duration, now func() time.Time, ch chan<- struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	prev := now().Round(0) // Round(0) strips the monotonic reading
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := now().Round(0)
			if cur.Sub(prev) > interval+slack {
				tick(ch)
			}
			prev = cur
		}
	}
}
