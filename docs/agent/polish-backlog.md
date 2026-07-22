<!-- Companion to /CLAUDE.md ("Current phase"). Seeded from the full-repo audit of
     2026-07-13; last coherence pass 2026-07-17 (line cites drift between passes —
     verify before acting). Remove items when fixed; add new ones with the same
     file:line discipline. -->

# Polish backlog — concrete, file-cited

Ordered by user-visible impact. "Fix" describes the smallest on-system change.

## Honesty (UI promises > backend truth)

1. **Stub controls promise unimplemented behavior** — the top item; see CLAUDE.md
   "Reality check" for the full inventory. Down to one after the honesty pass wired
   Offline MOTD, the bandwidth cap, Prometheus, and the Transport selector, the
   tray shipped for real ("Close to tray" in Settings is honest now), and the
   tunnel Protocol picker became real when udp tunnels landed (it was a one-option
   Select with a no-op onChange):
   - Autostart ("Start on login") — stored (`config.go Autostart`), consumed by
     nothing; the honesty pass removed its toggle, so today the promise is absence.
   - Fix: implement it (registry Run key or startup-folder shortcut), or delete the
     config field. Decide with the human (scope call — escalation trigger).

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

7. **BandwidthChart's range + Line/Candles pickers are hand-rolled button rows**
   (`BandwidthChart.tsx:85-121`) while every sibling toggle is `SegmentedControl`.
   Likely deliberate (mono tabular labels, 9 dense options) — either port
   `SegmentedControl` to support a dense/mono variant, or record the exception in the
   component comment so it stops looking like drift.

8. **Direction-column vocabulary varies**: "Received/Sent" (`Traffic.tsx:108-109`),
   "Total ↓/Total ↑" (`Traffic.tsx:136-137`), bare "↓/↑" (`Analytics.tsx:532-533`,
   `Players.tsx:374-375`). Pick: glyphs for dense tables, words for wide ones — and
   apply it once.

9. **Legend ramps differ for the same idea** — peak-hours swatches
   `[10,30,55,80,100]%` (`Analytics.tsx:343`) vs map activity swatches
   `[16,38,58,76,92]%` (`WorldMap.tsx:203`); the map's fill floor is 14 % + 78 %·t
   (`WorldMap.tsx:18`) vs heatmap 7 % + 85 %·t (`Analytics.tsx:326`). Harmonize the
   ramp constants (one exported pair in `charts/util.ts`).

10. **Two duration styles side by side** — live sessions use clock format `1:02:03`
    (`fmtElapsed`, `Traffic.tsx:341`) while dossier/history tables use `1h 2m`
    (`fmtDuration`, `state.ts:61`). Both are defensible; choose per-context
    (live-updating → clock, historical → words) and write that rule into `state.ts`.

## Small judgment calls

11. **Overview shows RTT twice** — HealthPanel metric and the "Round trip" StatTile
    (`Overview.tsx:177,262`). Probably drop the tile in favor of something not already
    on screen (e.g. peak rate today).

12. **AnalyticsUnavailable reuses the Players icon on the Analytics screen**
    (`Players.tsx:53-60` rendered from `Analytics.tsx:42`). Give it a neutral icon
    (IconAnalytics/IconActivity) or parameterize.

13. **ConnectionPill ignores truncation** — at >150 conns the pill's source data is
    clamped but nothing in the titlebar hints at it (`ConnectionPill.tsx`). Low
    priority; only matters on very busy gateways.

15. **`frontend/src/hooks.ts:26` carries an `eslint-disable` comment but no ESLint
    config exists** — either add ESLint (decision) or drop the comment.

16. **The analytics DB is role-blind, and the sidebar can now switch roles**
    (`RoleSwitcher.tsx`). `engine.New` opens the same `analytics.db` in the config dir
    for both roles (`engine.go:96`) and the schema has no role column on `sessions`,
    `peers`, `rollup_*`, or `lifetime` (`internal/analytics/schema.go`). Both roles do
    record the same *conceptual* population — the gateway forwards the player's address
    to the agent (`agent.go Conns`) — but a machine that ran as a gateway and then as an
    agent of a *different* tunnel splices two measurement points into one history with
    no way to tell the rows apart. Told, not engineered around: the switch confirm
    (`RoleSwitcher.tsx`) says so out loud. Fixing it properly means either a `role`
    column + a filter on every query, or per-role data dirs — and the config dir also
    holds the cert, the logs, and the setup import/export target, so that is an
    `overhaul`-scale change, not a migration.

17. **Analytics Geography empty state collapses three GeoIP conditions into one** —
    the map's fallback branches only on `!geoStatus.cityLoaded` and always renders
    "GeoIP not configured" (`Analytics.tsx:238`), so a *failed* city-DB open (a real
    `geoStatus.cityError`) and a *pending* reload read identically to *unconfigured*.
    Settings' `MmdbBadge` already tells Failed/Pending/Loaded apart from the same
    `useGeoStatus` data, and CLAUDE.md's GUI contract cites `Analytics.tsx` as the
    example of telling states apart — so this is a real gap against it. Predates the
    glass pass. Fix: branch on `geoStatus.cityError` the way `MmdbBadge` does.

18. **Wizard role-choice cards both wear Signal Glass** — the two `RoleCard`s on the
    "choose role" step are each `.pf-signal` (`Wizard.tsx:327`), so two pointer-reactive
    surfaces coexist on one screen, which `DESIGN.md` rule 2 and `glass.css`'s
    live-activity-only restriction warn against (a role picker isn't live traffic). The
    glass pass only dropped this card's inset bevel (§8); the `pf-signal` class predates
    it. Fix: move `RoleCard` to `.pf-card` (or a "choice" recipe) reserving `.pf-signal`
    for the live-handshake step, or record it as a reviewed exception in a one-line
    comment (as #7 does for BandwidthChart's pickers).

34. **Chart SVG text is px-locked outside `--ui-scale`** — axis ticks and in-chart
    annotations set `fontSize` as SVG attributes (`BandwidthChart.tsx`,
    `charts/LineChart.tsx`), deliberately excluded from the type-token sweep: their
    sizes are coupled to px tick geometry, and scaling the text alone breaks the
    layout. At the 1.33× viewport step chart labels sit visibly smaller than the
    caption tier around them. Fix means scaling the chart's whole coordinate frame
    (or deriving tick geometry from the resolved font size), not just the text.

## QUIC transport follow-ons

23. **Gateway per-session QUIC link bytes are unattributed** — the QUIC listener shares
    one UDP socket across every agent session, so `sess.link` (the GUI's per-agent
    LinkBytesIn/Out) can't be counted at the socket layer the way `NewCountingConn` wraps
    a single yamux conn. Process totals (`g.linkTotals`) are exact; the agent side is exact
    (one socket = one session). v1 leaves the gateway per-agent link bytes at 0 (renders
    "—"). Proper fix: count at the stream layer inside `transport/quic.go` per accepted
    session (would need the session's `*stats.LinkCounters` threaded in).
24. **No QUIC connection-migration test** — passive migration (gateway follows an agent
    whose public IP changes) is a claimed benefit but untested; a UDP relay that swaps its
    source port mid-transfer would exercise it. Flaky-prone, hence deferred.
25. **QUIC perf headroom on Windows** — `TestBurstThroughputQUIC` clears the floor
    comfortably on loopback, but quic-go's GSO/GRO/ECN fast paths are largely Linux; a
    Windows perf pass (and, if quic-go ever exposes a pluggable CC, BBR for lossy
    residential links) is future work.

## Backend polish adjacent to UX

19. **Gateway jitter/loss comment drift** — `ipc.go:101-103` says "the gateway
    reports -1/unknown" but the gateway has measured its own jitter/loss since the
    bidirectional heartbeat landed (`gateway.go pingLoop`, `engine.go:391-395`).
    Fix the comment before it misleads someone.

20. **stats.redacted.json in diagnostics is pre-migration only** — after
    `ImportLegacyStats` renames `stats.json` away, bundles carry no stats snapshot
    (`app/tools.go:90`, `analytics/importjson.go`). Either export a redacted snapshot
    from SQLite or drop the stale zip entry.

## CI debt

21. **`errcheck` is disabled in `.golangci.yml`** — it reports 50 findings, and most
    are deliberate (`windows.SetStdHandle`, deferred `Close()` on read paths, netsh
    calls whose exit code is the real signal). Turning it on means auditing all 50 and
    writing explicit `_ =` assignments with a reason. Worth doing as its own commit;
    it was kept out of the CI suite so the lint gate could be green on day one.

22. **Linux/macOS binaries build but cannot run** — CI compiles them to keep the
    `*_other.go` stubs honest, but `ipc.Serve` returns `ErrUnsupported` off Windows
    (`internal/ipc/stub_other.go`), so the engine never starts. Shipping them means a
    real unix-socket IPC port — an `overhaul`-skill change, not a stub fix.

## Agent-management surfacing (backend ahead of the GUI)

The per-agent identity/enrollment/config work landed backend-first. Items 26–29 and 31
are now built (enrollment issuance on Agents + the Overview strip, one-click port
reclaim, the version "Update" badge, the gateway-side config editor with mutual
consent, and typed `tnl_` ids on create); what remains is below.

30. **Per-agent transport pin absent** — transport (auto / quic / per-conn / mux) is one
    global agent setting; per-agent management (`frontend/src/screens/Agents.tsx`) can't pin
    a single agent's transport. Low priority, and arguably correct as-is: the agent owns its
    own dial. Fix: a per-agent override if ever needed.
32. **The gateway editor cannot add a tunnel** — it edits ports and removes tunnels
    (`frontend/src/screens/Agents.tsx AgentConfigPanel`), because the wire carries no
    `LocalAddr` and an operator has no way to know where a tunnel points on the agent's
    LAN — an invented one would land inert. Adding stays the agent's job. Fix (if wanted):
    let the operator propose a named tunnel the agent must complete with a local address
    before it enables.
33. **A dropped session forgets a pending proposal** — consent proposals are session-scoped
    by design (no persisted schema), so a disconnect mid-approval drops the edit and the
    author must re-submit. Self-healing and honest (the surfaces go quiet), but a
    long-offline gateway operator can't queue a change. Fix (if wanted): persist pending
    proposals on `AgentRecord` and re-offer them on connect. The related "nobody may be
    watching" gap is now told, not fixed: the hello's `Headless` flag makes both pending
    surfaces say the peer runs headless, so an approval may wait indefinitely.
