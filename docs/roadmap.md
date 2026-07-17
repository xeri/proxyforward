<!-- Seeded from the 2026-07-17 full audit (branch feat/agent-management-gui @ a4a54c9
     plus the uncommitted accept-path hardening). Lives OUTSIDE docs/agent/ on purpose:
     the doccheck citation gate scans CLAUDE.md, docs/agent/, .claude/rules/, and
     .claude/skills/, and a roadmap must be free to name symbols that do not exist yet.
     Verify any citation before copying it into a gated doc. -->

# Roadmap, cut into work packages

One work package (WP) is one branch and one reviewable unit. Each lists: the value in
one line, scope, what it depends on, the skill to open before starting, a size, and
the check that proves it done. Delete a WP in the same commit that ships it (the same
discipline as CLAUDE.md's reality-check rows).

Sizes: **S** under a day, **M** one to three days, **L** one to two weeks,
**XL** bigger than that (decompose into its own plan before starting).

Tags: **NEW** marks ideas proposed by the 2026-07-17 audit, not previously on any
backlog. **escalate** marks changes CLAUDE.md requires confirming with the human
first (wire protocol, releases).

Sections are parallel-safe against each other (different subsystems). Inside a
section, order is dependency order. The short version of the overall order:

1. Section 0 first; the tree must be clean and the in-flight doubts settled.
2. A1 (rekey) and A2 (UDP) are the headline engine work; B1 (event bus) and
   B4 (status poller) can proceed in parallel with them.
3. C1 (tunnel subpage) early, because C2/C3 and several analytics surfaces hang
   off it. D0 (schema decision) before anything grows the analytics schema.
4. Section E is background work; pick items up between larger WPs.

---

## 0. In flight: finish before anything else

- **WP-0.1 Commit the accept-path hardening** (S). Uncommitted in the tree right
  now: accept-loop retry with backoff plus a pre-auth admission gate
  (`internal/gateway/gateway.go`, `limits.go`, new `accept_test.go`).
  Done when: `go test ./...` green, committed, tree clean.
- **WP-0.2 Verify offline MOTD against modern clients** (S–M). The responder
  exists (`internal/mc/status.go`, e2e `offline_motd_test.go`) but has never been
  driven against a real modern client. Check the status JSON against 1.20.3+ text
  components and legacy `§` codes, and that the Minecraft-aware editor settings
  round-trip. Done when: a real client shows the offline MOTD correctly, or the fix
  lands.
- **WP-0.3 Fix the live-edge dip in line charts** (S). `frontend/src/history.ts`
  has no partial-bucket handling, so the newest bucket reads low then jumps.
  Exclude or pro-rate the in-progress bucket at the right edge.
  Done when: 15s/1m ranges show no sawtooth at the live edge.
- **WP-0.4 Polish-backlog sweep** (M). Work `docs/agent/polish-backlog.md` items
  #2–#20 as a batch of small commits. Pull from that file before inventing new
  polish; it stays the UX-debt ledger, this file is everything else.

---

## A. Wire protocol and trust (escalate each; open the wire-protocol skill)

Mostly serial: each changes the capability set, and A1 gets harder to retrofit
with every agent enrolled.

- **WP-A1 Rekey: online token and cert rotation** (L, escalate). Today regenerating
  the token or `gateway.crt` breaks every agent until re-pairing. Add a `rekey`
  capability: push the new fingerprint/token over the currently authenticated link,
  accept both during a switch window, then retire the old. Include the GUI flows
  that currently warn-and-break. Done when: rotating cert and token under a live
  agent causes no visible drop, and an e2e proves stale credentials fail closed
  after the window.
- **WP-A2 UDP tunnels (Bedrock)** (XL, escalate, decompose first). The biggest
  product gap: Bedrock is RakNet over UDP, so today only Java Edition works.
  Scope: gateway UDP listener with a flow table keyed by source address (idle
  expiry replaces FIN semantics; same "idleness is a property of the flow"
  philosophy as the splice), agent-side UDP relay, `type:"udp"` end-to-end
  (config already parses it; `validateSpec` rejects it, a latent gap), bandwidth
  caps and per-source limits keyed on the network (the `sourceNet` invariant),
  and re-advertising `tunnel-udp` only once real end-to-end.
  Sub-item (NEW): a RakNet offline pong, so an offline Bedrock server still
  answers pings the way WP-0.2 does for Java.
  Done when: a Bedrock client joins through the tunnel; the reality-check row is
  deleted in the same commit; the TCP burst floor is untouched.
- **WP-A3 Cross-side stats sharing** (M, escalate). A `stats-share` capability:
  periodic per-tunnel summaries (bytes, conns, health) in both directions over the
  control stream, chunked under the frame cap. Unlocks the gateway showing
  agent-side health end-to-end and properly fixes the unattributed per-agent QUIC
  link bytes (polish #23). Done when: the gateway drill-in shows agent-side numbers
  with the sentinel rules intact (unknown renders as a dash, never a fake zero).
- **WP-A4 Multiple gateways / failover** (M). Agent config holds an ordered list of
  pairings; the reconnect loop iterates candidates with the existing backoff, and
  export/import carries all of them. Agent-side only (each gateway keeps its own
  cert, token, and enrollment). Done when: killing the primary lands the agent on
  the secondary within the backoff budget, proven by e2e.
- **WP-A5 Config roaming via the gateway** (L, escalate). The agent pushes an
  encrypted blob of its config keyed by agent identity; a fresh install pulls it
  down after pairing. Open design question to settle first: a fresh install has a
  *new* identity and enrollment tickets are single-use, so roaming needs either a
  reusable pairing mode or an explicit "adopt previous agent" step. Done when: the
  dual-boot scenario syncs tunnels with no `.pfsetup` file, and the gateway
  provably cannot read the blob.
- **WP-A6 Synced IP allowlist / blocklist** (L, escalate). Both sides author
  entries (authored edits ride consent; sync rides arbitration), the gateway
  enforces at accept time before TLS. Entries: network (reduced via `sourceNet`,
  or it is decorative), temporary-with-expiry or permanent, and a note. Includes a
  Connections card on both sides, inclusion in setup export, and eviction of live
  connections on block. Done when: a block takes effect on live conns, survives
  restart, syncs to the peer, and the diagnostics leak test covers the new data
  channel with every field seeded.

---

## B. Engine plumbing (backend-capability skill; parallel with A)

- **WP-B1 Event bus + native notifications** (M). One typed internal event stream:
  agent/gateway connected and disconnected, player join/leave, port reassigned,
  update available. Windows toasts with per-category toggles; connection events on
  by default, verbose ones (player join) off. Everything in B2/B3 and several GUI
  features consume this, so it lands first. Done when: real transitions fire
  events, and toggles persist.
- **WP-B2 Discord + ntfy notifications** (S–M, needs B1). Outbound POSTs on
  selected categories. Opt-in (the privacy invariant: nothing leaves the machine by
  default), webhook URLs treated as secrets by the log scrubber.
- **WP-B3 Generic webhook macros** (M, needs B1). Custom URL plus JSON payload
  template per event, documented template variables. The "run a local command on
  event" variant is off by default and carries a visible warning; it is arbitrary
  code execution by design.
- **WP-B4 Minecraft status poller** (M). The agent polls its *own* local server
  (server list ping) per Minecraft-aware tunnel: MOTD, player count, version,
  favicon. Replaces the bare TCP dial as the health probe, feeds the history
  surfaces in C3, and deletes the "MC status polling" reality-check row.
  Done when: the row is gone and the poll interval is gentle enough to be
  invisible to the server.
- **WP-B5 Profiles, portable mode, backup, reset** (L, decompose). One
  config-directory abstraction under four features: named profiles with workspace
  switching, portable next-to-exe mode, one-file encrypted backup/restore
  (identity keys, config, stats, cert), identity export/import, and a
  reset-to-wizard that wipes everything cleanly (keys, analytics, certs) behind a
  confirm. Design the backup format before any UI. Done when: backup on machine A
  restores on machine B and the agent reconnects as itself.
- **WP-B6 Update checker, then auto-updater** (M, then L escalate). Checker first:
  poll GitHub releases, surface through B1 and the existing update badge. The
  auto-updater (download, verify signature, swap on restart) is a separate later
  WP gated on the release pipeline maturing.
- **WP-B7 Unix-socket IPC** (XL, overhaul skill). Port the named-pipe IPC to unix
  sockets with peer-credential auth. The single unlock for Linux/macOS actually
  running and for B8. Done when: the reality-check row about non-Windows builds is
  deleted.
- **WP-B8 Headless agent build** (M, needs B7 for non-Windows). A CLI-only agent
  binary (no Wails), config via file/flags, same pairing codes. Done when: a
  Docker container or Pi connects to a Windows gateway.
- **WP-B9 Silent test mode** (M). A temporary shadow tunnel on an ephemeral public
  port; the engine connects to itself through the public edge, moves bytes,
  reports pass/fail, tears down. Done when: one click answers "would a player get
  in right now?" without touching live tunnels.
- **WP-B10 Crash auto-bundle** (S, NEW). On engine panic, write a redacted
  diagnostics bundle locally (reusing the existing redaction path) and offer it on
  next start.
- **WP-B11 Scheduled tunnels** (M, NEW). Per-tunnel enable windows (days plus a
  time range); the engine flips the existing enable path on schedule. Useful for
  "the server is open evenings and weekends". Owner-authored and local, so no
  consent round-trip.

---

## C. GUI features (ui-change skill; ui-design-reviewer pass after each)

- **WP-C1 Tunnel detail subpage** (M). Click a tunnel, get a page: per-tunnel
  graphs, uptime/loss, settings shortcut. Prerequisite for C2, C3, and the
  per-tunnel analytics surfaces; do it early.
- **WP-C2 Tunnel health badge** (S, needs C1 for placement). Composite of jitter,
  packet loss, and uptime (all already measured) into one tri-state badge,
  respecting the never-color-alone rule.
- **WP-C3 MOTD, version, and icon history** (M, needs B4 and C1).
- **WP-C4 Connections table rework** (M). Sort, filter, and search on both tables;
  page size 30/50/100; IP and port split into separate columns with the port as a
  compact badge; a slightly stronger shade on the client column; an aggregated
  average-latency-per-player column. One `Traffic.tsx` pass with the full state
  matrix walk.
- **WP-C5 Connection approval and trusted devices** (L). Design decision first:
  Minecraft clients time out in seconds, so holding the TCP accept for a human is
  a dead end. The workable model: unknown sources connect but are flagged
  "pending"; approving trusts the network for the future, rejecting adds a block
  entry (rides A6's list). Device nicknames live on the trusted entry.
- **WP-C6 Tray icon** (M). Feasibility check first (Wails v2 tray support on
  Windows; a hidden spike exists). Quick stats, pause/resume all, open dashboard.
  Deletes the tray reality-check row and un-inerts the stored Minimize-to-tray and
  autostart settings, or removes them.
- **WP-C7 QR pairing, then LAN discovery** (S, then M). Render the pairing code as
  a QR (pure UI, the code already carries everything). Then mDNS: the gateway
  advertises, the agent lists found gateways; the full code with fingerprint is
  still required, discovery only saves the typing.
- **WP-C8 SRV generator and DDNS** (S, then M). SRV: pure UI, emit the record for
  the user's domain. DDNS (Cloudflare, DuckDNS, No-IP): opt-in outbound calls per
  the privacy invariant, tokens scrubbed from logs and bundles.
- **WP-C9 Theming ladder** (S then M then S). Accent color picker first; then
  density modes (comfortable / compact / focus) as a global row-height and padding
  switch independent of theme; then `.pftheme` export/import carrying tokens only.
  No custom-CSS import (see Parked).
- **WP-C10 Global search in the palette** (M). Ctrl+K today is
  navigation/commands only. Add data providers: tunnels, players, agents, log
  lines; ranked sections, same overlay.
- **WP-C11 Log bookmarks and annotations** (M). Persist locally keyed to log
  timestamps; survive restart; surface in the Activity screen.
- **WP-C12 Tunnel templates, cloning, grouping, multi-select** (M). Includes the
  quick actions and advanced filtering for multi-select.
- **WP-C13 Friendly hostname override** (S–M). Editable display name for a machine,
  synced live to the peer (rides the existing hostname exchange).
- **WP-C14 Local service discovery in the tunnel editor** (M, NEW). Enumerate
  listening TCP ports with owning process names (local Windows API only), suggest
  them when adding a tunnel; one click prefills `LocalAddr`. Kills the "what port
  is my server on" support question.
- **WP-C15 Onboarding checklist + in-app changelog** (S–M, NEW; changelog pairs
  with B6). First-run card: create a tunnel, invite a friend, see your first
  player. Changelog screen fed by release notes.
- **WP-C16 Apply-when-empty for disruptive changes** (M, NEW). An option on config
  proposals/applies that defers a port change until conntrack shows zero players
  on the affected tunnels, with an honest pending state. Very Minecraft: don't
  kick everyone for a rename.
- **WP-C17 Player notes and favorites** (S). Local fields on the player dossier.
- **WP-C18 Debug overlay + FPS counter** (S). Dev-gated, off in normal use.
- **WP-C19 Reset-statistics button** (S). Clears the analytics store behind a
  confirm; distinct from B5's full reset.
- **WP-C20 Re-pair in place** (S–M). Pasting a new pairing code over an existing
  gateway entry updates cert/token/address without the full wizard, for gateway
  moves and manual cert changes.
- **WP-C21 Port-conflict process lookup** (S–M). When a local bind fails or a
  public port clashes, name the owning process live in the error surface.

---

## D. Analytics and players (schema care; D0 gates the rest)

- **WP-D0 Role-blind DB decision** (S, design only). The analytics DB mixes
  gateway-role and agent-role rows with no way to tell them apart (polish #16).
  Decide the fix (role column on every table vs per-role data dirs) *before* D1/D2
  grow the schema, even if the implementation lands later. Every future query
  shape depends on this.
- **WP-D1 Peak tracking + historical connection counts** (M, after D0). Store
  maxima (concurrent players, bandwidth, connections) in the rollups; peak tiles
  and richer peak-hours surfaces.
- **WP-D2 Per-player bandwidth and latency history** (L, after D0). Store RTT and
  per-player byte samples with an explicit sampling policy that bounds rows per
  player per day; feeds per-player graphs in the dossier.
- **WP-D3 Username history + rename audit** (S). The resolver already refreshes
  names; verify the Mojang rate budget, add a name-history table if absent.
- **WP-D4 Chart event annotations** (M, NEW). The uptime events journal already
  records link and tunnel transitions; render them as markers on the bandwidth
  charts (reconnected, config pushed, port reassigned) with tooltips. Turns "why
  is there a dip at 3am" into a label.
- **WP-D5 Consent audit trail** (M, NEW). Persist proposals and decisions (author,
  diff, timestamp, outcome) and surface them in the Agents drill-in. Persisting
  proposals also gives polish #33 (a dropped session forgets a pending proposal) a
  natural fix.
- **WP-D6 Security events panel** (M, NEW; pairs with A6). The gateway's
  rate-limiting and admission machinery is invisible today. Surface current bans,
  pre-auth refusals, and gate pressure as a card plus log events, so an operator
  can see an attack and so A6's manual blocks live next to the automatic ones.
- **WP-D7 ISP / ASN display** (S–M). GeoLite2 ASN database alongside the city one,
  same configured/failed/pending states as the existing badge.
- **WP-D8 Session timeline polish** (S). The timeline exists; add zoom/scrubbing
  and cross-link the D4 markers.

---

## E. Engineering debt (background; pick up between larger WPs)

- **WP-E1 errcheck audit** (M). Turn the linter on, write the ~50 explicit
  `_ =` decisions with reasons (polish #21).
- **WP-E2 README drift fix** (S). Polish #3.
- **WP-E3 QUIC follow-ons** (M–L). Per-agent link-byte attribution (falls out of
  A3, or count at the stream layer), a connection-migration e2e, the Windows perf
  pass (polish #23–25).
- **WP-E4 Chaos/soak harness** (M–L, NEW). A nightly CI job driving agent↔gateway
  through a proxy that injects latency, loss, and disconnects; asserts reconnect
  behavior and no goroutine leaks over hours, not seconds.
- **WP-E5 Keyboard and accessibility pass** (M, NEW). Tab order, focus rings,
  aria labels across screens; reduced motion is already handled, the rest of a11y
  is not.

---

## Parked: decided against for now, and why

Recorded so the decision doesn't get re-litigated by a future session. Revisit
only if the stated reason changes.

- **3D rotating globe**: duplicates the shipped 2D map, fights the perf budget and
  the reduced-motion policy.
- **Plugin system (Lua/JS sandbox)**: a security surface as large as the app;
  revisit only after B1–B3 prove the event/webhook layer can't cover the long
  tail.
- **Discord Rich Presence**: low value for a server-side tool; would be the only
  always-on third-party integration.
- **InfluxDB endpoint**: the Prometheus endpoint already feeds Grafana.
- **FEC / ECN / adaptive heartbeat / adaptive congestion control**: nothing to
  build on reliable transports (the OS and quic-go own ECN; FEC on TCP/QUIC burns
  bandwidth; the fixed heartbeat is a deliberate liveness invariant). Reconsider
  FEC only if A2's UDP tunnels meet measurably lossy links.
- **"What's my latency" one-way test**: one-way latency is not honestly measurable
  without clock sync; the health surfaces already show RTT. If wanted, show RTT/2
  labeled as an estimate, inside existing surfaces, not as a button.
- **Multi-factor firewall checks**: undefined as written; needs a spec before it
  can be scheduled.
- **Custom-CSS import in theme sharing**: arbitrary CSS is an injection surface;
  `.pftheme` carries tokens only (C9).

---

## Cross-cutting gates (apply to every WP; not schedulable work)

- Anything visual: the ui-change skill's state-matrix walk plus a
  ui-design-reviewer pass. The responsive rules (minimum window, no overflow,
  truncation with tooltips, tables scroll before they compress) are enforced per
  change, not batched.
- Anything touching relay/transport/stats counting: the hot-path skill, burst
  floor before and after.
- Anything in section A: the wire-protocol skill and human confirmation first.
- New settings ship wired or hidden (the honesty rule). New data exports pass the
  no-leak test style with every channel seeded.
- Tuned numbers go to the architecture doc's table; reality-check rows are deleted
  in the same commit that implements them.
