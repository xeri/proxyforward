package netnotify

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestWatchResumeDetectsClockJump(t *testing.T) {
	var (
		mu   sync.Mutex
		base = time.Now()
		skew time.Duration
	)
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return base.Add(skew)
	}
	jump := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		skew += d
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchResume(ctx, 20*time.Millisecond, 50*time.Millisecond, now, ch)
	}()

	// Normal ticking: no notifications.
	time.Sleep(100 * time.Millisecond)
	select {
	case <-ch:
		t.Fatal("resume detected without a clock jump")
	default:
	}

	// A 5-minute wall jump (as after suspend) must notify.
	jump(5 * time.Minute)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("clock jump not detected")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchResume leaked after cancel")
	}
}

func TestSubscribeStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, wait := Subscribe(ctx, slog.New(slog.DiscardHandler))
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe watchers did not exit after cancel")
	}
	select {
	case <-ch:
		// A tick raced the cancel — acceptable.
	default:
	}
}
