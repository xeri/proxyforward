# Tunnel Groups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add gateway-side tunnel groups so several agents can share one public port for redundancy (`standby` failover) or load spreading (`spread`, least-connections), closing the frp-parity load-balancing gap.

**Architecture:** A group is a first-class gateway-side object in `gateway_agents.json`. Membership is a `GroupID` tag on a member's gateway-held `DesiredTunnels` entry. The actor owns a new `groupListeners` table (one shared socket per group, one accept loop) entirely separate from the per-agent `listeners` map — the solo-tunnel path is untouched. A per-connection `handleGroupClient` picks an eligible member (lock-free snapshot read) and splices through the **existing** data plane unchanged. No agent-facing wire change for the core feature.

**Tech Stack:** Go 1.x (single-writer actor + `atomic.Pointer` snapshots), the existing `relay.Splice` / `dataPlane.openFlow` data path, Wails v2 IPC bindings, React 19 GUI.

**Design spec:** `docs/superpowers/specs/2026-07-24-tunnel-groups-design.md` (rev. 2, approved). Read it before starting; every "why" lives there.

## Global Constraints

Copied verbatim from CLAUDE.md / the spec. Every task's requirements implicitly include this section.

- **Additive only.** The solo-tunnel path (`actor.go listeners`, `bindLocked`, `handleClient`) must be behaviorally unchanged. Group state is a separate `groupListeners` table.
- **Single-writer actor.** All group listener/member lifecycle runs on the actor's one `run()` goroutine (via `a.do`). Never nest `a.do` inside `a.do` (deadlocks the command loop). The pick is off-actor and lock-free: it reads an immutable `atomic.Pointer[[]*groupMember]` snapshot + per-member atomics only.
- **Group ghost-listener guarantee.** A group socket has exactly one accept loop; it is closed and its `done` channel awaited on the actor goroutine at `DeleteGroup`/shutdown before anything else proceeds — a rebound group port is provably free.
- **The gateway never decides what the agent exposes.** A member serves a tunnel only if the agent locally enabled it (`agent.go enabledTunnelByID` already refuses a data stream otherwise). Grouping adds no bypass.
- **Positive-health eligibility (divergence from solo).** Solo uses `unknown ⇒ up`. Grouped requires a **positive** `up` health report; unknown/down/paused/dead-session members are excluded from `pick`.
- **Caps bound every growth dimension.** `MaxGroups` and `MaxMembersPerGroup` are validated at the config layer (`SetGroup`). Membership is operator-assigned, so it is **not** counted against `MaxTunnelsPerAgent` (that cap bounds the listeners an agent allocates by asking).
- **Config is arbitration, not consent.** Group config (`SetGroup`/`DeleteGroup`, tagging) pushes immediately with an event notice; no member veto. Consistent with "gateway decides ports."
- **Wire compatibility.** Any new `control.TunnelSpec` field is `omitempty` + zero-value-safe; frames to/from legacy peers stay byte-identical (`internal/control/hello_compat_test.go`). `ProtocolVersion` stays `1`.
- **conntrack `ConnKey` is immutable after `Open`** — read unlocked (`TestConnKeyPlumbing` under `-race`). A new `groupID` is set at `Open`, never mutated.
- **Numbers live only in the numbers table** (`docs/agent/architecture.md`). New caps get a row each, cited by constant name.
- **No TODO/FIXME markers.** `go test ./...` must be green before each commit; commit one concern at a time (lowercase, scope-prefixed; protocol and implementation never in the same commit).
- **Burst floor.** `TestBurstThroughputAndCrossStreamLatency` (`internal/e2e/e2e_test.go`) must still pass; run it before/after Phase 4 (the `hot-path` skill; never lower the floor).
- **Project skills gate each phase:** Phase 2 → `wire-protocol`; Phase 4 → `hot-path`; Phase 5 → `backend-capability` + `ui-change`. Open the matching skill at that phase's start.

---

## New constants (numbers-table rows)

Add to `docs/agent/architecture.md` "The numbers" table in Phase 1, cited by symbol:

| Constant | Value | Where |
|---|---|---|
| Group caps | 16 groups / 16 members per group (operator-assigned membership, not counted against `MaxTunnelsPerAgent`) | `gateway/group.go MaxGroups MaxMembersPerGroup` |

These are gateway-package constants (like `eventRingCap`), not TOML config — the operator creates groups over IPC, not by editing a file.

---

## File structure

- **Create `internal/gateway/group.go`** — `GroupSpec`, `MaxGroups`/`MaxMembersPerGroup`, `groupListener`, `groupMember`, `agentTun` key, the `pick` policy functions, and group event kinds. One responsibility: everything about the group object and member selection that is *not* actor lifecycle or the store.
- **Create `internal/gateway/group_test.go`** — unit tests for `pick` (least-connections argmin + round-robin over sorted slice; standby min-ordinal) and cap/duplicate validation.
- **Modify `internal/gateway/agentstore.go`** — `storeFile.Groups`, `AgentStore.Groups()`/`SetGroup`/`DeleteGroup`, duplicate-tunnel-per-agent + cap validation, `AgentRecord` tag helpers.
- **Modify `internal/control/control.go`** — `TunnelSpec.GroupID` (Phase 2), `GroupName` (Phase 6, optional).
- **Modify `internal/gateway/actor.go`** — `actor.groupListeners`, `bindGroup`/`unbindGroup`/`reconcileGroupMembers`, member removal in `evict`, group accept loop.
- **Modify `internal/gateway/gateway.go`** — `SetGroup`/`DeleteGroup`/`ListGroups` (IPC-facing), `handleGroupClient`, desired-config partition (solo vs grouped), startup group bind.
- **Modify `internal/gateway/conntrack` (`conntrack.go`)** — `ConnKey.GroupID` (Phase 5).
- **Modify IPC + Wails + React** (Phase 5) — a group read surface + a group card, per `backend-capability` and `ui-change`.
- **Modify `internal/e2e/e2e_test.go`** — the group e2e suite (Phases 3–4).

---

## Phase 1 — Group object + store (no runtime behavior)

### Task 1.1: `GroupSpec` type + group caps

**Files:**
- Create: `internal/gateway/group.go`
- Modify: `docs/agent/architecture.md` (numbers table)

**Interfaces:**
- Produces: `GroupSpec` struct; `MaxGroups`, `MaxMembersPerGroup` constants; `PolicySpread`/`PolicyStandby` string constants; `GroupProtoTCP`.

- [ ] **Step 1: Write `group.go` skeleton with the spec type and constants.**

```go
package gateway

// GroupSpec is a first-class, operator-owned gateway object: N interchangeable
// member backends behind one shared public port. Members share world state (a
// Velocity/Bungee proxy or mirrored lobbies) — that precondition is the
// operator's; the group treats members as interchangeable. Lives in
// gateway_agents.json alongside agents; grouping is arbitration (gateway decides
// the port and membership, pushes immediately), never member consent.
type GroupSpec struct {
	ID          string `json:"id"`          // "grp_" + id
	Name        string `json:"name"`        // display
	PublicPort  int    `json:"publicPort"`  // the one shared public port
	Proto       string `json:"proto"`       // "tcp" only this iteration
	Policy      string `json:"policy"`      // "spread" | "standby"
	OfflineMOTD string `json:"offlineMotd,omitempty"`
}

const (
	// PolicySpread distributes new players across healthy members by least
	// connections (round-robin tiebreak); PolicyStandby sends all players to the
	// lowest-ordinal eligible member and fails over on its death.
	PolicySpread  = "spread"
	PolicyStandby = "standby"
	// GroupProtoTCP is the only protocol this iteration binds. UDP groups are
	// deferred (a UDP port has no offline-responder analogue).
	GroupProtoTCP = "tcp"
)

const (
	// MaxGroups / MaxMembersPerGroup bound total group membership at the config
	// layer where the operator creates it. Membership is operator-assigned, not
	// agent-requested, so it is not counted against MaxTunnelsPerAgent. Numbers
	// table: docs/agent/architecture.md.
	MaxGroups          = 16
	MaxMembersPerGroup = 16
)
```

- [ ] **Step 2: Add the numbers-table row** (see "New constants" above) to `architecture.md`.

- [ ] **Step 3: `go build ./internal/gateway/...` and `go test ./internal/doccheck/...`** — the doccheck citation test asserts `MaxGroups`/`MaxMembersPerGroup` exist for the row.
Expected: PASS.

- [ ] **Step 4: Commit.**

```
git add internal/gateway/group.go docs/agent/architecture.md
git commit -m "gateway: a group is a first-class object (type + caps)"
```

### Task 1.2: Store fields, mutators, and validation

**Files:**
- Modify: `internal/gateway/agentstore.go`
- Test: `internal/gateway/agentstore_test.go`

**Interfaces:**
- Consumes: `GroupSpec`, `MaxGroups`, `MaxMembersPerGroup` (Task 1.1); `AgentStore.mutate`, `storeFile`, `AgentRecord.DesiredTunnels`.
- Produces:
  - `func (s *AgentStore) Groups() []GroupSpec`
  - `func (s *AgentStore) SetGroup(spec GroupSpec) error` — validates caps, policy, proto, non-empty ID/port, and **one-tunnel-per-agent-per-group** across all agents' `DesiredTunnels`; upsert by `ID`; persists.
  - `func (s *AgentStore) DeleteGroup(id string) bool`
  - `func (s *AgentStore) memberTags(groupID string) []agentTun` (helper; `agentTun` defined in Task 3.1) — every `(agentID, tunnelID)` tagged with `groupID`. *(If `agentTun` is not yet defined, return `[]struct{ AgentID, TunnelID string }`; reconcile the type in Task 3.1.)*
  - Sentinel errors: `ErrGroupCapReached`, `ErrTooManyMembers`, `ErrDuplicateTunnelInGroup`, `ErrBadGroupSpec`.

- [ ] **Step 1: Write failing tests** in `agentstore_test.go`:

```go
func TestSetGroupAndList(t *testing.T) {
	s := freshStore(t) // existing helper pattern; else LoadAgentStore(t.TempDir())
	if err := s.SetGroup(GroupSpec{ID: "grp_a", Name: "A", PublicPort: 25565, Proto: "tcp", Policy: PolicySpread}); err != nil {
		t.Fatalf("SetGroup: %v", err)
	}
	if got := s.Groups(); len(got) != 1 || got[0].ID != "grp_a" {
		t.Fatalf("Groups() = %+v", got)
	}
	// Reload persists.
	s2, _ := LoadAgentStore(filepath.Dir(s.path))
	if len(s2.Groups()) != 1 {
		t.Fatalf("group did not persist")
	}
}

func TestSetGroupRejectsBadSpec(t *testing.T) {
	s := freshStore(t)
	for name, spec := range map[string]GroupSpec{
		"no id":     {Name: "x", PublicPort: 25565, Proto: "tcp", Policy: PolicySpread},
		"no port":   {ID: "grp_a", Proto: "tcp", Policy: PolicySpread},
		"bad proto": {ID: "grp_a", PublicPort: 25565, Proto: "udp", Policy: PolicySpread},
		"bad policy":{ID: "grp_a", PublicPort: 25565, Proto: "tcp", Policy: "random"},
	} {
		if err := s.SetGroup(spec); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestGroupCapsEnforced(t *testing.T) { // mirrors spec test name at the store layer
	s := freshStore(t)
	for i := 0; i < MaxGroups; i++ {
		if err := s.SetGroup(GroupSpec{ID: fmt.Sprintf("grp_%d", i), PublicPort: 30000 + i, Proto: "tcp", Policy: PolicyStandby}); err != nil {
			t.Fatalf("group %d: %v", i, err)
		}
	}
	if err := s.SetGroup(GroupSpec{ID: "grp_over", PublicPort: 40000, Proto: "tcp", Policy: PolicyStandby}); !errors.Is(err, ErrGroupCapReached) {
		t.Fatalf("expected ErrGroupCapReached, got %v", err)
	}
}

func TestGroupDuplicateTunnelRejected(t *testing.T) {
	s := freshStore(t)
	// Enroll one agent with two tunnels both tagged into grp_a.
	s.agents["k"] = AgentRecord{AgentID: "agt_x", DesiredTunnels: []control.TunnelSpec{
		{ID: "t1", GroupID: "grp_a"}, {ID: "t2", GroupID: "grp_a"},
	}}
	if err := s.SetGroup(GroupSpec{ID: "grp_a", PublicPort: 25565, Proto: "tcp", Policy: PolicySpread}); !errors.Is(err, ErrDuplicateTunnelInGroup) {
		t.Fatalf("expected ErrDuplicateTunnelInGroup, got %v", err)
	}
}
```

Note: `TunnelSpec.GroupID` does not exist until Phase 2. **Either** land Task 1.2 without the duplicate-tunnel test and add it in Phase 2, **or** pull the one-line `GroupID` field addition forward into Task 1.1. Recommended: pull the field forward (a bare struct field is not itself a wire behavior; the wire-protocol skill's byte-compat test lands in Phase 2). Update this task's `git add` to include `control.go` if so.

- [ ] **Step 2: Run tests, verify they fail** (`SetGroup` undefined).

- [ ] **Step 3: Implement** in `agentstore.go`:

```go
// Groups returns a snapshot of every configured group.
func (s *AgentStore) Groups() []GroupSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]GroupSpec(nil), s.groups...)
}

// SetGroup upserts a group by ID after validating the spec, the group cap, and
// the one-tunnel-per-agent-per-group rule. Persists atomically (mutate's AV-retry
// path). Arbitration: the gateway decides grouping, so this never consults a member.
func (s *AgentStore) SetGroup(spec GroupSpec) error {
	if spec.ID == "" || spec.PublicPort <= 0 || spec.PublicPort > 65535 {
		return fmt.Errorf("%w: id and a valid public port are required", ErrBadGroupSpec)
	}
	if spec.Proto != GroupProtoTCP {
		return fmt.Errorf("%w: only tcp groups are supported", ErrBadGroupSpec)
	}
	if spec.Policy != PolicySpread && spec.Policy != PolicyStandby {
		return fmt.Errorf("%w: policy must be %q or %q", ErrBadGroupSpec, PolicySpread, PolicyStandby)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Cap counts groups after accounting for an in-place update of an existing ID.
	if _, existing := s.groupIndex(spec.ID); !existing && len(s.groups) >= MaxGroups {
		return ErrGroupCapReached
	}
	// One tunnel per agent per group, and total membership <= MaxMembersPerGroup.
	seenAgent := map[string]struct{}{}
	members := 0
	for _, r := range s.agents {
		for _, t := range r.DesiredTunnels {
			if t.GroupID != spec.ID {
				continue
			}
			if _, dup := seenAgent[r.AgentID]; dup {
				return fmt.Errorf("%w: agent %s tags two tunnels into %s", ErrDuplicateTunnelInGroup, r.AgentID, spec.ID)
			}
			seenAgent[r.AgentID] = struct{}{}
			members++
		}
	}
	if members > MaxMembersPerGroup {
		return ErrTooManyMembers
	}
	if i, ok := s.groupIndex(spec.ID); ok {
		s.groups[i] = spec
	} else {
		s.groups = append(s.groups, spec)
	}
	return s.save()
}

// DeleteGroup removes a group by ID, reporting whether it existed. Members' tags
// are left in DesiredTunnels — an untagged tunnel reverts to solo on the next
// reconcile; the caller (Gateway.DeleteGroup) tears the socket down separately.
func (s *AgentStore) DeleteGroup(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.groupIndex(id)
	if !ok {
		return false
	}
	s.groups = append(s.groups[:i], s.groups[i+1:]...)
	_ = s.save()
	return true
}

// groupIndex finds a group's slice index by ID. Caller holds s.mu.
func (s *AgentStore) groupIndex(id string) (int, bool) {
	for i := range s.groups {
		if s.groups[i].ID == id {
			return i, true
		}
	}
	return -1, false
}
```

Add `groups []GroupSpec` to `AgentStore`, `Groups []GroupSpec` to `storeFile` (loaded in `LoadAgentStore`, written in `save`), and the sentinel errors near the existing `ErrTicket*` block.

- [ ] **Step 4: Run tests, verify PASS.** Also `go test ./internal/gateway/... -run Group`.

- [ ] **Step 5: Commit.**

```
git add internal/gateway/agentstore.go internal/gateway/agentstore_test.go
git commit -m "gateway store: groups with caps and one-tunnel-per-agent-per-group"
```

---

## Phase 2 — `TunnelSpec.GroupID` tag + desired-config partition

> Open the **wire-protocol** skill first. This adds one `omitempty` field; the byte-compat guard is the deliverable's test.

### Task 2.1: `TunnelSpec.GroupID` field + byte-compat

**Files:**
- Modify: `internal/control/control.go`
- Test: `internal/control/hello_compat_test.go` (assert byte-identical when zero)

**Interfaces:**
- Produces: `TunnelSpec.GroupID string \`json:"groupId,omitempty"\``.

- [ ] **Step 1: Add the field** (if not pulled forward in Task 1.1):

```go
	// GroupID, when set, tags this tunnel into a gateway-side group: the gateway
	// binds it behind the group's shared listener instead of its own PublicPort
	// (which is ignored while tagged, reclaimed on untag). Operator-assigned on the
	// gateway's DesiredTunnels; omitempty keeps frames to legacy peers byte-identical.
	GroupID string `json:"groupId,omitempty"`
```

- [ ] **Step 2: Verify `HashTunnels` + hello-compat.** A zero `GroupID` is omitted, so `HashTunnels` of a legacy set is unchanged and `hello_compat_test.go` stays green. Add an assertion that a spec with `GroupID:""` encodes byte-identically to the pre-field form (if the test file has a fixtures list, add nothing — the omitempty covers it; if it enumerates fields, add `GroupID`).
Run: `go test ./internal/control/...`
Expected: PASS.

- [ ] **Step 3: Commit** (protocol-only, no implementation — per the "protocol and implementation never in the same commit" rule).

```
git add internal/control/control.go internal/control/hello_compat_test.go
git commit -m "control: TunnelSpec.GroupID tags a tunnel into a group"
```

### Task 2.2: Partition desired config into solo vs grouped

**Files:**
- Modify: `internal/gateway/gateway.go`
- Test: `internal/gateway/gateway_test.go` (or a new `group_desired_test.go`)

**Interfaces:**
- Consumes: `AgentStore.Groups()`, `validateSpec`, `reconcile`.
- Produces:
  - `func (g *Gateway) partitionDesired(specs []control.TunnelSpec) (solo, grouped []control.TunnelSpec)` — a spec is `grouped` iff `GroupID != ""` **and** that group is currently configured; otherwise `solo`. A tagged-but-unconfigured tunnel falls to `solo` (binds its own port) and records an `EventGroupTagDangling` event so the operator sees why. Grouped specs keep their `GroupID` but their `PublicPort` is not bound solo.

- [ ] **Step 1: Write failing test** — a desired set `[{t1,groupId:grp_a}, {t2}]` with `grp_a` configured partitions to `grouped=[t1]`, `solo=[t2]`; with `grp_a` NOT configured, both are `solo` and an event is recorded.

- [ ] **Step 2: Implement `partitionDesired`.** Build a `configured` set from `g.agents.Groups()`; split. This is the seam that keeps grouped tunnels out of `a.reconcile` (the solo path).

- [ ] **Step 3: Route it.** In `pushConfigOnConnect`, `adoptAndPush`, and `syncTunnels`, replace the single `reconcile(sess, valid, …)` call with: `solo, grouped := g.partitionDesired(valid)`; `reconcile(sess, solo, …)`; `g.act().reconcileGroupMembers(sess, grouped)` (the latter is a no-op stub until Phase 3). Keep the pushed/adopted set = the full `valid` (tags ride to the agent for Phase 6 display; harmless now). Verify `HashTunnels(valid)` still drives drift.

- [ ] **Step 4: Run `go test ./internal/gateway/...`.** With `reconcileGroupMembers` a stub, grouped tunnels simply get no listener yet — assert no solo listener is bound for a grouped tunnel.

- [ ] **Step 5: Commit.**

```
git add internal/gateway/gateway.go internal/gateway/group_desired_test.go
git commit -m "gateway: keep group-tagged tunnels off the solo listener path"
```

---

## Phase 3 — Actor group state, socket lifecycle, membership, port conflict

This is the load-bearing phase. Break into tasks; each ends green.

### Task 3.1: Actor group types + `agentTun` key

**Files:**
- Modify: `internal/gateway/group.go` (types), `internal/gateway/actor.go` (`actor.groupListeners`)

**Interfaces:**
- Produces:
  - `type agentTun struct { AgentID, TunnelID string }`
  - `type groupMember struct { sess *agentSession; tunnelID string; joinOrdinal uint64; liveConns atomic.Int64 }`
  - `type groupListener struct { spec GroupSpec; ln net.Listener; done chan struct{}; snap atomic.Pointer[[]*groupMember]; members map[agentTun]*groupMember; nextOrdinal uint64; rr atomic.Uint64; conns sync.Map }`
    - `snap` — immutable, sorted by `joinOrdinal`, read lock-free by the pick.
    - `members` + `nextOrdinal` — actor-only (single goroutine).
    - `rr` — spread round-robin index (atomic; hot path).
    - `conns` — live client conns (connID→net.Conn) for removal-drain.
  - `actor.groupListeners map[string]*groupListener` (init in `newActor`).
  - `func (gl *groupListener) rebuildSnap()` — copy `members` to a slice, sort by `joinOrdinal`, `snap.Store`. Actor-only.

- [ ] **Step 1: Add the types + field + `newActor` init.** No behavior yet.
- [ ] **Step 2: `go build ./internal/gateway/...`.**
- [ ] **Step 3: Commit** (`gateway: additive actor group state`).

### Task 3.2: Bind/teardown the shared socket (config-scoped lifetime + ghost-listener)

**Files:**
- Modify: `internal/gateway/actor.go`, `internal/gateway/gateway.go`
- Test: `internal/e2e/e2e_test.go` (`TestGroupListenerSurvivesMemberEviction` foundation) and/or `internal/gateway/group_test.go`

**Interfaces:**
- Produces:
  - `func (a *actor) bindGroup(spec GroupSpec, movable map[agentTun]struct{}, handle func(*groupListener, net.Conn)) error` — actor-goroutine bind with **port-conflict-by-rejection** (Task 3.4 refines `movable`); on success stores a `*groupListener` and starts `acceptGroupClients`. Upsert: if the group already exists with the same port, keep the socket and just update `spec`; if the port changed, close+await old socket then rebind.
  - `func (a *actor) unbindGroup(id string)` — close socket, `<-gl.done`, then range `gl.conns` closing each client conn (drain in-flight legs), delete from map. Actor-goroutine.
  - `func (a *actor) acceptGroupClients(gl *groupListener, handle func(*groupListener, net.Conn))` — mirrors `acceptClients`; `defer close(gl.done)`; `a.clientWG.Add(1)` per conn.
  - Gateway wrappers:
    - `func (g *Gateway) SetGroup(spec GroupSpec) error` — `store.SetGroup` then `a.do(bindGroup(...))`; on bind rejection, roll back the store write (delete the just-added group) and return the error, so an accepted group always has its socket up (§7).
    - `func (g *Gateway) DeleteGroup(id string) bool` — `a.do(unbindGroup)` then `store.DeleteGroup`.
    - `func (g *Gateway) ListGroups() []GroupSpec` — `store.Groups()`.
    - Startup: in `Start`, after `g.actor.Store(a)` and the actor goroutine is running, `for _, sp := range g.agents.Groups() { g.SetGroup(sp) }` (or a dedicated `bindConfiguredGroups`) so groups claim their ports **before** any agent connects and binds solo listeners.

- [ ] **Step 1: Write failing test** `TestGroupSocketBoundAtConfigTime`: create a gateway, `SetGroup` on a free port, assert something is listening on that port and `ListGroups` shows it with **zero members**; `DeleteGroup` and assert the port is free (a fresh `net.Listen` succeeds).
- [ ] **Step 2: Implement** `bindGroup` (free-port case only), `unbindGroup`, `acceptGroupClients`, and the gateway wrappers. Use `net.Listen("tcp", …)` on `g.cfg.Gateway.BindAddr` + `spec.PublicPort` (TCP only this iteration).
- [ ] **Step 3: Run test, verify PASS.** The accept loop with a nil-safe `handle` (offline responder stub — Task 4.x fills it) is fine here; `handle` can be `g.handleGroupClient` from the start if Phase 4 is stubbed to serve offline.
- [ ] **Step 4: Commit** (`gateway: group socket bound for the group's config lifetime`).

### Task 3.3: Join/leave membership + eviction removal

**Files:**
- Modify: `internal/gateway/actor.go`
- Test: `internal/gateway/group_test.go`, `internal/e2e/e2e_test.go`

**Interfaces:**
- Produces:
  - `func (a *actor) reconcileGroupMembers(sess *agentSession, grouped []control.TunnelSpec)` — actor-goroutine; for this session, make its membership across all groups equal `grouped`: add a `groupMember` for each `(sess, spec.ID)` whose `GroupID` names a live `groupListener` (stamp `nextOrdinal++` as `joinOrdinal`), remove ones no longer present, `rebuildSnap()` each affected group. Skips a `grouped` spec whose group is not bound (defensive; partition already filtered).
    - Idempotence: a member already present (same `agentTun`) for the same group keeps its `joinOrdinal` (no re-stamp) — otherwise a benign re-sync would reset standby priority.
  - Extend `evict`: after closing the agent's solo listeners, iterate `a.groupListeners`, delete any member whose `sess == sess`, `rebuildSnap()`. (Its data legs drain via `sess.closeAll()`, already called.) **Standby no-failback**: because a rejoin stamps a *new, higher* `joinOrdinal`, a recovered ex-primary never reclaims primary.

- [ ] **Step 1: Write failing unit tests** using fake sessions (construct `*agentSession` with a set `sess` pointer / health map — see existing `actor` tests for the pattern, or build minimal fakes):
  - `TestGroupJoinStampsMonotonicOrdinals` — two joins → ordinals 1,2; leave #1, rejoin → ordinal 3 (`TestGroupStandbyNoFailback` foundation).
  - `TestGroupEvictionRemovesMemberFromSnap` — evict a member → `snap` drops it, socket + other members untouched.
- [ ] **Step 2: Implement** `reconcileGroupMembers` + `evict` extension. Wire `reconcileGroupMembers` in place of the Phase 2 stub.
- [ ] **Step 3: Run tests, verify PASS.**
- [ ] **Step 4: Commit** (`gateway: group membership join/leave with monotonic ordinals`).

### Task 3.4: Port-conflict-by-rejection + startup priority

**Files:**
- Modify: `internal/gateway/actor.go` (`bindGroup` conflict arm), `internal/gateway/gateway.go` (`SetGroup` computes `movable`)
- Test: `internal/e2e/e2e_test.go` (`TestGroupPortConflictRejected`)

**Interfaces:**
- Consumes: `a.portHolder(port, proto)`, `a.unbindLocked`.
- Refines `bindGroup`: when `spec.PublicPort` is held —
  - held by a solo listener whose `(agentID, tunnelID)` is in `movable` (the tunnels being moved into *this* group) → `unbindLocked` it (waits its accept loop) then bind. The common "convert my server into a group" flow.
  - held by any other listener (non-member solo, or another group) → **reject** with `ErrGroupPortInUse`; the store write is rolled back by `SetGroup`, so no socket-less group exists.
- `Gateway.SetGroup` computes `movable` from `g.agents.memberTags(spec.ID)` (the `(agentID,tunnelID)` set tagged into this group) before the actor hop, passing it in.
- Startup priority is already satisfied by Task 3.2 (groups bind in `Start`, before agents connect) — assert it in the e2e test.

- [ ] **Step 1: Write failing `TestGroupPortConflictRejected`**: (a) a non-member solo tunnel holds 25565; `SetGroup(port:25565)` → error, no group created, solo tunnel still bound. (b) an agent's tunnel tagged into `grp_a` holds 25565 solo; `SetGroup(grp_a, 25565)` with that tunnel in `movable` → success, solo listener gone, group socket up.
- [ ] **Step 2: Implement** the conflict arm + `memberTags` (may reuse the Task 1.2 helper) + `SetGroup` rollback.
- [ ] **Step 3: Run test + full `go test ./internal/gateway/...`.**
- [ ] **Step 4: Commit** (`gateway: group port conflict is resolved by rejection, not stealing`).

### Task 3.5: Group removal drains in-flight players

**Files:**
- Test: `internal/e2e/e2e_test.go` (`TestGroupRemovalDrains`)

- [ ] **Step 1: Write failing `TestGroupRemovalDrains`**: a live group with an in-flight player (an established splice through a member); `DeleteGroup` → the socket closes (new `net.Listen` on the port succeeds) **and** the in-flight player's conn is closed. (`gl.conns` drain from Task 3.2 makes this pass.)
- [ ] **Step 2: Verify PASS** (behavior implemented in 3.2; this task is the regression test). If in-flight tracking was deferred, implement `gl.conns` store/delete in `handleGroupClient` now.
- [ ] **Step 3: Commit** (`gateway: DeleteGroup drains in-flight player legs`).

---

## Phase 4 — Routing: `handleGroupClient`, `pick`, eligibility, re-pick, offline

> Open the **hot-path** skill first. Run `TestBurstThroughputAndCrossStreamLatency` before starting (baseline) and after (no regression).

### Task 4.1: `pick` policy functions (pure, unit-tested)

**Files:**
- Modify: `internal/gateway/group.go`
- Test: `internal/gateway/group_test.go`

**Interfaces:**
- Produces:
  - `func eligible(m *groupMember) bool` — `m.sess.session() != nil` **and** not paused (`!m.sess.pauseMine.Load().covers(m.tunnelID) && !m.sess.pauseTheirs.Load().covers(m.tunnelID)`) **and** positive health (`v, ok := m.sess.health.Load(m.tunnelID); ok && v.(bool)`).
  - `func pick(policy string, members []*groupMember, exclude map[*groupMember]bool, rr *atomic.Uint64) *groupMember` — filter `members` (already sorted by `joinOrdinal`) to eligible-and-not-excluded, then:
    - `PolicySpread` → `argmin(liveConns.Load())`; ties broken by `rr.Add(1) % len(eligible)` over the **sorted** eligible slice (deterministic).
    - `PolicyStandby` → the eligible member with the lowest `joinOrdinal` (first in the sorted slice).
    - returns `nil` when no member is eligible.

- [ ] **Step 1: Write failing unit tests** with hand-built `groupMember` slices (set `liveConns`, and stub eligibility via a test seam — e.g. inject an `eligibleFn` or build members with live-session/health fakes):
  - `TestPickLeastConnections` — members with liveConns {3,1,2} → picks the `1`.
  - `TestPickSpreadRoundRobinTiebreak` — all liveConns 0 → successive picks cycle deterministically over the sorted slice.
  - `TestPickStandbyLowestOrdinal` — picks min-ordinal eligible; when it becomes ineligible, picks the next ordinal.
  - `TestPickExcludesIneligible` — down/unknown-health/paused/dead-session members are never returned.
- [ ] **Step 2: Implement `eligible` + `pick`.** Keep it allocation-light (one filtered slice; no maps on the hot path beyond the small `exclude`).
- [ ] **Step 3: Run tests, verify PASS.**
- [ ] **Step 4: Commit** (`gateway: least-connections and standby group pickers`).

### Task 4.2: `handleGroupClient` — route, splice, re-pick, offline

**Files:**
- Modify: `internal/gateway/gateway.go`
- Test: `internal/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `pick`, `eligible`, `sess.dp.openFlow`, `relay.Splice`, `g.gate`, `g.Conns`, `serveOffline`.
- Produces: `func (g *Gateway) handleGroupClient(gl *groupListener, clientConn net.Conn)` — mirrors `handleClient`'s connGate admit/release, connID minting, conntrack `Open`, and splice, but selects the member via `pick` with a **bounded 2-try re-pick** on `openFlow` failure, incrementing/decrementing `member.liveConns` around the attempt, tracking `gl.conns` for drain, and serving `gl.spec.OfflineMOTD` when no member is eligible or both picks fail.

Reference implementation (fill against `handleClient`'s exact helpers):

```go
func (g *Gateway) handleGroupClient(gl *groupListener, clientConn net.Conn) {
	defer clientConn.Close()
	ip := remoteIP(clientConn)
	if !g.gate.admit(ip) { // abuse limits sit ahead of the pick, as for solo listeners
		return
	}
	defer g.gate.release(ip)

	tcp, ok := clientConn.(*net.TCPConn)
	if !ok {
		return
	}
	connID := strconv.FormatUint(g.connSeq.Add(1), 10)
	gl.conns.Store(connID, clientConn) // for DeleteGroup drain
	defer gl.conns.Delete(connID)

	members := *gl.snap.Load() // immutable, sorted by joinOrdinal
	tried := map[*groupMember]bool{}
	for attempt := 0; attempt < 2; attempt++ {
		m := pick(gl.spec.Policy, members, tried, &gl.rr)
		if m == nil {
			break
		}
		m.liveConns.Add(1)
		stream, releaseFlow, err := m.sess.dp.openFlow(m.sess, connID)
		if err != nil {
			m.liveConns.Add(-1)
			tried[m] = true
			continue
		}
		// entry carries (agentID, tunnelID, groupID) — groupID lands in Phase 5.
		entry, closeEntry := g.Conns.Open(m.sess.agentID, m.tunnelID, gl.spec.Name, clientConn.RemoteAddr().String(), connID, true)
		func() {
			defer closeEntry()
			defer releaseFlow()
			defer stream.Close()
			defer m.liveConns.Add(-1)
			stream.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
			if err := control.WriteMsg(stream, control.TypeOpenConn, control.OpenConn{
				TunnelID: m.tunnelID, ClientAddr: clientConn.RemoteAddr().String(), ConnID: connID,
			}); err != nil {
				return
			}
			stream.SetWriteDeadline(time.Time{})
			m.sess.rttConns.Store(connID, &rttConn{tcp: tcp, entry: entry})
			defer m.sess.rttConns.Delete(connID)
			var client relay.Conn = tcp
			if gl.spec.MinecraftAware() { // groups are MC-aware by nature; see note
				client = mcsniff.Tap(tcp, entry)
			}
			_ = relay.Splice(client, stream, entry.Counters, relay.SpliceOpts{Ctx: m.sess.ctx})
		}()
		return // spliced (success path returns from within the closure's caller)
	}
	g.serveOffline(clientConn, control.TunnelSpec{OfflineMOTD: gl.spec.OfflineMOTD})
}
```

Notes to resolve while implementing:
- The success-path `return` must be after the splice closure runs — restructure so a successful `openFlow` splices then returns, and only a failed attempt loops. (The closure form above returns from `handleGroupClient` after the splice; verify control flow.)
- Bandwidth caps: groups have no per-tunnel `BandwidthLimitMbps` in this iteration — pass an uncapped `SpliceOpts` (the member's own solo cap does not apply; document this as a YAGNI, matching spec §10 "member weighting" is out).
- MinecraftAware: `GroupSpec` has no `MinecraftAware` field. Decide: (a) always sniff (groups are Minecraft by domain) or (b) add no sniffing this iteration. Recommend **always MC-aware** for player attribution; drop the `gl.spec.MinecraftAware()` guard and always `mcsniff.Tap`. Confirm during implementation and reflect in the card copy.

- [ ] **Step 1: Baseline** `go test ./internal/e2e/ -run TestBurstThroughputAndCrossStreamLatency`.
- [ ] **Step 2: Write failing e2e tests** (mirror existing e2e harness: 3 agents each behind a mock backend, one group):
  - `TestGroupSpreadsAcrossMembers`
  - `TestGroupFailoverStandby`
  - `TestGroupStandbyNoFailback`
  - `TestGroupOfflineWhenNoEligibleMember`
  - `TestGroupUnhealthyMemberSkipped` / `TestGroupColdMemberIneligible`
  - `TestGroupRepickOnOpenFlowFailure`
  - `TestGroupListenerSurvivesMemberEviction`
- [ ] **Step 3: Implement `handleGroupClient`; wire it as the group accept handler** (replace the Task 3.2 offline stub).
- [ ] **Step 4: Run the group e2e suite + the burst floor.** Burst must be unchanged (splice path untouched).
- [ ] **Step 5: `-race` join/leave test** `TestGroupPickRaceUnderChurn` — hammer `reconcileGroupMembers`/`evict` on the actor while a goroutine calls `pick` on `snap`. (CI runs `-race`; locally run without.)
- [ ] **Step 6: Commit** (`gateway: route group players by policy with bounded re-pick`).

---

## Phase 5 — IPC read surface + conntrack groupID + Wails binding + React group card

> Open **backend-capability** then **ui-change**. Follows engine → IPC → Wails binding → React hook, then the state-matrix walk.

### Task 5.1: `ConnKey.GroupID` (two counters, one truth)

**Files:**
- Modify: `internal/conntrack/conntrack.go`
- Test: `internal/conntrack/*_test.go` (`TestConnKeyPlumbing` extension, `-race`)

**Interfaces:**
- Produces: `ConnKey.GroupID` set at `Open` (immutable after), so the GUI can read conntrack for group live-conn display while `pick` keeps its own `liveConns` atomic. Add a `groupID` parameter to `Registry.Open` (or an `OpenGroup` variant); solo `handleClient` passes `""`, `handleGroupClient` passes `gl.spec.ID`.

- [ ] **Step 1: Write failing `-race` test** asserting `GroupID` is set at `Open` and read without a lock.
- [ ] **Step 2: Implement** the field + plumb through both `Open` call sites.
- [ ] **Step 3: Run `go test ./internal/conntrack/...` (and `-race` in CI).**
- [ ] **Step 4: Commit** (`conntrack: ConnKey carries groupID`).

### Task 5.2: Engine group status + IPC + Wails binding

**Files:**
- Modify: engine status assembly (`internal/engine` / `app`), `internal/ipc`, `frontend/wailsjs` (regenerate — never hand-edit).
- Test: engine/ipc round-trip test per the `backend-capability` skill.

**Interfaces:**
- Produces: a `GroupStatus` surface exposing per group: `ID, Name, PublicPort, Policy, OfflineMOTD`, member count, and per-member `{AgentID, TunnelID, Eligible, IsPrimary, LiveConns}` — assembled from `a.groupListeners` snapshots (a new `func (a *actor) groupStatuses() []GroupStatus` on the actor goroutine) + conntrack group counts. `IsPrimary` = lowest-ordinal eligible member (standby) / n/a (spread). Clamp member lists under the frame cap like `MaxStatusConns`.
- Wails binding: regenerate via `wails generate module` (fast) or `wails build`; the CI stale-`frontend/wailsjs` check gates this.

- [ ] **Step 1:** Add `actor.groupStatuses()` + `Gateway.GroupStatuses()`.
- [ ] **Step 2:** Map into `ipc.GroupStatus`, clamp, expose over the pipe; add the Wails-bound accessor.
- [ ] **Step 3:** Regenerate `frontend/wailsjs`; run `go test ./...` and the stale-binding check.
- [ ] **Step 4: Commit** (`gateway, ipc: group status read surface`).

### Task 5.3: React group card + create/edit/tag UI

**Files:**
- Modify: `frontend/src/...` (a `Groups`/`GroupCard` surface under the agent-management GUI), following `frontend/DESIGN.md`.
- Verify: `cd frontend && npm run build`; devmock state-matrix walk (`?mock=agent`), headless Brave per the ui-change skill.

**Interfaces:**
- Consumes: the Wails group binding (Task 5.2).
- Produces: a group card — members, per-member eligibility, live-conn spread, primary indicator (standby), and an **honest** offline state when empty; group create/edit + member tagging (offering to widen a member's scope when tagging, per spec §2). All four data-surface states designed (skeleton / data / empty / unavailable).

- [ ] **Step 1:** Add the read-only group card (all four states) + a devmock scenario.
- [ ] **Step 2:** Add create/edit/tag actions wired to `SetGroup`/`DeleteGroup` + tunnel tagging (rewrites the member's `DesiredTunnels` `GroupID`, arbitration push).
- [ ] **Step 3:** `npm run build`; walk the state matrix; ui-design-reviewer pass.
- [ ] **Step 4: Commit** (`ui: tunnel group card and management`).

---

## Phase 6 — (Optional, later) push `GroupID`/`GroupName` to the agent for display

> Protocol-only commit, guarded by `hello_compat_test.go`. Not required to ship.

### Task 6.1: `TunnelSpec.GroupName` + agent-side display

- [ ] Add `TunnelSpec.GroupName string \`json:"groupName,omitempty"\``; the gateway stamps `GroupID`/`GroupName` on pushed specs so a member agent's own GUI can show "part of group X". Byte-compat when zero. Agent renders it read-only. Commit protocol and agent-display separately.

---

## Self-review notes (author)

- **Spec coverage:** §1 store object → 1.1/1.2; §2 tag + partition → 2.1/2.2; §3 orchestration-over-existing-data-plane → 4.2 (no new data code); §4 actor state → 3.1; §5 routing/eligibility/least-conns → 4.1/4.2; §6 lifecycle + ghost-listener + removal-drain → 3.2/3.3/3.5; §7 port-conflict-by-rejection + startup priority → 3.4; §8 invariants preserved → global constraints + 3.3 (cap untouched) + 4.2 (abuse gate ahead of pick); §9 wire/IPC/GUI → 2.1/5.x; §10 scope → group.go has no UDP/weighting/affinity. All spec test names appear in Phases 3–4.
- **Open decisions flagged inline** (resolve at implementation): (a) pull `TunnelSpec.GroupID` field into Phase 1 vs Phase 2 — recommend Phase 1 for the store test, byte-compat test still Phase 2; (b) groups always-MC-aware vs a `GroupSpec.MinecraftAware` field — recommend always; (c) tagged-but-unconfigured-group tunnel → solo + event (least-surprise, keeps it serving).
- **Type consistency:** `agentTun`, `groupListener`, `groupMember`, `pick`, `eligible`, `reconcileGroupMembers`, `bindGroup`/`unbindGroup`, `SetGroup`/`DeleteGroup`/`ListGroups`, `groupStatuses` are used consistently across tasks.
