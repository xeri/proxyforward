package gateway

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"proxyforward/internal/control"
)

// worstCaseView is an AgentView with every field at the largest value the roster
// can actually produce: a live agent with a nickname, a hostname, a bracketed IPv6
// address, a version and transport, and a scope naming several ports and full-width
// tunnel ids. maxAgentsPerPoll is sized against this row, so the test builds it
// rather than trusting the estimate in that constant's comment.
func worstCaseView(i int) AgentView {
	return AgentView{
		AgentID:      fmt.Sprintf("agt_%016x", i),
		Nickname:     "Survival Server (Frankfurt) — spare box",
		ScopePorts:   []int{25565, 25566, 25567, 25568, 25569, 25570},
		ScopeTunnels: []string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)},
		IssuedAtMs:   1700000000000,
		EnrolledVia:  "invite for the second minigames box",
		Headless:     true,
		PausedAll:    true,
		PausedAllBy:  "both",
		Connected:    true,
		Hostname:     "SURVIVAL-RIG-FRANKFURT-01",
		RemoteIP:     "[2001:0db8:85a3:0000:0000:8a2e:0370:7334]",

		LinkUpSinceMs:   1700000000000,
		Tunnels:         12,
		Version:         "0.1.0-dev+build.20260727",
		Transport:       "per-conn",
		PauseSupported:  true,
		PendingInbound:  true,
		PendingOutbound: true,
	}
}

// TestAgentRosterPageFitsFrame (D7) pins maxAgentsPerPoll to the frame it exists to
// respect. A full page of worst-case rows must be a frame the daemon can actually
// write: the roster's failure mode is not a truncated list but no list at all — the
// daemon serves "reply exceeds frame limit", the GUI hook swallows it into the
// written-empty state, and every later poll asks the same impossible question.
func TestAgentRosterPageFitsFrame(t *testing.T) {
	page := make([]AgentView, maxAgentsPerPoll)
	for i := range page {
		page[i] = worstCaseView(i)
	}
	if err := control.WriteMsg(io.Discard, "ipc_analytics_resp", page); err != nil {
		t.Fatalf("a full page of %d agents is not a writable frame: %v", maxAgentsPerPoll, err)
	}

	// And the cap has to be doing something: one page past it must NOT fit, or the
	// test would keep passing after AgentView grew enough to make the real cap wrong.
	over := make([]AgentView, 0, 2*maxAgentsPerPoll)
	for i := 0; i < 2*maxAgentsPerPoll; i++ {
		over = append(over, worstCaseView(i))
	}
	if err := control.WriteMsg(io.Discard, "ipc_analytics_resp", over); err == nil {
		t.Fatalf("%d agents still fit one frame — maxAgentsPerPoll is not the bound it claims", len(over))
	}
}

// TestAgentRosterPagesLoseNothing (D7) is the regression for a roster that never
// loaded on a gateway with enough agents to need one. Paging must deliver every
// enrolled agent across successive calls, in agentID order, with no gaps and no
// repeats — chunking is only an acceptable answer if it is lossless.
func TestAgentRosterPagesLoseNothing(t *testing.T) {
	s, err := LoadAgentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// Two and a bit pages, so the walk covers a full page, a middle page, and a
	// short final one.
	const total = 2*maxAgentsPerPoll + 7
	for i := 0; i < total; i++ {
		ticket, err := s.IssueEnrollment(false, time.Time{}, Scope{}, "")
		if err != nil {
			t.Fatal(err)
		}
		key := make([]byte, 32)
		key[0], key[1] = byte(i), byte(i>>8)
		if _, err := s.Enroll(key, fmt.Sprintf("agt_%016x", i), ticket, now); err != nil {
			t.Fatalf("enrolling agent %d: %v", i, err)
		}
	}
	g := &Gateway{agents: s}

	var got []AgentView
	cursor := ""
	for polls := 0; polls < total+2; polls++ {
		page := g.ListAgentViewsAfter(cursor)
		if len(page) == 0 {
			break
		}
		if len(page) > maxAgentsPerPoll {
			t.Fatalf("poll %d answered %d agents, over the %d cap", polls, len(page), maxAgentsPerPoll)
		}
		got = append(got, page...)
		cursor = page[len(page)-1].AgentID
	}

	if len(got) != total {
		t.Fatalf("paged %d agents, want all %d", len(got), total)
	}
	for i := 1; i < len(got); i++ {
		if got[i].AgentID <= got[i-1].AgentID {
			t.Fatalf("agent %d (%s) does not follow %s — the pages gapped or repeated",
				i, got[i].AgentID, got[i-1].AgentID)
		}
	}
}
