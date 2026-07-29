// Dev-only Wails bridge mock. When the app runs in a plain browser (vite dev,
// no WebView2 host) `window.go`/`window.runtime` are absent and every binding
// throws. This installs a faithful fake so the UI can be exercised and
// screenshotted outside the desktop shell. It is a no-op when a real host is
// present, and tree-shaken out of production builds (guarded by import.meta.env.DEV
// at the call site in main.tsx).
//
// Choose a scenario with ?mock=agent|gateway|wizard (default: agent).
// Extra axes compose with any scenario:
//   &link=down       — the tunnel link is down (reconnecting / waiting)
//   &mode=attached   — a service owns the engine; gated actions reject
//   &fatal=1         — terminal engine error (bad credentials) surfaces
//   &engine=stopped  — the tunnel run is deliberately off (runState stopped, no
//                      fatal): the "am I on" axis. Distinct from &fatal (a crash)
//                      and &link=down (trying, failing): here nothing is trying.
//                      Start/Stop mutate it, so the controls can be driven live.
//   &fresh=1         — first-run data: no history, no peers yet
//   &fx=high         — high-fx glass: refraction filter on the palette
//   &fx=low          — low-fx glass: solid cards, no caustics/chart glow
//   &analytics=off   — daemon without the analytics store (unsupported state)
//   &fleet=multi|old — gateway only. multi: a five-agent fleet (good/fair/poor
//                      health spread) instead of the default single agent, to
//                      exercise the Agents roster, its sort/filter, and drill-in.
//                      The ListAgents roster ALSO carries two offline-enrolled
//                      agents and one revoked agent in every gateway scenario
//                      (they never appear in status.agents), so the roster's
//                      connected/offline/revoked states render without any axis.
//                      old: a pre-roster daemon (no agents array) → the Agents
//                      screen's honest-unavailable state (ListAgents also [])
//                      clamped: a fleet past the status caps — the same five
//                      agents, but agentsTotal says 31 and tunnelsTotal 97, so
//                      the title-bar pill and the Tunnels card count the fleet
//                      rather than the rows, and the Agents screen says which rows
//                      carry no live link state
//   &groups=spread|standby|both
//                    — gateway only. Seeds the Agents screen's tunnel-group cards:
//                      spread → a 3-member least-connections group with one
//                      ineligible member; standby → a 2-member failover group with
//                      a primary; both → both. No axis → the groups empty state.
//                      Pair with &fleet=multi so member agent names resolve.
//   &conflict=port|clone|both
//                    — gateway only. Seeds GatewayEvents so the Agents screen's
//                      conflict callout cards + event log have data: port →
//                      one port-reassigned event; clone → one clone-suspected
//                      event (references agent0, always linkable); both → both
//                      plus an older historical reassign. No axis → no events
//                      (event log shows its written-empty state, no cards).
//                      Pair with &fleet=multi so the port event links to its
//                      contesting agent in the roster.
//   &pending=in|out|both
//                    — config-consent (mutual approval) axes. On the gateway:
//                      in → SURVIVAL-RIG has proposed an edit awaiting your
//                      approval; out → you have proposed one awaiting the agent.
//                      On the agent: in → the gateway has proposed an edit
//                      awaiting yours; out → your own edit is awaiting the
//                      gateway; both → both at once. No axis → consent is live
//                      but nothing is pending (the editor's idle state).
//   &consent=off     — the peer is an older build (or shared-token) with no
//                      config-consent, so edits apply the old way and the
//                      approval surfaces must say so honestly rather than
//                      promise an approval that never comes.
//   &paired=0        — this machine has never been paired to a gateway, so the
//                      sidebar's role switcher can't become the agent and must
//                      route to setup instead (pair with ?mock=gateway)
//   &geo=off|empty|error|pending
//                    — GeoIP axes: unconfigured / configured-but-no-locations /
//                      database failed to open / picked but engine not restarted
//   &invites=friend|single|both
//                    — gateway only. Live pairing codes as roster rows: friend →
//                      a reusable labeled invite (2 enrolled); single → a
//                      pending single-use code expiring in ~7 min; both → both.
//                      Minting from Overview/the modal also appends a row, and
//                      revoke removes it — the real write-then-repoll loop. No
//                      axis → no invite rows (the roster is agents only).
//   &pause=mine|theirs|both
//                    — the default tunnel's pause bits: paused by this side,
//                      by the peer, or by both. Pause/Resume mutate this side's
//                      bit live (the peer's is only ever reported — each side
//                      clears its own). Gateway per-agent pause rides the same
//                      state; &consent=off doubles as the "peer predates
//                      pause" axis (roster pauseSupported false).
//   &mcstatus=on|empty
//                    — the default agent tunnel's health probe is mc-status, so
//                      the card carries the server's own report. on → 7/40 and
//                      an MOTD. empty → the server answered but omitted its
//                      player counts, so they must render "—" and not 0/0.
//                      No axis → the tcp probe: no report, no reading shown.
//   &tick=silent|reject|late
//                    — the FIRST status snapshot, which is the one App has no
//                      other instrument for (useTickStale only works after one
//                      has landed). Both Status() and the tick emitter are gated
//                      together, because the deadline is only reached when both
//                      are quiet. silent → Status() never settles and no ticks
//                      are emitted: the deadline state with no reason to show.
//                      reject → the same, but Status() rejects, so the state
//                      must print the reason verbatim. late → the first snapshot
//                      lands after ~9s, so the deadline state has to clear
//                      itself when it finally does. Retry re-runs the call, so
//                      silent/reject show the spinner→deadline loop again.
//   &crash=screen|shell
//                    — a deliberate render throw, for the two error boundaries.
//                      screen → the keyed screen switch throws: the inner
//                      boundary answers, Shell/sidebar/title bar stay alive, and
//                      navigating away resets it. shell → the console throws
//                      above the inner boundary: the outer boundary answers with
//                      its own minimal frame (no Shell to borrow). Read by
//                      CrashProbe (components/ErrorBoundary.tsx) — the mock
//                      cannot reach React, so the throw comes from a component;
//                      both halves are dropped from production builds.
//   &config=broken   — the config on disk would not load, so the running document
//                      is a substituted default. Composes with (and outranks)
//                      every scenario including wizard: the point of the axis is
//                      that the fault surface stands in FRONT of the wizard,
//                      whose completion would save over the file. "Set this
//                      machine up again" is the only way past it.
//   &tray=off|notice — the close fork (X button). Default: the tray preference
//                      is on and the one-time notice was already seen, so X
//                      with players shows the Minimize/Quit fork and X without
//                      them "hides" (logged). off → the preference is off: X
//                      with players shows the disconnect confirm, without them
//                      it quits. notice → the notice hasn't been seen, so an
//                      uneventful X shows the one-time tray notice instead.
//   &service=running|stopped
//                    — the Windows service is installed and in that state.
//                      No axis → not installed. Install/Start/Stop/Uninstall
//                      mutate it live, so the badge and the
//                      installed-but-stopped warning follow the buttons.
//   &autostart=on    — the HKCU Run entry exists, so Settings → System reads
//                      "Start on login" as on. No axis → off. Compose with
//                      &service to reach the overlap the row has to explain:
//                      &service=stopped alone → the row is disabled (the
//                      service already answers this question); with
//                      &autostart=on → both are on, so the row warn-washes and
//                      stays operable so it can be turned back off. Either
//                      service state reaches those here; on a real machine a
//                      running service usually means the GUI attached to it
//                      instead, and &mode=attached hides this row along with
//                      Close to tray.
//   &update=newer|none|odd|fail
//                    — Settings → About, with the release check ON (the axis
//                      turns it on; without one the toggle starts off and the
//                      row is absent, which is the shipped default). newer → a
//                      newer release exists. none → nothing published yet. odd
//                      → this build's version cannot be ranked against a tag,
//                      so the row states the tag and passes no verdict. fail →
//                      the request failed. No axis but the toggle flipped by
//                      hand → up to date.

type AnyFn = (...a: any[]) => any

export function installDevMock() {
  const w = window as any
  if (w.go && w.runtime) return // real host present

  const params = new URLSearchParams(location.search)
  const scenario = params.get('mock') || 'agent'
  const isGateway = scenario === 'gateway'
  const isWizard = scenario === 'wizard'
  const axisLinkDown = params.get('link') === 'down'
  const axisAttached = params.get('mode') === 'attached'
  const axisFatal = params.get('fatal') === '1'
  const axisStopped = params.get('engine') === 'stopped'
  const axisService = params.get('service') || '' // '' (not installed) | running | stopped
  const axisAutostart = params.get('autostart') === 'on' // the HKCU Run entry exists
  const axisUpdate = params.get('update') || '' // '' (up to date) | newer | none | odd | fail
  const axisFresh = params.get('fresh') === '1'
  const axisAnalyticsOff = params.get('analytics') === 'off'
  const axisUnpaired = params.get('paired') === '0'
  const axisGeo = params.get('geo') || '' // off | empty | error | pending
  const axisFleet = params.get('fleet') || '' // '' (single agent) | multi | old | clamped
  const axisGroups = params.get('groups') || '' // '' (none → empty state) | spread | standby | both
  const axisConflict = params.get('conflict') || '' // '' | port | clone | both
  const axisDeepLink = params.get('deeplink') === '1' // simulate a clicked pxf:// invite
  const axisPending = params.get('pending') || '' // '' | in | out | both
  const axisInvites = params.get('invites') || '' // '' | friend | single | both
  const axisConsentOff = params.get('consent') === 'off' // peer without config-consent
  const axisTray = params.get('tray') || '' // '' (on, notice seen) | off | notice
  const axisPause = params.get('pause') || '' // '' | mine | theirs | both
  const axisMCStatus = params.get('mcstatus') || '' // '' (tcp probe, no report) | on | empty
  const axisTick = params.get('tick') || '' // '' | silent | reject | late
  const TICK_LATE_MS = 9_000 // &tick=late: comfortably past App's FIRST_TICK_MS
  // The &crash axis is not a binding, so it cannot live in the App stub below:
  // the throw has to happen inside React. CrashProbe reads this flag; it and
  // this line are both dev-only, so neither reaches a shipped build.
  w.__pfCrash = params.get('crash') || '' // '' | screen | shell
  // &local=unset is the tunnel the gateway created by id: it holds a public port
  // but names no local address, because the wire has no field to carry one. It
  // must never read as "server down" — that sends the operator to restart a
  // machine that was never the problem.
  const axisLocal = params.get('local') || '' // '' (a named, reachable server) | unset
  const axisConfigBroken = params.get('config') === 'broken' // the file on disk would not load
  // Pause bits, mutable via the SetPause stub: mine/theirs per the default
  // tunnel, allMine the gateway's per-agent bit. &consent=off doubles as the
  // "peer predates pause" state (pauseSupported false on the roster).
  const pauseBits = {
    mine: axisPause === 'mine' || axisPause === 'both',
    theirs: axisPause === 'theirs' || axisPause === 'both',
    allMine: false,
  }
  const pauseMineNow = () => pauseBits.mine || pauseBits.allMine
  const pausedNow = () => pauseMineNow() || pauseBits.theirs
  const pausedByNow = () =>
    pauseMineNow() && pauseBits.theirs ? 'both' : pauseMineNow() ? 'mine' : pauseBits.theirs ? 'theirs' : ''
  // Latches when the mock's HideToTray runs, like the persisted flag it fakes.
  let trayNoticeSeen = axisTray !== 'notice'
  const fx = params.get('fx')
  if (fx) document.documentElement.dataset.fx = fx // &fx=high | &fx=low

  // ---- event bus (runtime.EventsOn/EventsEmit) ----
  const listeners: Record<string, AnyFn[]> = {}
  const emit = (name: string, ...data: any[]) => (listeners[name] || []).forEach(cb => cb(...data))

  // Window-control + clipboard stubs are explicit because the Proxy fallback
  // returns a plain () => {} — not a Promise — and callers .then() on some.
  w.runtime = new Proxy({
    EventsOn: (name: string, cb: AnyFn) => {
      (listeners[name] ||= []).push(cb)
      return () => { listeners[name] = (listeners[name] || []).filter(x => x !== cb) }
    },
    EventsOnMultiple: (name: string, cb: AnyFn) => w.runtime.EventsOn(name, cb),
    EventsEmit: (name: string, ...d: any[]) => emit(name, ...d),
    EventsOff: () => {},
    WindowIsMaximised: () => Promise.resolve(false),
    WindowMinimise: () => {},
    WindowToggleMaximise: () => {},
    WindowSetBackgroundColour: () => {},
    // The X button. Mirrors app/close.go closeDecision so clicking it in a
    // browser exercises the whole close fork: plain quit outside engine mode,
    // hide (logged) when the tray takes it, the pxf:close dialog otherwise.
    Quit: () => {
      const st = status()
      const players = st.connections?.length ?? 0
      const trayOn = config.UI.MinimizeToTray && st.mode === 'engine'
      if (st.mode !== 'engine' || (!trayOn && players === 0)) {
        console.info('[devmock] Quit() — plain quit')
        return
      }
      if (!trayOn) { emit('pxf:close', {kind: 'askquit', players}); return }
      if (players > 0) { emit('pxf:close', {kind: 'asktray', players}); return }
      if (!trayNoticeSeen) { emit('pxf:close', {kind: 'notice', players}); return }
      console.info('[devmock] Quit() — hidden to tray')
    },
    ClipboardSetText: (text: string) => navigator.clipboard.writeText(text).then(() => true, () => false),
  }, {get: (t, p) => (p in t ? (t as any)[p] : () => {})})

  // ---- deterministic traffic model ----
  // Every surface (chart history at any range, tick totals, peers) derives
  // from the same pure functions of absolute time, so polls at different
  // cadences are self-consistent. The fake install is ~32 days old; the fake
  // process has been up 5h; the link 3.2h.
  const INSTALLED_AT = axisFresh ? Date.now() - 90_000 : Date.now() - 32 * 86_400_000
  const PROCESS_START = axisFresh ? Date.now() - 90_000 : Date.now() - 5 * 3_600_000
  const LINK_UP_SINCE = axisFresh ? Date.now() - 60_000 : Date.now() - Math.round(3.2 * 3_600_000)

  const hash01 = (n: number): number => {
    let x = Math.imul(n ^ 0x9e3779b9, 0x85ebca6b)
    x ^= x >>> 13
    x = Math.imul(x, 0xc2b2ae35)
    x ^= x >>> 16
    return (x >>> 0) / 4294967296
  }
  // Smooth value noise over time, one octave per (period, seed).
  const vnoise = (t: number, periodMs: number, seed: number): number => {
    const p = t / periodMs
    const i = Math.floor(p), f = p - i
    const s = f * f * (3 - 2 * f)
    return hash01(i + seed * 374761) * (1 - s) + hash01(i + 1 + seed * 374761) * s
  }
  // Download rate (server → players), bytes/sec: evening-peaked diurnal curve
  // × weekend boost × two-octave noise × occasional multi-minute spikes.
  const downRate = (t: number): number => {
    if (t < INSTALLED_AT) return 0
    const d = new Date(t)
    const h = d.getHours() + d.getMinutes() / 60
    const evening = Math.exp(-((h - 20.5) ** 2) / 7)
    const afternoon = 0.5 * Math.exp(-((h - 15.5) ** 2) / 18)
    const weekend = d.getDay() === 0 || d.getDay() === 6 ? 1.5 : 1
    const diurnal = 0.06 + (evening + afternoon) * weekend
    const n = 0.45 + 1.2 * vnoise(t, 95_000, 1) * vnoise(t, 660_000, 2)
    const flutter = 0.8 + 0.45 * vnoise(t, 4_200, 6) // fast octave → visible OHLC ranges
    const spike = vnoise(t, 420_000, 3) > 0.82 ? 2.6 + 2 * vnoise(t, 40_000, 4) : 1
    return 250 * 1024 * diurnal * n * flutter * spike
  }
  const upRate = (t: number): number => downRate(t) / 100 * (0.8 + 0.4 * vnoise(t, 30_000, 5))
  // Player/connection count: tracks the traffic level with its own slow noise.
  // Recording "starts" 12 days ago so 30d/All show the unknown (-1) gap that
  // real pre-upgrade history produces.
  const CONN_SINCE = axisFresh ? INSTALLED_AT : Date.now() - 12 * 86_400_000
  const connCount = (t: number): number =>
    Math.max(0, Math.min(12, Math.floor(downRate(t) / (45 * 1024) + 2.5 * vnoise(t, 240_000, 7))))
  // Round-trip time (ms): a calm ~22ms baseline that drifts with slow noise and
  // rises a little under heavy load. Recorded from CONN_SINCE like the gauge.
  const rttMs = (t: number): number =>
    18 + 10 * vnoise(t, 180_000, 8) + 8 * vnoise(t, 26_000, 9) + downRate(t) / (250 * 1024) * 6

  // History = OHLC of 4 sub-samples per bucket, bytes = mean rate × duration.
  const bandwidthHistory = (windowMs: number, maxBuckets: number) => {
    const now = Date.now()
    maxBuckets = Math.max(1, Math.min(300, maxBuckets || 300))
    let bucketMs: number
    let t0: number
    if (!windowMs) {
      bucketMs = 86_400_000
      t0 = Math.floor(INSTALLED_AT / bucketMs) * bucketMs
      windowMs = now - t0
    } else {
      bucketMs = Math.max(50, Math.ceil(windowMs / maxBuckets / 50) * 50)
      t0 = Math.floor((now - windowMs) / bucketMs) * bucketMs
    }
    const buckets: any[] = []
    for (let t = t0; t <= now; t += bucketMs) {
      if (t + bucketMs <= INSTALLED_AT) continue
      const rs = [0, 1, 2, 3].map(i => downRate(t + (i + 0.5) * bucketMs / 4))
      const us = [0, 1, 2, 3].map(i => upRate(t + (i + 0.5) * bucketMs / 4))
      const mean = (a: number[]) => a.reduce((x, y) => x + y, 0) / a.length
      const durSec = Math.min(bucketMs, Math.max(0, now - t)) / 1000
      const cs = t + bucketMs <= CONN_SINCE
        ? null // pre-recording bucket: gauge unknown
        : [0, 1, 2, 3].map(i => connCount(t + (i + 0.5) * bucketMs / 4))
      // Both roles measure RTT now; only pre-recording buckets are unknown.
      const ps = t + bucketMs <= CONN_SINCE
        ? null
        : [0, 1, 2, 3].map(i => rttMs(t + (i + 0.5) * bucketMs / 4))
      // Identified players track the connection gauge minus the odd probe.
      const pls = cs ? cs.map(c => Math.max(0, c - (c > 2 ? 1 : 0))) : null
      // Packet loss: usually 0 with rare small blips, like the live mock.
      const ls = cs
        ? [0, 1, 2, 3].map(i => (vnoise(t + (i + 0.5) * bucketMs / 4, 60_000, 13) > 0.9 ? 0.4 : 0))
        : null
      buckets.push({
        t,
        out: Math.round(mean(rs) * durSec), in: Math.round(mean(us) * durSec),
        oo: rs[0], oh: Math.max(...rs), ol: Math.min(...rs), oc: rs[3],
        io: us[0], ih: Math.max(...us), il: Math.min(...us), ic: us[3],
        ...(cs
          ? {co: cs[0], ch: Math.max(...cs), cl: Math.min(...cs), cc: cs[3]}
          : {co: -1, ch: -1, cl: -1, cc: -1}),
        ...(ps
          ? {ro: ps[0], rh: Math.max(...ps), rl: Math.min(...ps), rc: ps[3]}
          : {ro: -1, rh: -1, rl: -1, rc: -1}),
        ...(pls
          ? {po: pls[0], ph: Math.max(...pls), pl: Math.min(...pls), pc: pls[3]}
          : {po: -1, ph: -1, pl: -1, pc: -1}),
        ...(ls
          ? {lo: ls[0], lh: Math.max(...ls), ll: Math.min(...ls), lc: ls[3]}
          : {lo: -1, lh: -1, ll: -1, lc: -1}),
      })
    }
    return {windowMs, bucketMs, buckets}
  }

  // Per-agent bandwidth (Agents drill-in): the gateway-wide history scaled to
  // each agent's share, with the RTT overlay shifted to that agent's baseline,
  // so every agent's chart looks distinct and agrees with its roster card.
  const AGENT_SCALE: Record<string, {factor: number; rtt: number}> = {
    agt_5w7d2k9rq3v8xj4m: {factor: 1, rtt: 24},
    'agt_7s9wv2rk4mq8x3dn': {factor: 0.4, rtt: 74},
    agt_cmp4t8vw2j6x9k3r: {factor: 0.06, rtt: 180},
    agt_ah3n7d5q9v2m8k4w: {factor: 0.6, rtt: 30},
    agt_f6x2r9km5v7d3p8t: {factor: 0.25, rtt: 55},
  }
  const agentBandwidth = (agentId: string, windowMs: number, maxBuckets: number) => {
    const {factor, rtt} = AGENT_SCALE[agentId] ?? {factor: 0.25, rtt: 40}
    const h = bandwidthHistory(windowMs, maxBuckets)
    const k = rtt / 24
    // Scale the byte/candle series by the agent's share and the connections and
    // players gauges too, so each agent's overlay is genuinely its own (the -1
    // "unknown" sentinel is preserved). RTT shifts to the agent's baseline; loss
    // is a rate, not volume, so it rides the shared curve.
    const g = (v: number): number => (v >= 0 ? Math.round(v * factor) : -1)
    return {
      ...h,
      buckets: h.buckets.map((b: any) => ({
        ...b,
        out: Math.round(b.out * factor), in: Math.round(b.in * factor),
        oo: b.oo * factor, oh: b.oh * factor, ol: b.ol * factor, oc: b.oc * factor,
        io: b.io * factor, ih: b.ih * factor, il: b.il * factor, ic: b.ic * factor,
        co: g(b.co), ch: g(b.ch), cl: g(b.cl), cc: g(b.cc),
        po: g(b.po), ph: g(b.ph), pl: g(b.pl), pc: g(b.pc),
        ...(b.ro >= 0 ? {ro: b.ro * k, rh: b.rh * k, rl: b.rl * k, rc: b.rc * k} : {}),
      })),
    }
  }

  // Lifetime peer records; the first two are the live connections' IPs.
  const now0 = Date.now()
  const peerSeeds = [
    {ip: '203.0.113.44', firstH: 42 * 24, lastH: 0, conns: 214, gib: 41.2},
    {ip: '198.51.100.7', firstH: 76, lastH: 0, conns: 11, gib: 2.4},
    {ip: '92.184.100.23', firstH: 31 * 24, lastH: 3, conns: 96, gib: 18.7},
    {ip: '84.113.9.201', firstH: 29 * 24, lastH: 26, conns: 71, gib: 12.9},
    {ip: '176.10.44.8', firstH: 21 * 24, lastH: 5, conns: 58, gib: 9.1},
    {ip: '203.0.113.101', firstH: 14 * 24, lastH: 49, conns: 22, gib: 4.6},
    {ip: '51.68.220.14', firstH: 6 * 24, lastH: 121, conns: 9, gib: 1.2},
    {ip: '188.166.32.75', firstH: 2, lastH: 1, conns: 2, gib: 0.15},
  ]
  const peerStats = () => peerSeeds.map(p => ({
    ip: p.ip,
    firstSeen: now0 - p.firstH * 3_600_000,
    lastSeen: p.lastH === 0 ? Date.now() : now0 - p.lastH * 3_600_000,
    totalBytesOut: Math.round(p.gib * 1024 ** 3),
    totalBytesIn: Math.round(p.gib * 1024 ** 3 / 96),
    totalConns: p.conns,
  }))

  // ---- mutable world state ----
  const tunnelID = 'a1b2c3d4e5f60718293a4b5c6d7e8f90'

  // Extra agents for the multi-agent gateway fleet (?mock=gateway&fleet=multi).
  // Each carries its own tunnels, live sessions, health, and remote identity so
  // the roster, its sort axes, the hostname filter, and the drill-in all have
  // real spread to render. With agent0 the fleet is five machines whose health
  // spans good / fair (jitter>30) / poor (loss>5). `upSinceMs` is stamped ONCE
  // here (a fixed epoch), never recomputed per tick, so the uptime readouts
  // actually advance like agent0's.
  const NOW0 = Date.now()
  const extraAgents = [
    {
      agentId: 'agt_7s9wv2rk4mq8x3dn', hostname: 'SURVIVAL-RIG',
      lan: ['10.0.0.9'], remote: '198.51.100.7', upSinceMs: NOW0 - 42 * 60_000,
      jitter: 41, loss: 1.3, rtt: 74, factor: 0.4,
      version: '0.0.9-dev', transport: 'per-conn', // older build → the roster "Update" badge
      tunnels: [
        {id: 'bb11223344556677889900aabbccddee', name: 'Survival', port: 25566, localUp: true, bandwidthLimitMbps: 100, bandwidthLimitScope: 'combined'},
        // Lobby sits on 25572 because it asked for 25569 and CREATIVE-HUB holds it —
        // the &conflict=port event below, and the case the reclaim card acts on.
        {id: 'cc22334455667788990011aabbccddff', name: 'Lobby', port: 25572, localUp: false},
      ],
      conns: [{
        id: 8801, tunnelName: 'Survival', clientAddr: '92.99.11.4:53001',
        startedAt: NOW0 - 600_000, bytesIn: 900_000, bytesOut: 5_400_000,
        playerName: 'DiggerDan', playerUuid: '11112222-3333-4444-5555-666677778888', rttMs: 76,
      }] as any[],
    },
    {
      agentId: 'agt_cmp4t8vw2j6x9k3r', hostname: 'MINIGAMES-PI',
      lan: ['192.168.1.30'], remote: '92.184.100.23', upSinceMs: NOW0 - 12 * 60_000,
      jitter: 22, loss: 7.5, rtt: 180, factor: 0.06,
      version: '0.1.0-dev', transport: 'quic',
      tunnels: [{id: 'dd33445566778899001122aabbccdd00', name: 'Minigames', port: 25568, localUp: true}],
      conns: [] as any[],
    },
    {
      agentId: 'agt_ah3n7d5q9v2m8k4w', hostname: 'CREATIVE-HUB',
      lan: ['10.0.0.12'], remote: '84.113.9.201', upSinceMs: NOW0 - 6 * 3_600_000,
      jitter: 5, loss: 0, rtt: 30, factor: 0.6,
      version: '0.1.0-dev', transport: 'mux',
      tunnels: [{id: 'ab00cd11ef22ab33cd44ef5566778890', name: 'Creative', port: 25569, localUp: true}],
      conns: [{
        id: 8811, tunnelName: 'Creative', clientAddr: '176.10.44.8:51900',
        startedAt: NOW0 - 1_800_000, bytesIn: 1_400_000, bytesOut: 9_800_000,
        playerName: 'BuilderBeth', playerUuid: '22223333-4444-5555-6666-777788889999', rttMs: 31,
      }] as any[],
    },
    {
      agentId: 'agt_f6x2r9km5v7d3p8t', hostname: 'SKYBLOCK-BOX',
      lan: ['192.168.0.20'], remote: '51.68.220.14', upSinceMs: NOW0 - 95 * 60_000,
      jitter: 34, loss: 0.4, rtt: 55, factor: 0.25,
      version: '0.1.0-dev', transport: 'quic',
      tunnels: [{id: 'c0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f', name: 'Skyblock', port: 25570, localUp: true, bandwidthLimitMbps: 20, bandwidthLimitScope: 'per-connection'}],
      conns: [{
        id: 8821, tunnelName: 'Skyblock', clientAddr: '203.0.113.77:52210',
        startedAt: NOW0 - 300_000, bytesIn: 420_000, bytesOut: 2_100_000,
        playerName: 'IslandIvy', playerUuid: '33334444-5555-6666-7777-88889999aaaa', rttMs: 57,
      }] as any[],
    },
  ]
  const agentHealthOf = (jitter: number, loss: number): string =>
    loss > 5 || jitter > 100 ? 'bad' : loss > 1 || jitter > 30 ? 'warn' : 'good'

  // ---- tunnel groups (ListGroups / SaveGroup / DeleteGroup / SetTunnelGroup) ----
  // A group is {spec, members}; members reference the extraAgents by (agentId,
  // tunnelId). The CRUD stubs mutate this array so create/edit/delete and tagging
  // round-trip live against the next ListGroups poll — the real write-then-repoll
  // loop. Seeded only when &groups asks, so the empty state renders by default.
  type MockMember = {agentId: string; tunnelId: string; eligible: boolean; isPrimary: boolean; liveConns: number}
  type MockGroup = {spec: {id: string; name: string; publicPort: number; proto: string; policy: string; offlineMotd: string}; members: MockMember[]}
  const mockGroups: MockGroup[] = []
  if (axisGroups === 'spread' || axisGroups === 'both') {
    mockGroups.push({
      spec: {id: 'grp_spread01', name: 'Survival cluster', publicPort: 25565, proto: 'tcp', policy: 'spread', offlineMotd: 'Back soon'},
      members: [
        {agentId: 'agt_7s9wv2rk4mq8x3dn', tunnelId: 'bb11223344556677889900aabbccddee', eligible: true, isPrimary: false, liveConns: 3},
        {agentId: 'agt_ah3n7d5q9v2m8k4w', tunnelId: 'ab00cd11ef22ab33cd44ef5566778890', eligible: true, isPrimary: false, liveConns: 2},
        {agentId: 'agt_cmp4t8vw2j6x9k3r', tunnelId: 'dd33445566778899001122aabbccdd00', eligible: false, isPrimary: false, liveConns: 0},
      ],
    })
  }
  if (axisGroups === 'standby' || axisGroups === 'both') {
    mockGroups.push({
      spec: {id: 'grp_standby1', name: 'Failover pool', publicPort: 25567, proto: 'tcp', policy: 'standby', offlineMotd: ''},
      members: [
        {agentId: 'agt_f6x2r9km5v7d3p8t', tunnelId: 'c0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f', eligible: true, isPrimary: true, liveConns: 5},
        {agentId: 'agt_7s9wv2rk4mq8x3dn', tunnelId: 'cc22334455667788990011aabbccddff', eligible: true, isPrimary: false, liveConns: 0},
      ],
    })
  }
  const reelectPrimary = (g: MockGroup) => {
    if (g.spec.policy !== 'standby') { g.members.forEach(m => (m.isPrimary = false)); return }
    let taken = false
    for (const m of g.members) { m.isPrimary = m.eligible && !taken; if (m.isPrimary) taken = true }
  }

  // ---- agent directory (roster: ListAgents / RenameAgent / SetAgentScope /
  //      RevokeAgent) ----
  // The gateway's enrollment store as the roster sees it: every enrolled agent,
  // online or offline. Connected entries mirror the live fleet (agent0 + the
  // &fleet=multi extras) by agentId, so the roster, the status.agents health
  // join, and the drill-in all agree; the offline and revoked entries exist ONLY
  // here (never in status.agents), which is what exercises the roster's
  // offline/revoked states. Renames, scope edits, and revokes land in
  // `agentOverrides` and surface on the next ListAgents poll — the same
  // write-then-repoll loop the real bindings drive. Every agentId wears the
  // key-derived agt_ prefix, matching link.AgentID / the gateway store.
  const mkView = (o: any) => ({
    agentId: o.agentId, nickname: o.nickname || '',
    revoked: !!o.revoked, scopePorts: o.scopePorts || [], scopeTunnels: o.scopeTunnels || [],
    issuedAtMs: o.issuedAtMs || 0, connected: !!o.connected, hostname: o.hostname || '',
    remoteIp: o.remoteIp || '', linkUpSinceMs: o.linkUpSinceMs || 0, tunnels: o.tunnels || 0,
    // version/transport come from the live session — empty for an offline agent, so
    // the roster shows no "Update" badge and the drill-in transport chip renders "—".
    version: o.version || '', transport: o.transport || '',
    // Consent proposals are session-scoped, so only a connected agent can carry one:
    // these drive the roster's Review / Proposed badges. Read lazily (the seeds are
    // declared below), which is fine — listAgents() only runs on demand.
    pendingInbound: !!o.connected && o.agentId === CONSENT_AGENT && !!gwPendingIn,
    pendingOutbound: !!o.connected && o.agentId === CONSENT_AGENT && !!gwPendingOut,
    // Pause: the default agent carries the axis-driven bits; a connected peer
    // supports pause unless &consent=off (which models an old peer generally).
    pausedAll: o.agentId === 'agt_5w7d2k9rq3v8xj4m' && pauseBits.allMine,
    pausedAllBy: o.agentId === 'agt_5w7d2k9rq3v8xj4m' && pauseBits.allMine ? 'mine' : '',
    pauseSupported: !!o.connected && !axisConsentOff,
  })
  // Enrollment metadata for the connected extras (nickname/scope/enrolled age),
  // keyed by the same agentId the live fleet and AGENT_SCALE use.
  const EXTRA_ENROLL: Record<string, {nick: string; ports: number[]; tuns: string[]; days: number}> = {
    'agt_7s9wv2rk4mq8x3dn': {nick: 'Survival server', ports: [25566, 25569, 25572], tuns: [], days: 26},
    agt_cmp4t8vw2j6x9k3r: {nick: '', ports: [25568], tuns: [], days: 12},
    agt_ah3n7d5q9v2m8k4w: {nick: 'Creative hub', ports: [], tuns: [], days: 47},
    agt_f6x2r9km5v7d3p8t: {nick: 'Skyblock island', ports: [], tuns: ['c0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f'], days: 9},
  }
  const connectedViews = () => {
    if (!state.linkUp) return []
    // agent0: the default connected agent — no nickname (→ hostname title),
    // unrestricted scope.
    const a0 = mkView({
      agentId: 'agt_5w7d2k9rq3v8xj4m', connected: true, hostname: 'DESKTOP-DEV',
      remoteIp: '84.23.101.7', linkUpSinceMs: LINK_UP_SINCE, tunnels: 1, issuedAtMs: INSTALLED_AT,
      version: '0.1.0-dev', transport: 'mux', // matches the gateway version → no Update badge
    })
    if (axisFleet !== 'multi') return [a0]
    const extras = extraAgents.map(a => {
      const e = EXTRA_ENROLL[a.agentId] ?? {nick: '', ports: [], tuns: [], days: 20}
      return mkView({
        agentId: a.agentId, nickname: e.nick, connected: true,
        hostname: a.hostname, remoteIp: a.remote, linkUpSinceMs: a.upSinceMs,
        tunnels: a.tunnels.length, scopePorts: e.ports, scopeTunnels: e.tuns,
        issuedAtMs: now0 - e.days * 86_400_000, version: a.version, transport: a.transport,
      })
    })
    return [a0, ...extras]
  }
  // Enrolled agents that are NOT currently connected, plus one revoked — always
  // in the roster regardless of link state, so their states always render.
  const offlineViews = [
    mkView({agentId: 'agt_h4t2q9zm', nickname: 'Backup box', hostname: 'BACKUP-NAS', scopePorts: [25580], issuedAtMs: now0 - 18 * 86_400_000}),
    mkView({agentId: 'agt_b7k3n5pd', hostname: 'SEASONAL-SMP', issuedAtMs: now0 - 40 * 86_400_000}),
  ]
  const revokedViews = [
    mkView({agentId: 'agt_x2m8r6ct', nickname: 'Old laptop', revoked: true, hostname: 'LAPTOP-OLD', issuedAtMs: now0 - 63 * 86_400_000}),
  ]
  const knownAgentIds = new Set<string>([
    'agt_5w7d2k9rq3v8xj4m', ...extraAgents.map(a => a.agentId),
    ...offlineViews.map(v => v.agentId), ...revokedViews.map(v => v.agentId),
  ])
  // Live pairing codes (ListInvites / RevokeInvite / IssuePairingCode): a plain
  // mutable array so mint and revoke land on the next poll, mirroring the real
  // write-then-repoll loop. Handles are fixed fakes; codes carry a fake ticket.
  const mkInvite = (o: any) => ({
    handle: o.handle, label: o.label || '', reusable: !!o.reusable,
    issuedAtMs: o.issuedAtMs || now0, expiresAtMs: o.expiresAtMs || 0, uses: o.uses || 0,
    scopePorts: o.scopePorts || [], scopeTunnels: o.scopeTunnels || [],
    code: `pxf://play.example.com:8474/v1/pair/tkt_${o.handle}f00d600df00d600d#sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08`,
  })
  const invites: any[] = []
  if (axisInvites === 'friend' || axisInvites === 'both') {
    invites.push(mkInvite({handle: 'a3f81c2e', label: 'friends', reusable: true, uses: 2, issuedAtMs: now0 - 12 * 86_400_000}))
  }
  if (axisInvites === 'single' || axisInvites === 'both') {
    invites.push(mkInvite({handle: '7b90d4e6', expiresAtMs: now0 + 7 * 60_000, issuedAtMs: now0 - 3 * 60_000}))
  }

  // Mutations from the write bindings, applied over the composed roster.
  const agentOverrides = new Map<string, any>()
  const applyOverride = (v: any) => {
    const o = agentOverrides.get(v.agentId)
    if (!o) return v
    return {
      ...v,
      ...(o.nickname !== undefined ? {nickname: o.nickname} : {}),
      ...(o.scopePorts !== undefined ? {scopePorts: o.scopePorts} : {}),
      ...(o.scopeTunnels !== undefined ? {scopeTunnels: o.scopeTunnels} : {}),
      ...(o.revoked !== undefined ? {revoked: o.revoked, connected: o.revoked ? false : v.connected} : {}),
    }
  }
  const listAgents = () => {
    // The roster is a gateway surface; an agent (or a pre-roster daemon) has none.
    if (state.role !== 'gateway' || axisFleet === 'old') return []
    return [...connectedViews(), ...offlineViews, ...revokedViews].map(applyOverride)
  }

  // ---- gateway-authoritative desired config (AgentDesiredConfig drill-in panel) ----
  // Agents return their gateway-held tunnel set + generation; an unknown id gets
  // the zero view. SEASONAL-SMP has no config yet → the empty "will seed on
  // connect" state; agent0 and the connected extras mirror their live tunnels so
  // the drill-in list matches what the fleet is serving.
  const toSpec = (t: any) => ({
    id: t.id, name: t.name, type: 'tcp', publicPort: t.port,
    bandwidthLimitMbps: t.bandwidthLimitMbps ?? 0, bandwidthLimitScope: t.bandwidthLimitScope ?? '',
  })
  const OFFLINE_CONFIG: Record<string, {gen: number; tunnels: any[]}> = {
    agt_h4t2q9zm: {gen: 4, tunnels: [{id: 'e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6', name: 'Backup', port: 25580}]},
    agt_b7k3n5pd: {gen: 0, tunnels: []}, // enrolled, no config yet → empty state
    agt_x2m8r6ct: {gen: 2, tunnels: []},
  }
  // consentState decorates a config view with the live session's approval state.
  // Only a connected agent can be consent-capable; an offline one has no
  // session, so the editor must fall back to its honest unavailable state rather
  // than offer a Save that could never be delivered.
  const consentState = (id: string, connected: boolean) => ({
    consentActive: connected && consentLive,
    pendingInbound: id === CONSENT_AGENT ? gwPendingIn ?? undefined : undefined,
    pendingOutbound: id === CONSENT_AGENT ? gwPendingOut ?? undefined : undefined,
  })
  const agentDesiredConfig = (id: string) => {
    if (state.role !== 'gateway') return {generation: 0, tunnels: [], consentActive: false}
    if (id === 'agt_5w7d2k9rq3v8xj4m') {
      return {generation: 2, tunnels: [toSpec({id: tunnelID, name: 'Minecraft', port: 25565})], ...consentState(id, state.linkUp)}
    }
    const extra = extraAgents.find(a => a.agentId === id)
    if (extra) return {generation: 3 + extra.tunnels.length, tunnels: extra.tunnels.map(toSpec), ...consentState(id, true)}
    const off = OFFLINE_CONFIG[id]
    if (off) return {generation: off.gen, tunnels: off.tunnels.map(toSpec), ...consentState(id, false)}
    return {generation: 0, tunnels: [], consentActive: false} // unknown id
  }

  // ---- config consent (mutual approval) ----
  // Pending proposals are session-scoped in the real engine, so they live here as
  // plain mutable state: approve/reject clear them and the next poll shows the
  // result, mirroring the real write-then-repoll loop. The proposed sets are a
  // visible *diff* against what the agent serves today (a port move plus an added
  // tunnel), so the approval surfaces have something to render rather than a set
  // identical to the current one.
  // SURVIVAL-RIG: enrolled, connected, and already the reclaim card's subject, so the
  // consent surfaces land on an agent every other gateway axis also populates.
  const CONSENT_AGENT = 'agt_7s9wv2rk4mq8x3dn'
  // A proposal is only interesting if it *differs* from what is served today: move
  // the first tunnel's port and add one, so the approval diff has both kinds of row.
  const consentTunnels = (base: any[], addName: string) => [
    ...base.map((t, i) => (i === 0 ? {...toSpec(t), publicPort: t.port + 100} : toSpec(t))),
    {id: `tnl_${addName.toLowerCase()}`, name: addName, type: 'tcp', publicPort: 0, bandwidthLimitMbps: 0, bandwidthLimitScope: ''},
  ]
  const wantIn = axisPending === 'in' || axisPending === 'both'
  const wantOut = axisPending === 'out' || axisPending === 'both'
  const consentLive = !axisConsentOff
  // Gateway side: inbound = the agent proposed, awaiting this operator.
  //               outbound = this operator proposed, awaiting the agent.
  const gwBase = extraAgents.find(a => a.agentId === CONSENT_AGENT)?.tunnels ?? []
  let gwPendingIn: any = wantIn && consentLive
    ? {proposalId: 'prp_a1b2c3d4', baseGen: 5, tunnels: consentTunnels(gwBase, 'Creative'), atMs: Date.now() - 4 * 60_000}
    : null
  let gwPendingOut: any = wantOut && consentLive
    ? {proposalId: 'prp_e5f6a7b8', baseGen: 5, tunnels: consentTunnels(gwBase, 'Staging'), atMs: Date.now() - 90_000}
    : null
  // Agent side: incoming = the gateway proposed, awaiting this machine.
  //             outgoing = this machine proposed, awaiting the gateway.
  const agBase = [{id: tunnelID, name: 'Minecraft', port: 25565}]
  let agPendingIn: any = wantIn && consentLive
    ? {proposalId: 'prp_c3d4e5f6', tunnels: consentTunnels(agBase, 'Creative'), atMs: Date.now() - 3 * 60_000}
    : null
  let agPendingOut: any = wantOut && consentLive
    ? {proposalId: 'prp_b2c3d4e5', tunnels: consentTunnels(agBase, 'Staging'), atMs: Date.now() - 45_000}
    : null
  let agLastDecline = ''

  // ---- gateway conflict / auto-fix events (GatewayEvents) ----
  // Seeded once from the &conflict axis. Both event kinds ARE conflicts (there
  // is no benign kind), so with no axis the ring is empty — the event log shows
  // its written-empty state and no conflict cards appear. GatewayEvents(seq) is
  // incremental by cursor, so the first poll returns all seeds and later polls
  // return [] (a quiet ring), which is exactly what the since-cursor tail wants.
  let evSeq = 0
  const gwEvents: any[] = []
  const evNow = Date.now()
  const pushEvent = (o: any) => gwEvents.push({seq: ++evSeq, ...o})
  if (axisConflict === 'both') {
    pushEvent({
      timeMs: evNow - 52 * 60_000, kind: 'port-reassigned',
      agentId: 'agt_ah3n7d5q9v2m8k4w', tunnelId: 'ab00cd11ef22ab33cd44ef5566778890',
      requestedPort: 25569, actualPort: 25574,
      message: 'Requested public port 25569 was busy; CREATIVE-HUB’s "Creative" tunnel was auto-assigned 25574.',
    })
  }
  if (axisConflict === 'port' || axisConflict === 'both') {
    // Reclaimable under &fleet=multi: the requester (SURVIVAL-RIG) and the agent that
    // holds the requested 25569 (CREATIVE-HUB) are both enrolled, so the card offers
    // the trade. Without &fleet=multi neither is in the roster and the card stays
    // informational — the honest gate, not a hidden action.
    pushEvent({
      timeMs: evNow - 8 * 60_000, kind: 'port-reassigned',
      agentId: 'agt_7s9wv2rk4mq8x3dn', tunnelId: 'cc22334455667788990011aabbccddff',
      requestedPort: 25569, actualPort: 25572,
      message: 'Requested public port 25569 was already bound; SURVIVAL-RIG’s "Lobby" tunnel was auto-assigned 25572 instead.',
    })
  }
  if (axisConflict === 'clone' || axisConflict === 'both') {
    pushEvent({
      timeMs: evNow - 3 * 60_000, kind: 'clone-suspected', agentId: 'agt_5w7d2k9rq3v8xj4m',
      message: 'Agent "agt_5w7d2k9rq3v8xj4m" reconnected from 203.0.113.77:52210 while still linked from 84.23.101.7:53880 — possible cloned identity key in use on two machines.',
    })
  }
  const gatewayEvents = (sinceSeq: number) => gwEvents.filter(e => e.seq > (sinceSeq || 0))

  // baseUp is "would the link be up if the run were on" — Start restores to it;
  // up folds in the deliberate stop, which Stop drops to.
  const baseUp = !isWizard && !axisLinkDown && !axisFatal
  const up = baseUp && !axisStopped
  const state = {
    role: isGateway ? 'gateway' : 'agent',
    linkUp: up,
    agentConnected: up,
    runState: axisStopped ? 'stopped' : 'running',
    rtt: 24,
    bytesIn: 0,
    bytesOut: 0,
    conns: [
      {id: 1, tunnelName: 'Minecraft', clientAddr: '203.0.113.44:51422', startedAt: Date.now() - 92000, bytesIn: 1_240_000, bytesOut: 8_900_000, playerName: 'Notch', playerUuid: '069a79f4-44e9-4726-a5be-fca90e38aaf5', rttMs: 34},
      {id: 2, tunnelName: 'Minecraft', clientAddr: '198.51.100.7:60011', startedAt: Date.now() - 15000, bytesIn: 120_000, bytesOut: 640_000, playerName: 'jeb_', playerUuid: '853c80ef-3c37-49fd-aa49-938b674adae6', rttMs: 58},
    ] as any[],
  }
  const config = {
    Role: state.role,
    Agent: {AgentID: 'agt_5w7d2k9rq3v8xj4m', GatewayHost: 'play.example.com', GatewayPort: 8474, CertFingerprint: 'sha256:ab', Transport: 'auto',
      Tunnels: [{ID: tunnelID, Name: 'Minecraft', Type: 'tcp', LocalAddr: axisLocal === 'unset' ? '' : '127.0.0.1:25565', PublicPort: 25565, Enabled: true,
        Options: {MinecraftAware: true, ProxyProtocolV2: false, OfflineMOTD: 'Server is offline — back soon',
          BandwidthLimitMbps: 40, BandwidthLimitScope: 'per-direction',
          HealthProbe: axisMCStatus ? 'mc-status' : '', HealthIntervalSec: 0, HealthTimeoutSec: 0, HealthMaxFailed: 0}}]},
    Gateway: {BindAddr: '0.0.0.0', ControlPort: 8474, PublicHost: 'play.example.com', PortAllowlist: [],
      MaxConnsGlobal: 4096, MaxConnsPerIP: 32, AuthAttemptsPerMin: 10, MaxTunnelsPerAgent: 32},
    Metrics: {PrometheusEnabled: false, PrometheusAddr: '127.0.0.1:9464'},
    Logging: {Level: 'info', FileEnabled: true},
    UI: {Theme: localStorage.getItem('pf-theme') || 'dark', MinimizeToTray: axisTray !== 'off', Autostart: axisAutostart,
      TrayNoticeShown: axisTray !== 'notice', UpdateCheck: axisUpdate !== ''},
    Analytics: {RetentionDays: 180, MojangLookups: true, GeoIPCityPath: '', GeoIPASNPath: ''},
  }
  // A machine that has never been paired holds no agent credentials — the
  // gateway half of the config is complete, the agent half is empty. This is
  // the one state where the role switcher cannot simply flip.
  if (axisUnpaired) {
    config.Agent.GatewayHost = ''
    config.Agent.CertFingerprint = ''
  }

  // Link-quality mock: healthy jitter/loss on the agent; the gateway leaves
  // them unknown (-1) like the real backend, since RTT is measured agent-side.
  const jitterMs = () => 2 + 3 * vnoise(Date.now(), 20_000, 11) + 2 * vnoise(Date.now(), 5_000, 12)
  const lossPct = () => (vnoise(Date.now(), 60_000, 13) > 0.9 ? 0.4 + vnoise(Date.now(), 8_000, 14) : 0)
  const healthOf = (jitter: number, loss: number): string => {
    if (!state.linkUp) return 'bad'
    if (loss > 5 || jitter > 100) return 'bad'
    if (loss > 1 || jitter > 30 || Date.now() - LINK_UP_SINCE < 60_000) return 'warn'
    return 'good'
  }

  const status = () => {
    const jitter = isWizard || !state.linkUp ? -1 : jitterMs()
    const loss = isWizard || !state.linkUp ? -1 : lossPct()
    // Read the LIVE role, not the URL scenario: the sidebar's role switcher
    // flips state.role, and every identity below has to follow it or the app
    // would keep wearing the old role's peer after a switch.
    const gw = state.role === 'gateway'

    // Gateway fleet: agent0 is the always-present peer (its identity mirrors the
    // coarse peer* fields so the other gateway screens stay populated); the
    // extras appear only under &fleet=multi. Tunnels and connections flatten
    // across the fleet, each stamped with its agentId so the Agents drill-in can
    // scope them. A downed gateway link means zero agents. The agent role sends
    // no roster at all (undefined), like the real backend.
    const agent0 = {
      agentId: 'agt_5w7d2k9rq3v8xj4m', hostname: 'DESKTOP-DEV', lanIps: ['10.0.0.5'], remoteIp: '84.23.101.7',
      linkUpSinceMs: LINK_UP_SINCE, rttMillis: state.rtt, jitterMillis: jitter, packetLossPct: loss,
      healthScore: healthOf(jitter, loss),
      linkBytesIn: Math.round(state.bytesIn * 1.06) + 2_400_000,
      linkBytesOut: Math.round(state.bytesOut * 1.06) + 3_100_000,
      tunnels: 1, players: state.conns.length,
    }
    const fleetExtras = gw && state.linkUp && (axisFleet === 'multi' || axisFleet === 'clamped') ? extraAgents : []
    const gwAgents = gw && state.linkUp ? [agent0, ...fleetExtras.map(a => ({
      agentId: a.agentId, hostname: a.hostname, lanIps: a.lan, remoteIp: a.remote,
      linkUpSinceMs: a.upSinceMs, rttMillis: a.rtt, jitterMillis: a.jitter, packetLossPct: a.loss,
      healthScore: agentHealthOf(a.jitter, a.loss),
      linkBytesIn: Math.round(a.factor * (state.bytesIn * 1.06 + 2_400_000) * 0.7),
      linkBytesOut: Math.round(a.factor * (state.bytesOut * 1.06 + 3_100_000)),
      tunnels: a.tunnels.length, players: a.conns.length,
    }))] : []
    // &fleet=old simulates a pre-roster background service: the gateway reports
    // no agents array at all, so the Agents screen shows its honest-unavailable
    // state (told apart from a live gateway with zero agents connected).
    const rosterOld = gw && axisFleet === 'old'
    const gwTunnels = gw
      ? (state.linkUp
        ? [{id: tunnelID, name: 'Minecraft', publicPort: 25565, localUp: true, localKnown: true, agentId: 'agt_5w7d2k9rq3v8xj4m', bandwidthLimitMbps: 40, bandwidthLimitScope: 'per-direction', paused: pausedNow(), pausedBy: pausedByNow()},
          ...fleetExtras.flatMap(a => a.tunnels.map((t: any) => ({
            id: t.id, name: t.name, publicPort: t.port, localUp: t.localUp, localKnown: true, agentId: a.agentId,
            bandwidthLimitMbps: t.bandwidthLimitMbps ?? 0, bandwidthLimitScope: t.bandwidthLimitScope ?? '',
          })))]
        : [])
      : [{id: tunnelID, name: 'Minecraft', publicPort: state.linkUp ? 25565 : 0,
        localUp: axisLocal !== 'unset', localKnown: true, needsLocalAddr: axisLocal === 'unset',
        paused: pausedNow(), pausedBy: pausedByNow(),
        // The mc-status probe's report. Absent without the axis (a tcp-probed
        // tunnel has nothing to say); `empty` is the server that answered but
        // omitted its counts, which must read "—" rather than 0/0.
        serverStatusKnown: axisMCStatus !== '',
        serverMotd: axisMCStatus ? 'A Minecraft Server — season 4' : '',
        serverPlayersOnline: axisMCStatus === 'empty' ? -1 : 7,
        serverPlayersMax: axisMCStatus === 'empty' ? -1 : 40,
        serverVersion: axisMCStatus ? '1.21.4' : ''}]
    const gwConns = gw
      ? (state.linkUp
        ? [...state.conns.map((c: any) => ({...c, agentId: 'agt_5w7d2k9rq3v8xj4m'})),
          ...fleetExtras.flatMap(a => a.conns.map(c => ({...c, agentId: a.agentId})))]
        : [])
      : (state.linkUp ? state.conns : [])

    return {
    mode: isWizard ? 'wizard' : axisAttached ? 'attached' : 'engine',
    role: isWizard ? '' : state.role,
    // A real Windows path, escaped once — the config-file row is sized against
    // exactly this string, so an over-escaped mock would flatter it.
    version: '0.1.0-dev', hostname: 'DESKTOP-DEV', pid: 4242, configPath: 'C:\\Users\\you\\AppData\\Roaming\\proxyforward\\config.toml',
    // Composes with any scenario, and outranks all of them: the fault surface
    // stands in front of the wizard, which is the whole point of the axis.
    configLoadError: axisConfigBroken
      ? 'invalid config C:\\Users\\you\\AppData\\Roaming\\proxyforward\\config.toml: agent.gateway_host: required (pair with a gateway first)'
      : '',
    linkUp: state.linkUp, rttMillis: state.linkUp ? state.rtt : 0, agentConnected: state.agentConnected,
    transport: !isWizard && !gw && state.linkUp ? 'quic' : '',
    jitterMillis: jitter,
    packetLossPct: loss,
    healthScore: isWizard ? 'unknown' : healthOf(jitter, loss),
    peerHostname: isWizard ? '' : gw ? 'DESKTOP-DEV' : 'GATEWAY-VPS-01',
    publicIp: isWizard ? '' : gw ? '203.0.113.9' : '84.23.101.7',
    peerPublicIp: isWizard ? '' : gw ? '84.23.101.7' : 'play.example.com',
    localLanIps: isWizard ? [] : gw ? ['10.0.0.5'] : ['192.168.1.24'],
    peerLanIps: isWizard ? [] : gw ? ['192.168.1.24'] : ['10.0.0.5'],
    agents: gw ? (rosterOld ? undefined : gwAgents) : undefined,
    // The real engine sends the fleet size beside the clamped list. &fleet=clamped
    // is the only way to see the past-the-cap wording without 31 mock agents.
    agentsTotal: gw && !rosterOld ? (axisFleet === 'clamped' ? 31 : gwAgents.length) : 0,
    tunnels: gwTunnels,
    // Same axis, the other clamped list: a fleet past the status caps has more
    // tunnels than the frame carries, and the Tunnels card must count the fleet's
    // rather than the rows'. The rows themselves stay as they are — the mock is
    // showing the wording, not 97 tunnels.
    tunnelsTruncated: gw && axisFleet === 'clamped',
    tunnelsTotal: gw ? (axisFleet === 'clamped' ? 97 : gwTunnels.length) : 0,
    connections: gwConns,
    totalBytesIn: state.bytesIn, totalBytesOut: state.bytesOut,
    linkUpSinceMs: isWizard || !state.linkUp ? 0 : LINK_UP_SINCE,
    processStartMs: isWizard ? 0 : PROCESS_START,
    peerAddr: isWizard ? '' : gw ? '84.23.101.7' : 'play.example.com:8474',
    linkBytesIn: Math.round(state.bytesIn * 1.06) + 2_400_000,
    linkBytesOut: Math.round(state.bytesOut * 1.06) + 3_100_000,
    allTimeBytesIn: Math.round(3.4 * 1024 ** 3) + state.bytesIn,
    allTimeBytesOut: Math.round(212 * 1024 ** 3) + state.bytesOut,
    cumulativeUptimeMs: 96 * 3_600_000 + (Date.now() - PROCESS_START),
    linkSessions: 14,
    historyUnsupported: false,
    analyticsUnsupported: axisAnalyticsOff,
    engineFatal: axisFatal ? 'authentication failed: the gateway rejected this agent\'s credentials (it may have been revoked) — re-pair with a fresh code' : '',
    runState: isWizard ? 'running' : state.runState,
    }
  }

  // simulate live traffic + a jittery RTT
  let logSeq = 0
  const logs: any[] = []
  // Levels are uppercase like the real backend (Go slog.Level.String()).
  const pushLog = (level: string, msg: string, attrs = '') => logs.push({seq: ++logSeq, timeMs: Date.now(), level, msg, attrs})
  pushLog('INFO', 'starting', 'role=' + state.role + ' version=0.1.0-dev')
  pushLog('INFO', 'gateway control listener up', 'addr=0.0.0.0:8474')
  pushLog('INFO', 'agent connected', 'id=agt_5w7d2k9rq3v8xj4m rtt=24ms')
  pushLog('WARN', 'local server is down', 'tunnel=Minecraft local_addr=127.0.0.1:25565')

  const startTicks = () => setInterval(() => {
    // Integrate the same rate functions the history serves, so the header
    // readout, tiles, and chart all agree. A downed link moves no traffic.
    if (state.linkUp) {
      const now = Date.now()
      state.bytesOut += Math.round(downRate(now) * 0.5)
      state.bytesIn += Math.round(upRate(now) * 0.5)
      state.conns[0].bytesOut += Math.round(downRate(now) * 0.5 * 0.8)
      state.conns[0].bytesIn += Math.round(upRate(now) * 0.5 * 0.8)
      state.conns[1].bytesOut += Math.round(downRate(now) * 0.5 * 0.2)
      state.conns[1].bytesIn += Math.round(upRate(now) * 0.5 * 0.2)
      state.rtt = 20 + Math.floor(Math.random() * 12)
      if (Math.random() < 0.15) pushLog('DEBUG', 'stream opened', 'client=203.0.113.44 tunnel=Minecraft')
    }
    emit('tick', status())
  }, 500)
  // &tick gates the stream, not just Status(): App's first-tick deadline is only
  // reachable when BOTH are quiet, so silencing one of them would prove nothing.
  if (!isWizard && axisTick !== 'silent' && axisTick !== 'reject') {
    if (axisTick === 'late') setTimeout(startTicks, TICK_LATE_MS)
    else startTicks()
  }

  // ---- players & sessions (analytics mock) ----
  // Deterministic wall of players derived from the same hash noise as the
  // traffic model. The first two carry the live connections' UUIDs so the
  // "online" join lights them up; every ~11th player is cracked/offline.
  const MC_NAMES = [
    'Notch', 'jeb_', 'Herobrine_x', 'EnderQueen', 'CraftyPete', 'redstone_rat', 'Skyfall9', 'ObsidianMike',
    'PixelPaula', 'TNT_Tim', 'shulker_sam', 'DiamondDee', 'GrassBlockGus', 'Nether_Nia', 'ZombieZoe', 'creeper_carl',
    'IronGolemIan', 'BlazeRunner', 'AxolotlAmy', 'villager_vic', 'PhantomPhil', 'CopperCleo', 'WardenWill', 'slime_sue',
    'ElytraElla', 'BeaconBen', 'kelp_farmer', 'AncientDebra', 'PiglinPia', 'trident_troy', 'MooshroomMo', 'SoulSandSid',
    'GhastGreta', 'lodestone_lu', 'HuskHarvey', 'CalciteCass', 'sniffer_stan', 'BreezeBex', 'MaceMarty', 'cherry_chloe',
    'TuffTina', 'VexVernon', 'camel_kai', 'StriderStella', 'AlayAllan', 'frog_light_fi', 'BundleBoris', 'SusStewNed',
  ]
  const CCS = ['NZ', 'AU', 'US', 'DE', 'GB', 'FR', 'SE', 'BR', 'JP', 'CA']
  const COUNTRY_NAMES: Record<string, string> = {
    NZ: 'New Zealand', AU: 'Australia', US: 'United States', DE: 'Germany',
    GB: 'United Kingdom', FR: 'France', SE: 'Sweden', BR: 'Brazil', JP: 'Japan', CA: 'Canada',
  }
  const hex32 = (seed: number) => {
    let s = ''
    for (let k = 0; k < 8; k++) s += Math.floor(hash01(seed * 8 + k) * 0xffff).toString(16).padStart(4, '0')
    return s
  }
  const dashUuid = (seed: number) => {
    const h = hex32(seed)
    return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
  }
  const mockPlayers = MC_NAMES.map((name, i) => {
    const offline = i > 1 && i % 11 === 7
    const uuid = i === 0 ? '069a79f4-44e9-4726-a5be-fca90e38aaf5'
      : i === 1 ? '853c80ef-3c37-49fd-aa49-938b674adae6'
      : offline ? 'offline:' + name.toLowerCase() : dashUuid(i + 101)
    // Recent-skewed last-seen inside the 12-day recording window; the two
    // live players are "seen" now.
    const lastSeen = i < 2 ? now0 : now0 - Math.round(hash01(i * 7 + 1) ** 2 * 12 * 86_400_000) - 60_000
    const firstSeen = Math.max(INSTALLED_AT, lastSeen - Math.round(hash01(i * 7 + 2) * 30 * 86_400_000) - 3_600_000)
    const sessions = 1 + Math.floor(hash01(i * 7 + 3) ** 1.6 * 80)
    const playMs = sessions * Math.round((20 + 160 * hash01(i * 7 + 4)) * 60_000)
    const bytesOut = sessions * Math.round(30_000_000 + 400_000_000 * hash01(i * 7 + 5))
    return {
      uuid, name, offline, online: i < 2 && state.linkUp,
      firstSeen, lastSeen, sessions, playMs,
      bytesIn: Math.round(bytesOut / 96), bytesOut,
      lastCc: CCS[Math.floor(hash01(i * 7 + 6) * CCS.length)],
      rttMs: offline ? 0 : Math.round(15 + 120 * hash01(i * 7 + 8) ** 2),
    }
  })
  // Connection history, newest first. The first two rows are the live
  // connections (endedMs 0); the rest scatter across the recording window.
  const mockSessions = (() => {
    const rows = state.conns.map((c: any, i: number) => ({
      id: 5000 + i, tunnelName: c.tunnelName, clientIp: c.clientAddr.split(':')[0],
      playerName: c.playerName, playerUuid: c.playerUuid, startedMs: c.startedAt, endedMs: 0,
      bytesIn: c.bytesIn, bytesOut: c.bytesOut, cc: CCS[i], rttAvg: 20 + i * 9,
    }))
    for (let s = 0; s < 240; s++) {
      const p = mockPlayers[Math.floor(hash01(s * 13 + 3) * mockPlayers.length)]
      const startedMs = now0 - 120_000 - Math.round(hash01(s * 13 + 5) ** 1.4 * 12 * 86_400_000)
      const durMs = Math.round((2 + 220 * hash01(s * 13 + 7) ** 2) * 60_000)
      const bytesOut = Math.round(durMs / 1000 * 40_000 * (0.3 + hash01(s * 13 + 9)))
      rows.push({
        id: 4999 - s, tunnelName: 'Minecraft', clientIp: `${13 + s % 200}.${37 + (s * 7) % 200}.${(s * 11) % 250}.${(s * 17) % 250}`,
        playerName: p.name, playerUuid: p.uuid, startedMs, endedMs: startedMs + durMs,
        bytesIn: Math.round(bytesOut / 96), bytesOut, cc: p.lastCc, rttAvg: p.rttMs || 25,
      })
    }
    return rows.sort((a, b) => b.startedMs - a.startedMs)
  })()
  const playersPage = (q: any) => {
    if (isWizard || axisFresh) return {total: 0, players: []}
    let rows = mockPlayers.slice()
    if (q?.search) rows = rows.filter(p => p.name.toLowerCase().includes(q.search.trim().toLowerCase()))
    if (q?.tunnelId && q.tunnelId !== tunnelID) rows = []
    if (q?.cc) rows = rows.filter(p => p.lastCc === q.cc)
    const sort = q?.sort
    rows.sort((a, b) =>
      sort === 'name' ? a.name.localeCompare(b.name)
      : sort === 'playtime' ? b.playMs - a.playMs
      : sort === 'sessions' ? b.sessions - a.sessions
      : sort === 'data' ? (b.bytesIn + b.bytesOut) - (a.bytesIn + a.bytesOut)
      : b.lastSeen - a.lastSeen)
    const offset = Math.max(0, q?.offset || 0)
    const limit = q?.limit > 0 && q.limit <= 80 ? q.limit : 80
    return {total: rows.length, players: rows.slice(offset, offset + limit)}
  }
  const playerDetail = (uuid: string) => {
    const p = mockPlayers.find(x => x.uuid === uuid)
    if (!p || isWizard || axisFresh) return {card: {}, names: [], ips: [], recent: []}
    const i = mockPlayers.indexOf(p)
    const names = [{name: p.name, firstSeen: p.firstSeen, lastSeen: p.lastSeen}]
    if (!p.offline && hash01(i * 31 + 2) > 0.7) // some players renamed once
      names.push({name: p.name + '_old', firstSeen: p.firstSeen - 40 * 86_400_000, lastSeen: p.firstSeen})
    const ips = [{
      ip: `${20 + i}.${113 - i}.${(i * 13) % 250}.${(i * 29) % 250}`,
      firstSeen: p.firstSeen, lastSeen: p.lastSeen, sessions: p.sessions, cc: p.lastCc,
    }]
    if (hash01(i * 31 + 4) > 0.6) ips.push({
      ip: `${90 + i}.${44 + i}.${(i * 7) % 250}.${(i * 3) % 250}`,
      firstSeen: p.firstSeen, lastSeen: p.firstSeen + 86_400_000, sessions: 2, cc: p.lastCc,
    })
    const recent = mockSessions.filter(s => s.playerUuid === uuid).slice(0, 25)
    return {card: p, names, ips, recent}
  }
  const playerHistory = (uuid: string, windowMs: number) => {
    const p = mockPlayers.find(x => x.uuid === uuid)
    if (!p || isWizard || axisFresh) return []
    if (!windowMs) windowMs = now0 - p.firstSeen
    const share = 0.15 + 0.5 * hash01(mockPlayers.indexOf(p) * 31 + 6)
    const bucketMs = Math.max(15_000, Math.ceil(windowMs / 300 / 15_000) * 15_000)
    const t0 = Math.floor((Date.now() - windowMs) / bucketMs) * bucketMs
    const pts: any[] = []
    for (let t = Math.max(t0, p.firstSeen); t <= Date.now(); t += bucketMs) {
      // Only emit buckets where the player was plausibly online.
      if (vnoise(t, 3_600_000, 17 + mockPlayers.indexOf(p)) < 0.45) continue
      const durSec = bucketMs / 1000
      pts.push({t, out: Math.round(downRate(t) * share * durSec), in: Math.round(upRate(t) * share * durSec)})
    }
    return pts.slice(-300)
  }
  // Per-player latency: a personal baseline (the player's ping) with slow
  // drift, min/max spread around it. Buckets align with playerHistory.
  const playerLatency = (uuid: string, windowMs: number) => {
    const p = mockPlayers.find(x => x.uuid === uuid)
    if (!p || !p.rttMs || isWizard || axisFresh) return []
    if (!windowMs) windowMs = now0 - p.firstSeen
    const bucketMs = Math.max(60_000, Math.ceil(windowMs / 300 / 60_000) * 60_000)
    const t0 = Math.floor((Date.now() - windowMs) / bucketMs) * bucketMs
    const seed = mockPlayers.indexOf(p)
    const pts: any[] = []
    for (let t = Math.max(t0, p.firstSeen); t <= Date.now(); t += bucketMs) {
      if (vnoise(t, 3_600_000, 17 + seed) < 0.45) continue // offline gaps
      const avg = p.rttMs * (0.82 + 0.36 * vnoise(t, 240_000, 30 + seed))
      const spread = 3 + 10 * vnoise(t, 90_000, 40 + seed)
      pts.push({t, avg, min: Math.max(1, avg - spread), max: avg + spread})
    }
    return pts.slice(-300)
  }
  const sessionsPage = (q: any) => {
    if (isWizard || axisFresh) return {total: 0, sessions: []}
    let rows = mockSessions
    if (q?.playerUuid) rows = rows.filter(s => s.playerUuid === q.playerUuid)
    if (q?.tunnelId && q.tunnelId !== tunnelID) rows = []
    if (q?.cc) rows = rows.filter(s => s.cc === q.cc)
    if (q?.sinceMs > 0) rows = rows.filter(s => s.startedMs >= q.sinceMs)
    const offset = Math.max(0, q?.offset || 0)
    const limit = q?.limit > 0 && q.limit <= 100 ? q.limit : 100
    return {total: rows.length, sessions: rows.slice(offset, offset + limit)}
  }
  // Session replay: per-connection traffic + RTT across the session's life,
  // integrated from the same rate model so a replay agrees with the tiles.
  const sessionTimeline = (id: number) => {
    const s = mockSessions.find((x: any) => x.id === id)
    if (!s || isWizard || axisFresh) return {traffic: [], rtt: []}
    const end = s.endedMs || Date.now()
    const span = Math.max(15_000, end - s.startedMs)
    const bucketMs = Math.max(15_000, Math.ceil(span / 300 / 15_000) * 15_000)
    const share = 0.2 + 0.5 * hash01(id)
    const traffic: any[] = []
    const rtt: any[] = []
    for (let t = s.startedMs; t <= end; t += bucketMs) {
      const durSec = Math.min(bucketMs, end - t + 1) / 1000
      traffic.push({t, out: Math.round(downRate(t) * share * durSec), in: Math.round(upRate(t) * share * durSec)})
      const avg = (s.rttAvg || 25) * (0.85 + 0.3 * vnoise(t, 120_000, 55))
      const spread = 3 + 8 * vnoise(t, 60_000, 56)
      rtt.push({t, avg, min: Math.max(1, avg - spread), max: avg + spread})
    }
    return {traffic: traffic.slice(-300), rtt: rtt.slice(-300)}
  }

  // GeoIP axes: 'off' leaves paths unset (unconfigured empty state); the
  // other three configure a city path whose load state varies — 'empty'
  // loads but locates nothing, 'error' fails to open (MmdbBadge Failed),
  // 'pending' is picked but the engine hasn't restarted (MmdbBadge Pending).
  if (axisGeo && axisGeo !== 'off') config.Analytics.GeoIPCityPath = 'C:\\maxmind\\GeoLite2-City.mmdb'
  const geoStatus = () => ({
    cityLoaded: axisGeo ? axisGeo === 'empty' : !!config.Analytics.GeoIPCityPath,
    asnLoaded: !axisGeo && !!config.Analytics.GeoIPASNPath,
    ...(axisGeo === 'error' ? {cityError: 'open C:\\maxmind\\GeoLite2-City.mmdb: unsupported database format'} : {}),
  })

  // Country aggregates for the world heatmap + latency-by-country list, rolled
  // up from the same session history the wall uses so the views agree.
  const geoSnapshot = (rangeMs: number) => {
    if (isWizard || axisFresh || axisGeo) return []
    const since = rangeMs > 0 ? now0 - rangeMs : 0
    const by = new Map<string, {players: Set<string>; sessions: number; bytesIn: number; bytesOut: number; rttSum: number; rttN: number}>()
    for (const s of mockSessions) {
      if (!s.cc || s.startedMs < since) continue
      let a = by.get(s.cc)
      if (!a) { a = {players: new Set(), sessions: 0, bytesIn: 0, bytesOut: 0, rttSum: 0, rttN: 0}; by.set(s.cc, a) }
      a.players.add(s.playerUuid)
      a.sessions++; a.bytesIn += s.bytesIn; a.bytesOut += s.bytesOut
      if (s.rttAvg > 0) { a.rttSum += s.rttAvg; a.rttN++ }
    }
    return [...by.entries()]
      .map(([cc, a]) => ({
        cc, country: COUNTRY_NAMES[cc] || cc, players: a.players.size, sessions: a.sessions,
        bytesIn: a.bytesIn, bytesOut: a.bytesOut, rttAvg: a.rttN ? a.rttSum / a.rttN : 0,
      }))
      .sort((x, y) => y.sessions - x.sessions)
  }

  // ---- dashboard aggregates (analytics) ----
  // Summary integrates the same rate/gauge model the chart serves, so the
  // tiles agree with the bandwidth history for any range.
  const summary = (rangeMs: number) => {
    const now = Date.now()
    if (isWizard || axisFresh) {
      return {
        rangeMs, bytesIn: 0, bytesOut: 0, sessions: 0, uniquePlayers: 0,
        peakPlayers: -1, peakPlayersAt: 0, peakInBps: 0, peakInAt: 0, peakOutBps: 0, peakOutAt: 0,
        avgRttMs: -1, avgLossPct: -1, linkUptimePct: -1,
        recInBps: 0, recInAt: 0, recOutBps: 0, recOutAt: 0, recPlayers: -1, recPlayersAt: 0, recConns: -1, recConnsAt: 0,
        lifetimeBytesIn: 0, lifetimeBytesOut: 0, lifetimeUptimeMs: 0, linkSessions: 0,
      }
    }
    const since = rangeMs > 0 ? now - rangeMs : CONN_SINCE
    const from = Math.max(since, CONN_SINCE)
    const step = Math.max(60_000, Math.round((now - from) / 400))
    let bytesIn = 0, bytesOut = 0, peakIn = 0, peakInAt = 0, peakOut = 0, peakOutAt = 0
    let peakPlayers = -1, peakPlayersAt = 0, rttSum = 0, rttN = 0, lossSum = 0, lossN = 0
    for (let t = from; t <= now; t += step) {
      const dr = downRate(t), ur = upRate(t)
      bytesOut += dr * step / 1000
      bytesIn += ur * step / 1000
      if (dr > peakOut) { peakOut = dr; peakOutAt = t }
      if (ur > peakIn) { peakIn = ur; peakInAt = t }
      const pl = Math.max(0, connCount(t) - 1)
      if (pl > peakPlayers) { peakPlayers = pl; peakPlayersAt = t }
      rttSum += rttMs(t); rttN++
      lossSum += vnoise(t, 60_000, 13) > 0.9 ? 0.4 : 0; lossN++
    }
    const inWindow = mockSessions.filter(s => s.startedMs >= since)
    const st = status()
    return {
      rangeMs, bytesIn: Math.round(bytesIn), bytesOut: Math.round(bytesOut),
      sessions: inWindow.length, uniquePlayers: new Set(inWindow.map(s => s.playerUuid)).size,
      peakPlayers, peakPlayersAt, peakInBps: peakIn, peakInAt, peakOutBps: peakOut, peakOutAt,
      avgRttMs: rttN ? rttSum / rttN : -1, avgLossPct: lossN ? lossSum / lossN : -1,
      linkUptimePct: 99.2 - 1.4 * vnoise(now, 3_600_000, 21),
      recInBps: peakIn * 1.35, recInAt: CONN_SINCE + 3 * 86_400_000,
      recOutBps: peakOut * 1.35, recOutAt: CONN_SINCE + 3 * 86_400_000,
      recPlayers: Math.max(peakPlayers, 12), recPlayersAt: CONN_SINCE + 5 * 86_400_000,
      recConns: Math.max(peakPlayers + 2, 14), recConnsAt: CONN_SINCE + 5 * 86_400_000,
      lifetimeBytesIn: st.allTimeBytesIn, lifetimeBytesOut: st.allTimeBytesOut,
      lifetimeUptimeMs: st.cumulativeUptimeMs, linkSessions: st.linkSessions,
    }
  }
  // Peak-hours matrix: hourly player samples bucketed by weekday × hour, the
  // same shape the backend rolls up from rollup_hourly.
  const peakMatrix = (weeks: number) => {
    const cells = Array.from({length: 7}, () => Array.from({length: 24}, () => ({avg: -1, max: -1})))
    if (isWizard || axisFresh) return {cells}
    const w = weeks > 0 ? weeks : 8
    const sum = Array.from({length: 7}, () => new Array(24).fill(0))
    const cnt = Array.from({length: 7}, () => new Array(24).fill(0))
    const now = Date.now()
    const start = Math.floor(Math.max(CONN_SINCE, now - w * 7 * 86_400_000) / 3_600_000) * 3_600_000
    for (let t = start; t <= now; t += 3_600_000) {
      const d = new Date(t), dow = d.getDay(), hod = d.getHours()
      const players = Math.max(0, connCount(t) - 1)
      sum[dow][hod] += players; cnt[dow][hod]++
      const peak = players + (connCount(t + 1_800_000) > players ? 1 : 0)
      if (peak > cells[dow][hod].max) cells[dow][hod].max = peak
    }
    for (let i = 0; i < 7; i++) for (let j = 0; j < 24; j++) if (cnt[i][j] > 0) cells[i][j].avg = sum[i][j] / cnt[i][j]
    return {cells}
  }
  // Uptime: mostly-up timelines with a few deterministic flaps; the percentage
  // is integrated from the same events the timeline renders.
  const uptimePctOf = (events: {t: number; up: boolean}[], a: number, b: number) => {
    if (!events.length || b <= a) return -1
    let up = 0, cursor = a, state = events[0].up
    for (const e of events) {
      if (e.t <= a || e.t >= b) continue
      if (state) up += e.t - cursor
      cursor = e.t; state = e.up
    }
    if (state) up += b - cursor
    return up / (b - a) * 100
  }
  const flapEvents = (a: number, b: number, seed: number, downMs: number) => {
    const ev = [{t: a, up: true}]
    const gap = (b - a) / 5
    for (let k = 1; k <= 4; k++) {
      if (hash01(seed * 10 + k) > 0.6) {
        const dt = a + gap * k + hash01(seed * 10 + k + 100) * gap * 0.4
        const down = Math.round(dt), up = Math.round(dt + downMs * (0.5 + hash01(seed * 10 + k + 200)))
        if (down < b) ev.push({t: down, up: false})
        if (up < b) ev.push({t: up, up: true})
      }
    }
    return ev.sort((x, y) => x.t - y.t).slice(-100)
  }
  const tunnelUptime = (windowMs: number) => {
    const now = Date.now()
    if (isWizard || axisFresh) return {link: {tunnelId: '', name: 'Control link', uptimePct: -1, events: []}, tunnels: []}
    const since = windowMs > 0 ? now - windowMs : CONN_SINCE
    const linkEv = flapEvents(Math.max(since, CONN_SINCE), now, 1, 75_000)
    const tunEv = flapEvents(Math.max(since, CONN_SINCE), now, 2, 22 * 60_000)
    return {
      link: {tunnelId: '', name: 'Control link', uptimePct: uptimePctOf(linkEv, Math.max(since, CONN_SINCE), now), events: linkEv},
      tunnels: [{tunnelId: tunnelID, name: 'Minecraft', uptimePct: uptimePctOf(tunEv, Math.max(since, CONN_SINCE), now), events: tunEv}],
    }
  }

  // A role flip in the mock is the same one-field change it is in the engine:
  // the config's Role and the status the tick reports. Peers/addresses are
  // derived from state.role in status(), so they follow automatically.
  const becomeRole = (r: string) => {
    config.Role = r
    state.role = r
  }

  // Mutable service state (the badge + the installed-but-stopped warning).
  let serviceState = axisService === 'running' ? 'running'
    : axisService === 'stopped' ? 'stopped'
      : 'not-installed'
  // Mutable startup-entry state. The real binding reads the registry, not the
  // config document, so the mock keeps its own bit and the toggle round-trips
  // through it — write, then re-read, exactly like the real refresh does.
  let autostartOn = axisAutostart

  const ok = <T,>(v: T) => Promise.resolve(v)
  // Attached mode: a service owns the engine — gated bindings reject exactly
  // like the real backend so the UI's disabled/degraded states can be tested.
  const gated = <T,>(v: () => T | Promise<T>): Promise<T> =>
    axisAttached
      ? Promise.reject(new Error('the engine runs in another process (service or headless run)'))
      : Promise.resolve(v())
  const App: Record<string, AnyFn> = {
    // The first snapshot, and the three ways it can fail to be one (&tick).
    // A never-settling promise is the honest model of a dead pipe: Wails leaves
    // the call outstanding rather than rejecting it.
    Status: () => {
      if (axisTick === 'silent') return new Promise(() => {})
      if (axisTick === 'reject') return Promise.reject(new Error('the engine is not answering on \\\\.\\pipe\\proxyforward'))
      if (axisTick === 'late') return new Promise(r => setTimeout(() => r(status()), TICK_LATE_MS))
      return ok(status())
    },
    BandwidthHistory: (windowMs: number, maxBuckets: number) => ok(bandwidthHistory(windowMs, maxBuckets)),
    AgentBandwidthHistory: (agentId: string, windowMs: number, maxBuckets: number) => ok(agentBandwidth(agentId, windowMs, maxBuckets)),
    PeerStats: () => ok(isWizard || axisFresh ? [] : peerStats()),
    // Analytics reads work in attached mode too (they ride the IPC envelope).
    Players: (q: any) => ok(playersPage(q)),
    PlayerDetail: (uuid: string) => ok(playerDetail(uuid)),
    PlayerHistory: (uuid: string, windowMs: number) => ok(playerHistory(uuid, windowMs)),
    PlayerLatency: (uuid: string, windowMs: number) => ok(playerLatency(uuid, windowMs)),
    Sessions: (q: any) => ok(sessionsPage(q)),
    SessionTimeline: (id: number) => ok(sessionTimeline(id)),
    GeoStatus: () => ok(geoStatus()),
    GeoSnapshot: (rangeMs: number) => ok(geoSnapshot(rangeMs)),
    Summary: (rangeMs: number) => ok(summary(rangeMs)),
    PeakMatrix: (weeks: number) => ok(peakMatrix(weeks)),
    TunnelUptime: (windowMs: number) => ok(tunnelUptime(windowMs)),
    // A picker isn't available in browser dev; hand back a plausible path so the
    // field + status badge can be exercised.
    BrowseMMDB: (title: string) => ok(/asn/i.test(title) ? 'C:\\maxmind\\GeoLite2-ASN.mmdb' : 'C:\\maxmind\\GeoLite2-City.mmdb'),
    GetConfig: () => ok(config),
    // Agent management: the roster (ListAgents) + the gateway event ring
    // (GatewayEvents) are reads (ok); the write bindings are gated and mutate the
    // mock world (agentOverrides) so the UI updates on the next poll, mirroring
    // the real write-then-repoll loop.
    ListAgents: () => ok(listAgents()),
    GatewayEvents: (sinceSeq: number) => ok(gatewayEvents(sinceSeq)),
    // Read: the gateway-held desired config for the drill-in panel. Write: reclaim is
    // a gated no-op here (the card only offers it when both parties are enrolled, so
    // the mock never has to model the legacy-party refusal).
    AgentDesiredConfig: (agentId: string) => ok(agentDesiredConfig(agentId)),
    ReclaimPort: (_agentId: string, _tunnelId: string, _port: number) => gated(() => undefined),
    // ---- config consent (mutual approval) ----
    // Gateway side. Approving/rejecting the agent's proposal clears it; the next
    // poll shows the result. Approve also folds the proposed set into the agent's
    // live tunnels so the drill-in reflects what was just adopted — the real flow
    // adopts, bumps, and pushes, and the panel re-reads that.
    ApproveAgentProposal: (agentId: string, proposalId: string) => gated(() => {
      if (!gwPendingIn || gwPendingIn.proposalId !== proposalId) throw new Error('that proposal is no longer pending')
      const extra = extraAgents.find(a => a.agentId === agentId)
      if (extra) {
        extra.tunnels = gwPendingIn.tunnels.map((s: any) => ({
          id: s.id, name: s.name, port: s.publicPort || 25590, localUp: true,
          bandwidthLimitMbps: s.bandwidthLimitMbps, bandwidthLimitScope: s.bandwidthLimitScope,
        }))
      }
      gwPendingIn = null
      return undefined
    }),
    RejectAgentProposal: (_agentId: string, proposalId: string, _reason: string) => gated(() => {
      if (!gwPendingIn || gwPendingIn.proposalId !== proposalId) throw new Error('that proposal is no longer pending')
      gwPendingIn = null // a rejection changes nothing else — that is the point
      return undefined
    }),
    // The gateway-side editor's submit: it proposes, it does not apply. The card
    // then shows "awaiting the agent", which is the whole contract.
    ProposeAgentConfig: (agentId: string, tunnels: any[]) => gated(() => {
      if (!consentLive) throw new Error(`agent ${agentId} runs a build without config-consent — update it to edit its tunnels from here`)
      gwPendingOut = {proposalId: `prp_${Math.random().toString(16).slice(2, 10)}`, baseGen: 5, tunnels, atMs: Date.now()}
      return undefined
    }),
    // Agent side.
    AgentConsent: () => ok({
      consentActive: state.role === 'agent' && state.linkUp && consentLive,
      incoming: agPendingIn ?? undefined,
      outgoing: agPendingOut ?? undefined,
      lastDecline: agLastDecline || undefined,
    }),
    ApproveGatewayProposal: (proposalId: string) => gated(() => {
      if (!agPendingIn || agPendingIn.proposalId !== proposalId) throw new Error('that proposal is no longer pending')
      agPendingIn = null
      return undefined
    }),
    RejectGatewayProposal: (proposalId: string, _reason: string) => gated(() => {
      if (!agPendingIn || agPendingIn.proposalId !== proposalId) throw new Error('that proposal is no longer pending')
      agPendingIn = null
      return undefined
    }),
    IssuePairingCode: (reusable: boolean, ttlSecs: number, ports: number[], tunnels: string[], label: string) =>
      gated(() => {
        // Each call mints a fresh enrollment ticket → a different token segment,
        // so "New code" and the reusable toggle visibly change the shown code —
        // and a live invite row appears in the roster, like the real store.
        const handle = Array.from({length: 8}, () => Math.floor(Math.random() * 16).toString(16)).join('')
        const iv = mkInvite({
          handle, label, reusable, uses: 0, issuedAtMs: Date.now(),
          expiresAtMs: ttlSecs > 0 ? Date.now() + ttlSecs * 1000 : 0,
          scopePorts: ports, scopeTunnels: tunnels,
        })
        invites.unshift(iv)
        return iv.code
      }),
    ListInvites: () => Promise.resolve(state.role === 'gateway' && axisFleet !== 'old' ? invites.map(iv => ({...iv})) : []),
    RevokeInvite: (handle: string) => gated(() => {
      const i = invites.findIndex(iv => iv.handle === handle)
      if (i < 0) throw new Error('that invite is no longer live — it may have been used up or already revoked')
      invites.splice(i, 1)
      return undefined
    }),
    RenameAgent: (agentId: string, nickname: string) => gated(() => {
      const o = agentOverrides.get(agentId) ?? {}
      o.nickname = nickname
      agentOverrides.set(agentId, o)
      return undefined
    }),
    SetAgentScope: (agentId: string, ports: number[], tunnels: string[]) => gated(() => {
      const o = agentOverrides.get(agentId) ?? {}
      o.scopePorts = ports; o.scopeTunnels = tunnels
      agentOverrides.set(agentId, o)
      return undefined
    }),
    // Rejects when the agent is unknown, exactly as the real RevokeAgent does.
    RevokeAgent: (agentId: string) => gated(() =>
      knownAgentIds.has(agentId)
        ? (() => {
          const o = agentOverrides.get(agentId) ?? {}
          o.revoked = true; o.connected = false
          agentOverrides.set(agentId, o)
          return undefined
        })()
        : Promise.reject(new Error(`agent ${agentId} not found (it may have already disconnected or been removed)`))),
    // ---- tunnel groups ----
    // ListGroups is a read; the writes are gated and mutate mockGroups so the card
    // updates on the next poll — the real write-then-repoll loop.
    ListGroups: () => ok(state.role === 'gateway' && axisFleet !== 'old' ? mockGroups.map(g => ({spec: {...g.spec}, members: g.members.map(m => ({...m}))})) : []),
    SaveGroup: (id: string, name: string, publicPort: number, policy: string, offlineMotd: string) => gated(() => {
      if (id) {
        const g = mockGroups.find(x => x.spec.id === id)
        if (!g) throw new Error('that group no longer exists')
        Object.assign(g.spec, {name, publicPort, policy, offlineMotd})
        reelectPrimary(g)
        return id
      }
      const newId = 'grp_' + Array.from({length: 8}, () => Math.floor(Math.random() * 16).toString(16)).join('')
      mockGroups.push({spec: {id: newId, name, publicPort, proto: 'tcp', policy, offlineMotd}, members: []})
      return newId
    }),
    DeleteGroup: (id: string) => gated(() => {
      const i = mockGroups.findIndex(x => x.spec.id === id)
      if (i < 0) throw new Error('that group is no longer in the roster — it may have already been removed')
      mockGroups.splice(i, 1)
      return undefined
    }),
    SetTunnelGroup: (agentId: string, tunnelId: string, groupId: string) => gated(() => {
      // Untag: drop the member from whatever group holds it, then re-elect.
      for (const g of mockGroups) {
        const before = g.members.length
        g.members = g.members.filter(m => !(m.agentId === agentId && m.tunnelId === tunnelId))
        if (g.members.length !== before) reelectPrimary(g)
      }
      if (groupId) {
        const g = mockGroups.find(x => x.spec.id === groupId)
        if (!g) throw new Error('that group no longer exists')
        g.members.push({agentId, tunnelId, eligible: true, isPrimary: false, liveConns: 0})
        reelectPrimary(g)
      }
      return undefined
    }),
    // The real app pulls this once on mount to open straight into pairing when
    // launched via a clicked pxf:// link. In browser dev &deeplink=1 simulates one.
    TakePendingDeepLink: () => ok(axisDeepLink
      ? 'pxf://play.example.com:8474/v1/pair/tkt_0123456789abcdef0123456789abcdef#sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
      : ''),
    Version: () => ok('0.1.0-dev'),
    LogsSince: (seq: number) => ok(axisAttached ? [] : logs.filter(l => l.seq > seq)),
    TestReachability: () => new Promise(r => setTimeout(() => r('Reachable: play.example.com:25565 answered in 38ms — players can connect.'), 700)),
    // Role setup actually flips the mock's role: state.role is read on every
    // tick, so the whole app (accent ramp, screens, sidebar) swaps live in the
    // browser with no Go running — which is the only way to exercise the
    // sidebar's role switcher on both axes.
    SetupGateway: () => gated(() => { becomeRole('gateway'); return undefined }),
    SetupAgent: () => gated(() => { becomeRole('agent'); return undefined }),
    SaveTunnels: (t: any) => { config.Agent.Tunnels = t; return ok(undefined) },
    SaveSettings: (c: any) => {
      // The real SaveSettings validates before it writes: an unpaired agent is
      // refused and nothing is persisted (config.go validateAgent). Mirror that,
      // or the switcher's error path can't be seen.
      if (c?.Role === 'agent' && !(config.Agent.CertFingerprint && config.Agent.GatewayHost)) {
        return Promise.reject(new Error('agent.cert_fingerprint: required (pair with a gateway first)'))
      }
      if (c?.Role) becomeRole(c.Role)
      if (c?.Logging) config.Logging = c.Logging
      return ok(undefined)
    },
    SetTheme: (t: string) => { config.UI.Theme = t; return ok(undefined) },
    SetMinimizeToTray: (v: boolean) => { config.UI.MinimizeToTray = v; return ok(undefined) },
    // SetPause flips THIS side's bit (the peer's is only ever reported), so
    // Pause/Resume round-trips live against the axis-seeded state.
    SetPause: (_agentId: string, tunnelId: string, paused: boolean, _disconnect: boolean) => {
      if (tunnelId === '') pauseBits.allMine = paused
      else pauseBits.mine = paused
      return ok(undefined)
    },
    HideToTray: () => { trayNoticeSeen = true; config.UI.TrayNoticeShown = true; console.info('[devmock] HideToTray()'); return ok(undefined) },
    QuitAndDisconnect: () => { console.info('[devmock] QuitAndDisconnect()'); return ok(undefined) },
    RestartEngine: () => gated(() => undefined),
    // Start/Stop drive the run and mutate the mock world, so the next poll shows
    // the result — the real write-then-repoll loop. Not gated: unlike Restart,
    // these work attached too (they ride the IPC lifecycle ops). Stop drops the
    // link and marks stopped; Start restores whatever the axes said the link was.
    StopEngine: (_force: boolean) => {
      state.runState = 'stopped'
      state.linkUp = false
      state.agentConnected = false
      return ok(undefined)
    },
    StartEngine: () => {
      state.runState = 'running'
      state.linkUp = baseUp
      state.agentConnected = baseUp
      return ok(undefined)
    },
    OpenConfigDir: () => ok(undefined),
    OpenExternal: (u: string) => { window.open(u, '_blank'); return ok(undefined) },
    ExportDiagnostics: () => ok('C\\\\Users\\\\you\\\\Downloads\\\\proxyforward-diagnostics.zip'),
    ExportSetup: () => gated(() => 'C\\\\Users\\\\you\\\\Downloads\\\\desktop-dev.pfsetup'),
    ChooseAndInspectSetupFile: () => gated(() => ({
      path: 'C\\\\Users\\\\you\\\\Downloads\\\\gateway-vps-01.pfsetup',
      role: 'gateway', appVersion: '0.1.0-dev',
      exportedAtMs: Date.now() - 86_400_000, encrypted: true,
    })),
    ImportSetup: () => gated(() => undefined),
    FirewallStatus: () => ok(true),
    FirewallRepair: () => ok(undefined),
    // Service state is mutable so Install/Start/Stop/Uninstall drive the badge
    // and the installed-but-stopped warning on the next poll.
    ServiceStatus: () => ok(serviceState),
    InstallService: () => { serviceState = 'running'; return ok(undefined) },
    UninstallService: () => { serviceState = 'not-installed'; return ok(undefined) },
    StartService: () => { serviceState = 'running'; return ok(undefined) },
    StopService: () => { serviceState = 'stopped'; return ok(undefined) },
    AutostartEnabled: () => ok(autostartOn),
    // The real one reconciles the registry and then records the intent in the
    // config; both halves are mirrored so a later SaveSettings can't revert it.
    SetAutostart: (v: boolean) => { autostartOn = v; config.UI.Autostart = v; return ok(undefined) },
    SetUpdateCheck: (v: boolean) => { config.UI.UpdateCheck = v; return ok(undefined) },
    // The release check answers from the &update axis rather than the network.
    // Off is not a failure and must not read as one: it returns enabled:false
    // with nothing else filled in, exactly as the real binding does before it
    // touches a socket.
    CheckForUpdate: () => {
      if (!config.UI.UpdateCheck) return ok({enabled: false, current: '1.4.0', latest: '', newer: false, comparable: false, url: '', checkedMs: 0})
      if (axisUpdate === 'fail') return Promise.reject(new Error('could not reach the release list: dial tcp: lookup api.github.com: no such host'))
      const base = {enabled: true, current: '1.4.0', url: 'https://github.com/xeri/proxyforward/releases/latest', checkedMs: Date.now() - 3 * 3600_000}
      switch (axisUpdate) {
        case 'newer': return ok({...base, latest: '1.5.0', newer: true, comparable: true})
        case 'none': return ok({...base, latest: '', newer: false, comparable: false, url: ''})
        case 'odd': return ok({...base, current: 'handbuilt', latest: '1.4.0', newer: false, comparable: false})
        default: return ok({...base, latest: '1.4.0', newer: false, comparable: true})
      }
    },
    MeasureLatency: () => gated(() => new Promise(r => setTimeout(() => {
      const avg = 21 + Math.random() * 6
      r({
        samples: 10, rttAvgMs: avg, rttMinMs: avg - 3.2, rttMaxMs: avg + 7.1,
        jitterMs: 2.4 + Math.random() * 2, oneWayEstimateMs: avg / 2,
        oneWayUpMs: avg / 2 - 1.8, oneWayDownMs: avg / 2 + 1.8,
        haveOneWay: true, clockSyncCaveat: true,
      })
    }, 1300))),
    CreatorInfo: () => ok({name: 'xeri', url: 'https://github.com/xeri'}),
    OpenCreatorURL: () => { window.open('https://github.com/xeri', '_blank'); return ok(undefined) },
    CreatorAvatar: () => ok('data:image/svg+xml;utf8,' + encodeURIComponent(
      '<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 96 96">' +
      '<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">' +
      '<stop offset="0" stop-color="#8b5cf6"/><stop offset="1" stop-color="#22d3ee"/></linearGradient></defs>' +
      '<rect width="96" height="96" rx="48" fill="url(#g)"/>' +
      '<text x="48" y="64" font-size="52" font-family="sans-serif" font-weight="700" text-anchor="middle" fill="#fff">x</text></svg>')),
  }
  w.go = {app: {App}}
  // Flag for helpers that must know there is no Go asset server behind them
  // (avatarUrl falls back to mc-heads / inline SVG in browser dev).
  w.__pfDevMock = true
  // The one console line in the app, and it only runs in browser dev: which
  // scenario installed is the first thing you check when a mock axis misbehaves.
  console.info('[devmock] installed — scenario:', scenario)
}
