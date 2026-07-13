package engine

import (
	"encoding/json"
	"fmt"
	"testing"

	"proxyforward/internal/conntrack"
	"proxyforward/internal/control"
	"proxyforward/internal/ipc"
)

// TestSetStatusConnsClamp (D7): 1000 live connections must clamp to the
// newest MaxStatusConns with the truncation flag set, and the marshalled
// status must stay under the IPC frame limit.
func TestSetStatusConnsClamp(t *testing.T) {
	snaps := make([]conntrack.Snapshot, 1000)
	for i := range snaps {
		snaps[i] = conntrack.Snapshot{
			ID:         uint64(i + 1),
			TunnelID:   "tunnel-with-a-real-id",
			TunnelName: "My Minecraft Server",
			ClientAddr: "[2001:0db8:85a3:0000:0000:8a2e:0370:7334]:65535", // worst-case address width
			StartedAt:  1700000000000,
			BytesIn:    1 << 40,
			BytesOut:   1 << 40,
			PlayerName: "SomePlayerName16",
			PlayerUUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5",
			Protocol:   767,
			RttMs:      123.456789,
		}
	}
	var st ipc.Status
	setStatusConns(&st, snaps)

	if len(st.Connections) != ipc.MaxStatusConns {
		t.Fatalf("connections = %d, want %d", len(st.Connections), ipc.MaxStatusConns)
	}
	if !st.ConnectionsTruncated || st.ConnectionsTotal != 1000 {
		t.Fatalf("truncated=%v total=%d, want true/1000", st.ConnectionsTruncated, st.ConnectionsTotal)
	}
	// Newest (highest-ID) connections survive.
	if st.Connections[0].ID != 1000-uint64(ipc.MaxStatusConns)+1 || st.Connections[len(st.Connections)-1].ID != 1000 {
		t.Fatalf("kept range %d..%d, want the newest %d",
			st.Connections[0].ID, st.Connections[len(st.Connections)-1].ID, ipc.MaxStatusConns)
	}
	// The worst-case clamped status must fit the frame.
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) >= control.MaxFrame {
		t.Fatalf("clamped status marshals to %d bytes, exceeds MaxFrame %d", len(b), control.MaxFrame)
	}

	// Under the cap: passthrough untouched.
	var small ipc.Status
	setStatusConns(&small, snaps[:5])
	if small.ConnectionsTruncated || small.ConnectionsTotal != 5 || len(small.Connections) != 5 {
		t.Fatalf("small set mangled: %+v", fmt.Sprintf("trunc=%v total=%d n=%d",
			small.ConnectionsTruncated, small.ConnectionsTotal, len(small.Connections)))
	}
}
