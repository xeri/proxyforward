<!-- Companion to /CLAUDE.md (see "How to think here"). Same provenance: audit @ 4a8b0c9, 2026-07-13. -->

# How to reason in this codebase

Procedural, not philosophical. Follow the steps; don't skip to a patch.

## 1. Debugging tunnel problems

### Step 0 — reproduce before reading code

| Bug class | Reproduction harness |
|---|---|
| Tunnel behavior (connect, relay, reconnect, throughput) | In-process loopback: copy `newHarness` usage from `internal/e2e/e2e_test.go` — real gateway + agent + TLS + mux, no WAN. Write the failing case as a test *first*. |
| Two-process behavior (IPC attach, service, supersede) | Two terminals: `go run . gateway --config <tmp>\gw.toml` + `go run . pair <code> --config <tmp>\ag.toml` + `go run . agent --config <tmp>\ag.toml`. Private configs keep your real setup untouched. |
| UI state/logic | `cd frontend && npm run dev` → `http://localhost:5173/?mock=…` with the axis that matches the report (`devmock.ts` header lists them). No Go needed. |
| UI ↔ real backend | `wails dev`. Only after the mock can't reproduce it. |

If you cannot reproduce, you may still gather evidence (logs below) — but you may not
write a fix, only instrumentation.

### Step 1 — isolate the layer

The system is a chain. Name the failing hop before opening any file:

```
[1 player↔gateway ingress] → [2 gateway↔agent transport] → [3 mux/stream layer]
→ [4 agent↔local-server egress] → [5 Go control plane] → [6 Go↔JS bridge] → [7 React state]
```

Evidence sources per layer:
1. **Ingress**: gateway log `tunnel registered`/`public conn rejected`; `netstat -ano`
   for the bound port; `proxyforward firewall status`; the Tunnels screen "Test player
   path" (dials the public port for real, `app/tools.go testReachability`).
2. **Transport**: gateway log `agent rejected`/`agent connected (generation N)`;
   agent log `connected to gateway`/`link down — reconnecting`; `EngineFatal` in the UI.
3. **Mux/stream**: `open stream for client failed`, `splice ended with error`,
   throughput/RTT from `go test -run TestBurst -v ./internal/e2e/`.
4. **Egress**: agent log `local server unreachable` / `local server is down`
   (health probe, 5 s cadence); PP2 misconfig shows as instant disconnects with
   garbage in the *server's* log.
5. **Control plane**: `tunnel rejected by gateway (code=…)`, `superseding previous
   session`, hot-apply logs in `agent/hotapply.go`.
6. **Bridge**: the ConnectionPill shows "Syncing…" when ticks stall (`state.ts
   useTickStale`); `historyUnsupported`/`analyticsUnsupported` flags on the tick;
   `Status().pid` tells you *which process* you're attached to.
7. **React**: module caches serve stale-but-instant data by design (`hooks.ts`);
   check the poll cadence before calling data "wrong" (5 s–60 s, `analytics.ts`).

### Step 2 — symptom → first suspect

| Symptom | First suspect (layer) | Check first |
|---|---|---|
| Players can't connect at all | 1: router/firewall/DNS, not code | "Test player path" button; `firewall status`; pairing host is a name players can resolve |
| Connects, then instant disconnect | 4: local server down, or PP2 on without `proxy-protocol: true` in paper config | agent log; toggle PP2 off and retry |
| Worked on LAN, fails from internet | 1: port-forward of control port *and* public port | Wizard's PortChecklist lists both (`Wizard.tsx`) |
| Link flaps ~every 15 s | 2/3: something is starving the control stream (blocking write, huge frame) or a clock jump | agent log timestamps; `netnotify` resume detector fires on wall-clock jumps > 40 s |
| `bad_token` loop | Must NOT loop — fatal errors return from `Run` (`agent.go:247`). If it retries, the fatal classification broke | `TestBadTokenRejected` |
| "another agent is already connected" | 5: second machine (or copied config) with a different `agent_id` — by design | `actor.admit`; re-use the identity via .pfsetup export/import instead |
| Port in use on rebind | 5: if the error names `proxyforward` itself, the ghost-listener guarantee broke (serious); a foreign PID is user-environment | `portowner` decorates the error with the owner; `TestAgentRestartRebinds` |
| Slow throughput / RTT spikes under load | 3: window/buffer/HOL — or someone added work per copy iteration | burst test before blaming the network; diff `relay.go`/`yamux.go` against invariants |
| One player frozen, others fine | 3: that splice hit the 2-min write stall (dead client) | `splice ended with error … deadline` at debug level |
| UI numbers frozen, engine alive | 6: tick stalled or you're attached to a different daemon | "Syncing…" pill; compare `status.pid` with Task Manager |
| UI shows 0 ms / 0 % instead of "—" | 7: sentinel misuse | grep the surface for `hasRtt` / `< 0` guards (`state.ts:71`) |
| Player names missing | 4/7: tunnel isn't `MinecraftAware`, or login didn't parse (fail-open by design) | tunnel options; `TestMinecraftLoginSniffed`; sniffing never blocks traffic |
| Avatars all placeholders | 6: cold cache warming (expected for seconds), Mojang rate limit, or you're in the browser mock (different URL path) | `avatars: …` debug logs; `.miss` files in `%APPDATA%\proxyforward\avatars` |
| Analytics screens empty in attached mode | 6: daemon predates analytics (latch) or DB failed to open | `analyticsUnsupported` on the tick; engine log `analytics: database unavailable` |
| History gaps / "unknown" bands after upgrade | Not a bug: pre-upgrade buckets carry -1 gauges on purpose (`stats/persist.go`) | — |
| GUI window never appears (windowsgui build) | Startup failure with no stderr — by design | `%APPDATA%\proxyforward\logs\crash.log` + `wails.log` (`main.go installCrashLog`), not "add prints" |

### Step 3 — confirm root cause before patching

A root cause is confirmed only when you can state: *"When X happens, file:line does Y,
which produces the observed Z"* — and you have a log line, failing test, or repro
command demonstrating it. Then write the fix, keep the repro as a regression test if
one didn't exist, and re-run the layer's gate (burst for 3, e2e for 2/5, mock walk for 6/7).

## 2. Standards of evidence

- PASS means: a named test that ran (`go test -run TestX ./internal/y/` output), a log
  line quoted from a real run, or a manual check with the exact command/URL used.
- Banned as conclusions: "should work now", "likely fixed", "this probably was the
  issue". If you haven't observed it, write "hypothesis:" in front of it.
- Numbers get sources: quote the burst line (`e2e_test.go:714` format), not an adjective.
- Silence is not success: after a fix, re-run the *failing* reproduction, not just the
  full suite.
- When a bug can't be reproduced in-process, the deliverable is instrumentation + a
  hypothesis list ranked by evidence — not a speculative patch.

## 3. Refactor discipline

1. Characterize current behavior first: identify (or write) the tests that pin it.
   For anything touching `relay/`, `transport/`, or per-connection code, record the
   burst baseline ×3 runs.
2. Smallest reversible step; one concern per commit; behavior-preserving commits
   separate from behavior-changing ones.
3. Never change protocol and implementation in one commit (the `wire-protocol`
   skill in `.claude/skills/`).
4. Preserve the why-comments — they are the spec. If your change invalidates one,
   updating it is part of the change.
5. goleak (`e2e TestMain`) and the private-pipe test rule survive every refactor.
6. After: full `go test ./...`, burst comparison if hot-path-adjacent, `wails build`.

## 4. UX judgment rules (derived from frontend/DESIGN.md + the kit)

Apply in order when polishing a screen; each is a question with a mechanical answer:

1. **Identity surface**: does the screen have exactly one `pf-signal` surface (or a
   deliberate bare artwork like Traffic's chart / Analytics' map)? More than one →
   demote the others to `pf-card`. None on a live-network screen → find the one
   surface that represents live activity.
2. **Box test**: is metadata sitting in its own card? Convert to type on whitespace —
   `Overline` label + value (`StatTile`/`HealthMetric` pattern). Cards are for
   *groups*, not single facts.
3. **Label recipe**: every label = `--fs-caption` + uppercase + `--tracking-label` +
   weight 600 + `--text-3` (`Overline`). Values dominate, labels recede. Type contrast
   over type size; only tokenized sizes.
4. **Color audit**: for every non-neutral color ask "what signal is this?" Allowed:
   role accent (identity/nav/primary/live), `--good/--warn/--bad` (state),
   `--dl/--ul/--conn/--rtt` (series). Anything else → neutral.
5. **Motion audit**: for every animation ask "what network state does this
   communicate?" Conduits flow with packets, ignite on connect, country pulses on
   join, halos on live dots. Decorative-only motion → cut. Everything gates on
   `prefersReduced()` and must land instantly under it (data never eases).
6. **Four states**: loading skeleton with the final geometry; data; a *written* empty
   state (what appears here, and what the user can do); an honest unavailable state
   distinct from empty. Check via mock axes `&fresh=1`, `&analytics=off`, `&geo=…`.
7. **Sentinel honesty**: any 0 that could mean "unknown" renders as "—" (`hasRtt`,
   `fmtPct`). Truncated lists say so (`connectionsTruncated` pattern).
8. **Spacing rhythm**: `--sp-12` between page groups, `--sp-6` within, `--grid-gap`
   in grids, `--sp-2` label→value. If you're inventing a margin, you're off-system.
9. **Interaction floor**: Esc closes floats; Enter/Space activate rows and pills;
   data (IPs, codes, addresses) is `select-text` + `CopyIcon`; destructive actions
   are two-step (`DeleteButton`, `TokenRotate`).
10. **Full matrix before "done"**: both themes × both roles × Animations Off ×
    `&fx=low`, at narrow (~1280 px) and wide widths (screens use container queries).

## 5. Escalation triggers — stop and ask the human

- Any `ProtocolVersion` bump, capability semantic change, or wire-field meaning change.
- Anything in the auth/TLS/pairing chain, the pipe ACL, redaction (`app/tools.go`),
  or `.pfsetup` crypto (`internal/setup/crypto.go`).
- Shipping UI that exposes a Reality-check stub as if it worked — or implementing the
  stub itself (scope decision: several are half-designed on purpose).
- Cutting or tagging a release; changing version stamping.
- Anything that deletes or rewrites user data: config schema, `analytics.db`
  migrations that drop columns/rows, retention defaults, stats import behavior.
- Loosening abuse limits or timeouts that are security-relevant
  (`preAuthTimeout`, frame caps, auth limiter, conn gates).
- Destructive machine operations beyond killing dev servers you started.
