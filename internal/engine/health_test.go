package engine

import (
	"testing"
	"time"
)

func TestHealthScore(t *testing.T) {
	old := time.Now().Add(-10 * time.Minute).UnixMilli()  // well-established link
	fresh := time.Now().Add(-5 * time.Second).UnixMilli() // just came up

	cases := []struct {
		name   string
		up     bool
		jitter float64
		loss   float64
		since  int64
		want   string
	}{
		{"link down", false, 5, 0, old, "bad"},
		{"healthy", true, 5, 0, old, "good"},
		{"unknown metrics healthy", true, -1, -1, old, "good"},
		{"fresh link warns", true, 5, 0, fresh, "warn"},
		{"mild jitter warns", true, 50, 0, old, "warn"},
		{"mild loss warns", true, 5, 2, old, "warn"},
		{"bad jitter", true, 150, 0, old, "bad"},
		{"bad loss", true, 5, 6, old, "bad"},
		{"loss dominates jitter", true, 5, 10, old, "bad"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := healthScore(c.up, c.jitter, c.loss, c.since); got != c.want {
				t.Fatalf("healthScore(up=%v jitter=%v loss=%v) = %q, want %q", c.up, c.jitter, c.loss, got, c.want)
			}
		})
	}
}
