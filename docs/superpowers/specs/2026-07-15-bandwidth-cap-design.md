# Per-tunnel bandwidth cap — enforcement + scope selector

Status: approved 2026-07-15. Layers on `feat/multi-agent-gateway`.

## Problem

`config.TunnelOptions.BandwidthLimitMbps` already exists — agent-side TOML, validated
`>= 0`, surfaced in the Tunnels editor — but is **never read**. It is the last
Reality-check stub sitting on the data hot path: advertised, stored, ignored. This design
makes it real. A configured cap actually throttles the tunnel, enforced on **both** the
agent and the gateway, and gains a **scope** dimension so the cap is a `(rate, scope)`
pair rather than a bare number.

## Decisions (locked)

- **Scope is selectable**: `combined` (default) | `per-direction` | `per-connection`.
- **Limiter** = `golang.org/x/time/rate` (new light direct dep; stdlib-only transitives).
- **Unit** = decimal megabits/sec: `bytesPerSec = Mbps * 125_000`, one `rate` token = one
  byte. This is the single source of truth for limiter construction, the e2e throughput
  assertion, and the architecture note. No mebibits, no megabytes.
- **Gateway limiter home** = the limiter set hangs on `publicListener`, reached by
  **widening the accept-loop callback to pass `pl`** to `handleClient` (no parallel
  Registry on the gateway; lifecycle rides the nested `listeners` map).
- Build all three phases in one pass.

## Branch reality

The multi-agent gateway (nested `listeners map[agentID]map[tunnelID]*publicListener`,
per-agent `admit`/`evict`, per-agent `agentSession`) is **not on master** — it lives on
`feat/multi-agent-gateway` (Go committed; the frontend Agents dashboard / drill-in is
still uncommitted in the working tree). This work layers on that branch. Two agents may
serve the **same `tunnelID`**, so bucket identity on the gateway is `(agentID, tunnelID)`,
never a bare `tunnelID`.

## Scope semantics (what "N Mbps" applies to)

Scope applies **within one `(agentID, tunnelID)`** — never across agents.

- **combined** (default; `"" ` normalizes here): one shared bucket for that tunnel — both
  directions and all its connections sum to ≤ N Mbps. Both `copyHalf` goroutines call
  `WaitN` on the *same* limiter.
- **per-direction**: two shared buckets (inbound = client→server, outbound =
  server→client), each ≤ N Mbps, each summed across that tunnel's connections.
- **per-connection**: each connection gets its own fresh *combined* bucket of N Mbps;
  writes no shared state, takes no lock.

## Corrections to the working assumptions (verified in code)

The design was drafted against a `pl`-field / `SpliceOpts` / session-context model that
does not yet exist in the tree. Five gaps, each with its fix:

1. `gateway.go handleClient(sess, spec, conn)` receives the spec **unpacked**;
   `acceptClients` calls `handle(pl.owner, pl.spec, conn)` — `pl` is never passed. Fix:
   widen the callback to `func(*publicListener, net.Conn)`.
2. `agentSession` has **no context**. Fix: add `ctx`/`cancel`, cancelled in `evict`, so a
   `copyHalf` blocked in `WaitN` unblocks promptly and per-agent.
3. `relay.Splice(a, b, counters)` has no opts/ctx, and the read-buffer constant `bufSize`
   is **unexported**. Fix: add `SpliceOpts` + a `Limiter` interface; export `BufSize`.
4. Agent side has no ctx at the splice — `session` has no `ctx` field and
   `handleDataStream(st)` isn't given the serve ctx. Fix: thread the serve ctx onto
   `session`.
5. `golang.org/x/time` is not yet a dependency.

Confirmed sound: `TunnelSpec` decodes without `DisallowUnknownFields` (additive fields are
safe); the multi-agent e2e helpers exist (`addAgent`, `waitPublicPortOf`, `tcpTunnel`,
`TestEvictionIsolatesAndDrains`).

## Invariants

- **Uncapped = byte-identical fast path.** `mbps <= 0 ⇒` nil limiters ⇒ `copyHalf` skips
  the limiter branch, and `Splice` skips the per-splice `context.WithCancel` when both
  limiters are nil — zero added allocations/locks. The burst floor
  (`TestBurstThroughputAndCrossStreamLatency`, ≥20 MiB/s, worst cross-stream RTT ≤500 ms)
  sets no cap; re-run it best-of-3 before/after — the run is the *measured* proof.
- **Bucket identity is `(agentID, tunnelID)`** on the gateway, `tunID` on the agent
  (single-identity). Guarded by the two-agent e2e.
- **No per-iteration allocation on the capped path.** Pooled `BufSize` buffer + direction
  limiter resolved once per connection; `WaitN(ctx, n)` once per ≤`BufSize` chunk. Burst
  = `BufSize`, and `n ≤ BufSize` always, so `WaitN` never errors on burst.
- **Direction mapping lives in one place per call site.** Gateway `copyHalf` AToB =
  client→server = inbound; agent AToB = server→client = outbound.
- **Wire additivity.** New `TunnelSpec` fields are `omitempty`, zero-value-safe (0 mbps /
  `""` scope = legacy peer), no capability, no `ProtocolVersion` bump — proven byte-
  identical in `hello_compat_test.go`.

## Phase 1 — config + wire plumbing (no enforcement)

- `internal/config/config.go` — add `BandwidthLimitScope string` to `TunnelOptions`.
  Validate in `(*Config).validateAgent()`: normalize `"" → combined`, **reject** an
  unknown non-empty scope at load, skip the check (no error) when `mbps <= 0`.
- `internal/control/control.go` — add `BandwidthLimitMbps int
  json:"bandwidthLimitMbps,omitempty"` and `BandwidthLimitScope string
  json:"bandwidthLimitScope,omitempty"` to `TunnelSpec`; rewrite the "caps stay on the
  agent" doc comment.
- `internal/agent/agent.go` — `specFromTunnel` copies both fields; update its comment.
- `internal/control/hello_compat_test.go` — `TestTunnelSpecWireBackCompat`: zero value
  omits both keys; a legacy frame decodes both zero; a set value round-trips.
- Gate: `go test ./internal/control/... ./internal/config/... ./internal/agent/...`.
- Commits: (a) config scope + validation; (b) wire fields + `specFromTunnel` +
  `hello_compat` + doc rewrites.

## Phase 2 — enforcement engine

**Dep:** `go get golang.org/x/time && go mod tidy`; full suite + `govulncheck`; confirm no
transitive deps.

**`internal/bwcap` (new):**
- `type Scope string` (`ScopeCombined`/`ScopePerDirection`/`ScopePerConnection`);
  `NormalizeScope` (`"" →` combined, unknown → combined; fail-safe).
- `type LimiterSet` (exported, so `publicListener` can hold
  `atomic.Pointer[bwcap.LimiterSet]`) carrying `scope`, `rate` (mbps), and
  `combined/in/out *rate.Limiter`.
- `BuildSet(mbps, scope)` — combined: one shared limiter; per-direction: two;
  per-connection: shared nils; uncapped (`mbps<=0`): nil limiters, `rate=0`.
- `Resolve(set) (inbound, outbound relay.Limiter)` — uncapped→`nil,nil`; combined→same
  limiter both ways; per-direction→`in,out`; per-connection→mint a fresh combined limiter
  (Resolve is called once per connection).
- `newLimiter(mbps) = rate.NewLimiter(rate.Limit(mbps*125_000), relay.BufSize)`.
- Agent `type Registry` keyed by `tunID`: `Resolve(tunID, mbps, scope)` (get-or-build,
  apply rate-only `SetLimit` / scope-change rebuild), `Release(tunID)`. Mutex-guarded, no
  `config` import.
- Tests: each scope's sharing shape; nil uncapped; rate-only vs scope change; a real
  throughput assertion (drive N ≫ burst, `elapsed ≈ bytes/rate`).

**`internal/relay/relay.go`:**
- Export `BufSize`.
- `type Limiter interface { WaitN(ctx context.Context, n int) error }` (`*rate.Limiter`
  satisfies structurally); `nil` = fast path.
- `type SpliceOpts struct { Ctx context.Context; LimitAToB, LimitBToA Limiter }`; add
  `opts SpliceOpts` to `Splice` (zero value = today). Derive the per-splice child ctx only
  when a limiter is non-nil; `cancel` on either half's return.
- `copyHalf(dst, src, count, ctx, lim)` — inside `if n > 0`, before the write:
  `if lim != nil { if err := lim.WaitN(ctx, n); err != nil { src.Close(); return err } }`.
- `isExpectedCloseErr` — add `context.Canceled` / `context.DeadlineExceeded` as clean; the
  `WriteStallTimeout` net-deadline and a `n > burst` error stay loud.
- Gate: `go test ./internal/relay/...` + burst floor best-of-3 + a relay throttle test.

**Gateway wiring** (`internal/gateway/actor.go`, `gateway.go`):
- `publicListener` gains `limiters atomic.Pointer[bwcap.LimiterSet]`.
- Widen the accept-loop callback `func(*agentSession, control.TunnelSpec, net.Conn) →
  func(*publicListener, net.Conn)` across `bindLocked`, `bindTunnel`, `reconcile`,
  `acceptClients` (`handle(pl, conn)`).
- `bindLocked` builds and `.Store()`s the set from the spec's bandwidth fields.
- `reconcile`: keep the `pl.spec == spec` short-circuit; add a branch — structural fields
  equal but bandwidth differs (`sameListener` = equal on
  ID/Name/Type/PublicPort/OfflineMOTD/MinecraftAware) → update the limiter in place
  (rate-only `SetLimit`; scope change build + `.Store()`), keep the listener and its live
  connections. Never mutate `pl.spec` (read off-actor in `acceptClients`); the `LimiterSet`
  carries the live rate/scope so repeated reconciles converge idempotently. A structural
  change still rebinds.
- `agentSession` gains `ctx`/`cancel` (child of the gateway root ctx), cancelled in
  `evict` alongside `closeAll()`.
- `handleClient(pl, clientConn)` — `sess := pl.owner`, `spec := pl.spec`,
  `in, out := bwcap.Resolve(pl.limiters.Load())`; AToB = inbound:
  `relay.Splice(client, stream, entry.Counters, relay.SpliceOpts{Ctx: sess.ctx, LimitAToB:
  in, LimitBToA: out})`.

**Agent wiring** (`internal/agent/agent.go`):
- `*Agent` gets a `*bwcap.Registry`; `session` gains a `ctx` field set from `serve(ctx)`.
- `handleDataStream` resolves from `tun.Options` via the Registry; AToB = outbound:
  `relay.Splice(tcp, src, entry.Counters, relay.SpliceOpts{Ctx: s.ctx, LimitAToB: outbound,
  LimitBToA: inbound})`. `Release(tunID)` on tunnel removal.

**e2e:** add `bandwidthMbps`/`bandwidthScope` to `harnessOpts`.
- Single-tunnel capped, offered load above the cap, ≥ ~2.5–5 MB at 5 Mbps, throughput
  within ±15% of the cap; uncapped still clears the floor. Cover combined + per-direction.
- Two-agent isolation (guards the keying fix): two agents share one `tunnelID`, each capped
  N; each sustains ≈N independently (combined ≈ 2N).
- Evict-under-throttle (fold into `TestEvictionIsolatesAndDrains`): a capped agent's
  throttled connections drop promptly on evict (session-ctx cancel), the other unaffected.

**Docs:** delete the CLAUDE.md Reality-check "Bandwidth cap" row; add the relay-section
note to `docs/agent/architecture.md` (burst = read-buffer size; `Mbps × 125_000`; caps are
per-`(agentID, tunnelID)`); resolve the honesty-pass "future bandwidth spec" pointer here.

- Commits: (a) dep + `internal/bwcap` + tests; (b) relay threading + relay test;
  (c) call-site wiring + e2e + Reality-check row deletion + architecture note.

## Phase 3 — UI: scope selector (additive)

- `frontend/src/screens/Tunnels.tsx` — a scope `Select` (Combined / Per-direction /
  Per-connection) beside "Bandwidth cap (Mbps)", disabled/ignored at 0, with a one-line
  hint; the tunnel-summary chip shows the scope when a cap is set.
- Gateway drill-in: the cap/scope chip renders in the read-only Tunnels view scoped by
  `agentId`.
- devmock: a capped tunnel in the multi-agent gateway fixture (chip at `?mock=gateway`
  drill-in) + a capped example in the agent fixture (`?mock=agent`).
- Regenerate `frontend/wailsjs/go/models.ts` via `wails build` (never hand-edit).
- Gate: `npm run build`; walk the Tunnels editor + gateway drill-in (both themes ×
  Animations Off); dispatch `ui-design-reviewer`.

## Import layering (no cycle)

`relay` imports nothing new. `bwcap` imports `relay` (for `BufSize`) + `x/time/rate`.
`agent`/`gateway` import `bwcap` + `relay`; the gateway also reads `pl.limiters` (its own
field). `x/time/rate` is reachable only from `bwcap`.

## End-to-end verification (not just tests)

`wails dev`; pair two agents to one gateway (or use the multi-agent devmock gateway
fixture). Give each agent a low cap (e.g. 5 Mbps) on a same-named tunnel, in each of the
three scopes; push traffic; drill into each agent and confirm its per-agent
`BandwidthChart` plateaus at that agent's cap independently (a brief spike up to one
`BufSize` burst is expected). Evict one agent and confirm its throttled connections drop
promptly while the other keeps serving. Flip a cap to 0 and confirm full-rate returns. All
in light + dark, Animations On + Off.
