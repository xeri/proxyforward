<!-- Companion to /CLAUDE.md ("Current phase"). Seeded from the full-repo audit @ 4a8b0c9,
     2026-07-13. Every item cites the files as of that commit (+ uncommitted v2 frontend).
     Remove items when fixed; add new ones with the same file:line discipline. -->

# Polish backlog — concrete, file-cited

Ordered by user-visible impact. "Fix" describes the smallest on-system change.

## Honesty (UI promises > backend truth)

1. **Stub controls promise unimplemented behavior** — the top item; see CLAUDE.md
   "Reality check" for the full inventory.
   - Tunnel editor: "Offline MOTD" field, "Bandwidth cap (Mbps)" field, and the
     Minecraft-aware hint's "Poll the server for MOTD, player count and version"
     claim (`frontend/src/screens/Tunnels.tsx:268-283`) — none are wired
     (`gateway.go handleClient`, no cap enforcement, no status poller).
   - Settings: Transport "Per-connection" option (`Settings.tsx:139-144`), Prometheus
     toggle + address (`Settings.tsx:227-232`), "Minimize to tray" and "Start on
     login" (`Settings.tsx:122-125`) — all stored, all inert.
   - Fix: either implement, or visibly mark ("not yet wired") / hide until real.
     Decide per-feature with the human (scope call — escalation trigger).

2. **Overview under-reports live sessions when truncated** — pipeline node and the
   "Live sessions" tile use `conns.length` (clamped at 150 by `ipc.MaxStatusConns`)
   instead of `status.connectionsTotal || conns.length`
   (`Overview.tsx:157-158,180`). Traffic already handles it (`Traffic.tsx:160,169-173`).

3. **README ↔ app copy drift**: README says "Settings → Windows integration → Add
   rule" (`README.md:53-54`); the section is titled "System" (`Settings.tsx:489`).
   README also claims "115 tests, including 5 fuzz targets" (actual: 214 test funcs,
   8 fuzz targets) and "enforced in CI" (a workflow now exists at
   `.github/workflows/ci.yml` but has never run — verify green on first push).
   Fix the README.

## Consistency (same concept, forked treatments)

4. **RTT tone thresholds duplicated ×3** — `<60 good, <130 warn` in
   `Players.tsx:295` (PingBadge), `Analytics.tsx:105`, `GeoRank.tsx:10`. Extract one
   `rttTone` next to `hasRtt` in `state.ts`; note it intentionally differs from the
   *link*-health thresholds (`engine.go healthScore` — player ping ≠ control link).

5. **Two "just now" cutoffs** — `fmtRelative` uses 45 s (`Traffic.tsx:349-357`),
   `fmtLastSeen` uses 90 s (`players.ts:76-85`). Same rendering intent; unify into
   one relative-time helper (state.ts) with one cutoff.

6. **Pagination row duplicated** — identical "x–y of z · Previous/Next" blocks in
   `Players.tsx:196-206` and `Analytics.tsx:581-591`. Extract a `Pager` into `ui.tsx`.

7. **Players wall search forks the input recipe** — hand-rolled `h-8` input with its
   own focus style (`Players.tsx:147-154`) beside kit `TextInput`. Add a compact/icon
   variant to `TextInput` and use it.

8. **BandwidthChart's range + Line/Candles pickers are hand-rolled button rows**
   (`BandwidthChart.tsx:85-121`) while every sibling toggle is `SegmentedControl`.
   Likely deliberate (mono tabular labels, 9 dense options) — either port
   `SegmentedControl` to support a dense/mono variant, or record the exception in the
   component comment so it stops looking like drift.

9. **Direction-column vocabulary varies**: "Received/Sent" (`Traffic.tsx:108-109`),
   "Total ↓/Total ↑" (`Traffic.tsx:136-137`), bare "↓/↑" (`Analytics.tsx:532-533`,
   `Players.tsx:374-375`). Pick: glyphs for dense tables, words for wide ones — and
   apply it once.

10. **Legend ramps differ for the same idea** — peak-hours swatches
    `[10,30,55,80,100]%` (`Analytics.tsx:343`) vs map activity swatches
    `[16,38,58,76,92]%` (`WorldMap.tsx:203`); the map's fill floor is 14 % + 78 %·t
    (`WorldMap.tsx:18`) vs heatmap 7 % + 85 %·t (`Analytics.tsx:326`). Harmonize the
    ramp constants (one exported pair in `charts/util.ts`).

11. **Two duration styles side by side** — live sessions use clock format `1:02:03`
    (`fmtElapsed`, `Traffic.tsx:341`) while dossier/history tables use `1h 2m`
    (`fmtDuration`, `state.ts:61`). Both are defensible; choose per-context
    (live-updating → clock, historical → words) and write that rule into `state.ts`.

## Small judgment calls

12. **Overview shows RTT twice** — HealthPanel metric and the "Round trip" StatTile
    (`Overview.tsx:177,262`). Probably drop the tile in favor of something not already
    on screen (e.g. peak rate today).

13. **AnalyticsUnavailable reuses the Players icon on the Analytics screen**
    (`Players.tsx:53-60` rendered from `Analytics.tsx:42`). Give it a neutral icon
    (IconAnalytics/IconActivity) or parameterize.

14. **ConnectionPill ignores truncation** — at >150 conns the pill's source data is
    clamped but nothing in the titlebar hints at it (`ConnectionPill.tsx`). Low
    priority; only matters on very busy gateways.

15. **`frontend/src/hooks.ts:26` carries an `eslint-disable` comment but no ESLint
    config exists** — either add ESLint (decision) or drop the comment.

## Backend polish adjacent to UX

16. **Gateway jitter/loss comment drift** — `ipc.go:101-103` says "the gateway
    reports -1/unknown" but the gateway has measured its own jitter/loss since the
    bidirectional heartbeat landed (`gateway.go pingLoop`, `engine.go:391-395`).
    Fix the comment before it misleads someone.

17. **stats.redacted.json in diagnostics is pre-migration only** — after
    `ImportLegacyStats` renames `stats.json` away, bundles carry no stats snapshot
    (`app/tools.go:90`, `analytics/importjson.go`). Either export a redacted snapshot
    from SQLite or drop the stale zip entry.

## CI debt

18. **`errcheck` is disabled in `.golangci.yml`** — it reports 50 findings, and most
    are deliberate (`windows.SetStdHandle`, deferred `Close()` on read paths, netsh
    calls whose exit code is the real signal). Turning it on means auditing all 50 and
    writing explicit `_ =` assignments with a reason. Worth doing as its own commit;
    it was kept out of the CI suite so the lint gate could be green on day one.

19. **Linux/macOS binaries build but cannot run** — CI compiles them to keep the
    `*_other.go` stubs honest, but `ipc.Serve` returns `ErrUnsupported` off Windows
    (`internal/ipc/stub_other.go`), so the engine never starts. Shipping them means a
    real unix-socket IPC port — an `overhaul`-skill change, not a stub fix.
