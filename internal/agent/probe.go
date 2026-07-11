package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/linkquality"
)

const probeCollectTimeout = 3 * time.Second

// ProbeLatency runs an on-demand latency burst on the live control link,
// sending count pings spaced by interval and collecting their pongs. It returns
// an error when the link is down or another probe is already running.
func (a *Agent) ProbeLatency(ctx context.Context, count int, interval time.Duration) (linkquality.ProbeResult, error) {
	if count <= 0 {
		count = 10
	}
	if interval <= 0 {
		interval = 150 * time.Millisecond
	}
	s := a.curSession.Load()
	if s == nil {
		return linkquality.ProbeResult{}, errors.New("link is down — connect to the gateway before testing latency")
	}
	return s.probeLatency(ctx, count, interval)
}

func (s *session) probeLatency(ctx context.Context, count int, interval time.Duration) (linkquality.ProbeResult, error) {
	pc := linkquality.NewProbeCollector(count)
	if !s.probe.CompareAndSwap(nil, pc) {
		return linkquality.ProbeResult{}, errors.New("a latency test is already running")
	}
	defer s.probe.Store(nil)

	for i := 0; i < count; i++ {
		seq := s.pingSeq.Add(1)
		pc.Mark(seq)
		now := time.Now()
		s.quality.OnSent(seq, now) // probe pings feed the rolling quality window too
		if err := s.write(control.TypePing, control.Ping{Seq: seq, SentUnixNano: now.UnixNano()}); err != nil {
			return linkquality.ProbeResult{}, fmt.Errorf("send probe ping: %w", err)
		}
		if i < count-1 {
			select {
			case <-ctx.Done():
				return linkquality.ProbeResult{}, ctx.Err()
			case <-time.After(interval):
			}
		}
	}

	select {
	case <-pc.Full():
	case <-ctx.Done():
		return linkquality.ProbeResult{}, ctx.Err()
	case <-time.After(probeCollectTimeout):
	}
	res := pc.Summarize()
	if res.Samples == 0 {
		return res, errors.New("no pong received — the link may have dropped")
	}
	return res, nil
}
