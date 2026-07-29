<!-- Companion to /CLAUDE.md ("Current phase"). Seeded from the full-repo audit of
     2026-07-13; last coherence pass 2026-07-26, which removed the consistency
     cluster (one rttTone, one relative-time helper, one Pager, one direction
     vocabulary, one activity ramp), the small judgment calls, and the doc/backend
     truth items as they were fixed. What is left needs a human's scope call, an
     overhaul, or was deferred on purpose. Line cites drift between passes —
     verify before acting. Remove items when fixed; add new ones with the same
     file:line discipline. -->

# Polish backlog — concrete, file-cited

Ordered by user-visible impact. "Fix" describes the smallest on-system change.

## Small judgment calls

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

## CI debt

21. **Linux/macOS binaries build but cannot run** — CI compiles them to keep the
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
