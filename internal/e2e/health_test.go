package e2e

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"proxyforward/internal/config"
	"proxyforward/internal/mc"
)

// switchableBackend listens on one address and answers either as a Minecraft
// server or as a socket that accepts and says nothing — the "wrong process on
// the port" case a bare TCP dial cannot tell from a healthy server. The mode can
// be flipped without moving the address, so a health test can watch the gateway
// change its mind about the same backend.
type switchableBackend struct {
	addr    string
	speaks  atomic.Bool
	ln      net.Listener
	mu      sync.Mutex
	held    []net.Conn
	wg      sync.WaitGroup
	closing atomic.Bool
}

func newSwitchableBackend(t *testing.T) *switchableBackend {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &switchableBackend{addr: ln.Addr().String(), ln: ln}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if !b.speaks.Load() {
				b.mu.Lock()
				b.held = append(b.held, c) // accepted, never answered
				b.mu.Unlock()
				continue
			}
			b.wg.Add(1)
			go func(c net.Conn) {
				defer b.wg.Done()
				defer c.Close()
				_ = mc.ServeOffline(c, mc.OfflineInfo{MOTD: "e2e server", VersionName: "1.21.4"})
			}(c)
		}
	}()
	t.Cleanup(func() {
		b.closing.Store(true)
		ln.Close()
		b.mu.Lock()
		for _, c := range b.held {
			c.Close()
		}
		b.mu.Unlock()
		b.wg.Wait()
	})
	return b
}

// waitLocalUp blocks until the gateway's view of the tunnel's backend matches
// want, or fails the test.
func waitLocalUp(t *testing.T, h *harness, want bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if up, known := h.gw.TunnelLocalUp(h.tunnelID); known && up == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	up, known := h.gw.TunnelLocalUp(h.tunnelID)
	t.Fatalf("gateway health never reached up=%v (have up=%v known=%v)", want, up, known)
}

// TestMCStatusProbeDrivesHealth: with the mc-status probe configured, the
// gateway's idea of the backend follows whether it actually serves Minecraft —
// not merely whether something is listening. The backend accepts connections
// throughout; only its willingness to answer a status ping changes, and the
// verdict must move both ways.
func TestMCStatusProbeDrivesHealth(t *testing.T) {
	backend := newSwitchableBackend(t)
	h := newHarnessWith(t, backend.addr, harnessOpts{tweakTunnel: func(tun *config.Tunnel) {
		tun.Options.HealthProbe = config.HealthProbeMCStatus
		tun.Options.HealthIntervalSec = 1
		tun.Options.HealthTimeoutSec = 1
	}})
	h.waitPublicPort()

	// A socket that accepts and never speaks: down, where a bare dial would
	// have called it up.
	waitLocalUp(t, h, false)

	backend.speaks.Store(true)
	waitLocalUp(t, h, true)

	backend.speaks.Store(false)
	// Existing connections are unaffected; only new probes see the change.
	waitLocalUp(t, h, false)
}
