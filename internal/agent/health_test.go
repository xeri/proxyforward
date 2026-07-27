package agent

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/config"
)

func waitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHealthCheckerTransitions(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	cfg := config.Default()
	cfg.Agent.Tunnels = []config.Tunnel{{
		ID: "t1", Name: "test", Type: config.TunnelTCP,
		LocalAddr: ln.Addr().String(), PublicPort: 25565, Enabled: true,
	}}
	a := New(cfg, t.TempDir(), slog.New(slog.DiscardHandler))
	a.healthInterval = 30 * time.Millisecond
	a.healthDialTimeout = 500 * time.Millisecond

	// Capture pushes the way a live session would.
	var mu sync.Mutex
	var pushes []bool
	a.setHealthSink(func(id string, up bool) {
		mu.Lock()
		defer mu.Unlock()
		pushes = append(pushes, up)
	})
	// A process-lifetime observer (the engine's event recorder) sees the same
	// transitions independently of the session sink.
	var obs []bool
	a.SetHealthObserver(func(id string, up bool) {
		mu.Lock()
		defer mu.Unlock()
		obs = append(obs, up)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.runHealthChecker(ctx)
	}()
	defer func() { cancel(); <-done }()

	waitCond(t, "initial up state", func() bool {
		up, known := a.LocalUp("t1")
		return known && up
	})

	ln.Close() // backend goes away
	waitCond(t, "down transition", func() bool {
		up, known := a.LocalUp("t1")
		return known && !up
	})

	mu.Lock()
	defer mu.Unlock()
	if len(pushes) != 2 || pushes[0] != true || pushes[1] != false {
		t.Fatalf("pushes = %v, want [true false] (transitions only)", pushes)
	}
	if len(obs) != 2 || obs[0] != true || obs[1] != false {
		t.Fatalf("observer = %v, want [true false] (transitions only)", obs)
	}
}

// TestProbeReportsUnpointedTunnelDownWithoutProbing: a tunnel that names no local
// address is reported down at once, not probed.
//
// The state is reachable and not exotic: a push naming a tunnel id this agent has
// never held creates one (mergeTunnels), and the wire has no LocalAddr field to
// create it with — so the tunnel holds a public port and dials nowhere. Its health
// is a fact about the config, knowable without a probe; routing it through the probe
// path made it arrive as "the local server stopped answering", which is the one
// thing it is not, and sent its owner to restart a machine that was never involved.
//
// So: no dial, and no failure debounce — HealthMaxFailed exists to ride out a server
// restart and there is nothing here to ride out. The high debounce below is what
// separates this from the dial-and-fail it used to be.
func TestProbeReportsUnpointedTunnelDownWithoutProbing(t *testing.T) {
	a := New(config.Default(), t.TempDir(), slog.New(slog.DiscardHandler))
	tunnels := []config.Tunnel{
		{ID: "tnl_tcp", Name: "tcp", Type: config.TunnelTCP, Enabled: true,
			Options: config.TunnelOptions{HealthMaxFailed: 5}},
		// Both types. The UDP branch declines to *verdict* an address it cannot
		// handshake with, and unknown counts as up — so with no address at all
		// the empty check has to come first or a black hole reads as healthy.
		{ID: "tnl_udp", Name: "udp", Type: config.TunnelUDP, Enabled: true},
	}
	states := map[string]*healthState{}

	// One pass, well short of the 5 failures a dial-and-fail would have needed.
	a.probeTunnels(context.Background(), tunnels, states)

	for _, tun := range tunnels {
		up, known := a.LocalUp(tun.ID)
		if !known {
			t.Errorf("%s: health is unknown, and unknown counts as up — a tunnel naming no local address is knowably down", tun.ID)
			continue
		}
		if up {
			t.Errorf("%s: reported up — a tunnel with nowhere to dial is a black hole wearing a healthy badge", tun.ID)
		}
	}
	if st, ok := states["tnl_tcp"]; ok && st.failures > 0 {
		t.Errorf("the tunnel accrued %d probe failures; it must be settled from config, not dialled for", st.failures)
	}
}

// TestProbeStillDialsAPointedTunnel: the check above must not swallow the ordinary
// case. A tunnel that names an address is still probed, and still serves out its
// debounce before any verdict — one failed pass against a closed port must leave
// the health unknown, not down.
func TestProbeStillDialsAPointedTunnel(t *testing.T) {
	a := New(config.Default(), t.TempDir(), slog.New(slog.DiscardHandler))
	a.healthDialTimeout = 500 * time.Millisecond
	// A closed loopback port: refused fast, so the probe is quick and deterministic.
	tunnels := []config.Tunnel{{
		ID: "tnl_mc", Name: "mc", Type: config.TunnelTCP, LocalAddr: "127.0.0.1:1", Enabled: true,
		Options: config.TunnelOptions{HealthMaxFailed: 3},
	}}
	states := map[string]*healthState{}

	a.probeTunnels(context.Background(), tunnels, states)

	if _, known := a.LocalUp("tnl_mc"); known {
		t.Error("a pointed tunnel reached a verdict on its first failed probe — the debounce was bypassed")
	}
	if st := states["tnl_mc"]; st == nil || st.failures != 1 {
		t.Errorf("the probe did not run against a pointed tunnel: %+v", st)
	}
}
