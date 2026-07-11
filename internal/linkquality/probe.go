package linkquality

import (
	"sync"
	"time"

	"proxyforward/internal/control"
)

// ProbeResult summarizes an on-demand latency burst. One-way values are only
// populated when the peer echoed its receive timestamp (HaveOneWay); they
// depend on both clocks being NTP-synced, which callers surface as a caveat.
type ProbeResult struct {
	Samples    int
	RTTAvg     time.Duration
	RTTMin     time.Duration
	RTTMax     time.Duration
	Jitter     time.Duration // mean absolute successive RTT difference
	OneWayUp   time.Duration // pinger → ponger
	OneWayDown time.Duration // ponger → pinger
	HaveOneWay bool
}

type probeSample struct {
	rtt        time.Duration
	up, down   time.Duration
	haveOneWay bool
}

// ProbeCollector steals pongs whose seq belongs to an in-flight probe. It is
// installed on a session while a measurement runs and cleared afterward.
type ProbeCollector struct {
	mu      sync.Mutex
	want    map[uint64]struct{}
	samples []probeSample
	expect  int
	full    chan struct{}
	done    bool
}

// NewProbeCollector expects `expect` pongs before signalling Full.
func NewProbeCollector(expect int) *ProbeCollector {
	return &ProbeCollector{want: make(map[uint64]struct{}), expect: expect, full: make(chan struct{})}
}

// Mark registers a probe ping seq as expected.
func (pc *ProbeCollector) Mark(seq uint64) {
	pc.mu.Lock()
	pc.want[seq] = struct{}{}
	pc.mu.Unlock()
}

// Full is closed once `expect` matching pongs have arrived.
func (pc *ProbeCollector) Full() <-chan struct{} { return pc.full }

// Record is called for every pong; it keeps only those matching an outstanding
// probe seq.
func (pc *ProbeCollector) Record(pong control.Pong, now time.Time) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if _, ok := pc.want[pong.Seq]; !ok {
		return
	}
	delete(pc.want, pong.Seq)
	s := probeSample{rtt: now.Sub(time.Unix(0, pong.SentUnixNano))}
	if pong.RecvUnixNano > 0 {
		s.up = time.Duration(pong.RecvUnixNano - pong.SentUnixNano)
		s.down = now.Sub(time.Unix(0, pong.RecvUnixNano))
		s.haveOneWay = true
	}
	pc.samples = append(pc.samples, s)
	if len(pc.samples) >= pc.expect && !pc.done {
		pc.done = true
		close(pc.full)
	}
}

// Summarize reduces the collected samples to a ProbeResult.
func (pc *ProbeCollector) Summarize() ProbeResult {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	res := ProbeResult{Samples: len(pc.samples)}
	if len(pc.samples) == 0 {
		return res
	}
	var sum, upSum, downSum, jitterSum time.Duration
	res.RTTMin = pc.samples[0].rtt
	res.RTTMax = pc.samples[0].rtt
	oneWayN := 0
	for i, s := range pc.samples {
		sum += s.rtt
		if s.rtt < res.RTTMin {
			res.RTTMin = s.rtt
		}
		if s.rtt > res.RTTMax {
			res.RTTMax = s.rtt
		}
		if i > 0 {
			d := s.rtt - pc.samples[i-1].rtt
			if d < 0 {
				d = -d
			}
			jitterSum += d
		}
		if s.haveOneWay {
			upSum += s.up
			downSum += s.down
			oneWayN++
		}
	}
	res.RTTAvg = sum / time.Duration(len(pc.samples))
	if len(pc.samples) > 1 {
		res.Jitter = jitterSum / time.Duration(len(pc.samples)-1)
	}
	if oneWayN > 0 {
		res.HaveOneWay = true
		res.OneWayUp = upSum / time.Duration(oneWayN)
		res.OneWayDown = downSum / time.Duration(oneWayN)
	}
	return res
}
