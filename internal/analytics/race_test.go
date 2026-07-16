package analytics

import (
	"sync"
	"testing"
	"time"

	"proxyforward/internal/conntrack"
)

// TestConcurrentReadWrite exercises the hot surface all at once — conntrack
// open/close, RTT recording, one live sampler, and dashboard reads on the
// read pool — for ~200 ms. Its value is under `go test -race`; the assertions
// only catch outright query failures.
func TestConcurrentReadWrite(t *testing.T) {
	d := openTest(t, t.TempDir())
	rec := d.NewRecorder(fakeGeo{})
	reg := conntrack.NewRegistry()
	reg.SetHooks(
		func(e *conntrack.Entry) { rec.SessionOpened(e) },
		func(e *conntrack.Entry, in, out int64) { rec.SessionClosed(e, in, out) },
		func(e *conntrack.Entry) { rec.PlayerSeen(e) },
		func(e *conntrack.Entry) { rec.RecordRTT(e, e.RTT()) },
	)

	stop := make(chan struct{})
	time.AfterFunc(200*time.Millisecond, func() { close(stop) })
	var wg sync.WaitGroup

	// Connection churn (splice-goroutine shape): open, identify, RTT, close.
	for i := range 4 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				e, closeEntry := reg.Open("", "t1", "mc", "203.0.113.9:1", "k", true)
				e.SetPlayer(conntrack.PlayerInfo{Name: "Steve", UUID: steveUUID})
				e.SetRTT(float64(i + 1))
				e.Counters.AToB.Add(64)
				closeEntry()
			}
		}(i)
	}
	// Exactly one sampler goroutine, as in production.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			rec.SampleLive(reg.Snapshot())
		}
	}()
	// Dashboard readers on the read pool.
	readers := []struct {
		name string
		fn   func() error
	}{
		{"players", func() error {
			_, err := d.Players(PlayersQuery{}, map[string]bool{"name:steve": true}, time.Now().UnixMilli())
			return err
		}},
		{"summary", func() error {
			_, err := d.Summary(0, time.Now().UnixMilli())
			return err
		}},
		{"timeline", func() error {
			_, err := d.SessionTimeline(1, time.Now().UnixMilli())
			return err
		}},
	}
	for _, r := range readers {
		wg.Add(1)
		go func(name string, fn func() error) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := fn(); err != nil {
					t.Errorf("%s under load: %v", name, err)
					return
				}
			}
		}(r.name, r.fn)
	}
	wg.Wait()
}
