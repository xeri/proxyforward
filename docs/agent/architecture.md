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
  transport/            Session/Stream interfaces + tuned yamux impl (only yamux user)
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
| Heartbeat / idle deadline / ctrl write | 5 s / 15 s / 10 s | `agent.go:38-43`, `gateway.go:45-53` |
| yamux window / conn-write timeout | 1 MiB / 30 s | `transport/yamux.go` |
| Splice buffer / write-stall deadline | 128 KiB pooled / 2 min | `relay.go:23-28` |
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
| Perf floor | ≥20 MiB/s, worst cross-stream RTT ≤500 ms (64 MiB loopback burst) | `e2e_test.go:716,719` |
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
- Legacy: per-tunnel `register_tunnel`/`unregister_tunnel` + `register_ok|register_err`.
Steady state: `ping/pong` both directions (RTT/jitter/loss both sides; pong echoes
`recvUnixNano` for one-way estimates); agent pushes `health{tunnelId, localUp}` on
probe transitions; gateway pushes `conn_stats{[{c,r}]}` (cap `conn-stats`) mapping
kernel RTT onto agent conn entries via `ConnKey`. Data streams: gateway →
`OpenStream` + `open_conn{tunnelId, clientAddr, connId}` header, then raw bytes.

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
`&fx=low|high`. The traffic model is deterministic functions of absolute time, so
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
