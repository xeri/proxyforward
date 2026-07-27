package engine

// The pause op — the pipe half of the pause verbs, so an attached GUI (or a
// future CLI) can pause a daemon it does not host. Like the lifecycle and
// agent-admin ops it acts on the live role object, never the analytics store,
// so it answers with the DB down. It rides the pipe that carries
// OpIssuePairing: the ACL (SYSTEM, Administrators, the serving account —
// ipc/security_windows.go pipeSecurity) is not widened by this, and pausing is
// strictly less powerful than the revoke/config ops already served.

import (
	"encoding/json"
	"fmt"

	"proxyforward/internal/config"
)

// OpSetPause flips one pause bit on this engine's role: the gateway's own bit
// for one of an agent's tunnels (agentId set), or the agent's own local bit
// (agentId ignored). tunnelId "" = the whole agent.
const OpSetPause = "set_pause"

func isPauseOp(op string) bool { return op == OpSetPause }

func (e *Engine) pauseOp(body json.RawMessage) (json.RawMessage, error) {
	var q struct {
		AgentID    string `json:"agentId"`
		TunnelID   string `json:"tunnelId"`
		Paused     bool   `json:"paused"`
		Disconnect bool   `json:"disconnect"`
	}
	if err := decodeBody(body, &q); err != nil {
		return nil, err
	}
	if err := e.SetPause(q.AgentID, q.TunnelID, q.Paused, q.Disconnect); err != nil {
		return nil, err
	}
	return encodeResult(struct {
		OK bool `json:"ok"`
	}{true}, nil)
}

// SetPause is the in-process form of OpSetPause — the GUI host calls it
// directly; attached GUIs reach it through the op above.
func (e *Engine) SetPause(agentID, tunnelID string, paused, disconnect bool) error {
	switch {
	case e.Gateway != nil:
		if agentID == "" {
			return fmt.Errorf("set_pause on a gateway names the agent")
		}
		if !e.Gateway.SetPause(agentID, tunnelID, paused, disconnect) {
			return fmt.Errorf("no enrolled agent %q", agentID)
		}
		return nil
	case e.Agent != nil:
		return e.Agent.SetPause(tunnelID, paused, disconnect)
	}
	return fmt.Errorf("no role is running")
}

// SetHeadless marks this process as running with no GUI attached (service/CLI
// hosts); the flag rides the hello exchange so the peer's UI can temper
// expectations. Call after New, before Run.
func (e *Engine) SetHeadless(on bool) {
	if e.Agent != nil {
		e.Agent.SetHeadless(on)
	}
	if e.Gateway != nil {
		e.Gateway.SetHeadless(on)
	}
}

// pausedBy folds the two sides' bits into the status fields ("mine" is the
// reporting side's own).
func pausedBy(mine, theirs bool) (bool, string) {
	switch {
	case mine && theirs:
		return true, "both"
	case mine:
		return true, "mine"
	case theirs:
		return true, "theirs"
	}
	return false, ""
}

// persistPause is the agent's default pause persister for hostless runs
// (service/CLI). The agent has already flipped these bits in the copy it reads
// from — the same document here — but the write stays, so the persister is
// self-contained and means the same thing in both hosting modes. A GUI host
// overrides it with its own hook, which writes the app's separate copy.
func (e *Engine) persistPause(agentWide bool, tunnels []config.Tunnel) error {
	e.cfgWrite(func(c *config.Config) {
		c.Agent.Paused = agentWide
		c.Agent.Tunnels = tunnels
	})
	return e.saveConfig()
}
