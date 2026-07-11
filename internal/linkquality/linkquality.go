// Package linkquality derives jitter and packet loss from a control-stream
// ping/pong heartbeat. Both roles use it: the agent and the gateway each run
// their own ping loop and their own Tracker, so their status surfaces report
// the same set of link-quality stats.
package linkquality

import (
	"sync"
	"time"
)

// DefaultWindow is how many finalized heartbeats the loss ratio averages over.
const DefaultWindow = 32

// jitterGain is the EWMA weight (1/16) from RFC 3550's interarrival jitter.
const jitterGain = 1.0 / 16.0

// Tracker maintains jitter and packet-loss estimates. The ping loop calls
// OnSent for every ping and Sweep on every tick; the control reader calls
// OnPong for every pong — two goroutines, so all state is mutex-guarded.
type Tracker struct {
	mu sync.Mutex

	jitter     float64 // EWMA of |ΔRTT|, nanoseconds
	prevRTT    time.Duration
	jitterInit bool

	outstanding map[uint64]time.Time

	results []bool
	ridx    int
}

// New builds a Tracker with the given loss window (<=0 uses DefaultWindow).
func New(window int) *Tracker {
	if window <= 0 {
		window = DefaultWindow
	}
	return &Tracker{
		outstanding: make(map[uint64]time.Time),
		results:     make([]bool, 0, window),
		ridx:        window, // sentinel: grow until cap, then ring
	}
}

// OnSent records that ping seq went out at t.
func (q *Tracker) OnSent(seq uint64, t time.Time) {
	q.mu.Lock()
	q.outstanding[seq] = t
	q.mu.Unlock()
}

// OnPong records a received pong for seq with the measured round-trip. Unknown
// seqs (already swept as lost, or a stale duplicate) are ignored.
func (q *Tracker) OnPong(seq uint64, rtt time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.outstanding[seq]; !ok {
		return
	}
	delete(q.outstanding, seq)
	q.pushResult(true)
	if q.jitterInit {
		d := rtt - q.prevRTT
		if d < 0 {
			d = -d
		}
		q.jitter += (float64(d) - q.jitter) * jitterGain
	} else {
		q.jitterInit = true
	}
	q.prevRTT = rtt
}

// Sweep finalizes any ping still outstanding older than timeout as lost.
func (q *Tracker) Sweep(now time.Time, timeout time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for seq, sent := range q.outstanding {
		if now.Sub(sent) >= timeout {
			delete(q.outstanding, seq)
			q.pushResult(false)
		}
	}
}

// pushResult appends one finalized outcome to the ring (caller holds mu).
func (q *Tracker) pushResult(received bool) {
	win := cap(q.results)
	if len(q.results) < win {
		q.results = append(q.results, received)
		return
	}
	if q.ridx >= win {
		q.ridx = 0
	}
	q.results[q.ridx] = received
	q.ridx++
}

// JitterMillis is the current jitter EWMA in milliseconds, or -1 before any
// two samples exist.
func (q *Tracker) JitterMillis() float64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.jitterInit {
		return -1
	}
	return q.jitter / float64(time.Millisecond)
}

// LossPct is the fraction of finalized pings that timed out, 0–100, or -1
// before any ping has been finalized.
func (q *Tracker) LossPct() float64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.results) == 0 {
		return -1
	}
	lost := 0
	for _, r := range q.results {
		if !r {
			lost++
		}
	}
	return 100 * float64(lost) / float64(len(q.results))
}
