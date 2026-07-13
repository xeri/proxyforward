package analytics

import (
	"testing"
)

func ev(pairs ...int64) []uptimeEvent {
	// pairs: t, up(0/1), t, up, ...
	var out []uptimeEvent
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, uptimeEvent{t: pairs[i], up: pairs[i+1] != 0})
	}
	return out
}

func TestComputeUptimePct(t *testing.T) {
	cases := []struct {
		name   string
		events []uptimeEvent
		cover  [][2]int64
		since  int64
		now    int64
		want   float64
	}{
		{"all up no cover", ev(0, 1), nil, 0, 200, 100},
		{"one flap", ev(0, 1, 100, 0, 150, 1), nil, 0, 200, 75},
		{"seeded window", ev(0, 1, 150, 0), nil, 100, 200, 50},
		{"unknown before first event", ev(50, 1), nil, 0, 100, 100},
		{"no events", nil, nil, 0, 100, -1},
		{"down whole window", ev(0, 0), nil, 0, 100, 0},
		// Graceful gap: engine off between [100,300]; link was up, but the gap
		// is uncovered so it counts as unknown, not down → 100% of known time.
		{"graceful gap excluded", ev(0, 1), [][2]int64{{0, 100}, {300, 400}}, 0, 400, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeUptimePct(c.events, c.cover, c.since, c.now)
			if !approx(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestEngineCoverage(t *testing.T) {
	// up, down, up(current run to now).
	cover := engineCoverage(ev(0, 1, 100, 0, 300, 1), 400)
	want := [][2]int64{{0, 100}, {300, 400}}
	if len(cover) != len(want) {
		t.Fatalf("cover=%v want %v", cover, want)
	}
	for i := range want {
		if cover[i] != want[i] {
			t.Fatalf("cover[%d]=%v want %v", i, cover[i], want[i])
		}
	}
	if len(engineCoverage(nil, 100)) != 0 {
		t.Fatal("no events should yield empty coverage")
	}
}

func TestWindowSpans(t *testing.T) {
	spans := windowSpans(ev(0, 1, 150, 0, 250, 1), 100, 300)
	want := []UptimeSpan{{100, true}, {150, false}, {250, true}}
	if len(spans) != len(want) {
		t.Fatalf("spans=%v want %v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("spans[%d]=%v want %v", i, spans[i], want[i])
		}
	}
}

func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}
