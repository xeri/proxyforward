package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"proxyforward/internal/analytics"
	"proxyforward/internal/ipc"
)

// startTestDaemon serves the (test-private, see setup_test.go) IPC pipe with
// the given analytics source and returns after the pipe answers a ping.
func startTestDaemon(t *testing.T, analyticsFn func(ipc.AnalyticsReq) ipc.AnalyticsResp) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ipc.Serve(ctx, slog.New(slog.DiscardHandler), ipc.Sources{
			Status:    func() ipc.Status { return ipc.Status{Role: "agent", PID: 1} },
			Analytics: analyticsFn,
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	for range 100 {
		if c, err := ipc.Dial(200 * time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("test daemon pipe never came up")
}

// attachTestApp puts an App into attached mode against the test daemon.
func attachTestApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp()
	c, err := ipc.Dial(time.Second)
	if err != nil {
		t.Fatalf("attach dial: %v", err)
	}
	a.mu.Lock()
	a.client = c
	a.mode = ModeAttached
	a.mu.Unlock()
	t.Cleanup(func() { a.Shutdown(context.Background()) })
	return a
}

// TestAnalyticsDoesNotBlockStatus (3.4): a 2 s analytics query must not park
// the status tick — analytics rides its own pipe and its own mutex.
func TestAnalyticsDoesNotBlockStatus(t *testing.T) {
	startTestDaemon(t, func(req ipc.AnalyticsReq) ipc.AnalyticsResp {
		time.Sleep(2 * time.Second)
		return ipc.AnalyticsResp{Body: []byte(`{}`)}
	})
	a := attachTestApp(t)

	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		a.Summary(0)
	}()
	time.Sleep(100 * time.Millisecond) // let the slow op take anMu

	start := time.Now()
	st := a.Status()
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("Status blocked %v behind a slow analytics op", elapsed)
	}
	if st.Mode != ModeAttached || st.PID != 1 {
		t.Fatalf("status = mode %q pid %d, want attached/1", st.Mode, st.PID)
	}
	<-slowDone
}

// TestServedErrorDoesNotLatch (3.1): a daemon that answers with an error is
// alive — the analytics surface must stay usable for the next poll.
func TestServedErrorDoesNotLatch(t *testing.T) {
	fail := true
	startTestDaemon(t, func(req ipc.AnalyticsReq) ipc.AnalyticsResp {
		if fail {
			return ipc.AnalyticsResp{Err: "transient query failure"}
		}
		return ipc.AnalyticsResp{Body: []byte(`{"total":5,"players":[]}`)}
	})
	a := attachTestApp(t)

	if page := a.Players(analytics.PlayersQuery{}); page.Total != 0 {
		t.Fatalf("failed op returned %+v, want empty page", page)
	}
	a.mu.Lock()
	latched := a.analyticsUnsupported
	a.mu.Unlock()
	if latched {
		t.Fatal("served error latched analyticsUnsupported")
	}

	fail = false
	if page := a.Players(analytics.PlayersQuery{}); page.Total != 5 {
		t.Fatalf("recovered op returned total %d, want 5", page.Total)
	}
}

// TestAnalyticsUnsupportedOnTick (3.5): the unsupported latch rides the
// status tick so the UI can render one honest empty state, and it clears on
// mode transitions (a restarted daemon or a fresh engine may support
// analytics again).
func TestAnalyticsUnsupportedOnTick(t *testing.T) {
	startTestDaemon(t, func(req ipc.AnalyticsReq) ipc.AnalyticsResp {
		return ipc.AnalyticsResp{Body: []byte(`{}`)}
	})
	a := attachTestApp(t)

	if st := a.Status(); st.AnalyticsUnsupported {
		t.Fatal("fresh attach reported AnalyticsUnsupported")
	}
	a.mu.Lock()
	a.analyticsUnsupported = true
	a.mu.Unlock()
	if st := a.Status(); !st.AnalyticsUnsupported {
		t.Fatal("latched flag did not ride the status tick")
	}
	// Latched → every poll short-circuits without touching the pipe.
	if page := a.Players(analytics.PlayersQuery{}); len(page.Players) != 0 {
		t.Fatalf("latched op returned %+v, want empty", page)
	}
	// A mode transition (engine start, daemon loss) clears the latch.
	a.mu.Lock()
	a.closeAnalyticsConnLocked()
	latched := a.analyticsUnsupported
	a.mu.Unlock()
	if latched {
		t.Fatal("reset did not clear the latch")
	}
}
