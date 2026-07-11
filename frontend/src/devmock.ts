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
//   &fatal=1         — terminal engine error (bad token) surfaces
//   &fresh=1         — first-run data: no history, no peers yet
//   &fx=high         — high-fx glass: refraction filter on the palette
//   &fx=low          — low-fx glass: solid cards, no caustics/chart glow

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
  const axisFresh = params.get('fresh') === '1'
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
    Quit: () => { console.info('[devmock] Quit()') },
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
      })
    }
    return {windowMs, bucketMs, buckets}
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
  const up = !isWizard && !axisLinkDown && !axisFatal
  const state = {
    role: isGateway ? 'gateway' : 'agent',
    linkUp: up,
    agentConnected: up,
    rtt: 24,
    bytesIn: 0,
    bytesOut: 0,
    conns: [
      {id: 1, tunnelName: 'Minecraft', clientAddr: '203.0.113.44:51422', startedAt: Date.now() - 92000, bytesIn: 1_240_000, bytesOut: 8_900_000},
      {id: 2, tunnelName: 'Minecraft', clientAddr: '198.51.100.7:60011', startedAt: Date.now() - 15000, bytesIn: 120_000, bytesOut: 640_000},
    ] as any[],
  }
  const config = {
    Role: state.role,
    Agent: {AgentID: 'agentid', GatewayHost: 'play.example.com', GatewayPort: 8474, Token: 'tok', CertFingerprint: 'sha256:ab', Transport: 'mux',
      Tunnels: [{ID: tunnelID, Name: 'Minecraft', Type: 'tcp', LocalAddr: '127.0.0.1:25565', PublicPort: 25565, Enabled: true,
        Options: {MinecraftAware: true, ProxyProtocolV2: false, OfflineMOTD: 'Server is offline — back soon', BandwidthLimitMbps: 0}}]},
    Gateway: {BindAddr: '0.0.0.0', ControlPort: 8474, Token: 'tok', PublicHost: 'play.example.com', PortAllowlist: [],
      MaxConnsGlobal: 4096, MaxConnsPerIP: 32, AuthAttemptsPerMin: 10},
    Metrics: {PrometheusEnabled: false, PrometheusAddr: '127.0.0.1:9464'},
    Logging: {Level: 'info', FileEnabled: true},
    UI: {Theme: localStorage.getItem('pf-theme') || 'dark', MinimizeToTray: true, Autostart: false},
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
    return {
    mode: isWizard ? 'wizard' : axisAttached ? 'attached' : 'engine',
    role: isWizard ? '' : state.role,
    version: '0.1.0-dev', hostname: 'DESKTOP-DEV', pid: 4242, configPath: 'C\\\\Users\\\\you\\\\AppData\\\\Roaming\\\\proxyforward\\\\config.toml',
    linkUp: state.linkUp, rttMillis: state.linkUp ? state.rtt : 0, agentConnected: state.agentConnected,
    jitterMillis: jitter,
    packetLossPct: loss,
    healthScore: isWizard ? 'unknown' : healthOf(jitter, loss),
    peerHostname: isWizard ? '' : isGateway ? 'DESKTOP-DEV' : 'GATEWAY-VPS-01',
    publicIp: isWizard ? '' : isGateway ? '203.0.113.9' : '84.23.101.7',
    peerPublicIp: isWizard ? '' : isGateway ? '84.23.101.7' : 'play.example.com',
    localLanIps: isWizard ? [] : isGateway ? ['10.0.0.5'] : ['192.168.1.24'],
    peerLanIps: isWizard ? [] : isGateway ? ['192.168.1.24'] : ['10.0.0.5'],
    tunnels: [{id: tunnelID, name: 'Minecraft', publicPort: state.linkUp ? 25565 : 0, localUp: true, localKnown: true}],
    connections: state.linkUp ? state.conns : [],
    totalBytesIn: state.bytesIn, totalBytesOut: state.bytesOut,
    linkUpSinceMs: isWizard || !state.linkUp ? 0 : LINK_UP_SINCE,
    processStartMs: isWizard ? 0 : PROCESS_START,
    peerAddr: isWizard ? '' : isGateway ? '84.23.101.7' : 'play.example.com:8474',
    linkBytesIn: Math.round(state.bytesIn * 1.06) + 2_400_000,
    linkBytesOut: Math.round(state.bytesOut * 1.06) + 3_100_000,
    allTimeBytesIn: Math.round(3.4 * 1024 ** 3) + state.bytesIn,
    allTimeBytesOut: Math.round(212 * 1024 ** 3) + state.bytesOut,
    cumulativeUptimeMs: 96 * 3_600_000 + (Date.now() - PROCESS_START),
    linkSessions: 14,
    historyUnsupported: false,
    engineFatal: axisFatal ? 'authentication failed: the gateway rejected this agent\'s token (it may have been rotated) — re-pair with a fresh code' : '',
    }
  }

  // simulate live traffic + a jittery RTT
  let logSeq = 0
  const logs: any[] = []
  // Levels are uppercase like the real backend (Go slog.Level.String()).
  const pushLog = (level: string, msg: string, attrs = '') => logs.push({seq: ++logSeq, timeMs: Date.now(), level, msg, attrs})
  pushLog('INFO', 'starting', 'role=' + state.role + ' version=0.1.0-dev')
  pushLog('INFO', 'gateway control listener up', 'addr=0.0.0.0:8474')
  pushLog('INFO', 'agent connected', 'id=agentid rtt=24ms')
  pushLog('WARN', 'local server is down', 'tunnel=Minecraft local_addr=127.0.0.1:25565')

  if (!isWizard) setInterval(() => {
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

  const ok = <T,>(v: T) => Promise.resolve(v)
  // Attached mode: a service owns the engine — gated bindings reject exactly
  // like the real backend so the UI's disabled/degraded states can be tested.
  const gated = <T,>(v: () => T | Promise<T>): Promise<T> =>
    axisAttached
      ? Promise.reject(new Error('the engine runs in another process (service or headless run)'))
      : Promise.resolve(v())
  const App: Record<string, AnyFn> = {
    Status: () => ok(status()),
    BandwidthHistory: (windowMs: number, maxBuckets: number) => ok(bandwidthHistory(windowMs, maxBuckets)),
    PeerStats: () => ok(isWizard || axisFresh ? [] : peerStats()),
    GetConfig: () => ok(config),
    PairingCode: () => gated(() => 'pf1://play.example.com:8474/3f8a1c9e2b7d4056a1b2c3d4e5f60718#sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08'),
    Version: () => ok('0.1.0-dev'),
    LogsSince: (seq: number) => ok(axisAttached ? [] : logs.filter(l => l.seq > seq)),
    TestReachability: () => new Promise(r => setTimeout(() => r('Reachable: play.example.com:25565 answered in 38ms — players can connect.'), 700)),
    SetupGateway: () => gated(() => undefined),
    SetupAgent: () => gated(() => undefined),
    SaveTunnels: (t: any) => { config.Agent.Tunnels = t; return ok(undefined) },
    SaveSettings: () => ok(undefined),
    SetTheme: (t: string) => { config.UI.Theme = t; return ok(undefined) },
    RestartEngine: () => gated(() => undefined),
    RegenerateToken: () => gated(() => undefined),
    OpenConfigDir: () => ok(undefined),
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
    ServiceStatus: () => ok('Not installed'),
    InstallService: () => ok(undefined),
    UninstallService: () => ok(undefined),
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
  // eslint-disable-next-line no-console
  console.info('[devmock] installed — scenario:', scenario)
}
