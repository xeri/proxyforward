<!-- Companion to /CLAUDE.md. Audit @ 4a8b0c9, 2026-07-13. Owns the architecture map
     (moved from the root file in the same-day restructure) and the deep reference for
     the subsystems the root only names. "The numbers" below is the single source for
     every tuned value — the root cites constants by name and never restates them. -->

# Architecture map & deep-dives

## Repo map

```
main.go                 CLI (cobra): GUI default | agent|gateway headless | pair |
                        service | firewall | elevated-task | tray-spike; crash.log setup
app/                    Wails-bound layer: App methods = the whole JS API; 2 Hz tick;
                        avatars (player-head cache + /pf/avatar/ HTTP); setup export/
                        import (.pfsetup); diagnostics bundle (redacted)
internal/
  engine/               Composition root: role engine + IPC pipe + stats sampler +
                        analytics recorder/resolver; analytics_api.go = op registry
  agent/                Dial→hello→register→serve; health probes; hot-apply; probe.go
  gateway/              TLS listener, pre-auth, admission; actor.go = ONE goroutine
                        owning sessions+listeners; limits.go; per-conn RTT sampler
  relay/                Splice (the hot path) + TapConn (read-only sniff hook)
  transport/            Session/Stream interfaces + tuned yamux & QUIC impls (only yamux/quic-go user)
  control/              Wire protocol: envelope framing, messages, capabilities
  link/                 Pairing codes, self-signed cert + pin, backoff
  ipc/                  Named-pipe JSON-RPC (same framing); status/history/analytics
  conntrack/            Live-connection registry, lock-free counters, hooks
  stats/                RRD tiers (100ms→1d, ~3y) + lifetime + peers; Persister seam
  analytics/            SQLite (modernc, WAL, single writer + read pool): sessions,
                        players, geo, rollups, uptime events, retention
  mc/ mcsniff/          Minecraft protocol: VarInt/handshake/login parsers (fuzzed),
                        push-parser Sniffer, offline responder (unwired); tap glue
  players/ geo/         Mojang identity resolver (rate-limited); GeoLite2 lookups
  linkquality/          Jitter EWMA + loss window from the heartbeat; probe collector
  logging/ netid/ netnotify/ portowner/ proxyproto/ setup/ svc/ tcpinfo/ wincon/
                        (rotating+ring logs, LAN IPs, reconnect triggers, "port in use
                        by java.exe (PID)", PP2 header, .pfsetup crypto, service+
                        firewall+UAC helper, kernel RTT, console attach)
frontend/src/           React 19 + Tailwind v4 + hand-rolled SVG charts, no router,
                        no state lib. state.ts (tick) · history/analytics/players.ts
                        (polled data layers + module caches) · devmock.ts (browser dev)
                        · motion.ts (the gate) + rubberband.ts (scroll rubber band)
                        · styles/ = tokens → base → glass → motion · DESIGN.md charter
frontend/wailsjs/       GENERATED bindings — never edit; regen via wails build/dev
```

**Life of a player byte**: player TCP → gateway public listener (bound by the actor)
→ `connGate.admit` → `mux.OpenStream()` + `open_conn{tunnelID, clientAddr, connID}`
header → agent `handleDataStream` → dial local server (optional PP2 header first) →
`relay.Splice` on both sides (gateway splices player↔stream, agent splices
stream↔local; Minecraft-aware tunnels wrap the client leg in `mcsniff.Tap`).
Counters: per-conn `conntrack.Entry` (atomics) → 2 Hz status + 10 Hz `stats.Sample`
→ SQLite via the 45 s flush; gateway samples kernel RTT per conn every 5 s and ships
it over `conn_stats` so both ends attribute per-player ping.

**Bandwidth cap** (`internal/bwcap`): a tunnel's `BandwidthLimitMbps` (+ scope: combined
| per-direction | per-connection) throttles the splice on **both** sides via
`golang.org/x/time/rate` — one token = one byte, rate = `Mbps × 125_000` (decimal), burst
= `relay.BufSize`, so `WaitN(n)` with n ≤ that never trips the burst. Buckets are keyed
per **`(agentID, tunnelID)`**: the gateway hangs the `bwcap.LimiterSet` on the tunnel's
`publicListener` (two agents' same-named tunnels cap independently), the agent keys a
`bwcap.Registry` by tunnel ID. `mbps ≤ 0` is the byte-identical uncapped fast path (nil
`relay.Limiter`, no per-iteration cost, no per-splice context). A throttled `WaitN`
unblocks on the session ctx, cancelled by gateway `evict` / agent session teardown.

**Go↔JS boundary**: methods on `app.App` (promise-returning bindings) + the 2 Hz
`tick` event; attached mode proxies the same data over the pipe, analytics on its own
pipe conn so slow queries never stall the tick (`app/analytics.go`). The Wails
generator can't model cross-package embedded structs or `time.Time` — use app-local
mirror types and unix-ms ints (`app.go UIStatus`, `logging.Entry`).

**State lives**: config = TOML at `%APPDATA%\proxyforward\config.toml`
(service: `%ProgramData%\proxyforward`), atomic save (`config/`); history/analytics =
`analytics.db` next to it (owner-process only); GUI = `useTick` + polled hooks with
module caches; theme/motion/fx/chart prefs = localStorage (`pf-*` keys), theme also
persisted to config.

## The numbers (single source when writing docs or tests)

| Constant | Value | Where |
|---|---|---|
| Protocol version | 1 | `control.go:19` |
| Frame caps | 64 KiB post-auth / 4 KiB pre-auth | `control.go:88-91` |
| Pre-auth prologue deadline | 10 s | `gateway.go:43` |
| Per-conn dial-back wait | 12 s (> data-conn pre-auth, so its failure surfaces first) | `perconn.go dataDialTimeout` |
| Heartbeat / idle deadline / ctrl write | 5 s / 15 s / 10 s | `agent.go:38-43`, `gateway.go:45-53` |
| yamux window / conn-write timeout | 1 MiB / 30 s | `transport/yamux.go` |
| QUIC keepalive / idle / handshake | off / 30 s / 5 s (dial aborts at 2×) | `transport/quicconfig.go quicConfig` |
| QUIC recv windows / max bidi streams / ALPN | 1→6 MiB stream, 2→12 MiB conn / 65536 / `pf-quic/1` | `transport/quicconfig.go quicConfig` |
| Auto-transport re-probe cooldown | 5 min (cleared on network change) | `agent.go transportReprobeAfter` |
| Splice buffer / write-stall deadline | 128 KiB pooled / 2 min | `relay.go` (`BufSize` / `WriteStallTimeout`) |
| Bandwidth cap unit / burst | `Mbps × 125_000` B/s / `relay.BufSize` | `bwcap.go` |
| Backoff | 1 s → 60 s full jitter, reset after 60 s stable | `link/backoff.go` |
| Abuse defaults | 4096 global / 32 per-IP conns; 10 auth fails/min/IP | `config.go Default` |
| Loss window / ping-loss timeout | 32 heartbeats / 2×interval | `agent.go:50-51`, `linkquality.go` |
| Gateway per-conn RTT sample | every 5 s, ≤200 entries per frame | `gateway.go:59`, `control.go:291` |
| GUI tick | 500 ms; log poll 250 ms; UI re-render tick 1 s | `app.go:42`, `Activity.tsx:35`, `Overview.tsx:33` |
| IPC clamps | 150 conns in status; 300 buckets/peers | `ipc.go:66-73` |
| Query clamps | 80 players / 100 sessions / 300 points / 60 name+IP spans / 250 countries / 16 tunnels × 100 uptime events | `queries.go:14-20`, `geoquery.go:73`, `summary.go:16,294` |
| Stats tiers | 100 ms×1200, 1 s×1800, 15 s×7200, 10 m×5760, 1 d×1100; tiers ≥2 persisted | `stats.go:117-127` |
| Sampler | 10 Hz sample, 45 s flush, 15 s session-replay samples | `engine.go:36-44` |
| Health score | bad: loss>5 % or jitter>100 ms; warn: loss>1 %, jitter>30 ms, or up<1 min | `engine.go:320` |
| DB writer | batches ≤256 ops / 250 ms; queue 4096 drop-oldest (barriers never dropped) | `db.go:22-35,196` |
| Retention | sessions 180 d (config), geo cache 30 d, hourly rollups 90 d, daily forever | `retention.go` |
| Rollup cadence | 5 min + on start + final on close | `rollup.go:22`, `db.go writer` |
| Resolver | bulk 10 names / 2 s coalesce; 1 rps burst 3; +TTL 30 d, −TTL 24 h; profile re-check 24 h | `players/resolver.go:24-50` |
| Avatars | sizes 16–128 (default 64), 8×8 master; Mojang spacing 60 s/player, 1 rps burst 3; miss TTL 15 min; evict 4000 files / 64 MiB / 6 h | `app/avatars.go:37-56` |
| Pipe | 5 s request / 2 min idle timeouts; ACL BA+SY+IU | `ipc/server_windows.go` |
| Cert | ECDSA P-256, 20-year validity (trust = pin, not expiry) | `link/cert.go:86` |
| Key exchange | X25519MLKEM768 (PQ hybrid, Go default; `CurvePreferences` unset) | `link/cert.go`, `link/pq_test.go` |
| Agent identity | Ed25519 (`agent_identity.key`, PKCS#8 PEM, `0600`); `agentID` = `agt_` + 8-char Crockford base32 of sha256(pubkey)[:5] = 40 bits | `link/cred.go LoadOrCreateIdentity fingerprint` |
| Enrollment ticket | `tkt_` + 128-bit nonce; single-use default, reusable optional; TTL caller-set, 0 = never (UI single-use default 10 min) | `link/cred.go NewEnrollTicket`, `gateway/agentstore.go IssueEnrollment` |
| Agent allowlist | `gateway_agents.json` (`0600`, atomic write + AV-retry): identity + scope + desired config per agent | `gateway/agentstore.go AgentStore` |
| Pairing code | `pxf://host:port/v1/pair/<tkt>#sha256:<64hex>`, ≤ 512 B before parse | `link/pairing.go ParsePairingCode` |
| Agent auth | Ed25519 proof-of-possession over `proxyforward-agent-auth-v1` + gateway cert FP — no bearer token in steady state | `link/cred.go AgentAuthMessage` |
| Perf floor | ≥20 MiB/s, worst cross-stream RTT ≤500 ms (64 MiB loopback burst); per-transport twins `TestBurstThroughputPerConn` / `TestBurstThroughputQUIC` | `e2e_test.go:716,719` |
| Blur ladder | control 10, Signal Glass 20, card frost 30, chrome 36, island 40, float 48, pop 56 px | `tokens.css` |
| Switch geometry | 40×22 track, 1px rim + 2px seat → 16px knob (7px radius), 18px travel; ×`--ui-scale` | `tokens.css`, `ui.tsx Switch` |
| Control height | 2.25rem + 2px = 1px rim + 0.5rem padding + 1.25rem line, per side | `tokens.css` |
| Halo clearance | 12px dot→label (the halo ring breathes out to 5px) | `tokens.css`, `motion.css` |
| Hero bleed | 0 → (page-pad − 8px), continuous from 640px of container width | `tokens.css`, `Overview.tsx` |

## Control-plane message flow

Hello (pre-mux, pre-auth caps apply) → `hello_ok{generation, capabilities, hostname,
localIps, observedIp}` | `hello_err{code}` → mux starts (client=agent) → agent opens
the control stream → tunnel registration:
- Capability `tunnel-sync`: one `sync_tunnels{seq, tunnels[]}` desired-state frame;
  gateway's actor reconciles (identical specs keep listeners + live conns:
  `actor.reconcile`), answers `sync_result` (stale seq dropped agent-side).
- Capability `gateway-config` (enrolled agents only — it is keyed to the Ed25519
  identity, so the gateway negotiates it away for a shared-token agent, which then
  falls back to `tunnel-sync`): the gateway is authoritative. It stores each identity's
  desired set in the `AgentStore` (`DesiredConfig`/`AdoptConfig`) with a monotonic
  generation, hashed by `HashTunnels`. The agent reports its `configHash`/
  `configGeneration` in the hello; the gateway reconciles its set onto the fresh
  session's listeners and, on drift, pushes `push_config{generation, hash, tunnels[]}`
  (agent applies + `config_ack`s: `applyPushedConfig`). First contact is bootstrapped by
  the `hello_ok{configSeedNeeded}` flag, which asks the agent for one
  `propose_config{tunnels[]}` seed. A local edit is a `propose_config` the gateway
  adopts, bumps, and re-pushes — deterministic, not last-write-wins; a proposal on a
  stale generation is refused and the authoritative set re-pushed
  (`pushConfigOnConnect`/`adoptProposal`).
- Legacy: per-tunnel `register_tunnel`/`unregister_tunnel` + `register_ok|register_err`.
Steady state: `ping/pong` both directions (RTT/jitter/loss both sides; pong echoes
`recvUnixNano` for one-way estimates); agent pushes `health{tunnelId, localUp}` on
probe transitions; gateway pushes `conn_stats{[{c,r}]}` (cap `conn-stats`) mapping
kernel RTT onto agent conn entries via `ConnKey`. Data streams: gateway →
`OpenStream` + `open_conn{tunnelId, clientAddr, connId}` header, then raw bytes.

Per-conn data plane (cap `per-conn-data`; agent config `transport = "per-conn"`): the
control plane stays on the mux, but instead of `OpenStream` the gateway sends
`open_data{connId}` on the control stream and the agent dials back (`dialBackData`) a
fresh `KindData` TCP+TLS connection carrying that connId. The gateway authenticates it
through the same `Validator` and matches it to the waiting player (`perconn.go`
`pendingConn` — an exactly-once, loser-closes handoff), then writes the same `open_conn`
header and splices. One dedicated connection per player, so a lost segment on one
player's connection cannot head-of-line-block another's (the one defect of yamux-over-one-
TCP). Data conns resume the control conn's TLS session (`dialGateway` shares an LRU
`ClientSessionCache`) and are drained on eviction alongside the mux (`agentSession`
`dataConns`, `closeAll`). The gateway advertises the capability only because it serves
the accept path end-to-end; a mux/legacy agent never offers it and rides `OpenStream`.

QUIC data plane (agent config `transport = "quic"`; gateway `Gateway.QUICEnabled`, on by
default): a separate wire, not a capability. The gateway binds a UDP QUIC listener on the
**same port number** as the TCP control listener (`startQUIC`; TCP/UDP port spaces are
independent, so the pairing code's one host:port serves both) and accepts sessions on it
(`acceptQUIC`/`handleQUICSession`), reusing the same pre-auth guards, `Validator`, and the
shared `buildAndAdmit`/`serveAdmitted` admission as the TCP path. A QUIC session is a
`transport.Session` (`transport/quic.go`) that rides the existing `muxDataPlane` — control
and every player are independent QUIC streams over one connection, so a lost packet on one
stream can't head-of-line-block another (per-conn's benefit, one connection/handshake/NAT
entry). No new control message or capability, and the hello frames are byte-identical.
Liveness stays the app ping (`quicConfig` sets `KeepAlivePeriod=0`, `MaxIdleTimeout` above
the 15 s budget); passive connection migration follows an agent whose IP changes. Eviction
is simpler than per-conn — the session's `Close` (a `*quic.Conn`) tears down every stream,
so `dataConns` stays empty (`closeAll` nil-guards the absent raw conn).

Auto transport (agent config `transport = "auto"`, the shipped default): a connect-time
fallback ladder, best-isolation first — QUIC → per-conn → mux (`transportPreference`).
`runSessionAuto` tries each non-cooled rung; a rung that *connects* is served (Run backs
off and the ladder re-evaluates on reconnect), a rung that fails to *connect* falls through
immediately. A failed rung is cooled (`transportReprobeAfter`, cleared on `netnotify`
change) only once a later rung succeeds — the "UDP blocked" tell; if every rung fails the
link is simply down, so nothing is cooled. The rung that connects is reported to the GUI as
`ActiveTransport` (tick `Status.Transport`) so a user can see what auto settled on.

## Per-agent identity, enrollment & revocation (`internal/link/cred.go`, `internal/gateway/agentstore.go`, `internal/gateway/auth.go`)

The trust root is still the gateway's pinned self-signed cert (`link/cert.go`); layered on
top is a per-agent cryptographic identity so agents are told apart, scoped, and revoked
individually rather than sharing one bearer token.

**Identity.** On first run the agent generates a long-term Ed25519 keypair and persists the
PKCS#8 private key `0600` beside its config (`link/cred.go LoadOrCreateIdentity`); the private
half never leaves the machine, and a corrupt/non-Ed25519 file is a fatal, actionable error,
never a silent regeneration (which would orphan the allowlist entry). The **canonical
identity is the raw public key** — the gateway allowlist is keyed by it. The human-facing
`agentID` is *derived*: `agt_` + an 8-char Crockford-base32 fingerprint (no confusable
`i/l/o/u`) of the first 40 bits of sha256(pubkey) (`link/cred.go AgentID fingerprint`).
Derived, so it is stable (the same machine always re-derives it; re-pairing never dupes) and
unforgeable (bound to a private key nobody else holds). The ID grammar is
`<type>_<fingerprint>`: `gw_` over the cert DER, `agt_` over the pubkey, `tnl_` a slug of the
tunnel name with a `-2` collision suffix (`link/cred.go GatewayID TunnelID`). A freely-editable
nickname layers on top as display sugar.

**Steady-state auth is proof-of-possession, not a bearer token.** In the hello the agent
sends `AgentPubKey` plus an Ed25519 `AgentSig` over `AgentAuthMessage` = the constant
`proxyforward-agent-auth-v1` joined to the *pinned gateway cert fingerprint* (`link/cred.go
AgentAuthMessage SignAgentAuth`). The gateway verifies the signature, checks the pubkey is
allowlisted and not revoked (`gateway/auth.go identityValidator`), and admits — no extra
round-trip, and the agent still speaks first, so hello frames to a legacy gateway stay
byte-identical (all new fields `omitempty`). Binding to the cert fingerprint (rather than the
originally-specced per-session TLS exporter) means a signature made for one gateway can never
be replayed to another; same-gateway replay resistance rests on TLS 1.3 confidentiality —
only the real gateway or the agent itself ever sees the signature and either already holds the
private key — so the signature is static per (agent, gateway) pair and the identical message
works over both TCP and QUIC. The bearer token now survives only as the enrollment ticket.

**Enrollment.** The gateway mints a single-use ticket `tkt_` + 128-bit nonce (`link/cred.go
NewEnrollTicket`) and embeds it in a pairing code. On first contact the agent replays it in
`Hello.EnrollTicket`; the gateway validates-and-consumes it under one lock (a spent single-use
ticket is refused — `ErrTicketConsumed`), records the pubkey, derives and stores the
`agentID`, and returns it in `HelloOK.AssignedAgentID` alongside `GatewayID`. Single-use is
the default; a **reusable** ticket (enrolls many agents until revoked) and an optional expiry
(zero = never) are the flagged alternatives. Enrollment is **field-driven, not a capability**
— acted on before capability negotiation, so there is deliberately no `CapEnroll`.

**AgentStore.** The allowlist and outstanding tickets persist to `gateway_agents.json` (`0600`,
single writer, atomic temp+rename with the AV-retry of `setup.atomicWrite`) — deliberately
*not* in `analytics.db`, which is role-blind history (`gateway/agentstore.go AgentStore
LoadAgentStore`). Each record carries identity, nickname, scope, and the gateway-authoritative
desired tunnel set (see "Control-plane message flow").

**Validators.** A `compositeValidator` tries the identity path first, then — only while
`Gateway.AcceptSharedToken` is on (a migration default) — falls back to the legacy shared token
(`gateway/auth.go compositeValidator sharedTokenValidator`; token and fingerprint still compare
in constant time). Every accept path (TCP control, QUIC, per-conn data) funnels through the one
`Validator.Validate` seam, so identity is enforced uniformly.

**Revocation.** `Gateway.RevokeAgent` removes the pubkey from the allowlist and evicts any live
session at once; the next connect is a fatal `ErrCodeRevoked`, which the agent classifies as
fatal (`agent.go isFatal`) and stops on rather than retry-hammering — surfaced in the GUI via
`EngineFatal`. Regression: e2e `TestEnrollAndRevoke`.

**Scope.** A ticket/record carries `Scope{Ports, TunnelIDs}` (empty = unrestricted), enforced
at bind in `validateSpec` — an out-of-scope port is `ErrCodePortNotAllowed`, an out-of-scope
tunnel `ErrCodeBadTunnel` — on *both* the register path and the gateway-config push/adopt path,
so a scoped agent can never bind outside its grant by any route (`gateway/agentstore.go Scope`;
`gateway/gateway.go validSpecs`). Regressions: `TestGatewayConfigScopeNarrowingHidesTunnel`,
`gateway/scope_test.go`.

**Pairing scheme & click-to-pair.** The pairing code is `pxf://host:port/v1/pair/<tkt>#sha256:<hex>`
(`link/pairing.go PairingCode`). `pxf` is a permanent brand *and* the OS deep-link scheme
(`wails.json` registers it via the NSIS macros); the `/v1/` segment versions the code's *shape*
independently of the wire `ProtocolVersion`; the `/pair/` segment is a role/kind marker so a
wrong-kind link fails loudly instead of half-parsing (`link/pairing.go tokenFromV1Path`). The
parser caps input at 512 bytes before parsing and validates host, port, and the 64-hex
fingerprint; it is fuzzed (`link/pairing_fuzz_test.go FuzzParsePairingCode`). A clicked `pxf://`
link reaches the app as `os.Args` on a cold launch or via single-instance forwarding on a warm
one (`main.go deepLinkArg`, `app/app.go HandleDeepLink TakePendingDeepLink`); the frontend drains
it on mount and opens the wizard straight onto the agent paste step with the code prefilled —
confirm-to-connect, never auto-connect (`frontend/src/App.tsx`, `frontend/src/screens/Wizard.tsx`).

**Version axes** (independent, so any one evolves alone):

| Axis | Where | Bumps when |
|---|---|---|
| Scheme | `pxf` | Never (permanent brand) |
| Pairing format | the `/v1/` path segment | The code's shape changes |
| Protocol | `Hello.ProtocolVersion` (stays 1) | The hello exchange itself breaks |
| Config authority | `Hello.ConfigGeneration` (per-agent, monotonic) | The gateway adopts a new desired set |
| Crypto suite | TLS-negotiated (`X25519MLKEM768` today) | An algorithm is added/retired |

**Deferred (honest state).** Per-agent **key rotation** ("old key signs new") and PQ
*signatures* are roadmap, not shipped (the KEM is already PQ; forging Ed25519 needs a quantum
computer at attack time, so there is no harvest-now risk). There is no on-disk `config_version`
schema field yet — there are no config migrations and the local config dir is disposable. The
`pf1://` scheme was dropped outright rather than kept as a compat parser (pre-release, no codes
in the wild). Several agent-management surfaces are backend-complete but not yet fully exposed in
the GUI — tracked in `docs/agent/polish-backlog.md`.

## Analytics data model (`internal/analytics/schema.go`)

- `rrd(tier,t,…)` — persisted image of stats tiers 2–4 (28 OHLC/gauge columns; −1 =
  unknown). Written incrementally on flush via dirty watermarks (`statspersist.go`).
- `sessions` — one row per proxied connection: conn_key, tunnel, ip:port, times,
  bytes, sniffed player name/protocol, resolved `player_uuid` (backfilled ≤6 h,
  `identity.go backfillWindow`), geo (cc/asn/as_org), running rtt_avg/min/max/n.
- `session_traffic` / `session_rtt` — 15 s deltas and per-minute RTT aggregates for
  the replay timeline; orphans swept by the daily prune.
- `players`, `player_names`, `player_ips` — identity + locally-observed history
  (Mojang killed the name-history API in 2022; renames come from 24 h profile
  re-checks of players actually seen). Offline players key as `offline:<name>` and
  reconcile onto the real UUID if it later resolves (`reconcileOffline`, which also
  re-rolls affected rollup buckets).
- `uuid_cache` / `geo_cache` — resolution caches ('' uuid = confirmed miss).
- `rollup_hourly` / `rollup_daily` / `peaks` — dashboard aggregates; idempotent
  INSERT-OR-REPLACE rollups every 5 min from rrd tier 2 + sessions (`rollup.go`);
  lapped hours are never re-rolled (would zero them).
- `events(kind: link|tunnel_local|engine)` — uptime transition journal; `engine`
  rows bracket runs so off-time counts as *unknown*, not down (`summary.go
  engineCoverage`); prune parks a synthetic carrier row at the cutoff.
- Migrations: append-only ladder in `migrations` (`schemaV1`, `schemaV2`, …),
  `PRAGMA user_version` tracked, each step transactional. Never edit an applied step.
- Ownership: exactly one process opens the DB (WAL safety on Windows); one writer
  connection + a query_only read pool of 4; unreadable DB → renamed `.bad` +
  recreated — analytics must never block engine start (`db.go Open`).

## Stats store (`internal/stats/`)

Five ring tiers; a completed bucket cascades into the coarser tier (`add`). Rates are
OHLC of bytes/sec; conn/RTT/players/loss are gauge OHLC with −1 = unknown, and
`mergeGauge` skips unknown sides so pre-upgrade data never poisons merges. Sampling
is delta-of-monotonic-totals with re-baseline on shrink (restart-safe). History
queries pick the finest tier covering the window, then group to ≤300 buckets.
Persistence is the `Persister` seam — SQLite in production (`analytics/statspersist.go`),
fakes in tests; legacy `stats.json` (v1–v3 packed arrays) imports once then renames
to `.imported` (`importjson.go`).

## Avatar pipeline (`app/avatars.go`)

`/pf/avatar/<id>.png?size=N` on the Wails asset server (id regex = path-traversal
guard; sizes clamp 16–128). One 8×8 *master* face per player id — Mojang profile →
skin → compose face+hat locally, else mc-heads.net, else crafatar, else procedural
Steve/Alex placeholder — every size is a nearest-neighbor upscale, cached on disk.
Requests never block on network: cold ids answer an instant `no-cache` placeholder
while a bounded background warm builds the master (singleflight per id); failures
write a 15-min `.miss` marker and are never cached as real heads. Frontend
counterpart: `AvatarImg` re-asks on a short backoff until the long-lived render
lands (`components/AvatarImg.tsx`). In browser dev the asset server doesn't exist —
`avatarUrl` falls back to mc-heads / inline SVG (`avatars.ts`).

## Frontend data layers

| Layer | Source | Cadence | Cache |
|---|---|---|---|
| `state.ts useTick` | `tick` event + initial `Status()` | 2 Hz push | none (live) |
| `history.ts` | `BandwidthHistory(windowMs, buckets)` | 250 ms–60 s per range (`RANGES`) | module map per range |
| `players.ts` | Players/PlayerDetail/History/Latency | 5–15 s | module maps |
| `analytics.ts` | Summary/PeakMatrix/Uptime/Sessions/Timeline/Geo | 15–60 s | module maps |
| `Activity.tsx` | `LogsSince(seq)` | 4 Hz | ring-capped 2000 |

Shared plumbing: `usePolled` (null key pauses; cache gives instant paint),
`useDebounced` for search. Cross-screen handoff without a router: module-level
mailbox (`players.ts openDossierOnMount`). Title-bar context: `pagecontext.ts`
(`useSyncExternalStore` module store). Chart vocabulary: `charts/util.ts` (binary
vs 1/2/5 axis scales, measured width, time ticks, 220 ms timestamp-keyed tween that
snaps under reduced motion). The world map is pre-baked Natural Earth 110m
equirectangular paths (`worldgeo.ts`, generated — regenerate, don't edit).

## devmock axes (the UI test matrix, `frontend/src/devmock.ts`)

`?mock=agent|gateway|wizard` plus composable: `&link=down`, `&mode=attached`
(gated bindings reject like the real backend), `&fatal=1`, `&fresh=1`,
`&analytics=off`, `&paired=0` (never paired to a gateway — the sidebar's role
switcher cannot become the agent and must route to setup), `&geo=off|empty|error|pending`,
`&fx=low|high`, `&fleet=multi|old` (gateway only — `multi`: a five-agent fleet with a
good/fair/poor health spread instead of the default single agent, for the Agents roster
+ drill-in; `old`: a pre-roster daemon that sends no agents array → the roster's
honest-unavailable state). The traffic model is deterministic functions of absolute time, so
chart/tiles/replay all agree at any poll cadence. When you add a binding, add its stub
here or the mock throws. Role setup mutates the mock's role, so the switcher flips the
whole app live in the browser with no Go running.

## Windows integration corners

- Service: kardianos/service, `service run` reads `%ProgramData%` config
  (seeded from the user's on install), OnFailure=restart, fatal engine errors exit(1)
  so the SCM restarts (`svc/service.go`).
- Elevation: `ShellExecuteExW "runas"` relaunches `proxyforward elevated-task <one
  task>`; the main process never elevates (`svc/elevate_windows.go`).
- Firewall: program-scoped rule (port changes need no new prompt), delete-then-add
  idempotence, exit-code detection (`svc/firewall_windows.go`).
- Reconnect triggers: `NotifyAddrChange` loop (heap-pinned OVERLAPPED) + wall-clock
  jump resume detector (`netnotify/`).
- Kernel RTT: `SIO_TCP_INFO` v0 (Win10 1703+), best-effort (`tcpinfo/`).
- Port conflicts: `GetExtendedTcpTable` → "in use by java.exe (PID 1234)"
  (`portowner/`).
- Console attach for windowsgui builds: `wincon.AttachParent()` at the top of every
  CLI subcommand — new subcommands must call it too.
