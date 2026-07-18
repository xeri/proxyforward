package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"proxyforward/internal/ipc"
)

// OpSetPause answers over the pipe like every live-engine op — here against a
// gateway with no such agent, which must come back as a served error naming
// the problem, not a transport failure (the pipe stays healthy). (pause)
func TestSetPauseOpAnswersOverPipe(t *testing.T) {
	eng := newTestEngine(t, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	startSupervisor(t, eng)

	c, err := ipc.Dial(time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	body, _ := json.Marshal(struct {
		AgentID string `json:"agentId"`
		Paused  bool   `json:"paused"`
	}{"agt_ghost", true})
	_, err = c.Analytics(OpSetPause, body)
	if err == nil {
		t.Fatal("set_pause for an unknown agent must be refused")
	}
	if !strings.Contains(err.Error(), "agt_ghost") {
		t.Fatalf("the refusal must name the agent: %v", err)
	}
	// The pipe survived the served error: a second op still answers.
	if _, err := c.Analytics(OpSetPause, nil); err == nil {
		t.Fatal("a gateway set_pause without an agentId must be refused")
	}
}
