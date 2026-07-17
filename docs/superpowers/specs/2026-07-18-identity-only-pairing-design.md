# Identity-only pairing

Date: 2026-07-18. Author: Claude (Fable 5). Status: design approved, plan written.

## Context

proxyforward carries two ways an agent can authenticate: the Ed25519 per-agent
identity (ticket enrollment, scoped, revocable, `agt_` IDs derived from the key) and
the legacy fleet-wide shared token (bare hex, unscoped, self-asserted agent IDs).
The shared path undermines the identity path's guarantees: a revoked machine that
still holds the token can rejoin under a fresh self-asserted ID, and rotating the
token disconnects every agent at once. There is no deployed fleet, so no migration
is owed.

The user decided (in-conversation, 2026-07-18, including explicit sign-off on the
wire-protocol escalation): delete the shared-token path entirely, wire fields
included; patch the UI for its removal; and improve the identity path so the core
use case ("two people who know each other easily set it up") is one click, plus fix
two confirmed backend gaps found during exploration.

## Decisions

### 1. The shared token is deleted, not deprecated

- Gateway: `sharedTokenValidator` and the composite fallback go; the identity
  validator is the only authenticator. `AcceptSharedToken` leaves the config. The
  `agt_` namespace reservation (`link.IsDerivedAgentID`) becomes moot and goes too.
- Config: `Gateway.Token`, `Agent.Token`, and `NewToken` are removed. An agent
  config is valid with `GatewayHost` + `CertFingerprint` (identity key material is
  managed by the engine; a load failure is fatal, since no fallback exists).
- Wire: `Hello.Token` and the self-asserted `Hello.AgentID` are removed outright,
  in a protocol-only commit. **No `ProtocolVersion` bump**: the hello exchange
  mechanics are unchanged, and a pre-removal binary talking to a new gateway gets a
  clean fatal "re-pair" error rather than a protocol mismatch.
- UI: the Overview legacy disclosure, the Settings shared-token row, token rotate,
  and the copy-pairing command all go. `SetupAgent` rejects bare-hex codes with an
  instruction to mint a fresh code on the gateway.
- Bootstrap replaces the console token code: when the gateway starts with an
  **empty roster**, `RunStarted` mints a single-use ticket (TTL 60 min, a numbers-
  table row) and prints that pairing code to the console. Non-empty roster: nothing
  is printed, nothing stands.

### 2. The agent finishes its own enrollment (bug fix)

Today the agent ignores `HelloOK.AssignedAgentID` and never clears the spent
`EnrollTicket` from config.toml (the config comment claiming otherwise is
aspirational). After this work: on an enrolled hello_ok the agent reports the
assignment, and the engine (the config-path owner) persists `Agent.AgentID` and
clears `EnrollTicket`, through a single-writer seam so app-layer saves cannot race
it. A stolen config file no longer contains a live credential once enrollment
completes.

### 3. One-click friend pairing

The Overview pairing strip keeps the strict default (single-use, 10 min) and gains
**"Mint friend code"**: one click issues a reusable, never-expiring, full-access
code, revealed masked with copy and a "manage it under Agents" hint. Nothing is
auto-created at install; nothing is standing until the user clicks. The friend code
is honest about what it is: a standing credential, visible and revocable in the
roster.

### 4. Invites live in the Agents roster

Every live pairing code is a row in the roster, so a standing credential is never
invisible:

- `enrollNonce` gains `IssuedAt`, `Label`, `Uses`; `Enroll` bumps `Uses` and stamps
  the new `AgentRecord.EnrolledVia` so each agent records which invite admitted it.
- `ListInvites` returns live invites only (unconsumed, unexpired); `RevokeInvite`
  deletes the nonce, so the next redemption attempt gets the existing honest
  `ErrTicketUnknown`. Revoking an invite never disconnects agents it already
  enrolled (unlike token rotation, which severed everyone).
- The invite handle is the first 8 hex chars of sha256(ticket); views and revoke
  use the handle, and the raw code is only recomposed engine-side for copy.
- Enrollment emits a gateway event into the existing event ring; freshly-enrolled
  agents wear a "New" badge for a bounded window (numbers-table row).
- Roster rows: reusable invites persist until revoked; a single-use invite shows
  during its pending window and is replaced by the agent that consumed it. Rows
  show kind, scope, expiry countdown, uses, copy, revoke; pending-invite cards are
  visually distinct (dashed accent, no Signal Glass — the roster is the screen's
  identity surface). Agent detail gains "Enrolled {date} via {invite}" provenance.
- `IssuePairingModal` gains a label field and a friend preset.

### 5. The test suite proves the identity path first

The e2e harness authenticates via the shared token by default today. Before any
removal, the default flips to ticket enrollment, so the entire suite (reconnect,
eviction, consent, drift, throughput floor) runs on the identity path and the
removal commits are test-led deletions.

## Guiding facts (verified against the code)

- All production readers of `hello.AgentID` sit inside `sharedTokenValidator`
  (`internal/gateway/auth.go`); deleting the validator frees the wire field.
- The sole `AcceptSharedToken` read is `gateway.go` `Start`; both `Validate` call
  sites (QUIC and TCP) share one seam and do not change.
- The nonce map is keyed by the raw ticket string, so full codes can be recomposed
  for invite copy via the existing `issuePairingCode` assembly (`engine/agents_api.go`).
- The engine already knows `configPath`; production config saves currently happen
  only in `app/app.go`, which is why enrollment persistence needs one owner.
- `hello_compat_test.go` refixtures around the identity-era hello as the new
  protected baseline; omitempty guarantees for optional fields are still asserted.

## Verification

- Every commit: `go test ./...` green (unit + e2e + burst floor + doccheck);
  bindings/frontend commits also `cd frontend && npm run build`.
- Devmock state walk (headless Brave): Overview strip + friend code, roster invite
  rows in all four data states, fatal re-pair wording, wizard `tkt_` paste.
- `wails dev` smoke: mint a friend code, see it as a roster row, copy, revoke.

## Out of scope

`HelloOK.GatewayID` stays unconsumed (no config field wants it). QUIC migration,
PQ signatures, and key rotation remain roadmap. UDP tunnels unchanged (still a
latent config gap, not part of this work).
