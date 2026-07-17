<!-- Generated 2026-07-13 by Claude (Fable 5) from a full-repo audit;
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
- **Why each security invariant exists, and the bug that proved it** —
  `docs/agent/security-invariants.md`. The Invariants section below states the rules
  and their symbols; that file is the reasoning behind them.
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
- Never advertise a capability that isn't implemented end-to-end — the peer acts on
  the offer, then fails. (`tunnel-udp` violated this until it was un-advertised.)
- **Consent vs arbitration** — every config path picks a side (`CapConfigConsent`).
  *Authored edits* (either party editing tunnel definitions) ride
  `config_proposal`→`config_decision`, applying only on approval (`gateway/consent.go`,
  `agent/consent.go`). *Arbitration* (connect-sync, drift, reclaim, auto-reassign)
  stays immediate on `push_config` with a notice, never a veto. `push_config` is the
  single commit point for both — so a rejected edit has nothing to roll back — and an
  edit the gateway cannot see (`LocalAddr`, PP2) applies unproposed. Mechanics in
  `docs/agent/architecture.md`.

### Security (`internal/link/`, `internal/gateway/`)

The rule and its symbol are here; the **why**, and the bug that proved each one, is
`docs/agent/security-invariants.md`. Read it before changing what a rule is about —
all of these looked reasonable right up until they didn't.

- TLS 1.3 only, both sides; trust = the gateway's self-signed cert pinned by SHA-256
  fingerprint in the pairing code, out-of-band. **No CA, ever.** Token and
  fingerprint compare in constant time (`link/pairing.go`, `cert.go`, `crypto/subtle`).
- **Per-agent identity is the trust model**: an Ed25519 key the gateway allowlists,
  its derived `agentID` scoped and revocable. Revocation *and* a scope change evict
  the live session — a session captures its scope at admission (`gateway.go
  SetAgentScope`). Same agentID supersedes, a distinct one joins alongside (`actor.go
  admit`). `agentID` width is a security parameter (`cred.go agentFingerprintBytes`).
  The shared token is a migration fallback only (`Gateway.AcceptSharedToken`); the
  `agt_` namespace is reserved against it (`link/cred.go IsDerivedAgentID`).
- **The unit of abuse accounting is the network, not the address** (`limits.go
  sourceNet`): IPv4 address, IPv6 **/64**. Any new per-source limit keys on it or it
  is decorative. Callers pass the raw IP — `remoteIP`'s other callers need it whole.
- **A limiter's own table is attack surface**: bounded by construction, O(1) to
  update — a token bucket per network in a hard-capped LRU (`limits.go authFailNets`),
  never grow-then-scan. Spend whole tokens; floor them at 0.
- **The unauthenticated set is bounded by a gate, not by the rate limiter** (`limits.go
  preAuthGate`) — the limiter can't refuse what hasn't failed yet. Release the slot the
  instant auth succeeds, exactly once (`sync.OnceFunc`).
- **The pre-auth prologue's deadline is on the prologue, not on its reads** — every
  branch writes a reply to a peer who may never read it (`gateway.go
  handleControlConn`/`handleQUICSession`, `preAuthTimeout`).
- **An accept loop stops only when its listener is closed** (`limits.go
  isListenerClosed`, `gateway.go retryAccept`) — every other Accept error is transient,
  and returning on one is a permanent outage wearing a healthy face. Capped backoff,
  never a bare `continue`.
- **Port 0 is not a port, it is "any port in the ephemeral range"** — policy-valid only
  when nothing constrains the choice, so it is enforced where the choice is made
  (`bindLocked` via `conflict.go allowedPorts`). Any new port-granting path uses
  `allowedPorts` or it is not constrained.
- **An authenticated agent is bounded in every dimension it can grow**: conns
  (`connGate`), unauthenticated sockets (`preAuthGate`), bandwidth (`bwcap`), and
  listeners — the one it allocates by asking (`MaxTunnelsPerAgent`, `actor.go
  bindLocked`). Count growth, never rebinding.
- **The gateway never decides what the agent exposes**: a pushed spec must not enable a
  disabled tunnel (`agent/hotapply.go mergeTunnels`) and a data stream must not dial one
  (`agent/agent.go enabledTunnelByID`). `push_config` auto-applies, so both are load-bearing.
- **The IPC pipe is no easier to reach than the config dir it fronts**: SYSTEM,
  Administrators, and the account serving it — never a *group* like `IU`
  (`ipc/security_windows.go pipeSecurity`). It carries `OpIssuePairing`. Service mode
  records its own SID (`ipc/owner.go PipeOwnerFile`); an ACL that can't be built is not
  served.
- **"Something answered the pipe" is not evidence an engine did** — `ipc.Dial` verifies
  the pipe's **owner** (`ipc/security_windows.go verifyServer`), the one part a squatter
  can't fake.
- **A bundle is made to be handed to strangers**: every secret, host, IP, and identity
  redacted; IPs and hostnames pseudonymised under a per-bundle salt (`app/tools.go
  pseudonymiser`). The salt is the property, not the hash. Anything new that exports
  data seeds *every* channel in its leak test — one left empty passes vacuously.
- **Never log a secret**; the scrubber (`tools.go logScrubber`) is a net, not a licence —
  it only knows what config holds. The pairing code goes to the console, never `slog`
  (`gateway.go RunStarted`).
- Fatal auth errors (`bad_token`, `agent_conflict`, `version`, `revoked`) stop the agent
  instead of retry-hammering the gateway (`agent.go isFatal`); the UI shows `EngineFatal`.

### Liveness & lifecycle
- **One liveness owner**: app-level ping in **both** directions (`pingInterval`) with
  an idle read deadline (`controlIdleTimeout`); the transport's own keepalive is OFF
  and its write/idle timeout deliberately long so the heartbeat, not the transport,
  declares death (yamux keepalive off + long write timeout in `transport/yamux.go
  muxConfig`; QUIC `KeepAlivePeriod=0` + a `MaxIdleTimeout` above the liveness budget
  in `transport/quicconfig.go quicConfig`).
- Nothing outside `internal/transport` imports yamux or quic-go. Agent and gateway
  program against `transport.Session` / `Stream` so the transport can be swapped
  (yamux-over-TCP, per-conn multi-TCP, or QUIC); the gateway chooses the data
  plane once per session behind `dataPlane.openFlow` (QUIC rides the shared-session
  mux plane), keeping `handleClient` transport-agnostic (`dataplane.go pickDataPlane`).
- Reconnect: full-jitter exponential backoff, sequence resets after a stable period;
  network-change/resume ticks short-circuit it; DNS re-resolves every attempt
  (`link/backoff.go`, `netnotify/`, `agent.go runSession`).
- **Ghost-listener guarantee**: all session/listener lifecycle runs on the gateway's
  single actor goroutine; per-agent eviction closes that agent's listeners and waits
  each accept loop before anything else proceeds — a rebound port is provably free, and
  evicting one agent leaves the others' listeners and connections untouched (the
  per-agent mux is the connection-drain boundary). (`actor.go evict` / `bindLocked`;
  regressions: e2e `TestAgentRestartRebinds`, `TestEvictionIsolatesAndDrains`.)
- Exactly one process owns ports and config: every engine serves the named pipe
  `\\.\pipe\proxyforward`; a GUI that finds it attaches as a thin client; pipe
  conflict is fatal by design (`engine.go Run`, `app/app.go Startup`).

### Hot path & performance budgets (`internal/relay/relay.go`)
- The splice (`relay.go Splice`): pooled buffers, zero per-iteration allocations,
  atomic byte counters; EOF on one leg propagates as `CloseWrite` (FIN) while the
  other direction drains — a disconnect message written just before close arrives
  intact (`relay_test.go`, e2e `TestFinalBytesThroughTunnel`). Every write refreshes
  a progress deadline so a parked peer can't leak a goroutine.
- **Idleness is a property of the splice, never of a direction.** A splice moving no
  bytes *either* way for `DefaultIdleTimeout` is reaped (`ErrIdle`); both halves share
  one progress clock, because a client downloading says nothing for the whole transfer
  and a per-direction read deadline would cut every large download
  (`TestSpliceIdleReaperSparesAOneWayTransfer`). A peer that connects and never speaks
  is not free: the agent dials the local server on `open_conn`, before any client byte,
  so it costs a Minecraft slot too (`TestSpliceReapsAConnectionThatNeverSpeaks`). The
  deadline is armed once and re-armed only when it fires, so the read loop stays clean.
  Read deadlines are recognised via `net.Error.Timeout()`, never a concrete type — every
  transport spells it differently and misreading one kills healthy connections hourly.
- No Nagle anywhere: Go's default `TCP_NODELAY` end-to-end, set explicitly on the
  agent's dials (`SetNoDelay` in `agent.go dialGateway` — control conn and every
  per-conn data conn — and `handleDataStream` for the local dial); the yamux
  window is sized so a chunk burst fits in flight.
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
  eased (`NumberTicker`, `charts/util.ts useTweenedValues`). That kill switch only
  zeroes CSS durations, so *scripted* motion must gate itself in JS — the scroll
  rubber band attaches no listener at all under it (`rubberband.ts useRubberBand`).
- Chart series tokens `--dl/--ul/--conn/--rtt` are load-bearing names; the direction
  convention (wire "out" = server→player = UI "download") is defined once, in the
  `frontend/src/history.ts` header, and every consumer applies it as written. The
  sole re-map is the role-relative link-counter flip, confined to `Traffic.tsx`.
  Never invent a third mapping.
- The design charter is `frontend/DESIGN.md` — one identity surface per screen; every
  surface is glass but only Signal Glass *answers the pointer* (never give a card the
  caustics/streak/wake); motion communicates network state; color is signal.

### Privacy
- Player/traffic analytics are local-only (SQLite next to the config); the only
  outbound calls are Mojang identity/skins (opt-out: `analytics.mojang_lookups`),
  avatar fallbacks, and the creator avatar (`players/resolver.go`, `app/avatars.go`,
  `app/credits.go`). Never add telemetry that leaves the machine.

## Reality check — implemented vs. advertised

The README and the Settings UI **oversell**. Ground truth re-verified by the
2026-07-17 integrity audit:

| Feature (advertised in README/UI) | Actual state |
|---|---|
| UDP tunnels | Not implemented: no UDP socket code, `validateSpec` rejects `type:"udp"`. No longer advertised (the `tunnel-udp` capability was removed); config still accepts `type:"udp"` but the gateway rejects it — a latent gap, not an oversell. |
| MC status polling (MOTD/players) | Only login sniffing (`mcsniff/`); the health probe is a bare TCP dial (`health.go probeOnce`). |
| Tray / minimize-to-tray / autostart | Hidden `tray_spike.go` command only; `MinimizeToTray` / `Autostart` stored, unused. |
| Linux / macOS binaries | CI **builds** them (`.github/workflows/ci.yml`) so the `*_other.go` stubs can't rot, but they **cannot run**: `ipc.Serve` returns `ErrUnsupported` off Windows and every engine must serve the pipe (`engine.go Run`), so the window opens and the engine dies. Unpublished artifacts, never release assets. Fixing this means a real unix-socket IPC port. |

Rules: don't document these as working; don't build UI atop them; trust code over the
README everywhere. When you implement one, delete its row **in the same commit** (the
Settings toggles promising them are polish-backlog item #1).

## Working practice — commit as you go

Nothing enforces this; it is judgment, and ignoring it has already cost a session.

- **Commit each concern the moment it stands alone and `go test ./...` is green.** The
  tree is not a staging area. Batching to the end of a session is how two workstreams
  end up interleaved in one file, and untangling them costs far more than the commits
  would have. Message style and scoping: `.claude/rules/go.md`.
- **Never let two concerns share a dirty tree.** If a second one appears mid-edit
  (you came to add a feature and found a bug), commit or stash the first before
  starting it — deciding later which hunk was whose means reading every line back.
- **Anything independently shippable branches off `master`**, so it never waits behind
  an unfinished feature; a security fix especially. Stack only on a real dependency,
  and check with `git merge-base --is-ancestor` rather than assuming one exists.
- **Label knowingly-incomplete work `wip:` and say what is missing.** An unlabelled
  stub reads as a finished decision to the next session.

## Enforcement — what is a gate, not advice

Blocks a merge → CI. `.github/workflows/ci.yml`: gofmt, vet, `go test -short` (unit +
e2e + goleak + doccheck), `-race` (CI is the **only** place it runs — it needs cgo),
the burst floor (own job, best-of-3 — never lower the floor to go green),
`golangci-lint` (`.golangci.yml`), a TODO/FIXME ban, `actionlint`, and a
stale-`frontend/wailsjs` check. `.github/workflows/security.yml`: CodeQL, govulncheck,
gitleaks, dependency-review, npm audit (gosec/zizmor/Scorecard are advisory → Security
tab). `.github/workflows/fuzz.yml` fuzzes the parsers nightly;
`.github/workflows/release.yml` builds a tag into a **draft** release.
Must-never-happen at edit time → hook (`.claude/settings.json`: `frontend/wailsjs`
write-block, per-edit gofmt check, and a `git commit` whose message embeds a claude.ai
session URL is denied — attribution.sessionUrl is off besides, so the harness never
appends the trailer). Doc accuracy → the `internal/doccheck` citation
test, which asserts every file, symbol, and test name cited by CLAUDE.md,
`docs/agent/`, `.claude/rules/`, and `.claude/skills/` exists. Needs judgment → this
file and `docs/agent/reasoning.md`. Before adding a rule here, ask which of those four
homes it belongs in.

Two CI facts that are load-bearing and non-obvious: `.gitattributes` pins `eol=lf`
(Windows runners check out CRLF, which makes gofmt reject **every** file), and every Go
job must materialize `frontend/dist` first — `main.go` embeds it, and a `go:embed`
matching zero files is a compile error.

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
