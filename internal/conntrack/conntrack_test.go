package conntrack

import (
	"sync"
	"testing"
)

func TestOpenSnapshotClose(t *testing.T) {
	r := NewRegistry()

	// e1: splice's first arg is the client leg (inIsAToB=true).
	e1, close1 := r.Open("t1", "Tunnel", "1.2.3.4:1111", true)
	// e2: reversed orientation (inIsAToB=false).
	e2, close2 := r.Open("t1", "Tunnel", "1.2.3.4:2222", false)

	e1.Counters.AToB.Store(100) // in
	e1.Counters.BToA.Store(10)  // out
	e2.Counters.AToB.Store(7)   // out
	e2.Counters.BToA.Store(70)  // in

	if got := r.Count(); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}

	snaps := r.Snapshot()
	if len(snaps) != 2 {
		t.Fatalf("Snapshot len = %d, want 2", len(snaps))
	}
	if snaps[0].ID > snaps[1].ID {
		t.Fatalf("Snapshot not sorted by ID: %d before %d", snaps[0].ID, snaps[1].ID)
	}
	if snaps[0].BytesIn != 100 || snaps[0].BytesOut != 10 {
		t.Errorf("e1 snapshot bytes = in %d out %d, want in 100 out 10", snaps[0].BytesIn, snaps[0].BytesOut)
	}
	if snaps[1].BytesIn != 70 || snaps[1].BytesOut != 7 {
		t.Errorf("e2 snapshot bytes = in %d out %d, want in 70 out 7", snaps[1].BytesIn, snaps[1].BytesOut)
	}

	in, out := r.Totals()
	if in != 170 || out != 17 {
		t.Fatalf("Totals = in %d out %d, want in 170 out 17", in, out)
	}

	// Closing moves bytes from live to closed without changing the totals.
	close1()
	close1() // idempotent
	if got := r.Count(); got != 1 {
		t.Fatalf("Count after close = %d, want 1", got)
	}
	in, out = r.Totals()
	if in != 170 || out != 17 {
		t.Fatalf("Totals after close1 = in %d out %d, want in 170 out 17", in, out)
	}

	close2()
	in, out = r.Totals()
	if in != 170 || out != 17 {
		t.Fatalf("Totals after close2 = in %d out %d, want in 170 out 17", in, out)
	}
	if got := r.Count(); got != 0 {
		t.Fatalf("Count after all closes = %d, want 0", got)
	}
}

// TestTotalsMonotonicDuringClose guards the live→closed handoff: a reader
// sampling Totals while connections close must never see the total dip
// (bandwidth graphs diff consecutive samples).
func TestTotalsMonotonicDuringClose(t *testing.T) {
	r := NewRegistry()
	const n = 500
	closers := make([]func(), 0, n)
	for i := 0; i < n; i++ {
		e, c := r.Open("t", "T", "1.2.3.4:1", true)
		e.Counters.AToB.Store(1000)
		closers = append(closers, c)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var dipped bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		var last int64
		for {
			in, _ := r.Totals()
			if in < last {
				dipped = true
				return
			}
			last = in
			select {
			case <-stop:
				return
			default:
			}
		}
	}()

	for _, c := range closers {
		c()
	}
	close(stop)
	wg.Wait()
	if dipped {
		t.Fatal("Totals decreased while connections were closing")
	}
}
