<!-- Companion to /CLAUDE.md. Full-repo audit, 2026-07-13. Owns the architecture map
     (moved from the root file in the same-day restructure) and the deep reference for
     the subsystems the root only names. "The numbers" below is the single source for
     every tuned value — the root cites constants by name and never restates them. -->

# Architecture map & deep-dives

## Repo map

```
main.go                 CLI (cobra): GUI default | agent|gateway headless | pair |
                        service | firewall | elevated-task; crash.log setup
app/                    Wails-bound layer: App methods = the whole JS API; 2 Hz tick;
                        system tray + the close fork (tray.go, close.go); avatars
                        (player-head cache + /pf/avatar/ HTTP); setup export/
                        import (.pfsetup); diagnostics bundle (redacted)
internal/
  engine/               Composition root, two nested lifetimes: Run = supervisor
                        (IPC pipe + stats sampler + analytics + metrics, lives as
                        long as the process is configured); StartRun/StopRun = the
                        tunnel run inside it (stop leaves the pipe serving).
                        analytics_api.go = op registry; lifecycle_api.go = start/stop ops
  agent/                Dial→hello→register→serve; health probes; hot-apply; probe.go
  gateway/              TLS listener, pre-auth, admission; actor.go = ONE goroutine
                        owning sessions+listeners; limits.go; per-conn RTT sampler;
                        udp.go = UDP port presented as a net.Listener
  relay/                Splice (the hot path) + TapConn (read-only sniff hook) +
                        datagram framing/legs for udp tunnels (datagram.go)
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
| Protocol version | 1 | `control.go ProtocolVersion` |
| Frame caps | 64 KiB post-auth / 4 KiB pre-auth | `control.go MaxFrame PreAuthMaxFrame` |
| Pre-auth prologue deadline | 10 s | `gateway.go preAuthTimeout` |
| Per-conn dial-back wait | 12 s (> data-conn pre-auth, so its failure surfaces first) | `perconn.go dataDialTimeout` |
| Heartbeat / idle deadline / ctrl write | 5 s / 15 s / 10 s | `agent.go pingInterval controlIdleTimeout controlWriteTimeout`, `gateway.go pingInterval` |
| yamux window / conn-write timeout | 1 MiB / 30 s | `transport/yamux.go` |
| QUIC keepalive / idle / handshake | off / 30 s / 5 s (dial aborts at 2×) | `transport/quicconfig.go quicConfig` |
| QUIC recv windows / max bidi streams / ALPN | 1→6 MiB stream, 2→12 MiB conn / 65536 / `pf-quic/1` | `transport/quicconfig.go quicConfig` |
| Auto-transport re-probe cooldown | 5 min (cleared on network change) | `agent.go transportReprobeAfter` |
| Splice buffer / write-stall deadline | 128 KiB pooled / 2 min | `relay.go` (`BufSize` / `WriteStallTimeout`) |
| Splice idle reap | 10 min with no bytes in **either** direction (>30× Minecraft's 30 s keepalive limit) | `relay.go DefaultIdleTimeout` |
| Datagram idle reap | 2 min — a UDP flow has no FIN, so the reaper is the only thing that retires it; both ends share the constant | `relay/datagram.go DatagramIdleTimeout` |
| Datagram frame | 2-byte big-endian length + payload; max payload 65507 B, checked before the buffer is sized | `relay/datagram.go MaxDatagram` |
| UDP session queue / datagram buffer | 64 datagrams per player (full = drop, never block the shared read loop) / 2048 B pooled | `gateway/udp.go udpSessionQueue udpBufSize` |
| Bandwidth cap unit / burst | `Mbps × 125_000` B/s / `relay.BufSize` | `bwcap.go` |
| Backoff | 1 s → 60 s full jitter, reset after 60 s stable | `link/backoff.go` |
| Run stop grace | 10 s — the caller's wait for a clean unwind, not the run's; force skips it, over-IPC always does | `engine.go stopGrace`, `app.go engineStopGrace` |
| Abuse defaults | 4096 global / 32 per-network conns; 10 auth fails/min/network (token bucket: burst 10, refill 10/min) | `config.go Default` |
| Abuse accounting unit | one IPv4 address; one IPv6 **/64** (smallest block a subscriber holds) | `limits.go sourceNet` |
| Per-agent listener cap | 32 public listeners per agent (the one resource an agent allocates by asking) | `config.go Default`, `actor.go bindLocked` |
| Group caps | 16 groups / 16 members per group (operator-assigned membership, not counted against the per-agent listener cap) | `gateway/group.go MaxGroups MaxMembersPerGroup` |
| Auth-failure table cap | 4096 networks, LRU-evicted (hard cap, not a cleanup trigger) | `limits.go authFailNets` |
| Pre-auth gate | `MaxConnsGlobal + 256` concurrent unauthenticated conns; slot freed at auth | `limits.go preAuthGate`, `preAuthHeadroom` |
| Accept retry backoff | 5 ms doubling → 1 s; only a closed listener stops the loop | `gateway.go retryAccept` |
| Loss window / ping-loss timeout | 32 heartbeats / 2×interval | `agent.go lossWindow lossTimeout`, `linkquality.go` |
| Gateway per-conn RTT sample | every 5 s, ≤200 entries per frame | `gateway.go rttSampleInterval`, `control.go MaxConnStatsPerFrame` |
| GUI tick | 500 ms; log poll 250 ms; UI re-render tick 1 s | `app.go tickInterval`, `Activity.tsx pull`, `Overview.tsx` |
| IPC clamps | 150 conns in status; 300 buckets/peers | `ipc.go MaxStatusConns maxIPCEntries` |
| Query clamps | 80 players / 100 sessions / 300 points / 60 name+IP spans / 250 countries / 16 tunnels × 100 uptime events | `queries.go MaxPlayersPage MaxHistoryPoints`, `geoquery.go MaxCountryRows`, `summary.go MaxUptimeEvents MaxUptimeTunnels` |
| Stats tiers | 100 ms×1200, 1 s×1800, 15 s×7200, 10 m×5760, 1 d×1100; tiers ≥2 persisted | `stats.go tierSpecs` |
| Sampler | 10 Hz sample, 45 s flush, 15 s session-replay samples | `engine.go sampleInterval flushInterval sessionSampleInterval` |
| Health score | bad: loss>5 % or jitter>100 ms; warn: loss>1 %, jitter>30 ms, or up<1 min | `engine.go healthScore` |
| DB writer | batches ≤256 ops / 250 ms; queue 4096 drop-oldest (barriers never dropped) | `db.go writeQueueCap batchMax batchDelay` |
| Retention | sessions 180 d (config), geo cache 30 d, hourly rollups 90 d, daily forever | `retention.go` |
| Rollup cadence | 5 min + on start + final on close | `rollup.go rollupEvery`, `db.go writer` |
| Resolver | bulk 10 names / 2 s coalesce; 1 rps burst 3; +TTL 30 d, −TTL 24 h; profile re-check 24 h | `players/resolver.go batchSize defaultBatchDelay positiveTTL negativeTTL defaultProfileTTL` |
| Avatars | sizes 16–128 (default 64), 8×8 master; Mojang spacing 60 s/player, 1 rps burst 3; miss TTL 15 min; evict 4000 files / 64 MiB / 6 h | `app/avatars.go avatarMinSize masterSize profileSpacing missTTL evictMaxFiles` |
| Pipe | 5 s request / 2 min idle timeouts; ACL BA+SY+IU | `ipc/server_windows.go` |
| Cert | ECDSA P-256, 20-year validity (trust = pin, not expiry) | `link/cert.go NotAfter` |
| Key exchange | X25519MLKEM768 (PQ hybrid, Go default; `CurvePreferences` unset) | `link/cert.go`, `link/pq_test.go` |
| Agent identity | Ed25519 (`agent_identity.key`, PKCS#8 PEM, `0600`); `agentID` = `agt_` + 16-char Crockford base32 of sha256(pubkey)[:10] = 80 bits | `link/cred.go LoadOrCreateIdentity agentFingerprintBytes` |
| Enrollment ticket | `tkt_` + 128-bit nonce; single-use default, reusable optional; TTL caller-set, 0 = never (UI single-use default 10 min) | `link/cred.go NewEnrollTicket`, `gateway/agentstore.go IssueEnrollment` |
| Bootstrap pairing code | headless first start, empty roster only: one single-use ticket, TTL 60 min, printed to the console | `gateway.go bootstrapTicketTTL RunStarted` |
| Invite retention | dead (consumed/expired) tickets keep their honest error for 7 d, then prune at the next issue | `agentstore.go inviteRetention` |
| "New" agent badge | 24 h after enrollment | `Agents.tsx NEW_AGENT_MS` |
| Agent allowlist | `gateway_agents.json` (`0600`, atomic write + AV-retry): identity + scope + desired config per agent | `gateway/agentstore.go AgentStore` |
| Pairing code | `pxf://host:port/v1/pair/<tkt>#sha256:<64hex>`, ≤ 512 B before parse | `link/pairing.go ParsePairingCode` |
| Agent auth | Ed25519 proof-of-possession over `proxyforward-agent-auth-v1` + gateway cert FP — no bearer token in steady state | `link/cred.go AgentAuthMessage` |
| Perf floor | ≥20 MiB/s, worst cross-stream RTT ≤500 ms (64 MiB loopback burst); per-transport twins `TestBurstThroughputPerConn` / `TestBurstThroughputQUIC` | `e2e_test.go runBurst` |
| Blur ladder | control 4, bloom frame 5, card 4, chrome 8, island 8, float 10, pop 12 px (water, not frost) | `tokens.css` |
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
- Capability `gateway-config` (keyed to the agent's Ed25519 identity; a peer
  that never learned the capability falls back to `tunnel-sync`): the gateway is
  authoritative. It stores each identity's
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
- Capability `config-consent` (layers on `gateway-config`, so it is negotiated
  only alongside it): mutual consent for *authored* edits. Either side proposes
  `config_proposal{proposalId, baseGen, tunnels[], hash}`; the other holds it for its
  user and answers `config_decision{proposalId, approved, reason, generation, hash}`.
  Held state lives on the live session (`agentSession.pendingInbound`/`pendingOutbound`,
  agent `session.pendingIncoming`/`pendingLocal`), so it is session-scoped: a
  disconnect drops it and the author re-proposes. Nothing is durable.
  - **The commit point is always `push_config`.** Approval makes the gateway adopt +
    bump + push (`adoptAndPush`), and applying that push is what changes the agent —
    the same path connect-sync, drift, and reclaim use. So a rejection needs no
    rollback: nothing was written. `App.SaveTunnels` therefore persists nothing under
    consent; the approval's push does, via the `ConfigPersister` the app wires.
  - **Consent covers authored content only.** Arbitration (reclaim, auto-reassign,
    connect-sync, drift) stays immediate on `push_config` with a notice. And an edit
    the gateway cannot see — `LocalAddr`, PP2 — is applied unproposed, because
    consent governs the gateway's set, not the agent's wiring
    (`agent/consent.go ProposeTunnels` compares `HashTunnels` before proposing).
  - Agent-local fields never ride the wire, so an approved *new* tunnel would come
    back with no `LocalAddr`. The authored draft rides the pending proposal and is
    overlaid on the merge (`agent/hotapply.go overlayDraft`); the push lands before
    its decision on one ordered stream, so the draft is still there.
  - A pushed spec can never *enable* a disabled draft (`mergeTunnels`), and a data
    stream can never dial one (`enabledTunnelByID`) — see CLAUDE.md's Security
    invariants.
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

Pause (cap `pause`): one symmetric message, `pause_state{tunnelIds[], agentWide}` — the
sender's **complete** current pause set, never a delta, so a reconnect re-assert is
idempotent and both views converge. Two independent bit sets exist (each side's own,
each side clears only its own); a tunnel serves players iff neither covers it. The
bits are state, not config: they never touch `HashTunnels`, `sameListener`, a push, or
consent — a pause/resume can never rebind a listener (the port is held by
construction). Enforcement is local-first at each side's own choke point: the gateway
folds both sets into `publicListener.paused` on the actor (`actor.go refreshPause`,
the `pl.limiters` pattern) and `handleClient` reads it beside `backendDown` (paused
players get the offline MOTD treatment); the agent refuses its own local dial for its
own bits (`handleDataStream`), so a paused tunnel costs no Minecraft slot even against
a legacy gateway — a legacy pairing loses only cross-side visibility. Persistence is
local per side (`AgentRecord.PausedTunnels/PausedAll` on the gateway;
`config.Tunnel.Paused` + the agent-wide `AgentConfig.Paused` on the agent),
re-asserted after hello in both directions; kicking is a deliberate extra
(`kickConns` walking `rttConns`; the agent closes its own tracked local legs). Both
hellos also carry `headless` (presence-driven, not a capability): "no GUI is attached
here, nobody may be watching for your consent proposal" — surfaced on the pending
surfaces, never acted on.

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

**Validators.** `gateway/auth.go identityValidator` is the only authenticator: verify the
proof-of-possession signature, then allowlist lookup (or ticket enrollment on first contact).
Every accept path (TCP control, QUIC, per-conn data) funnels through the one
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

## UDP tunnels (`internal/gateway/udp.go`, `internal/relay/datagram.go`)

A UDP port has no `accept()`, so `udpListener` manufactures one and thereby satisfies
`net.Listener`: the first datagram from an unseen source address *is* the connection.
That single decision is what makes the feature small — the actor's ghost-listener
wait, per-agent eviction, the pause bits, conntrack, the bandwidth caps, and
`handleClient` itself all apply through the code that already implements them for TCP.

**Life of a player datagram**: player UDP → gateway `udpListener` read loop (one
goroutine per port, one reusable scratch) → per-source `udpSession` (a `net.Conn`
whose `Read` yields exactly one datagram) → `handleClient` as usual → `open_conn`
header on a normal data stream → the stream leg wrapped in `relay.DatagramStream`
(2-byte length prefix, so boundaries survive the reliable hop) → agent
`serveDatagramStream` → connected `DialUDP` socket → local server. `relay.Splice` is
**unmodified**: it works because every leg's `Read` returns one datagram and it writes
once per read.

Four things differ from TCP and each is load-bearing:

- **Admission moves into the listener.** The first datagram is the connection, so the
  `connGate` must decide before a session exists; `handleClient` skips its own
  `admit` rather than counting one player twice. This is also what bounds the session
  table by construction — the gate's global and per-network caps bound it, so there
  is no second UDP-only limit to drift from the TCP policy.
- **`CloseWrite` closes.** A datagram flow has no half-open state. Splice reaches EOF
  on one direction and relies on the other noticing a FIN; with no FIN, a no-op
  `CloseWrite` leaves the opposite copy parked until the idle reaper — a goroutine,
  a socket and a gate slot per departed player. Both `udpSession` and
  `relay.DatagramConn` therefore end the flow, and both must, or one side hangs.
- **Idle reaping is the only teardown**, on a much shorter clock
  (`DatagramIdleTimeout`), shared by both ends so neither reaps a session the other
  still believes in.
- **A full per-player queue drops.** One read loop serves every player on the port,
  so blocking for a stalled client would freeze the others behind it — which is also
  what a real socket's receive buffer does under the same pressure.

**Ports are per protocol**: TCP 25565 and UDP 25565 are different ports and routinely
both in use (Simple Voice Chat listens on UDP at the Java server's number), so
`portHolder`/`portHolderDesc` are protocol-scoped and `portowner` — `GetExtendedTcpTable`,
TCP-only — is consulted only for TCP.

**Health is unknown, not up.** A UDP local address cannot be probed: `net.Dial("udp")`
connects to a dead port as happily as a live one, and a real check would have to speak
the server's protocol. So `probeTunnels` skips udp tunnels entirely; unknown health
counts as up (`backendDown`), and the GUI shows "unknown" rather than a convincing lie.

**Windows**: `ReadFromUDP` reports a *previous* send's ICMP Port Unreachable as
`ECONNRESET`, on an unconnected socket, for a different peer. The read loop ignores it
— returning would take the tunnel down the first time any player quit, leaving the
port bound and answering nothing.

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
(gated bindings reject like the real backend), `&fatal=1`, `&engine=stopped`
(the run is deliberately off: `runState` stopped, no fatal — the "am I on" axis,
distinct from `&fatal` which is a crash and `&link=down` which is trying and
failing; Start/Stop mutate it live), `&service=running|stopped` (the Windows
service badge + its installed-but-stopped warning; Install/Start/Stop/Uninstall
drive it), `&fresh=1`,
`&analytics=off`, `&paired=0` (never paired to a gateway — the sidebar's role
switcher cannot become the agent and must route to setup), `&geo=off|empty|error|pending`,
`&fx=low|high`, `&fleet=multi|old` (gateway only — `multi`: a five-agent fleet with a
good/fair/poor health spread instead of the default single agent, for the Agents roster
+ drill-in; `old`: a pre-roster daemon that sends no agents array → the roster's
honest-unavailable state), `&conflict=port|clone|both` (seeds the gateway event ring so
the conflict cards + event log have data), `&deeplink=1` (a clicked `pxf://` invite),
`&pending=in|out|both` (config-consent: a proposal awaiting this side, awaiting the
peer, or both — the approval surfaces on either role), `&consent=off` (the peer predates
config-consent, so edits apply the old way and the surfaces must say so honestly),
`&invites=friend|single|both` (gateway only — live pairing codes as roster rows: a
reusable labeled "friends" invite with two enrollments, a pending single-use code
expiring in minutes, or both; minting from Overview or the modal appends a row and
revoke removes it),
`&tray=off|notice` (the close fork: the mock's X mirrors `closeDecision` — default is
tray-on with the notice seen; `off` → close quits, confirming when players are on;
`notice` → an uneventful close shows the one-time tray notice),
`&pause=mine|theirs|both` (the default tunnel's pause bits — paused by this side, the
peer, or both; Pause/Resume mutate this side's bit live, and `&consent=off` doubles as
the "peer predates pause" state).
The traffic model is deterministic functions of absolute time, so
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
- System tray: energye/systray registers on the main goroutine in `runGUI` *before*
  `wails.Run` (its `init()` locks the main thread; the tray window is created on the
  registering thread and Wails' message pump services it — `OnStartup` runs in a
  goroutine, too late). Menu callbacks fire inside that pump, so every handler
  dispatches to a goroutine (`app/tray.go`). The close fork is one pure decision
  (`app/close.go closeDecision`, wired to Wails `OnBeforeClose` — the funnel for the
  X button, Alt-F4, and the frontend's `Quit()`); dialogs render in the frontend off
  the `pxf:close` event. The icon is rendered from `build/appicon.png` at startup
  (`internal/trayicon`), one ICO per run state.
