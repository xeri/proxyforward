# Tunnel groups (load-balancing & redundancy)

Date: 2026-07-24. Author: Claude (Opus 4.8). Status: design approved (rev. 2 after
review), plan pending.

## Context

proxyforward binds exactly one public listener per `(agentID, tunnelID)`
(`actor.go listeners[agentID][tunnelID]`). When two *different* agents ask for the
same public port, the second is auto-reassigned to a free port — they cannot share
one. So there is no way to put several agents behind a single public port for
redundancy or load spreading. This is the frp-parity "load-balancing" gap (frp calls
it proxy groups; `server/group/`).

Minecraft is session-affine: a player holds one TCP session to one backend for the
whole play session, and a reconnect is a fresh session. So a group is only *correct*
when its member backends share world state — e.g. a Velocity/Bungee proxy behind each
member, or mirrored lobbies. That precondition is the operator's to satisfy; the
design does not enforce it, but every choice below assumes members are interchangeable.

The user decided (in-conversation, 2026-07-24; this is the wire-protocol/actor-scope
escalation sign-off):

- **Hybrid group**: a named group of N live members with a per-group policy —
  `spread` (distribute new players across healthy members) or `standby` (all players
  to one primary, fail over to the next on death).
- **Gateway-side group object**: the group is defined in the gateway allowlist;
  membership is operator-controlled, **no shared group key** (identity + scope already
  authorize members). Not frp's agent-declared `group`+`groupKey`.
- **Least-connections** spread policy (round-robin tiebreak).
- **Approach A (additive)**: a new `groupListeners[groupID]` table in the actor,
  entirely separate from the per-agent `listeners` map; the solo-tunnel path is
  untouched.

Rev. 2 folds in a design review (2026-07-24) that closed several bugs in the parts
rev. 1 treated as settled: `(agentID, tunnelID)` member identity, positive-health
eligibility, config-scoped socket lifetime, port-conflict-by-rejection, bounded
re-pick, a lock-free member snapshot, group-side caps, and the standby no-failback
invariant. Those are the current text below, not a diff.

## Decisions

### 1. The group is a first-class gateway-side object

A new `GroupSpec` lives in `gateway_agents.json` alongside `agents`, owned by the
operator:

```go
type GroupSpec struct {
    ID          string // "grp_" + id
    Name        string // display
    PublicPort  int    // the one shared public port
    Proto       string // "tcp" only in this iteration (see §11)
    Policy      string // "spread" | "standby"
    OfflineMOTD string // shown when no member is eligible
}
```

The `AgentStore` gains `Groups []GroupSpec` plus `SetGroup`/`DeleteGroup` mutators
(atomic write + AV-retry, over the existing `mutate` helper — cf. `agentstore.go
SetScope`). Group config is **arbitration, not consent**: the gateway decides grouping
and the shared port and pushes immediately, never a member veto — consistent with
"gateway decides ports" (`CapConfigConsent`, the arbitration side).

**Caps** (validated in `SetGroup`, a numbers-table row each): `MaxGroups` and
`MaxMembersPerGroup`. These bound the total membership at the config layer where the
operator creates it. Note membership is **operator-assigned, not agent-requested**, so
it is *not* accounted against `MaxTunnelsPerAgent` — that cap bounds "the one resource
an agent grows by asking" (a listener it allocates), which a group membership is not.

### 2. Membership is a tag on the member's desired tunnel (single source of truth)

A member agent's gateway-side `DesiredTunnels` entry carries a new `GroupID` (a field
on `control.TunnelSpec`, `omitempty`). Reading it: "the gateway binds this agent's
tunnel *T* behind group *G*'s shared listener instead of its own port." The operator
tags a tunnel; the actor derives the live member set from tags at admission/reconcile,
no per-connection scan.

- **A tunnel's declared `PublicPort` is ignored while it is tagged** (the group's port
  wins) and **reclaimed on untag** — an untagged tunnel reverts to a solo listener
  requesting its own `PublicPort`, subject to the usual conflict/reassign.
- **One tunnel per agent per group.** Two of an agent's tunnels tagged with the same
  `GroupID` is rejected at `SetGroup`/config validation with an operator event — never
  a silent overwrite. Member identity is `(agentID, tunnelID)`, so an agent *may*
  contribute distinct tunnels to *distinct* groups.
- **Enabled-only.** A member serves only a tunnel it has locally enabled — `agent.go
  enabledTunnelByID` already refuses a data stream for a disabled tunnel, so grouping
  can never make the gateway expose something the agent didn't (security-invariant
  "the gateway never decides what the agent exposes").
- **Scope.** The member's `Scope` must allow the group's `PublicPort` and the tunnel ID
  (`agentstore.go Scope.AllowsPort`/`AllowsTunnel`); a member failing scope is dropped
  from the group with an operator-facing event, never silently bound. This is an
  operator surprise (a member needs scope for a port only the gateway binds), so the
  tagging UI offers to widen the member's scope when it tags.

### 3. Grouping is gateway-side orchestration over the existing data plane

The load-bearing insight: `handleClient` already routes every player through the
chosen session's `dp.openFlow(sess, connID)` (`gateway.go`, `dataplane.go`), and a
member serves its tunnel with zero awareness of peers. A group only changes **which
member session** a player is routed to; after the pick, the existing per-session data
plane (mux stream or per-conn dial-back) and `relay.Splice` carry the player
**unchanged**. No new data-path code, and — for the core feature — **no agent-facing
wire change**.

### 4. Additive actor state

```go
type groupListener struct {
    spec  GroupSpec
    ln    net.Listener                        // ONE shared socket, actor-owned, one accept loop
    snap  atomic.Pointer[[]*groupMember]      // immutable, sorted by joinOrdinal; the pick reads this
    // actor-only (single goroutine, no hot path): a map[agentTun]*groupMember for
    // O(1) mutate-time lookup, and the joinOrdinal counter. Every mutation rebuilds
    // `snap` copy-on-write and atomic-swaps it, so the accept path never locks.
}

type groupMember struct {
    sess        *agentSession
    tunnelID    string
    joinOrdinal uint64        // monotonic, stamped at join; standby primary = min ordinal
    liveConns   atomic.Int64  // least-connections load signal (hot path)
    // eligible = sess.session()!=nil && !paused && healthUp(sess, tunnelID)
}
```

`actor.groupListeners map[string]*groupListener` is separate from
`listeners[agentID][tunnelID]`. Untagged tunnels keep the solo `bindLocked` path with
zero behavioral change. The pick is **lock-free**: it loads `snap` (an immutable
slice) and reads per-member atomics; only the actor rebuilds `snap`, serialized on its
one goroutine.

### 5. Connection routing, eligibility & least-connections

`handleGroupClient` runs as its own per-connection goroutine (like `handleClient`),
with deferred cleanup so a panic or early return can't corrupt the counter:

```
handleGroupClient(gl, clientConn):
  connID := newConnID()
  members := *gl.snap.Load()                 // immutable, sorted by joinOrdinal
  tried := {}
  for attempt in 0..1:                       // bounded re-pick: 2 tries
     m := pick(gl.spec.Policy, members, exclude=tried)
     if m == nil: break
     m.liveConns.Add(1)
     stream, releaseFlow, err := m.sess.dp.openFlow(m.sess, connID)
     if err == nil:
        defer func(){ releaseFlow(); m.liveConns.Add(-1) }()
        relay.Splice(clientConn, stream)     // UNCHANGED existing relay path
        return
     m.liveConns.Add(-1); tried += m         // this member failed; re-pick excluding it
  serveOffline(clientConn, gl.spec.OfflineMOTD)   // no eligible member (or both picks failed)
```

**Eligibility** filters `members` to those that are: have a **live session**
(`sess.session()!=nil`), are **not paused** (existing pause bits), and have a
**positive `up` health report** for the tunnel. The last is a **deliberate divergence
from the solo `unknown⇒up` rule**: solo, unknown-up keeps a working-but-unprobed
server serving; grouped, a group *has* alternatives, so routing to an unverified member
would let a dead-backend member that keeps reconnecting (`liveConns==0`, so the
`argmin`) become a thundering-herd target. The agent already seeds current health on
session start (`agent.go serve`, the `setHealthSink`→`pushHealth` loop), so a member is
eligible within one probe of joining; the only unknown window is a cold agent's first
probe (≤ health dial timeout), during which it is ineligible and the group serves other
members or the responder.

**Policy** over the eligible set:
- `spread` → `argmin(liveConns)`, ties broken by round-robin over the **sorted** slice
  (a per-group atomic index modulo the eligible count) — deterministic because the
  snapshot order is stable, which is what makes the distribution test writable.
- `standby` → the eligible member with the **lowest `joinOrdinal`**. All players go to
  it; it changes only when it stops being eligible.

`liveConns` is a per-member atomic (not conntrack) because the pick is on the hot
accept path. It counts everything that opens a connection, **including Minecraft
status pings**, so a ping burst transiently skews `spread` — acceptable, documented,
not corrected (we can't know it's a ping before routing).

### 6. Membership lifecycle & the group ghost-listener guarantee

- **Socket lifetime = group config lifetime.** The shared socket is bound when the
  group is configured (`SetGroup`, or startup reconcile), and closed **only** at
  `DeleteGroup` or gateway shutdown. It is *not* bound on first member / closed on last
  member. So an empty-but-configured group and an empty-after-drain group behave
  identically (both serve the offline responder), and the ghost-listener proof is
  simpler than the solo one: the socket has exactly one accept loop, owned by the
  actor, closed and awaited on the single goroutine at teardown, so a rebound group
  port is provably free.
- **Join**: when a session admits/reconciles and one of its enabled tunnels is tagged
  with a configured `GroupID`, the actor adds a `(agentID, tunnelID)` member, stamps
  its `joinOrdinal`, and rebuilds `snap`. All on the actor goroutine.
- **Leave** (disconnect/evict): the actor removes the member, rebuilds `snap`, and
  drains **only that member's** data legs via its existing `closeAll`. The socket is
  untouched.
- **Standby no-failback** (invariant, tested): a member that leaves and rejoins gets a
  **new, highest** `joinOrdinal`, so a recovered ex-primary does **not** reclaim
  primary — this is what prevents failover flapping. It is correct-by-construction; a
  future change that turns `joinOrdinal` into an operator-configured priority would
  reintroduce the flap, so the invariant is stated here and asserted in a test.
- **Failover moves new connections only** (invariant, tested): when a `standby` primary
  dies, its in-flight players' sessions drop with its backend; only *new* connections
  land on the next primary. No player migration.
- **Group removal** (`DeleteGroup`): the actor closes the socket (no new players) **and
  drains the group's in-flight player legs** (kick), so removal leaves nothing running
  against a group that no longer exists. Members' tunnels revert per §2 (untag →
  reclaim own port).

### 7. Port conflict — by rejection, not waiting

Group and solo listeners share one port space; `portHolder`/`conflict.go` must consider
group sockets. Because the socket is bound at config time (§6), the conflict is
resolved at bind time with **no waiting state**:

- **`SetGroup` binds immediately on the actor.** If the port is free, bind. If it is
  held only by solo listeners of tunnels being **moved into this group in the same
  config update**, those solo listeners are torn down as the group binds (the common
  "convert my server into a group" flow, conflict-free). If it is held by **any other**
  listener (a non-member solo tunnel, or another group), `SetGroup` is **rejected** with
  an operator event — the group is not created socket-less, and no live listener is ever
  stolen ("agent B can never steal agent A's listener").
- **Startup reconcile binds groups before solo tunnels**, so a group claims its
  operator-intended fixed port first and solo tunnels reassign around it (existing
  reassign behavior) — group priority at startup without runtime stealing.

This eliminates rev. 1's dead-port-with-no-explanation: an accepted group always has
its socket up, so §6's "the GUI needs the port answering to show why it's down"
rationale always holds.

### 8. Invariants preserved

- **Per-agent listener cap** (`MaxTunnelsPerAgent`): unchanged and *not* touched by
  membership (§1) — group bounds live at the config layer.
- **Abuse limits** (`connGate`, `authLimiter`, `preAuthGate`) sit on the accept path
  ahead of `handleGroupClient` exactly as for solo listeners.
- **Pause**: a paused member (agent- or gateway-side bit) is excluded from `pick`, like
  an ineligible one — no new pause concept, no new wire form.

### 9. Wire / IPC / GUI surface

- **Agent wire protocol**: the core needs **no** agent-facing change. An optional
  `TunnelSpec.GroupID`/`GroupName` (`omitempty`, zero-value-safe) can be pushed later so
  an agent's own GUI can display "part of group X" — a byte-compatible, separate,
  protocol-only commit guarded by `hello_compat_test.go`. Not required to ship.
- **IPC (gateway→GUI)**: a new read surface exposes each group and its members
  (agentID, eligible, isPrimary, live conns, the group's public port). Follows the
  `backend-capability` skill (engine → IPC → Wails binding → React hook).
- **Two counters, one truth.** `pick` uses the per-member `liveConns` atomic (hot path);
  the GUI reads **conntrack** for display. They will disagree unless conntrack entries
  carry a `groupID` — so `ConnKey` gains one, and the group card reads conntrack while
  `pick` keeps the atomic. (`ConnKey` is immutable after `Open`; the `groupID` is set at
  `Open`, read unlocked, per the existing `TestConnKeyPlumbing` rule.)
- **GUI**: a group card (members, per-member eligibility, live-conn spread, primary
  indicator, honest offline state when empty), per the `ui-change` skill and the design
  charter. Group create/edit + member tagging rides the agent-management GUI.

### 10. Scope / YAGNI

**In**: TCP groups; `spread` (least-connections) and `standby`; positive-health member
eligibility; the gateway-side group object; the group offline responder; group caps;
the gateway GUI surface.

**Explicitly out** (not built, not designed for): UDP groups (deferred until the
datagram plane is proven; a UDP port has no offline-responder analogue), member
weighting, connection stickiness/affinity, group-wide operator pause, `groupKey`-style
shared secrets, per-agent membership ceilings, and any cross-gateway coordination.

## Testing

Mirrors existing e2e naming (`internal/e2e/e2e_test.go`); `-race` cases run in CI (no
local race detector — cgo):

- `TestGroupSpreadsAcrossMembers` — 3 members, N players; assert least-connections
  distribution + deterministic round-robin tiebreak (needs the sorted snapshot).
- `TestGroupFailoverStandby` — `standby`; kill the primary; assert the next-ordinal
  member becomes primary and new players land on it.
- `TestGroupStandbyNoFailback` — recovered ex-primary rejoins with a higher ordinal and
  does **not** reclaim primary.
- `TestGroupListenerSurvivesMemberEviction` — evict one member; shared socket stays up,
  others keep serving (group form of `TestEvictionIsolatesAndDrains`).
- `TestGroupOfflineWhenNoEligibleMember` — all members unhealthy/cold → offline
  responder, socket still bound.
- `TestGroupUnhealthyMemberSkipped` / `TestGroupColdMemberIneligible` — a member with a
  down report, and a member with no report yet, are both excluded from `pick`.
- `TestGroupRepickOnOpenFlowFailure` — chosen member's `openFlow` fails; the player is
  re-picked onto a healthy member, not sent to offline.
- `TestGroupRemovalDrains` — `DeleteGroup` closes the socket and kicks in-flight legs.
- `TestGroupPortConflictRejected` — `SetGroup` onto a port held by a non-member listener
  is rejected with an event; the convert-in-same-update flow succeeds.
- `TestGroupCapsEnforced` — `MaxGroups` / `MaxMembersPerGroup` rejected at validation.
- `TestGroupDuplicateTunnelRejected` — two of one agent's tunnels tagged one group is
  rejected.
- Race (`-race`, CI): hammer join/leave against the accept goroutine's `pick` to prove
  the `snap` copy-on-write is sound.
- Unit: least-connections picker (argmin + rr over sorted slice), standby min-ordinal
  selection.
- **Burst floor**: the grouped path adds only `pick` (a slice scan + atomics) to the
  accept path; splice unchanged, so `TestBurstThroughputAndCrossStreamLatency` must
  still pass — run before/after per the `hot-path` skill.

## Build order (for the plan)

1. `GroupSpec` + store fields/mutators + validation, **including `MaxGroups`/
   `MaxMembersPerGroup` and duplicate-tunnel-per-agent rejection** (no runtime behavior
   yet).
2. `TunnelSpec.GroupID` tag + desired-config resolution tags member tunnels; declared
   `PublicPort` ignored-while-tagged / reclaimed-on-untag; scope validation.
3. Actor: `groupListeners`, config-scoped socket bind/teardown, join/leave with
   `joinOrdinal` + copy-on-write `snap`, **port-conflict-by-rejection + startup group
   priority**, group ghost-listener + removal-drain tests.
4. `handleGroupClient` + `pick` (spread/standby) + positive-health eligibility +
   least-connections + bounded re-pick + offline fallback; distribution/failover/
   no-failback/re-pick e2e tests + the `-race` join/leave test.
5. IPC surface + `ConnKey.groupID` + Wails binding + React group card (own commit,
   `ui-change`).
6. (Optional, later) `GroupID`/`GroupName` push to the agent for display — protocol-only
   commit.
