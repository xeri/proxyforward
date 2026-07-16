# Agent-management GUI surfacing

Date: 2026-07-16. Author: Claude (Opus 4.8). Status: design approved, ready for plan.

## Context

The per-agent identity/enrollment/config work landed backend-first. Six
management surfaces are implemented and tested in Go but only partly exposed in the GUI
(polish-backlog #26–31). The user asked to build the agent-management GUI and chose all six.

During design, the user set a **mutual-consent principle** for the config-authority half:
"everything that changes anything on the other party needs approval and notice first — if
one side can do anything it opens up for exploits." That reshapes the write path (agent
edits auto-adopt today; a gateway-side editor would push unilaterally). Building a
propose→notice→approve protocol in both directions is a **wire-protocol change** and
security-sensitive, so it is split into its own follow-up spec rather than rushed here.

**This spec covers the surfacing and local-authority items** that need no wire change
and no unilateral cross-party mutation. The consent protocol + gateway-side
tunnel editor gets its own design/spec/plan cycle afterward.

## Guiding facts (verified against the code)

- The agent's version already rides `Hello.AppVersion` (`control.go`), logged at connect
  (`gateway.go` "agent connected"). No wire change to surface it.
- The gateway knows each session's transport (it accepted QUIC vs TCP control + mux/per-conn).
- `AgentView` (`gateway/gateway.go`) is the roster row the GUI polls via `App.ListAgents`;
  `AgentLink` (`gateway/gateway.go`) is the live-session projection feeding it.
- `AgentRecord` (`gateway/agentstore.go`) already stores `DesiredTunnels` + `ConfigGen`.
- Enrollment issuance exists end-to-end: `App.IssuePairingCode(reusable, ttlSecs, ports, tunnels)`.
- Overview's onboarding strip uses the **legacy shared-token** `App.PairingCode()`
  (`Overview.tsx GatewayPairingStrip`), not enrollment.
- Conflicts are recorded as `GatewayEvent{Kind, AgentID, TunnelID, RequestedPort, ActualPort}`
  (`gateway/conflict.go`); `EventPortReassigned` is the reclaimable one.
- Tunnel IDs are random hex (`config.NewID`; frontend `newTunnelID`); `link.TunnelID`
  (`tnl_`+slug, `-2` suffix) is implemented+tested but unused in production.
- New App methods/fields require `wails build` to regenerate `frontend/wailsjs` (never
  hand-edit); every new binding needs a `devmock.ts` stub or the mock throws.

## Items

### #26 — Issue enrollment code + Overview swap (no backend change)
- **Agents screen (gateway role):** an "Issue pairing code" control — mode toggle
  (single-use default / reusable), optional scope (ports + tunnel IDs), TTL (single-use
  10 min; reusable = never), calling the existing `IssuePairingCode`. Renders the code with
  a copy button, mirroring the wizard's `GatewayLive`. Reuse `ui.tsx` kit; one code shown at
  a time; "New code" re-issues.
- **Overview swap:** `GatewayPairingStrip` issues a single-use enrollment code via
  `IssuePairingCode(false, 600, [], [])` instead of `PairingCode()`. The legacy shared-token
  code moves behind an "Advanced / legacy" disclosure (still available while
  `AcceptSharedToken` is on).
- **States:** generating (skeleton), code (data), issue-error (honest unavailable:
  non-gateway / old daemon), reusable-hint. Copy stays honest about single-use vs reusable
  lifetime (the identity-finalize fix, #13, already made reusable never-expire).

### #27 — One-click port reclaim (new op; enrolled agents only)
- **Backend:** `Gateway.ReclaimPort(agentID, tunnelID, port) bool` on the actor. Steps
  (all on the actor goroutine): (1) find the tunnel currently bound to `port`; if it belongs
  to a different **enrolled** agent, rewrite that tunnel's `DesiredConfig` PublicPort → 0
  (ephemeral), `AdoptConfig` (bump), reconcile + push that agent; (2) set the requesting
  agent's tunnel PublicPort → `port`, `AdoptConfig` (bump), reconcile + push; (3) record a
  `port-reclaimed` event. If either side is a shared-token/legacy agent (gateway can't
  rewrite its config), the op is refused (returns false) and the card explains why. Engine op
  `OpReclaimPort` + `App.ReclaimPort(agentID, tunnelId, port) error` via `agentMutate`.
- **Frontend:** the `EventPortReassigned` conflict card gains "Give :`<RequestedPort>` to
  this agent" (calls `ReclaimPort`) and "Keep the reassignment" (dismiss). Offered only when
  both parties are enrolled.
- **New event kind:** `EventPortReclaimed = "port-reclaimed"` in `conflict.go`.

### #28 — Version "Update" badge (no wire change)
- **Backend:** capture `hello.AppVersion` on the session → `AgentLink.Version` →
  `AgentView.Version`.
- **Frontend:** in the roster card, compare `agent.Version` to the gateway's own version
  (`App.Version()`); when the agent is older, show an "Update" pill. Unknown/empty version →
  no badge (never a fake "up to date").

### #29 (read-only half) — Gateway-authoritative config panel
- **Backend:** `App.AgentDesiredConfig(agentID) ({tunnels: TunnelSpec[], generation})`
  from `AgentStore.DesiredConfig`. (Roster stays light; detail method for the drill-in.)
- **Frontend:** in the agent drill-in, a read-only "Gateway config (generation N)" panel
  listing the tunnels the gateway holds for that agent (name, public port, type). States:
  skeleton / list / empty ("no tunnels yet — the agent will seed on connect") / unavailable
  (non-enrolled agent → "shared-token agent; config is agent-pushed").
- The **write** half (editing + promote with consent) is the follow-up consent-protocol spec.

### #30 — Active-transport display (read-only)
- **Backend:** `AgentLink.Transport` (from the session type: `quic` | `per-conn` | `mux`) →
  `AgentView.Transport`.
- **Frontend:** show the active transport in the drill-in (a labeled chip). The agent owns
  its transport dial choice; the gateway reports, never pins. Unknown → "—".

### #31 — Typed tunnel IDs on create (adopt `tnl_`)
- **Precheck:** grep for any code that assumes the 32-hex tunnel-ID format (length/charset
  validators, analytics keying). If a hard assumption exists, stop and fall back to dropping
  the minter (flag it); otherwise proceed.
- **Go create paths:** use `link.TunnelID(name, taken)` where the current tunnel set gives
  the `taken` predicate (`main.go`, `app.go` tunnel-create sites).
- **Frontend create path:** slug the tunnel name to `tnl_<slug>` with a `-2` suffix against
  current tunnel names (`newTunnelID` in `Tunnels.tsx`), mirroring `link.TunnelID`.
- Existing hex IDs are untouched (the local config dir is disposable — no migration).

## New bindings & fields (→ `wails build` regen; `backend-capability` skill)

- `App.ReclaimPort(agentID, tunnelId string, port int) error`
- `App.AgentDesiredConfig(agentID string) AgentConfigView` (tunnels + generation)
- `AgentView` gains `Version string`, `Transport string`
- devmock stubs for `ReclaimPort`, `AgentDesiredConfig`; extend the `&fleet=multi` fixture
  with version skew (one older agent), a transport spread, and a reclaimable
  `&conflict=port` event so every new surface has data.

## Verification

- `go test ./...` green (new: `ReclaimPort` actor test — reclaim moves both agents' ports
  and pushes; refuse when a party is legacy; `AgentDesiredConfig` returns the stored set).
- `golangci-lint run ./...` 0 issues; `gofmt` clean.
- `cd frontend && npm run build` green.
- Headless (playwright-core + Brave against the devmock, `&mock=gateway&fleet=multi`):
  issue-code control renders + copies; Overview strip shows an enrollment code; the port
  card shows both actions; the Update badge appears on the skewed agent; the config panel
  and transport chip render; all four states per surface.
- No `frontend/wailsjs` hand-edits; every new binding stubbed in devmock.

## Out of scope (consent protocol — its own spec)

The mutual-consent config-change protocol: gateway-side tunnel **editing** and agent-side
edits both become propose → notice → approve/reject → apply, with pending UI on both sides.
Wire-protocol + security change. Also out: per-agent transport **override** (agent owns its
transport), QUIC migration, PQ signatures, key rotation.
