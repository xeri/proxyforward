# Design — Honesty pass: wire up (and stop over-advertising) four stubbed features

- **Date:** 2026-07-15
- **Status:** proposed (awaiting review)
- **Context:** `docs/agent/polish-backlog.md` item #1 ("Stub controls promise unimplemented
  behavior") and the `CLAUDE.md` "Reality check" table. Each change here **deletes a
  Reality-check row** — the app stops lying, and where cheap, starts telling the truth for real.

## Goal

Make proxyforward's advertised surface honest. Four independent changes, none touching the
data hot path. Two of the four *implement* a stubbed feature (Prometheus, Offline MOTD); one
*removes a false capability advertisement* (UDP); one *corrects/hides UI copy* for things we
are deliberately not building yet (per-conn transport, tray/autostart, MC status polling).

## Scope

### In scope (this spec)

1. **CapTunnelUDP — stop advertising** an unimplemented capability (the one live protocol bug).
2. **UI honesty** — correct the Minecraft-aware hint; hide the per-connection transport option
   and the tray/autostart toggles.
3. **Prometheus `/metrics`** — stand up the endpoint that config already stores.
4. **Offline MOTD** — wire the already-built, already-fuzzed `mc.ServeOffline` into the gateway.

### Out of scope (each its own spec)

- **Bandwidth cap enforcement** — the only stub touching the data hot path; carries three skill
  gates (`hot-path`, `wire-protocol`, `dep-bump`) and a new dependency. Split into its own
  burst-gated spec immediately after this one. Its Reality-check row (`CLAUDE.md`, "Bandwidth
  cap") and its UI controls (`Tunnels.tsx` bandwidth field) are **left untouched** by this spec.
- **Multi-agent namespacing** (`actor.go` single-session → `map[agentID]`) — the committed next
  architectural direction; its own spec. Data-wipe is authorized (no on-disk migration needed).
- **UDP tunnels / per-conn transport / tray / autostart** — real features for later lanes. This
  spec only makes their *advertising* honest, it does not build them.

## Cross-cutting discipline

- **Reality-check rows deleted in the landing commit.** Per `CLAUDE.md` "when you implement one,
  delete its row in the same commit." This spec lands three deletions: Offline MOTD row, UDP row,
  Prometheus row. The per-conn, MC-status-polling, and tray/autostart rows **stay** (we hide UI,
  we do not implement the backend). The bandwidth row stays (separate spec).
- **`internal/doccheck`** scans `CLAUDE.md`, `docs/agent/`, `.claude/rules/`, `.claude/skills/`.
  It does **not** scan `docs/superpowers/`, so this file's citations are informational. But every
  symbol removed (e.g. `CapTunnelUDP`) must have its doc mentions updated in the same commit for
  accuracy — the repo treats a stale citation as a defect even where doccheck can't catch it.
- **One checker per surface.** Go: `go test ./...`. Frontend: `cd frontend && npm run build`.

---

## Feature 1 — CapTunnelUDP: stop advertising *(wire-protocol · size S)*

**Problem.** `CapTunnelUDP` is advertised in `SupportedCapabilities`, but no UDP code exists and
`validateSpec` rejects `type:"udp"`. The gateway echoes the capability back in `hello_ok`, the
agent may act on a udp spec, and it then dies — a live protocol lie the `CLAUDE.md` invariant
("Never advertise a capability that isn't implemented end-to-end") explicitly names as violated.

**Design.** Remove the advertisement. `SupportedCapabilities` (`internal/control/control.go:48`)
is the single source of truth for both the agent offer (`agent.go:283`) and the gateway's
accepted intersection (`gateway.go:445`), so one edit closes both sides.

**Changes (one protocol-only commit):**
- `internal/control/control.go:48` — `SupportedCapabilities` → `[]string{CapTunnelSync, CapConnStats}`.
- `internal/control/control.go:34-38` — delete the `CapTunnelUDP` const + comment (nothing consumes it; no `CapSet.Has(CapTunnelUDP)` anywhere).
- `internal/control/control.go:183` — fix the `TunnelSpec.Type` comment (drop "udp requires CapTunnelUDP").
- `internal/config/config.go:38-39` — fix the `TunnelUDP` comment ("Requires the tunnel-udp capability" is now false).
- `internal/control/control_test.go:146` — remove `!s.Has(CapTunnelUDP)` (won't compile once the const is gone); update the stale comment at `:149`; add a positive assertion that the supported set no longer contains `"tunnel-udp"` to lock the fix in.
- Docs: delete the UDP Reality-check row (`CLAUDE.md`, ~line 158); drop the "(Currently violated by `CapTunnelUDP`…)" parenthetical from the invariant bullet (~line 64-65); reword the "standing counterexample" bullet in `.claude/skills/wire-protocol/SKILL.md:21-22` to past tense.

**Confirmed safe.** No golden-frame impact — `capabilities` is a live-negotiated `omitempty`
field, never present in the byte-identical assertions of `hello_compat_test.go` /
`TestLegacyHelloCompat`. **No `ProtocolVersion` bump** — dropping a capability is
backward-compatible (a legacy peer that offered `tunnel-udp` simply has it negotiated away, the
already-tested sync-only path). `validateSpec` and `validateAgent` are untouched: a `type:"udp"`
agent config still passes agent validation and is still rejected at the gateway — this change
only stops the false "I support udp" signal, it does not add or remove udp support.

**Escalation.** Wire-protocol change → human sign-off required (this spec's approval). It is
*protocol-only* (no implementation rides with it), satisfying "protocol and implementation never
change in the same commit."

---

## Feature 2 — UI honesty *(frontend · size S)*

Three edits. The Offline-MOTD, Bandwidth-cap, and Prometheus controls are **left in place** —
two of them become real in this spec, and the bandwidth field becomes real in the next.

1. **Minecraft-aware hint** (`frontend/src/screens/Tunnels.tsx:270`). The claim "Poll the server
   for MOTD, player count and version" is fiction (no status poller exists — only login
   sniffing). Replace with a hint describing only the true behavior:
   `"Sniff player usernames from the login handshake for the traffic and players views."`
   The `MinecraftAware` toggle itself stays (sniffing is real, via `mcsniff/`).

2. **Per-connection transport option** (`frontend/src/screens/Settings.tsx`, transport `Select`
   in the agent-role branch, ~line 170-174). The backend never honors `per-conn`. Remove that
   one option from the `Select` (leaving `mux`), and correct the Field hint (~line 170) which
   currently sells the removed option, e.g.
   `"All player traffic is multiplexed over the single control connection."`

3. **Tray / autostart toggles** (`frontend/src/screens/Settings.tsx`, "Behavior" `Section`,
   ~line 152-157). Both inert. Remove the whole Section (it contains only these two toggles) —
   **and** its `SectionRail` entry (`{id: 'behavior', label: 'Behavior'}`, ~line 54), or the
   scrollspy will point at a missing `s-behavior` node and mis-measure. The codebase has no
   "coming soon" convention; straight removal is idiomatic. `cfg.UI.MinimizeToTray`/`Autostart`
   are unread elsewhere in the frontend after this (the `devmock.ts` mock value is harmless).

**Reality-check rows:** per-conn, MC-status-polling, and tray/autostart rows **stay** (backend
still unimplemented; we corrected the UI only).

**Gate:** `cd frontend && npm run build` (tsc is the only checker). State-matrix walk is a near
no-op (copy/visibility only); re-verify the Settings scrollspy in both roles after edit 3.

---

## Feature 3 — Prometheus `/metrics` *(engine · size M)*

**Problem.** `MetricsConfig` (`config.go:108-111`, default `127.0.0.1:9464`) is stored and the
Settings toggle round-trips it, but no HTTP server exists.

**Design.**
- **Source:** the handler calls `Engine.Status()` (`engine.go:341`) once per scrape — the same
  lock-free snapshot the IPC pipe already assembles. **Zero data-path cost**; no new counters,
  no per-byte work. Respects the `conntrack.go` lock-free-read invariant.
- **Dependency: hand-roll, do not add `client_golang`.** It's absent from `go.mod`/`go.sum`; the
  exposition format is trivial line-oriented text (`# HELP` / `# TYPE` / `name{label="v"} value`,
  `Content-Type: text/plain; version=0.0.4`). The repo's minimal-dependency posture
  (`dependency-review` CI gate) and a fixed ~13-metric set make a stdlib `net/http` handler the
  right call. Escape label values (`\`, `"`, `\n`).
- **Lifecycle:** new `internal/engine/metrics.go` — `func (e *Engine) serveMetrics(ctx)` plus a
  pure formatter helper (unit-testable). Spawn under `runCtx` in `Engine.Run` (~line 200-211,
  beside `runSampler`/`resolver.Run`), own a `metricsDone` channel drained at `engine.go:219-220`
  after `cancel()`. On `runCtx.Done()`, `srv.Shutdown(context.Background())` (or `ln.Close()`)
  **before `Run` returns** — mandatory, because `RestartEngine` constructs a new engine and
  re-binds the same `PrometheusAddr`; a leaked listener = "address already in use" on restart.
- **Non-fatal.** Unlike the IPC pipe (fatal by design via the 2-slot `errCh`), a metrics bind
  failure logs a WARN and returns — proxying is the core job. If `!PrometheusEnabled`, don't
  spawn at all (so the disabled-path test can assert the port is free).
- **Exposure/privacy.** Keep the loopback default. Warn on a non-loopback bind
  (`net.SplitHostPort` + `net.ParseIP().IsLoopback()/IsUnspecified()`) — soft warning, not a
  hard validation error (users may front it with a reverse proxy). **No player-name or peer-IP
  labels** (privacy charter) — use `Registry.PlayerCount()` (a count), never `Snapshot()`.
- **Sentinels.** `-1` "unknown" gauges (RTT/jitter/loss) are **omitted entirely**, never
  exported as `0`/`NaN` — preserves the honest-unknown invariant.

**Metric set (all from `Status()`, all prefixed `proxyforward_`):** `build_info{version,role}`,
`bytes_total{direction}`, `alltime_bytes_total{direction}`, `link_bytes_total{direction}`,
`connections`, `players`, `link_up`, `link_rtt_ms`/`link_jitter_ms`/`link_loss_pct` (omit at -1),
`link_sessions_total`, `uptime_ms`, `tunnel_local_up{tunnel_id,name}`.

**Tests (`internal/engine/metrics_test.go`, modeled on `engine_test.go`):**
1. Content/format — GET `/metrics`, assert 200 + content-type + a well-formed known series
   (e.g. `^proxyforward_connections \d+$`); assert **no** name/IP substring leaks.
2. Restart / port-leak — start on a fixed borrowed port, cancel, wait for `Run` to return, start
   a second engine on the **same** port and assert it also serves (proves listener release).
   `goleak` additionally enforces the goroutine exits.
3. Disabled path — `PrometheusEnabled=false` → port stays free.
4. Config validation — enabled + malformed addr fails `Validate`; enabled + public addr still
   validates but emits the WARN (assert via a captured `slog` handler).

**Doc:** delete the Prometheus Reality-check row (`CLAUDE.md`, ~line 160) in this commit.

---

## Feature 4 — Offline MOTD *(gateway · size M)*

**Problem.** `mc.ServeOffline` (`internal/mc/status.go:62`) is built and fuzzed but never called;
a player hitting a tunnel whose backend is down gets a dead socket. The seam even has a
"milestone 5 adds the offline MOTD here" comment (`gateway.go:782-785`).

**Design.** One helper, wired into `handleClient` (`gateway.go:786`), which is already called
with the owning `*agentSession` and this tunnel's `control.TunnelSpec` in scope.

- **Gate on opt-in.** `spec.OfflineMOTD == ""` ⇒ clean close (today's behavior). Non-empty ⇒
  serve. This guard is load-bearing: at the mc layer an empty MOTD silently becomes "Server
  offline", but the *gateway* contract is empty = feature off. Do **not** additionally require
  `MinecraftAware` — gate purely on `OfflineMOTD != ""`, matching the config contract.
- **Primary trigger: health map.** The common case is "agent connected, local server crashed."
  Read `sess.health.Load(spec.ID)` (the per-tunnel `LocalUp`, fed by `TypeHealth` frames,
  `actor.go:59-61` / `gateway.go:696`). Serve offline only when health is **known and down**
  (`ok && !up`); on unknown health, attempt the real connection (never false-positive a working
  backend). Reading `sess.health` directly is more correct than the `g.TunnelLocalUp` helper,
  which reads the *current* session.
- **Also cover the race paths.** Route the existing `mux == nil` early return (`gateway.go:794-797`)
  and the `OpenStream` failure (`~798-802`) through the same helper, so a session dying
  mid-accept also gets a graceful MOTD instead of a bare close.
- **No stream / no conntrack / no `OpenConn`** on the offline path (it returns before all of
  that). Add a serve deadline — the public conn has none today; `mc.ServeOffline` requires the
  caller to own deadlines, and `handleClient` already owns Close via `defer clientConn.Close()`.
- **Skip VersionName and player counts.** `TunnelSpec` carries no version; let it default to
  `"offline"` (protocol is pinned to `-1` regardless, so the string is cosmetic). Real player
  counts would need extending `OfflineInfo` + `StatusResponse` — not worth the surface; `0/0` is
  correct for an offline server.

**Sketch (`internal/gateway/gateway.go`):** import `internal/mc`; add `offlineServeTimeout`
(~10s) near the other deadlines; add `serveOffline(conn, spec)` (guard on empty, set deadline,
call `mc.ServeOffline(conn, mc.OfflineInfo{MOTD: spec.OfflineMOTD})`, debug-log the end) and a
tiny `healthDown(sess, id)` helper; rewire the two early returns to
`if mux == nil || healthDown(sess, spec.ID) { g.serveOffline(clientConn, spec); return }`.

**`ServeOffline` already handles all three exchanges:** server-list status ping (MOTD in the
list), login/transfer attempt (disconnect with MOTD as the chat reason), and legacy `0xFE` ping.

**Tests:**
- **Primary e2e** (`internal/e2e`, modeled on `TestHealthPropagates`): extend `harnessOpts` with
  an `offlineMOTD` field (thread it into the tunnel `Options` like `mcAware`); point `LocalAddr`
  at a dead port so `probeOnce` reports down; wait for `TunnelLocalUp` to read known-and-down;
  dial the public port, send a status handshake+request, unmarshal `mc.StatusResponse`, assert
  `Description.Text == offlineMOTD` and `Version.Protocol == -1`. Add a login-intent variant
  asserting the disconnect-reason JSON equals the MOTD.
- **Optional gateway unit test** (`mux == nil` path via a nil-session `agentSession` over
  `net.Pipe`). The mc protocol itself is already unit + fuzz tested, so the e2e alone is
  acceptable coverage if the fake session proves fiddly.

**Doc:** delete the Offline-MOTD Reality-check row (`CLAUDE.md`, ~line 156) in this commit.

---

## Build sequence

Independent features; order is lightest/riskiest-isolating first. Each is its own commit (or
small commit set) and lands its own doc-row deletion.

1. **CapTunnelUDP removal** — protocol-only; smallest; kills the live bug.
2. **UI honesty** — frontend only; `npm run build`.
3. **Prometheus** — engine HTTP server; non-wire, non-hot-path.
4. **Offline MOTD** — gateway wiring; non-wire, non-hot-path.

(Then, separate spec: bandwidth cap.)

## Testing & gates

- Full Go gate `go test ./...` after 1/3/4 (unit + e2e + goleak + doccheck).
- `cd frontend && npm run build` after 2.
- No `hot-path` burst gate applies to this spec (nothing touches `relay`/`transport`/`stats`
  counting) — that gate belongs to the bandwidth-cap spec.
- `-race` runs in CI only (no local C compiler).

## Risks & escalation

- **Feature 1 is a wire-protocol change** → the `wire-protocol` skill flags it as an escalation
  trigger. It is protocol-only and backward-compatible; this spec's approval is the documented
  human sign-off on the exact frame delta (removing `tunnel-udp` from the advertised set).
- **Feature 3 restart hazard:** a leaked metrics listener breaks `RestartEngine`. Mitigated by
  the drain-before-`Run`-returns discipline and the dedicated restart/port-leak test.
- **Feature 4 semantics:** the empty-MOTD guard must be exact, or every backend-down tunnel would
  suddenly emit "Server offline" to opted-out operators. Covered by the guard + gated e2e.

## Open questions

None outstanding. Resolved during brainstorming: implement all four (vs hide); split bandwidth
cap into its own spec; hand-roll Prometheus (no new dep); Offline MOTD triggers on health-down
with the race paths folded in.
