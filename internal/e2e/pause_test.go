package e2e

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"proxyforward/internal/control"
)

// enrolledAgentID is the gateway-derived agt_ identity of the harness's single
// enrolled agent — the id the gateway's management verbs key on.
func enrolledAgentID(t *testing.T, h *harness) string {
	t.Helper()
	recs := h.gw.ListAgents()
	if len(recs) != 1 {
		t.Fatalf("expected exactly one enrolled agent, got %d", len(recs))
	}
	return recs[0].AgentID
}

// expectRefused dials the public port and expects the pause treatment: the
// listener accepts (the port is held!) and the connection closes without ever
// echoing — no offline MOTD is configured, so refusal is a clean close.
func expectRefused(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial while paused must still connect (the port is held): %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write([]byte("knock"))
	buf := make([]byte, 1)
	if n, err := conn.Read(buf); err == nil {
		t.Fatalf("a paused tunnel echoed %q — players got through", buf[:n])
	}
}

// waitRefused polls until new connections get the pause treatment — for the
// paths where the bit lands asynchronously (a reconnecting agent re-asserting
// its set after its listeners bind).
func waitRefused(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Write([]byte("knock"))
		buf := make([]byte, 1)
		_, rerr := conn.Read(buf)
		conn.Close()
		if rerr != nil {
			return // no echo: the pause landed
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the pause never landed: players still get through")
}

// waitServed polls until a round-trip succeeds again (resume, or a reconnect).
func waitServed(t *testing.T, addr string, payload []byte) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err == nil {
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err := conn.Write(payload); err == nil {
				buf := make([]byte, len(payload))
				if _, err := io.ReadFull(conn, buf); err == nil {
					conn.Close()
					return
				}
			}
			conn.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the tunnel never served again")
}

// TestPauseTurnsPlayersAway: a gateway-side pause holds the port and refuses
// new players at the door, spares a splice that was already flowing (draining
// is the default), drops it only on the explicit disconnect, and resumes on
// the same port with no rebind. (pause)
func TestPauseTurnsPlayersAway(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addr := h.waitPublicPort()
	roundTrip(t, addr, []byte("before pause"))
	agentID := enrolledAgentID(t, h)

	// A connection opened before the pause, kept alive across it.
	live, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	echoOn := func(msg string) error {
		live.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := live.Write([]byte(msg)); err != nil {
			return err
		}
		buf := make([]byte, len(msg))
		_, err := io.ReadFull(live, buf)
		return err
	}
	if err := echoOn("pre"); err != nil {
		t.Fatalf("live conn before pause: %v", err)
	}

	// Pause (no disconnect): SetPause runs through the actor synchronously,
	// so the very next public connection sees the bit.
	if !h.gw.SetPause(agentID, h.tunnelID, true, false) {
		t.Fatal("SetPause refused a known agent")
	}
	expectRefused(t, addr)
	// Draining is the default: the pre-pause splice still echoes.
	if err := echoOn("mid-pause"); err != nil {
		t.Fatalf("pause dropped a live connection it should have drained: %v", err)
	}

	// The deliberate extra: pause with disconnect kicks the live player.
	if !h.gw.SetPause(agentID, h.tunnelID, true, true) {
		t.Fatal("SetPause (disconnect) refused")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := echoOn(fmt.Sprintf("kick? %d", time.Now().UnixNano())); err != nil {
			break // the kick landed
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnect never dropped the live connection")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Resume: the same port serves again — never rebound, just un-covered.
	if !h.gw.SetPause(agentID, h.tunnelID, false, false) {
		t.Fatal("resume refused")
	}
	waitServed(t, addr, []byte("after resume"))
}

// TestPauseSurvivesReconnect: both sides' bits are persisted locally and
// re-asserted on connect — an agent restart brings the tunnel back paused,
// from either side's bit, and resuming one side leaves the other's standing.
// (pause)
func TestPauseSurvivesReconnect(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarness(t, echoAddr)
	addr := h.waitPublicPort()
	roundTrip(t, addr, []byte("hello"))
	agentID := enrolledAgentID(t, h)

	// Pin the port so the restarted agent rebinds the same address.
	port := h.agent.MustPublicPort(h.tunnelID)
	h.agentCfg.Agent.Tunnels[0].PublicPort = port
	pinned := fmt.Sprintf("127.0.0.1:%d", port)

	// The agent pauses its own tunnel (its bit lives in its config, which the
	// harness shares with the restarted agent below).
	if err := h.agent.SetPause(h.tunnelID, true, false); err != nil {
		t.Fatal(err)
	}
	waitRefused(t, pinned)

	// The gateway pauses too — two independent bits are now set.
	if !h.gw.SetPause(agentID, h.tunnelID, true, false) {
		t.Fatal("gateway SetPause refused")
	}

	// Restart the agent: the listener rebinds, and both bits must still hold
	// (the agent re-asserts its set; the gateway seeds its own at admission).
	//
	// Both waits are load-bearing, and neither is a stand-in for the other. The
	// old listener stays bound and paused until the gateway notices the agent is
	// gone, and "accepts but does not echo" — what waitRefused looks for — is
	// already true of it. Without waiting for the port to be released first, that
	// check is answered by the session that just died and returns during the
	// window where the old listener is closed and the new one does not exist yet;
	// the assertions below then dial a port nothing is listening on.
	h.stopAgent()
	h.waitTunnelUnbound()
	h.startAgent()
	h.waitTunnelRebound()
	waitRefused(t, pinned)

	// The agent resumes its own bit — the gateway's still stands: each side
	// clears only its own, so one side's resume can never override the other.
	if err := h.agent.SetPause(h.tunnelID, false, false); err != nil {
		t.Fatal(err)
	}
	expectRefused(t, pinned)

	// The gateway clears its bit too — now it serves.
	if !h.gw.SetPause(agentID, h.tunnelID, false, false) {
		t.Fatal("gateway resume refused")
	}
	waitServed(t, pinned, []byte("both cleared"))
}

// TestLegacyPeerPause: against a peer that never learned CapPause, no
// pause_state frame crosses the wire — yet each side's own enforcement still
// stands, because enforcement is local-first: the gateway's bit gates its own
// listener, the agent's bit refuses its own local dial. What a legacy pairing
// loses is only cross-side visibility, and the status fields say so honestly
// (the peer's column stays empty). (pause)
func TestLegacyPeerPause(t *testing.T) {
	echoAddr, closeEcho := echoServer(t)
	defer closeEcho()
	h := newHarnessWith(t, echoAddr, harnessOpts{offerCaps: []string{control.CapTunnelSync, control.CapConnStats}})
	addr := h.waitPublicPort()
	roundTrip(t, addr, []byte("legacy"))
	agentID := enrolledAgentID(t, h)

	// The agent pauses: no capability, so nothing is sent — the gateway's view
	// of the agent's bits must stay empty.
	if err := h.agent.SetPause(h.tunnelID, true, false); err != nil {
		t.Fatal(err)
	}
	for _, ts := range h.gw.Tunnels() {
		if ts.ID == h.tunnelID && ts.PausedTheirs {
			t.Fatal("a legacy session leaked a pause_state frame to the gateway")
		}
	}
	// Yet the agent's own enforcement refuses the local dial, so players still
	// cannot get through — the gateway just can't say why.
	waitRefused(t, addr)
	if err := h.agent.SetPause(h.tunnelID, false, false); err != nil {
		t.Fatal(err)
	}
	waitServed(t, addr, []byte("agent resumed"))

	// The gateway's own bit gates its own listener — no wire needed at all.
	if !h.gw.SetPause(agentID, h.tunnelID, true, false) {
		t.Fatal("gateway SetPause refused")
	}
	expectRefused(t, addr)
	if !h.gw.SetPause(agentID, h.tunnelID, false, false) {
		t.Fatal("gateway resume refused")
	}
	waitServed(t, addr, []byte("gateway resumed"))
}
