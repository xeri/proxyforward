package analytics

import (
	"testing"
	"time"

	"proxyforward/internal/conntrack"
)

// TestRecorderAttributesSessionsToAgent drives the real recorder through the
// real conntrack registry: two agents open connections on the SAME tunnel_id,
// and each connection's sessions row must carry its own agent_id. This is the
// end-to-end proof of the attribution fix — without it, A's and B's history on
// a shared tunnelID would commingle.
func TestRecorderAttributesSessionsToAgent(t *testing.T) {
	d := openTest(t, t.TempDir())
	rec := d.NewRecorder(nil)
	reg := conntrack.NewRegistry()
	reg.SetHooks(
		func(e *conntrack.Entry) { rec.SessionOpened(e) },
		func(e *conntrack.Entry, in, out int64) { rec.SessionClosed(e, in, out) },
		nil, nil,
	)

	// Both agents serve tunnel_id "web" — the collision that used to commingle.
	_, closeA := reg.Open("agentA", "web", "mc", "203.0.113.1:1", "kA", true)
	_, closeB := reg.Open("agentB", "web", "mc", "203.0.113.2:1", "kB", true)
	defer closeA()
	defer closeB()
	d.Barrier() // flush the async writer

	type row struct{ agent, ip string }
	var got []row
	rows, err := d.sql.Query(`SELECT agent_id, client_ip FROM sessions ORDER BY agent_id`)
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.agent, &r.ip); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	rows.Close()
	if len(got) != 2 || got[0].agent != "agentA" || got[1].agent != "agentB" {
		t.Fatalf("session attribution = %+v, want one row each for agentA and agentB", got)
	}

	// The recorded (not hand-seeded) rows isolate per agent through the query.
	if s, _ := d.Sessions(SessionsQuery{AgentID: "agentA", TunnelID: "web"}, time.Now().UnixMilli()); s.Total != 1 || s.Sessions[0].ClientIP != "203.0.113.1" {
		t.Fatalf("agentA/web = %+v, want only agentA's connection", s)
	}
	if s, _ := d.Sessions(SessionsQuery{AgentID: "agentB", TunnelID: "web"}, time.Now().UnixMilli()); s.Total != 1 || s.Sessions[0].ClientIP != "203.0.113.2" {
		t.Fatalf("agentB/web = %+v, want only agentB's connection", s)
	}
}
