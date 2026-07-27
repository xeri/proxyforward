package ipc

import (
	"fmt"
	"testing"

	"proxyforward/internal/conntrack"
	"proxyforward/internal/control"
)

// worstConns builds n connection snapshots with every field at its widest
// (bracketed IPv6 address, full player identity) — the shape that decides
// whether the frame fits.
func worstConns(n int) []conntrack.Snapshot {
	out := make([]conntrack.Snapshot, n)
	for i := range out {
		out[i] = conntrack.Snapshot{
			ID:         uint64(i + 1),
			AgentID:    "agt_0123456789abcdef",
			TunnelID:   "tnl_0123456789abcdef",
			TunnelName: "Survival Server (Europe)",
			ClientAddr: "[2001:0db8:85a3:0000:0000:8a2e:0370:7334]:65535",
			StartedAt:  1700000000000,
			BytesIn:    1 << 40,
			BytesOut:   1 << 40,
			PlayerName: "SomePlayerName16",
			PlayerUUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5",
			Protocol:   767,
			RttMs:      123.456789,
		}
	}
	return out
}

func worstAgents(n int) []AgentStatus {
	out := make([]AgentStatus, n)
	for i := range out {
		out[i] = AgentStatus{
			AgentID: fmt.Sprintf("agt_%016x", i), Hostname: "some-machine-name.lan",
			LANIPs: []string{"192.168.1.100", "10.0.0.5"}, RemoteIP: "203.0.113.45",
			LinkUpSinceMs: 1700000000000, RTTMillis: 42, JitterMillis: 3.5,
			PacketLossPct: 0.5, HealthScore: "good",
			LinkBytesIn: 1 << 40, LinkBytesOut: 1 << 40, Tunnels: 32, Players: 12,
		}
	}
	return out
}

func worstTunnels(n int) []TunnelStatus {
	out := make([]TunnelStatus, n)
	for i := range out {
		out[i] = TunnelStatus{
			AgentID: fmt.Sprintf("agt_%016x", i/32), ID: fmt.Sprintf("tnl_%016x", i),
			Name: "Survival Server (Europe)", PublicPort: 25565, LocalUp: true, LocalKnown: true,
			BandwidthLimitMbps: 100, BandwidthLimitScope: "per-direction",
			Paused: true, PausedBy: "both", ServerStatusKnown: true,
			ServerMOTD:    "A Minecraft Server with a reasonably long message of the day",
			ServerVersion: "1.21.4", ServerPlayersOnline: 12, ServerPlayersMax: 100,
		}
	}
	return out
}

// TestFitStatusFitsFrame (D7) is the regression for a status frame the daemon
// could not write. Status carries three lists that grow on independent axes —
// live players, fleet size, tunnels per agent — and only two of them were ever
// clamped. A gateway with a busy public port and a handful of multi-tunnel
// agents marshalled past control.MaxFrame; WriteMsg refused the frame and
// serveConn returned, so the pipe connection was torn down on every status
// poll and the GUI could never read the gateway that most needed watching.
//
// The bound must hold for any combination of the three, including the absurd
// one, and it must hold on the exact bytes rather than on arithmetic in a
// comment — a field added to any of these structs must not quietly re-break it.
func TestFitStatusFitsFrame(t *testing.T) {
	cases := []struct {
		name                   string
		conns, agents, tunnels int
	}{
		{"empty", 0, 0, 0},
		{"typical home gateway", 12, 2, 4},
		{"busy port, small fleet", 400, 3, 9},
		{"the reported break: full port + modest fleet", 1000, 6, 40},
		{"every list past its ceiling", 5000, 200, 2000},
		{"fleet at the listener cap", 800, 64, 64 * 32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := Status{
				Role: "gateway", Version: "1.2.3", PID: 12345, RunState: "running",
				LocalHostname: "some-machine-name.lan",
				LocalLANIPs:   []string{"192.168.1.100", "10.0.0.5"},
				PeerLANIPs:    []string{"192.168.1.100", "10.0.0.5"},
				ConfigPath:    `C:\Users\someuser\AppData\Roaming\proxyforward\config.toml`,
				HealthScore:   "good", PublicIP: "203.0.113.45", PeerPublicIP: "198.51.100.7",
				PeerHostname: "gateway-machine.example.com", PeerAddr: "203.0.113.45:8474",
				Connections: worstConns(tc.conns),
				Agents:      worstAgents(tc.agents),
				Tunnels:     worstTunnels(tc.tunnels),
			}
			FitStatus(&st)

			// The frame the daemon actually writes, envelope and all.
			var n countingWriter
			if err := control.WriteMsg(&n, TypeStatusResp, st); err != nil {
				t.Fatalf("status frame rejected: %v (conns=%d agents=%d tunnels=%d)",
					err, len(st.Connections), len(st.Agents), len(st.Tunnels))
			}
			if n.n > control.MaxFrame {
				t.Fatalf("frame = %d bytes, exceeds MaxFrame %d", n.n, control.MaxFrame)
			}

			// Totals report the real counts even when the lists do not.
			if st.ConnectionsTotal != tc.conns || st.TunnelsTotal != tc.tunnels || st.AgentsTotal != tc.agents {
				t.Fatalf("totals = conns %d / tunnels %d / agents %d, want %d / %d / %d",
					st.ConnectionsTotal, st.TunnelsTotal, st.AgentsTotal, tc.conns, tc.tunnels, tc.agents)
			}
			// A shortened list must say it is short, or it reads as the whole set.
			if (len(st.Connections) < tc.conns) != st.ConnectionsTruncated {
				t.Fatalf("connections %d of %d, truncated=%v", len(st.Connections), tc.conns, st.ConnectionsTruncated)
			}
			if (len(st.Tunnels) < tc.tunnels) != st.TunnelsTruncated {
				t.Fatalf("tunnels %d of %d, truncated=%v", len(st.Tunnels), tc.tunnels, st.TunnelsTruncated)
			}
			// Shedding degrades every surface together: no list is blanked to
			// spare another while the others still hold rows.
			if tc.conns > 0 && len(st.Connections) == 0 {
				t.Fatal("connections blanked entirely")
			}
			if tc.tunnels > 0 && len(st.Tunnels) == 0 {
				t.Fatal("tunnels blanked entirely")
			}
			if tc.agents > 0 && len(st.Agents) == 0 {
				t.Fatal("agents blanked entirely")
			}
		})
	}
}

// TestFitStatusLeavesSmallStatusUntouched (D7): the common case must pay
// nothing and lose nothing — no truncation flags, no rows dropped.
func TestFitStatusLeavesSmallStatusUntouched(t *testing.T) {
	st := Status{
		Connections: worstConns(5),
		Agents:      worstAgents(2),
		Tunnels:     worstTunnels(3),
	}
	FitStatus(&st)
	if len(st.Connections) != 5 || len(st.Agents) != 2 || len(st.Tunnels) != 3 {
		t.Fatalf("lists changed: conns=%d agents=%d tunnels=%d", len(st.Connections), len(st.Agents), len(st.Tunnels))
	}
	if st.ConnectionsTruncated || st.TunnelsTruncated {
		t.Fatalf("small status flagged truncated: conns=%v tunnels=%v", st.ConnectionsTruncated, st.TunnelsTruncated)
	}
}

// TestStatusEstimateIsPessimistic (D7): the cheap estimate exists to skip the
// exact check, so it must never under-count — an under-estimate would let an
// oversize frame through the fast path.
func TestStatusEstimateIsPessimistic(t *testing.T) {
	for _, n := range []int{0, 1, 17, 150} {
		st := Status{
			Role: "gateway", LocalHostname: "some-machine-name.lan",
			ConfigPath:  `C:\Users\someuser\AppData\Roaming\proxyforward\config.toml`,
			Connections: worstConns(n), Agents: worstAgents(n), Tunnels: worstTunnels(n),
		}
		if est, exact := statusEstimate(&st), statusSize(&st); est < exact {
			t.Fatalf("n=%d: estimate %d under-counts the exact %d", n, est, exact)
		}
	}
}

type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }
