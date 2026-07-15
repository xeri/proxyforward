// The Persister seam: the Store snapshots itself into plain data and hands
// it to whatever medium the host wires in (SQLite via internal/analytics in
// production, an in-memory fake in tests). stats deliberately imports nothing
// from the rest of the project, so the interface lives here and the
// implementations elsewhere.
package stats

// TierSnapshot is one persisted tier's valid buckets plus the bookkeeping a
// Persister needs to write incrementally and expire lapped slots.
type TierSnapshot struct {
	// AgentID owns this tier's series; "" is the gateway-wide/global history.
	AgentID string
	Tier    int   // index into the store's tier ladder
	ResMs   int64 // bucket resolution

	// FloorT is the oldest bucket start still inside the ring window; rows
	// with T < FloorT have lapped out and should be deleted by the persister.
	FloorT int64

	// DirtyFromT: buckets with T >= DirtyFromT may have changed since the
	// last successful save (completed buckets never mutate; only each tier's
	// current bucket does). 0 means everything must be written.
	DirtyFromT int64

	Buckets []Bucket // valid buckets, oldest first
}

// SnapshotData is the persistable image of a Store.
type SnapshotData struct {
	Lifetime Lifetime
	Peers    []PeerStat
	Tiers    []TierSnapshot

	// DeleteAgents names agent histories evicted since the last save; the
	// Persister must drop their rrd rows so a gateway that has cycled through
	// many agent ids does not accumulate dead series on disk.
	DeleteAgents []string
}

// Persister stores and restores snapshots. LoadStats returning (nil, nil)
// means "nothing stored yet"; a load error never blocks the caller — the
// Store logs it and starts fresh.
type Persister interface {
	LoadStats() (*SnapshotData, error)
	SaveStats(*SnapshotData) error
}
