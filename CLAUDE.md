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
- TLS 1.3 only, both sides (`cert.go GatewayTLSConfig` / `AgentTLSConfig`). Trust =
  the gateway's self-signed ECDSA P-256 cert pinned by SHA-256 fingerprint carried
  out-of-band in the pairing code `pxf://host:port/v1/pair/<token>#sha256:<hex>`
  (`link/pairing.go`; `pxf` doubles as the OS deep-link scheme, `/v1/pair/` is a
  format-version + role marker so a wrong-kind link fails loudly). No CA, ever. Token and fingerprint compare in constant time
  (`crypto/subtle` — `gateway.go handleControlConn`, `cert.go`).
- The pre-auth prologue (accept → TLS → hello) finishes within `preAuthTimeout` or
  dies; failed auth rate-limits fail2ban-style — successes never count
  (`limits.go authLimiter`). Public conns gate globally and per-source (`limits.go
  connGate`; defaults in `config.go`).
- **The unit of abuse accounting is the network, not the address** (`limits.go
  sourceNet`): one IPv4 address, but one IPv6 **/64** — the smallest block a
  subscriber is handed, so per-address limits are 2^64 wide and free to evade. Both
  limiters reduce the address themselves; callers pass the raw IP, because
  `remoteIP`'s other callers (GUI link card, cloned-identity warning) need it whole.
  Any new per-source limit keys on `sourceNet` or it is decorative.
- **An accept loop stops only when its listener is closed** (`limits.go
  isListenerClosed`, used by `gateway.go retryAccept`). Every other Accept error is
  transient — descriptors, buffers, a reset between SYN and accept — and returning
  on one is a *permanent* outage wearing a healthy face: process up, GUI green,
  players still served, no agent able to reconnect ever again
  (`TestControlAcceptSurvivesATransientError`). Retry with capped backoff, never a
  bare `continue` (a broken listener would spin).
- **The unauthenticated set must be bounded by a gate, not by the rate limiter**
  (`limits.go preAuthGate`). `authLimiter.allow` only refuses a network once its
  failures have *landed*, and a stalled conn doesn't fail until `preAuthTimeout` —
  so it leaves a ~10 s window of unbounded goroutines/descriptors, which is what
  makes Accept fail in the first place. The slot is released the instant auth
  succeeds, or a handshake cap silently becomes a session cap; the release is
  `sync.OnceFunc` because it runs on every failure path *and* on success, and a
  double-release raises the ceiling forever (`TestPreAuthGateBoundsUnauthenticatedConns`).
- **A limiter's own table is attack surface**: it must be bounded by construction
  and O(1) to update, because `allow` runs before auth on every connection and holds
  a mutex every honest agent needs. Hence a token bucket per network (one scalar,
  refilled on read) in an LRU with a hard cap (`authFailNets`) — never "grow, then
  scan to clean up", which under a flood scans more the worse things get
  (`TestAuthLimiterIsBoundedUnderSourceRotation`). Bans stay bounded too: tokens
  floor at 0, so a burst can't ban a shared address for longer than the window
  (`TestAuthLimiterBanIsBoundedByTheWindow`).
- **Per-agent identity is the trust model.** Each agent proves possession of a long-term
  Ed25519 key the gateway allowlists; its derived `agentID` (`agt_…`) is unforgeable,
  individually **scoped** (ports/tunnels) and **revocable** — revocation *and* a scope
  change both evict the live session, and the next connect is fatal `ErrCodeRevoked`. A
  session captures its scope at admission, so without that eviction narrowing contains
  nothing (`gateway.go SetAgentScope`). Agents join via a single-use enrollment ticket in
  the pairing code (`gateway/auth.go identityValidator`, `agentstore.go`, `link/cred.go`;
  mechanics in `docs/agent/architecture.md`). A matching agentID **supersedes**
  (reconnect), a distinct one is admitted **alongside**; supersede is anti-flap dampened
  so an ID collision degrades to a slow contest, not a loop (`actor.go admit`,
  `noteSupersede`).
- **Port 0 is not a port, it is "any port in the ephemeral range"** — so it is
  policy-*valid* only when nothing constrains the choice. A port-0 request names
  nothing for `validateSpec` to check; it asks the gateway to choose, so policy is
  enforced where the choice is made, by `bindLocked` picking only from
  `conflict.go allowedPorts` (scope narrows, allowlist filters; empty means this
  agent may have no port at all). Waiving the check for 0 instead — which is what
  it did — let a scoped agent bind a public listener on a port nobody allowed, once
  per invented tunnel id (`TestEphemeralRequestStaysInsideScope`). Any new
  port-granting path uses `allowedPorts` or it is not constrained.
- "Unforgeable" above is a claim about **width**, and therefore a security parameter, not
  a display choice: `agentID` is the label the gateway keys supersede, scope, revocation,
  and config on, so it must stay wide enough that no second key answering to a victim's
  name can be searched for (`cred.go agentFingerprintBytes`, floored by
  `TestAgentIDIsWideEnoughToBeUnforgeable`); `AgentStore.Enroll` refuses a colliding join
  besides, so a found collision fails closed.
- The legacy **shared token** survives only as a migration fallback, accepted while
  `Gateway.AcceptSharedToken` is on (default for now). A shared-token agent self-asserts its
  `agentID`, so the old residual risk — supersede or port-squat, recoverable only by rotating
  the token — persists **for that path only**; enrolled agents are protected by their key and
  by revocation *because* the key-derived `agt_` namespace is **reserved**, so a self-asserted
  ID can never name an enrolled one (`link/cred.go IsDerivedAgentID`, `gateway/auth.go
  sharedTokenValidator`). Disabling `AcceptSharedToken` once every agent is enrolled closes it.
- **The gateway never decides what the agent exposes.** A disabled tunnel keeps a
  `LocalAddr` naming a real service and ids are guessable slugs (`link.TunnelID`), so
  a pushed spec must not enable one (`agent/hotapply.go mergeTunnels`) and a data
  stream must not dial one (`agent/agent.go enabledTunnelByID`) — `push_config`
  auto-applies without consent, so both halves are load-bearing
  (`TestMergeTunnelsKeepsDisabledDraftsDisabled`, `TestEnabledTunnelByIDIgnoresDisabledTunnels`).
- **The IPC pipe is no easier to reach than the config dir it fronts.** Same three
  principals as `secureio`, deliberately: SYSTEM, Administrators, and the account
  serving it (`ipc/security_windows.go pipeSecurity`) — never a *group* like `IU`
  (INTERACTIVE = every logged-on account, which is what it used to grant under a
  comment claiming otherwise). The pipe is not a status feed: `OpIssuePairing`,
  `OpRevokeAgent` and `OpSetAgentScope` ride the same envelope, and a pairing code
  carries the gateway token — so a wider ACL is a hole straight through
  `secureio` (`TestPipeDeniesOtherInteractiveUsers`). Service mode runs as
  LocalSystem, so the installer records its own SID for the GUI
  (`ipc/owner.go PipeOwnerFile`); an unelevated admin can't be let in via `BA`
  because UAC filters that SID to deny-only. Any SID formatted into the SDDL is
  parsed first, and a pipe whose ACL can't be built is not served.
- **"Something answered the pipe" is not evidence an engine did.** Any local process
  can create a pipe under a free name, so `ipc.Dial` verifies the pipe's **owner** is
  SYSTEM, Administrators, or this account before returning it
  (`ipc/security_windows.go verifyServer`, `ErrUntrustedServer`); a squatter that
  wins the race would otherwise *be* the engine to the GUI — feeding it status and
  its own pairing code. The owner is the check because it's the part a squatter
  can't fake (claiming a SID you don't hold needs `SeRestorePrivilege`).
- Diagnostics bundles redact every secret, host, IP, and identity; peer IPs become
  stable sha256 pseudonyms (`app/tools.go`, leak-tested in `app/tools_test.go`).
  Anything new that exports data must pass the same no-leak test style — seeding
  *every* channel it ships, since a leak test that leaves one empty passes vacuously.
  Bundles **ship logs**, so redacting only the config is theatre: log text is scrubbed
  of the config's own secrets (`tools.go logScrubber`).
- Never log a secret; the scrubber is a net, not a licence (it only knows what config
  holds). The pairing code embeds the gateway token, so it goes to the console, never
  through `slog` — slog fans out to the rotating file, the GUI ring, and every bundle
  (`gateway.go RunStarted`).
- Fatal auth errors (`bad_token`, `agent_conflict`, `version`, `revoked`) stop the agent
  instead of retry-hammering the gateway (fatal classification in `agent.go isFatal`), and
  surface in the UI via `EngineFatal` on the tick.

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
