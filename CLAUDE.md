<!-- Generated 2026-07-13 by Claude (Fable 5) from a full-repo audit at commit 4a8b0c9;
     restructured the same day per the docs-suite evaluation. This file = identity +
     invariants + reality check + routing, ~200-line budget. Conventions auto-load from
     .claude/rules/ by path; procedures are .claude/skills/; reference is docs/agent/.
     Citations are file + symbol, not line numbers; internal/doccheck fails `go test`
     when a cited file, symbol, or test name stops existing. -->

# proxyforward — operating manual

## What this is

An ngrok-style reverse tunnel that makes a Minecraft server behind NAT public. One
Windows binary, two roles: the **agent** (beside Minecraft, dials OUT) and the
**gateway** (publicly reachable, accepts players, relays traffic back through that one
outbound TLS link). Stack: Go engine → Wails v2 shell → React 19 GUI (`main.go`).
The tunnel works; the current phase is performance, stability, and above all UI/UX
polish. Protect what works; be honest about what doesn't exist yet (Reality check).

## Where everything lives

- **Conventions** (Go, tests, frontend) — `.claude/rules/`, auto-loaded by file path.
- **Procedures** — `.claude/skills/`: `backend-capability`, `hot-path`,
  `wire-protocol`, `ui-change`, `dep-bump`, `release`, `overhaul`. Open the matching
  skill before starting that kind of work; don't reconstruct the steps from memory.
- **Architecture map, subsystem deep-dives, and every tuned number** —
  `docs/agent/architecture.md`. Its "The numbers" table is the single source for
  timeouts/caps/floors; this file names constants and never restates their values.
- **Debugging procedure, symptom→suspect table, evidence standards, escalation
  triggers** — `docs/agent/reasoning.md`. Read it before chasing any bug.
- **Known UX debt** — `docs/agent/polish-backlog.md`; pull from it before inventing
  new polish work.
- **Full command list + devmock UI state matrix** — `docs/agent/commands.md`.
- **Design charter** — `frontend/DESIGN.md`; read it before any UI work.

Daily commands (everything else, including gotchas, in `docs/agent/commands.md`):

```
go test ./...                      # full gate (~35 s): unit + e2e + burst floor + doc citations
cd frontend && npm run build       # tsc + vite — the only frontend checker
wails dev                          # real app with hot reload
wails build                        # exe; ALSO regenerates frontend/wailsjs bindings
http://localhost:5173/?mock=agent  # UI without Go (via npm run dev); axes → commands.md
```

## Invariants — these survive any rewrite

Each entry: the rule, why, and the symbol that embodies it today. Numbers live in
`docs/agent/architecture.md` "The numbers" — cited here by constant name only.

### Wire protocol (`internal/control/control.go`)
- Framing is 4-byte big-endian length + JSON envelope `{type, data}`. Length is
  checked **before** allocation — `MaxFrame` post-auth, `PreAuthMaxFrame` pre-auth —
  so internet scanners can never cause a large allocation.
- `ProtocolVersion` is bumped **only** for changes that break the hello exchange
  itself. Everything else rides **capabilities**: agent offers, gateway replies
  offered ∩ supported, both act only on the negotiated set (`CapSet.Has`), unknown
  strings ignored, missing field = legacy peer.
- New wire fields are `omitempty` and zero-value-safe, so frames to/from legacy peers
  stay **byte-identical** — enforced by `internal/control/hello_compat_test.go`.
- Unknown message types are ignored, never errors (the `handleControlMsg` default
  arms in both `agent.go` and `gateway.go`). Replies that can grow are chunked or
  clamped under the frame cap (`MaxConnStatsPerFrame`, `ipc.MaxStatusConns`) —
  never raise the cap itself.
- Never advertise a capability that isn't implemented end-to-end. (Currently violated
  by `CapTunnelUDP` — see Reality check. Don't repeat this.)

### Security (`internal/link/`, `internal/gateway/`)
- TLS 1.3 only, both sides (`cert.go GatewayTLSConfig` / `AgentTLSConfig`). Trust =
  the gateway's self-signed ECDSA P-256 cert pinned by SHA-256 fingerprint carried
  out-of-band in the pairing code `pf1://host:port/<token>#sha256:<hex>`
  (`link/pairing.go`). No CA, ever. Token and fingerprint compare in constant time
  (`crypto/subtle` — `gateway.go handleControlConn`, `cert.go`).
- The pre-auth prologue (accept → TLS → hello) finishes within `preAuthTimeout` or
  dies; failed auth rate-limits per IP, fail2ban-style — successes never count
  (`limits.go authLimiter`). Public conns gate globally and per-IP (`limits.go
  connGate`; defaults in `config.go`).
- Same agentID reconnect **supersedes** (generation counter); a different agentID on
  the same token is **rejected** — no flapping (`actor.go admit`).
- The IPC pipe ACL admits Administrators, SYSTEM, and the interactive user only
  (`ipc/server_windows.go pipeSecurity`).
- Diagnostics bundles redact every secret, host, IP, and identity; peer IPs become
  stable sha256 pseudonyms (`app/tools.go`, leak-tested in `app/tools_test.go`).
  Anything new that exports data must pass the same no-leak test style.
- Fatal auth errors (`bad_token`, `agent_conflict`, `version`) stop the agent instead
  of retry-hammering the gateway (fatal classification in `agent.go Run`), and
  surface in the UI via `EngineFatal` on the tick.

### Liveness & lifecycle
- **One liveness owner**: app-level ping in **both** directions (`pingInterval`) with
  an idle read deadline (`controlIdleTimeout`); yamux keepalive OFF and its write
  timeout deliberately long so the heartbeat, not yamux, declares death
  (`transport/yamux.go muxConfig`).
- Nothing outside `internal/transport` imports yamux. Agent and gateway program
  against `transport.Session` / `Stream` so the mux can be swapped.
- Reconnect: full-jitter exponential backoff, sequence resets after a stable period;
  network-change/resume ticks short-circuit it; DNS re-resolves every attempt
  (`link/backoff.go`, `netnotify/`, `agent.go runSession`).
- **Ghost-listener guarantee**: all session/listener lifecycle runs on the gateway's
  single actor goroutine; eviction closes each listener and waits for its accept loop
  before anything else proceeds — a rebound port is provably free
  (`actor.go evictLocked` / `bindLocked`; regression: e2e `TestAgentRestartRebinds`).
- Exactly one process owns ports and config: every engine serves the named pipe
  `\\.\pipe\proxyforward`; a GUI that finds it attaches as a thin client; pipe
  conflict is fatal by design (`engine.go Run`, `app/app.go Startup`).

### Hot path & performance budgets (`internal/relay/relay.go`)
- The splice (`relay.go Splice`): pooled buffers, zero per-iteration allocations,
  atomic byte counters; EOF on one leg propagates as `CloseWrite` (FIN) while the
  other direction drains — a disconnect message written just before close arrives
  intact (`relay_test.go`, e2e `TestFinalBytesThroughTunnel`). Every write refreshes
  a progress deadline so a parked peer can't leak a goroutine.
- No Nagle anywhere: Go's default `TCP_NODELAY` end-to-end, set explicitly on the
  agent's two dials (`SetNoDelay` in `agent.go runSession` and `handleDataStream`);
  the yamux window is sized so a chunk burst fits in flight.
- **Enforced floor**: `TestBurstThroughputAndCrossStreamLatency` in
  `internal/e2e/e2e_test.go` — throughput and worst cross-stream RTT bounds are in
  the numbers table. Run it before and after any hot-path change (`hot-path` skill).
- No per-byte/per-packet logging or locking anywhere on the data path; the GUI reads
  lock-free snapshots (`conntrack.go`).
- Go→JS is coalesced: one `tick` event per `tickInterval` is the only push; logs and
  all analytics are polled (`app/app.go tickInterval`, `LogsSince`).

### GUI contract (the UX promises)
- Every data surface has all four states designed: geometry-matched skeleton, real
  data, a written empty state, and an *honest* unavailable state (old daemon /
  missing store / GeoIP unconfigured are told apart) — see `Players.tsx`,
  `Analytics.tsx`, `BandwidthChart.tsx emptyHint`.
- Unknown is a sentinel, never a fake zero: gauges use -1, RTT clamps ≥ 1 ms so 0
  stays "no sample" (clamp in `agent.go handleControlMsg`; sentinel merge rules in
  `stats.go`), and the UI renders "—" (`state.ts hasRtt`). Status is never color
  alone (`ui.tsx StatusDot`).
- All motion gates on `prefersReduced()` / `data-motion` (`motion.ts`, kill switch at
  the bottom of `motion.css`); data changes are instant under reduced motion, never
  eased (`NumberTicker`, `charts/util.ts useTweenedValues`).
- Chart series tokens `--dl/--ul/--conn/--rtt` are load-bearing names; direction
  mapping (wire "in/out" → UI "upload/download") happens in exactly one place,
  the `frontend/src/history.ts` header. Never re-map elsewhere.
- The design charter is `frontend/DESIGN.md` — one identity surface per screen, glass
  as a reward, motion communicates network state, color is signal.

### Privacy
- Player/traffic analytics are local-only (SQLite next to the config); the only
  outbound calls are Mojang identity/skins (opt-out: `analytics.mojang_lookups`),
  avatar fallbacks, and the creator avatar (`players/resolver.go`, `app/avatars.go`,
  `app/credits.go`). Never add telemetry that leaves the machine.

## Reality check — implemented vs. advertised

The README and the Settings/Tunnels UI **oversell**. Ground truth at 4a8b0c9:

| Feature (advertised in README/UI) | Actual state |
|---|---|
| Offline MOTD responder | `mc.ServeOffline` built + fuzzed, **never called**; gateway closes dead-session conns (`gateway.go handleClient`). |
| `per-conn` transport | Config-valid only; agent never reads it, gateway rejects `KindData` (`handleControlConn`). |
| UDP tunnels | No UDP socket code; `validateSpec` rejects `type:"udp"` — yet `CapTunnelUDP` is **advertised** (live protocol-bug risk). |
| Bandwidth cap | `BandwidthLimitMbps` stored, never enforced. |
| Prometheus `/metrics` | `MetricsConfig` stored, no server exists. |
| MC status polling (MOTD/players) | Only login sniffing (`mcsniff/`); the health probe is a bare TCP dial (`health.go probeOnce`). |
| Tray / minimize-to-tray / autostart | Hidden `tray_spike.go` command only; `MinimizeToTray` / `Autostart` stored, unused. |
| CI "enforced" (README) | `.github/workflows/ci.yml` now exists but **no run has executed yet** — verify green on first push, then delete this row. |

Rules: don't document these as working; don't build UI atop them; trust code over the
README everywhere. When you implement one, delete its row **in the same commit** (the
tunnel-editor/Settings hints promising them are polish-backlog item #1).

## Enforcement — what is a gate, not advice

Blocks a merge → CI (`.github/workflows/ci.yml`: gofmt, vet, full test suite incl.
burst floor). Must-never-happen at edit time → hook (`.claude/settings.json`:
`frontend/wailsjs` write-block, per-edit gofmt check). Doc accuracy → the
`internal/doccheck` citation test, which asserts every file, symbol, and test name
cited by CLAUDE.md, `docs/agent/`, `.claude/rules/`, and `.claude/skills/` exists.
Needs judgment → this file and `docs/agent/reasoning.md`. Before adding a rule here,
ask which of those four homes it belongs in.

## Maintaining this file

- Hard budget ~200 lines; overflow goes to `docs/agent/*.md`, `.claude/rules/`, or a
  skill, with a routing entry here. Cite symbols, not line numbers — `internal/doccheck`
  verifies existence, symbols survive edits.
- Update triggers: a command changed (run it first, then edit), architecture moved,
  a footgun was fixed (delete its bullet), a stub got implemented (delete its row),
  or the same failure happened twice (add a test or a checklist line — never prose
  history).
- Numbers live only in architecture.md's table; never restate a value here.
- The razor for every instruction: *"which file enforces this tomorrow?"* No file →
  it's wishful thinking; cut it or build the enforcement (hook, CI, or test).
- Forbidden: aspiration stated as fact, advice true of any repo, any claim you can't
  pin to a file, restating what `--help` or go.mod already says.
