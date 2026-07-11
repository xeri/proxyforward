package linkquality

import (
	"math"
	"testing"
	"time"
)

func TestTrackerJitter(t *testing.T) {
	q := New(32)
	base := time.Unix(0, 0)
	// First pong only seeds prevRTT — jitter stays unknown until a second.
	q.OnSent(1, base)
	q.OnPong(1, 10*time.Millisecond)
	if got := q.JitterMillis(); got != 0 {
		t.Fatalf("after one sample jitter should be 0 (seeded), got %v", got)
	}
	// Second pong: |30-10| = 20ms; EWMA gain 1/16 → 20/16 = 1.25ms.
	q.OnSent(2, base)
	q.OnPong(2, 30*time.Millisecond)
	if got := q.JitterMillis(); math.Abs(got-1.25) > 1e-6 {
		t.Fatalf("jitter after 10→30ms: want 1.25ms, got %v", got)
	}
	// Third pong equal to previous: |30-30| = 0 → jitter decays toward 0.
	q.OnSent(3, base)
	q.OnPong(3, 30*time.Millisecond)
	if got := q.JitterMillis(); got >= 1.25 {
		t.Fatalf("jitter should decay after a steady sample, got %v", got)
	}
}

func TestTrackerPacketLoss(t *testing.T) {
	q := New(32)
	base := time.Unix(1000, 0)
	for seq := uint64(1); seq <= 10; seq++ {
		q.OnSent(seq, base)
	}
	for seq := uint64(1); seq <= 8; seq++ {
		q.OnPong(seq, 12*time.Millisecond)
	}
	// Before the timeout elapses, the two silent pings are not yet counted.
	q.Sweep(base.Add(5*time.Second), 10*time.Second)
	if got := q.LossPct(); math.Abs(got) > 1e-9 {
		t.Fatalf("no loss should register before timeout, got %v", got)
	}
	// After the timeout, seqs 9 and 10 are lost: 2/10 = 20%.
	q.Sweep(base.Add(20*time.Second), 10*time.Second)
	if got := q.LossPct(); math.Abs(got-20) > 1e-9 {
		t.Fatalf("loss want 20%%, got %v", got)
	}
}

func TestTrackerUnknownUntilSampled(t *testing.T) {
	q := New(32)
	if got := q.JitterMillis(); got != -1 {
		t.Fatalf("jitter should be -1 before any sample, got %v", got)
	}
	if got := q.LossPct(); got != -1 {
		t.Fatalf("loss should be -1 before any finalized ping, got %v", got)
	}
}

func TestTrackerRingWraps(t *testing.T) {
	q := New(4)
	base := time.Unix(0, 0)
	for seq := uint64(1); seq <= 4; seq++ {
		q.OnSent(seq, base)
	}
	q.Sweep(base.Add(time.Minute), time.Second)
	if got := q.LossPct(); math.Abs(got-100) > 1e-9 {
		t.Fatalf("window of all losses want 100%%, got %v", got)
	}
	for seq := uint64(5); seq <= 8; seq++ {
		q.OnSent(seq, base)
		q.OnPong(seq, 5*time.Millisecond)
	}
	if got := q.LossPct(); math.Abs(got) > 1e-9 {
		t.Fatalf("window should have wrapped to all-received (0%%), got %v", got)
	}
}
