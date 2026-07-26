package agent

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/mc"
)

// healthAgent builds an agent with the given tunnels and a fast agent-wide
// cadence. The checker goroutine is not started — most of these tests drive
// probeTunnels/recordProbe directly, so the failure counts are exact rather
// than a race with a ticker.
func healthAgent(t *testing.T, tunnels []config.Tunnel) *Agent {
	t.Helper()
	cfg := config.Default()
	cfg.Agent.Tunnels = tunnels
	a := New(cfg, t.TempDir(), slog.New(slog.DiscardHandler))
	a.healthInterval = 20 * time.Millisecond
	a.healthDialTimeout = 300 * time.Millisecond
	return a
}

// acceptingBackend is a listener that accepts and immediately closes — the
// shape a bare TCP probe calls healthy.
func acceptingBackend(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln.Addr().String()
}

// TestHealthMaxFailedDebounces: with max_failed 3 a single failed probe must not
// take the tunnel down — a backend that stutters under load should not flap the
// gateway's routing — but three consecutive ones must.
func TestHealthMaxFailedDebounces(t *testing.T) {
	addr := acceptingBackend(t)
	a := healthAgent(t, []config.Tunnel{{
		ID: "t1", Name: "test", Type: config.TunnelTCP, LocalAddr: addr, Enabled: true,
		Options: config.TunnelOptions{HealthMaxFailed: 3},
	}})
	tun := a.snapshotTunnels()[0]
	st := &healthState{}
	a.recordProbe(tun, st, &mc.StatusInfo{}) // seed: up

	for i := 1; i <= 2; i++ {
		a.recordProbe(tun, st, nil)
		if up, _ := a.LocalUp("t1"); !up {
			t.Fatalf("failure %d of 3 took the tunnel down", i)
		}
	}
	a.recordProbe(tun, st, nil)
	if up, _ := a.LocalUp("t1"); up {
		t.Fatal("three consecutive failures must take the tunnel down")
	}
}

// TestHealthFailureCounterResetsOnSuccess: the counter is CONSECUTIVE failures.
// frp's equivalent never resets, so after the backend's first ever blip its
// max-failed setting silently degrades to 1 and two failures an hour apart take
// the tunnel down. A success in between must clear the count here.
func TestHealthFailureCounterResetsOnSuccess(t *testing.T) {
	addr := acceptingBackend(t)
	a := healthAgent(t, []config.Tunnel{{
		ID: "t1", Name: "test", Type: config.TunnelTCP, LocalAddr: addr, Enabled: true,
		Options: config.TunnelOptions{HealthMaxFailed: 2},
	}})
	tun := a.snapshotTunnels()[0]
	st := &healthState{}

	a.recordProbe(tun, st, nil)              // fail 1 of 2
	a.recordProbe(tun, st, &mc.StatusInfo{}) // success clears the counter
	if st.failures != 0 {
		t.Fatalf("failures = %d after a success, want 0", st.failures)
	}
	a.recordProbe(tun, st, nil) // fail 1 of 2 again, not 2 of 2
	if up, known := a.LocalUp("t1"); !known || !up {
		t.Fatal("a lone failure after a success took the tunnel down: the counter did not reset")
	}
	a.recordProbe(tun, st, nil)
	if up, _ := a.LocalUp("t1"); up {
		t.Fatal("two consecutive failures should take it down")
	}
	// Recovery is never debounced: one success is enough.
	a.recordProbe(tun, st, &mc.StatusInfo{})
	if up, _ := a.LocalUp("t1"); !up {
		t.Fatal("a single success must flip the tunnel back up")
	}
}

// TestHealthPerTunnelIntervals: one goroutine serves tunnels on different
// intervals, probing each only when its own has elapsed, and the loop's tick
// follows the fastest of them.
func TestHealthPerTunnelIntervals(t *testing.T) {
	addr := acceptingBackend(t)
	a := healthAgent(t, []config.Tunnel{
		{ID: "fast", Name: "fast", Type: config.TunnelTCP, LocalAddr: addr, Enabled: true},
		{ID: "slow", Name: "slow", Type: config.TunnelTCP, LocalAddr: addr, Enabled: true,
			Options: config.TunnelOptions{HealthIntervalSec: 60}},
	})
	tunnels := a.snapshotTunnels()
	states := map[string]*healthState{}

	a.probeTunnels(context.Background(), tunnels, states)
	fastFirst, slowFirst := states["fast"].lastProbe, states["slow"].lastProbe
	time.Sleep(40 * time.Millisecond)
	a.probeTunnels(context.Background(), tunnels, states)

	if !states["fast"].lastProbe.After(fastFirst) {
		t.Fatal("the 20 ms tunnel was not re-probed after 40 ms")
	}
	if !states["slow"].lastProbe.Equal(slowFirst) {
		t.Fatal("the 60 s tunnel was re-probed after 40 ms; its interval is not honoured")
	}
	if tick := a.tickFor(tunnels); tick > 20*time.Millisecond {
		t.Fatalf("tick = %v, want it pulled down to the fastest interval", tick)
	}
}

// TestHealthUDPStaysUnknown: a UDP tunnel is never probed and never gets a
// verdict. Regression for a tunnel switched from tcp to udp, whose stale entry
// must not outlive the change.
func TestHealthUDPStaysUnknown(t *testing.T) {
	addr := acceptingBackend(t)
	a := healthAgent(t, []config.Tunnel{
		{ID: "t1", Name: "t", Type: config.TunnelTCP, LocalAddr: addr, Enabled: true},
	})
	states := map[string]*healthState{}
	a.probeTunnels(context.Background(), a.snapshotTunnels(), states)
	if up, known := a.LocalUp("t1"); !known || !up {
		t.Fatal("setup: the tcp tunnel should be up")
	}

	a.cfgMu.Lock()
	a.cfg.Agent.Tunnels[0].Type = config.TunnelUDP
	a.cfgMu.Unlock()
	a.probeTunnels(context.Background(), a.snapshotTunnels(), states)

	if _, known := a.LocalUp("t1"); known {
		t.Fatal("a udp tunnel kept a health verdict; it must read unknown")
	}
	if len(states) != 0 {
		t.Fatalf("udp tunnel kept probe state: %+v", states)
	}
}

// TestHealthMCStatusProbeRejectsSilentBackend is the whole reason the probe kind
// exists: a socket that accepts and never speaks passes the bare dial and must
// fail the status ping. Same address, two tunnels, opposite verdicts.
func TestHealthMCStatusProbeRejectsSilentBackend(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // accepted and never spoken to
			mu.Unlock()
		}
	}()

	a := healthAgent(t, []config.Tunnel{
		{ID: "dial", Name: "dial", Type: config.TunnelTCP, LocalAddr: ln.Addr().String(), Enabled: true},
		{ID: "ping", Name: "ping", Type: config.TunnelTCP, LocalAddr: ln.Addr().String(), Enabled: true,
			Options: config.TunnelOptions{HealthProbe: config.HealthProbeMCStatus, HealthTimeoutSec: 1}},
	})
	a.probeTunnels(context.Background(), a.snapshotTunnels(), map[string]*healthState{})

	if up, known := a.LocalUp("dial"); !known || !up {
		t.Fatal("the bare dial should call a silent accepting socket up — that is its known weakness")
	}
	if up, known := a.LocalUp("ping"); !known || up {
		t.Fatal("the mc-status probe must call a silent socket down")
	}
	if a.ServerStatus("ping") != nil {
		t.Fatal("a failed probe must leave no status behind")
	}
}

// TestHealthMCStatusRetainsServerReport: a successful status ping is what the
// GUI's players/MOTD reading comes from, and it must not survive the server
// going away.
func TestHealthMCStatusRetainsServerReport(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = mc.ServeOffline(c, mc.OfflineInfo{MOTD: "A Minecraft Server", VersionName: "1.21.4"})
			}(c)
		}
	}()

	a := healthAgent(t, []config.Tunnel{{
		ID: "t1", Name: "mc", Type: config.TunnelTCP, LocalAddr: ln.Addr().String(), Enabled: true,
		Options: config.TunnelOptions{HealthProbe: config.HealthProbeMCStatus, HealthTimeoutSec: 2},
	}})
	tun := a.snapshotTunnels()[0]
	states := map[string]*healthState{}
	a.probeTunnels(context.Background(), []config.Tunnel{tun}, states)

	info := a.ServerStatus("t1")
	if info == nil {
		t.Fatal("a successful status ping must be retained for the GUI")
	}
	if info.MOTD != "A Minecraft Server" || info.Version != "1.21.4" {
		t.Fatalf("retained status = %+v", info)
	}

	a.recordProbe(tun, states["t1"], nil)
	if a.ServerStatus("t1") != nil {
		t.Fatal("a down server's last report must be dropped, not shown as current")
	}
}
