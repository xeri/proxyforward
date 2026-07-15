import {ReactNode, useEffect, useLayoutEffect, useRef, useState} from 'react'
import {
  BrowseMMDB, CreatorAvatar, CreatorInfo, FirewallRepair, FirewallStatus, GeoStatus, GetConfig,
  InstallService, OpenConfigDir, OpenCreatorURL, OpenExternal, PairingCode, RegenerateToken,
  RestartEngine, SaveSettings, ServiceStatus, UninstallService,
} from '../../wailsjs/go/app/App'
import {config, geo} from '../../wailsjs/go/models'
import {
  Badge, Banner, Button, Card, CopyButton, ErrorBanner, Field, FormRow, PageHeader,
  SegmentedControl, Select, Skeleton, Switch, TextInput, Toggle, WarnWash,
} from '../components/ui'
import {ExportSetupRow, ImportSetupFlow} from '../components/SetupBackup'
import {IconExternal, IconMonitor, IconMoon, IconRefresh, IconSun} from '../components/icons'
import {UIStatus} from '../state'
import {ThemePref, useTheme} from '../theme'
import {fxPref, setFxPref} from '../fx'
import {MotionPref, useMotion} from '../motion'

type Cfg = config.Config

// Engine defaults, mirrored from internal/config DefaultConfig. Numeric and
// address fields render an emptied value as a grayed placeholder of the
// default (never a bare "0"), and save() folds empties back to these so a
// cleared field can't fail backend validation.
const DEF = {
  controlPort: 8474,
  bindAddr: '0.0.0.0',
  maxConnsGlobal: 4096,
  maxConnsPerIP: 32,
  authAttemptsPerMin: 10,
  retentionDays: 180,
  prometheusAddr: '127.0.0.1:9464',
} as const

/** numStr renders a staged numeric field: 0 means "cleared" and shows as an
 * empty input so the default placeholder reads through. */
const numStr = (n: number) => (n > 0 ? String(n) : '')

type SectionDef = {id: string; label: string}

/** Settings: a scrollspy rail beside regrouped sections. Engine-affecting
 * changes stage into a dirty state and apply together from the save bar. */
export function Settings({status}: {status: UIStatus}) {
  const isAgent = status.role === 'agent'
  const attached = status.mode === 'attached'
  const [cfg, setCfg] = useState<Cfg | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')
  const [note, setNote] = useState('')

  const sections: SectionDef[] = [
    {id: 'appearance', label: 'Appearance'},
    {id: 'behavior', label: 'Behavior'},
    {id: 'connection', label: 'Connection'},
    ...(!isAgent ? [{id: 'security', label: 'Security'}] : []),
    {id: 'analytics', label: 'Analytics'},
    {id: 'telemetry', label: 'Telemetry'},
    {id: 'system', label: 'System'},
    {id: 'backup', label: 'Backup'},
    {id: 'about', label: 'About'},
  ]

  // Geo status reflects what the running engine loaded, not the staged config,
  // so it refreshes after a save (which restarts the engine) rather than on
  // every keystroke.
  const [geoStatus, setGeoStatus] = useState<geo.Status | null>(null)
  const refreshGeo = () => GeoStatus().then(setGeoStatus).catch(() => setGeoStatus(null))

  const reload = () => GetConfig().then(c => { setCfg(c); setDirty(false) }).catch(e => setErr(String(e)))
  useEffect(() => { reload(); refreshGeo() }, [])

  // Loading: skeletons that match the final geometry, so the real content
  // crossfades into place without a layout shift.
  if (!cfg) {
    return (
      <div className="mx-auto max-w-[62rem]">
        <PageHeader title="Settings" subtitle="Appearance, behavior, connection, and system integration." />
        <div className="grid grid-cols-1 items-start gap-6 md:grid-cols-[150px_minmax(0,44rem)]">
          <div className="hidden flex-col gap-1.5 md:flex" aria-hidden>
            {sections.map(s => <Skeleton key={s.id} className="h-7 w-full" />)}
          </div>
          <div className="space-y-4 pb-24" aria-busy="true">
            <Skeleton className="h-44 w-full rounded-[var(--r-lg)]" />
            <Skeleton className="h-56 w-full rounded-[var(--r-lg)]" />
            <Skeleton className="h-44 w-full rounded-[var(--r-lg)]" />
          </div>
        </div>
      </div>
    )
  }

  const patch = (fn: (c: Cfg) => void) => {
    const next = config.Config.createFrom(JSON.parse(JSON.stringify(cfg)))
    fn(next)
    setCfg(next); setDirty(true); setNote('')
  }

  const save = async () => {
    setSaving(true); setErr(''); setNote('')
    try {
      // Cleared fields staged as 0/'' mean "use the default" — fold them back
      // before validation so the promise the grayed placeholder makes holds.
      const out = config.Config.createFrom(JSON.parse(JSON.stringify(cfg)))
      out.Agent.GatewayPort = out.Agent.GatewayPort || DEF.controlPort
      out.Gateway.ControlPort = out.Gateway.ControlPort || DEF.controlPort
      out.Gateway.MaxConnsGlobal = out.Gateway.MaxConnsGlobal || DEF.maxConnsGlobal
      out.Gateway.MaxConnsPerIP = out.Gateway.MaxConnsPerIP || DEF.maxConnsPerIP
      out.Gateway.AuthAttemptsPerMin = out.Gateway.AuthAttemptsPerMin || DEF.authAttemptsPerMin
      out.Analytics.RetentionDays = out.Analytics.RetentionDays || DEF.retentionDays
      if (out.Metrics.PrometheusEnabled && !out.Metrics.PrometheusAddr.trim()) {
        out.Metrics.PrometheusAddr = DEF.prometheusAddr
      }
      await SaveSettings(out)
      setCfg(out)
      if (!attached) { try { await RestartEngine() } catch (e) { setErr(String(e)) } }
      refreshGeo()
      setDirty(false)
      setNote('Saved. The engine reconnected with the new settings.')
    } catch (e) { setErr(String(e)) }
    finally { setSaving(false) }
  }

  return (
    // Forms read best in a measured column — Settings caps its own width
    // instead of stretching across the full canvas, and the content column
    // is capped again so a label and its control never drift a monitor apart.
    <div className="mx-auto max-w-[62rem]">
      <PageHeader title="Settings" subtitle="Appearance, behavior, connection, and system integration." />
      {err && <div className="mb-4"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}
      {attached && (
        <div className="mb-4">
          <Banner tone="info">
            The Windows service owns this setup. Changes save here and apply once the service stops and this app runs the engine itself.
          </Banner>
        </div>
      )}

      <div className="grid grid-cols-1 items-start gap-6 md:grid-cols-[150px_minmax(0,44rem)]">
        <SectionRail sections={sections} />

        <div className="min-w-0">
        <div className="pf-stagger min-w-0 space-y-4 pb-24">
          <Section id="appearance" title="Appearance">
            <ThemeRow />
            <Divider />
            <MotionRow />
            <Divider />
            <FxRow />
          </Section>

          <Section id="behavior" title="Behavior" subtitle="How the app lives on this machine.">
            <Toggle checked={cfg.UI.MinimizeToTray} onChange={v => patch(c => { c.UI.MinimizeToTray = v })}
              label="Minimize to tray" hint="Keep running in the background when the window closes." />
            <Toggle checked={cfg.UI.Autostart} onChange={v => patch(c => { c.UI.Autostart = v })}
              label="Start on login" hint="Launch proxyforward when you sign in to Windows." />
          </Section>

          {isAgent ? (
            <Section id="connection" title="Gateway connection" subtitle="Editable without re-pairing — DNS re-resolves on every reconnect.">
              <div className="grid grid-cols-3 gap-3">
                <div className="col-span-2">
                  <Field label="Gateway address"><TextInput mono value={cfg.Agent.GatewayHost}
                    onChange={v => patch(c => { c.Agent.GatewayHost = v })} placeholder="play.example.com" /></Field>
                </div>
                <Field label="Control port"><TextInput mono value={numStr(cfg.Agent.GatewayPort)} placeholder={String(DEF.controlPort)}
                  onChange={v => patch(c => { c.Agent.GatewayPort = parseInt(v, 10) || 0 })} /></Field>
              </div>
              <div className="mt-3">
                <Field label="Transport" hint="Per-connection avoids TCP head-of-line blocking on lossy links, at the cost of more outbound connections.">
                  <Select value={cfg.Agent.Transport} onChange={v => patch(c => { c.Agent.Transport = v })} options={[
                    {value: 'mux', label: 'Multiplexed (default) — one connection'},
                    {value: 'per-conn', label: 'Per-connection — one outbound conn per player'},
                  ]} />
                </Field>
              </div>
            </Section>
          ) : (
            <Section id="connection" title="Gateway" subtitle="Where players and agents reach this machine.">
              <div className="grid grid-cols-3 gap-3">
                <div className="col-span-2">
                  <Field label="Public address" hint="Embedded in pairing codes. A stable DNS name (DDNS is fine) survives IP changes.">
                    <TextInput mono value={cfg.Gateway.PublicHost} onChange={v => patch(c => { c.Gateway.PublicHost = v })} placeholder="play.example.com" /></Field>
                </div>
                <Field label="Control port"><TextInput mono value={numStr(cfg.Gateway.ControlPort)} placeholder={String(DEF.controlPort)}
                  onChange={v => patch(c => { c.Gateway.ControlPort = parseInt(v, 10) || 0 })} /></Field>
              </div>
              <div className="mt-3">
                <Field label="Bind address" hint="0.0.0.0 listens on all interfaces.">
                  <TextInput mono value={cfg.Gateway.BindAddr} placeholder={DEF.bindAddr}
                    onChange={v => patch(c => { c.Gateway.BindAddr = v })} /></Field>
              </div>
            </Section>
          )}

          {!isAgent && (
            <Section id="security" title="Security" subtitle="Pairing and abuse limits, enforced at the gateway.">
              <FormRow
                label="Pairing token"
                hint="Anyone who has the pairing code can connect an agent to this gateway — never share it publicly. Rotating it disconnects agents until they re-pair with the new code."
              >
                <div className="flex items-center gap-2">
                  <CopyPairingCode attached={attached} />
                  <TokenRotate attached={attached} onDone={reload} />
                </div>
              </FormRow>
              <Divider />
              <div className="pt-1 text-sm font-medium text-[var(--text)]">Abuse limits</div>
              <div className="mt-2 grid grid-cols-3 gap-3">
                <Field label="Max connections"><TextInput mono value={numStr(cfg.Gateway.MaxConnsGlobal)} placeholder={String(DEF.maxConnsGlobal)}
                  onChange={v => patch(c => { c.Gateway.MaxConnsGlobal = parseInt(v, 10) || 0 })} /></Field>
                <Field label="Max per client IP"><TextInput mono value={numStr(cfg.Gateway.MaxConnsPerIP)} placeholder={String(DEF.maxConnsPerIP)}
                  onChange={v => patch(c => { c.Gateway.MaxConnsPerIP = parseInt(v, 10) || 0 })} /></Field>
                <Field label="Auth attempts / min"><TextInput mono value={numStr(cfg.Gateway.AuthAttemptsPerMin)} placeholder={String(DEF.authAttemptsPerMin)}
                  onChange={v => patch(c => { c.Gateway.AuthAttemptsPerMin = parseInt(v, 10) || 0 })} /></Field>
              </div>
            </Section>
          )}

          <Section id="analytics" title="Analytics" subtitle="History, player identity, and geo enrichment — everything is stored locally, nothing leaves this machine.">
            <Field label="History retention" hint="How long connection and player history is kept before it's pruned (1–3650 days).">
              <div className="w-40">
                <TextInput mono value={numStr(cfg.Analytics.RetentionDays)} placeholder={String(DEF.retentionDays)}
                  onChange={v => patch(c => { c.Analytics.RetentionDays = parseInt(v, 10) || 0 })} />
              </div>
            </Field>
            <Divider />
            <Toggle checked={cfg.Analytics.MojangLookups} onChange={v => patch(c => { c.Analytics.MojangLookups = v })}
              label="Resolve player identities"
              hint="Look up sniffed usernames against Mojang for UUIDs and skins. Turn off for offline-mode (cracked) servers." />
            <Divider />
            <div className="pt-1 text-sm font-medium text-[var(--text)]">GeoIP databases</div>
            <div className="mt-1 text-xs leading-relaxed text-[var(--text-3)]">
              Optional MaxMind GeoLite2 <code className="rounded-[var(--r-xs)] bg-[var(--panel-2)] px-1 py-0.5 font-mono">.mmdb</code> files
              add country and network data to sessions. They aren't bundled — download them free with a{' '}
              <button onClick={() => OpenExternal('https://www.maxmind.com/en/geolite2/signup')}
                className="text-[var(--accent)] hover:underline">MaxMind account</button>.
            </div>
            <div className="mt-3 space-y-3">
              <MmdbField label="City database (country)" path={cfg.Analytics.GeoIPCityPath}
                onChange={p => patch(c => { c.Analytics.GeoIPCityPath = p })}
                loaded={geoStatus?.cityLoaded ?? false} error={geoStatus?.cityError}
                attached={attached} browseTitle="Select GeoLite2 City database" />
              <MmdbField label="ASN database (network)" path={cfg.Analytics.GeoIPASNPath}
                onChange={p => patch(c => { c.Analytics.GeoIPASNPath = p })}
                loaded={geoStatus?.asnLoaded ?? false} error={geoStatus?.asnError}
                attached={attached} browseTitle="Select GeoLite2 ASN database" />
            </div>
          </Section>

          <Section id="telemetry" title="Telemetry" subtitle="Logging detail and the optional metrics endpoint — everything is stored locally, nothing is sent anywhere.">
            {/* Both cells are Fields, so the two labels share a baseline, and
                the switch stands in a box exactly as tall as the Select
                (--control-h) so it centers on the dropdown instead of floating
                above it. Pairing a Field with a whole Toggle ROW here — label
                beside control, nudged by an ad-hoc `items-end pb-1` — is why
                nothing lined up. */}
            <div className="grid grid-cols-2 gap-3">
              <Field label="Log level">
                <Select value={cfg.Logging.Level} onChange={v => patch(c => { c.Logging.Level = v })} options={[
                  {value: 'debug', label: 'Debug'}, {value: 'info', label: 'Info'},
                  {value: 'warn', label: 'Warn'}, {value: 'error', label: 'Error'},
                ]} />
              </Field>
              <Field label="Write to log file">
                <div className="flex h-[var(--control-h)] items-center">
                  <Switch checked={cfg.Logging.FileEnabled} onChange={v => patch(c => { c.Logging.FileEnabled = v })}
                    label="Write to log file" />
                </div>
              </Field>
            </div>
            <Divider />
            <Toggle checked={cfg.Metrics.PrometheusEnabled} onChange={v => patch(c => { c.Metrics.PrometheusEnabled = v })}
              label="Prometheus endpoint" hint="Expose /metrics for scraping. Off by default." />
            {cfg.Metrics.PrometheusEnabled && (
              <div className="mt-2"><Field label="Listen address"><TextInput mono value={cfg.Metrics.PrometheusAddr} placeholder={DEF.prometheusAddr}
                onChange={v => patch(c => { c.Metrics.PrometheusAddr = v })} /></Field></div>
            )}
          </Section>

          <SystemSection status={status} />

          <Section id="backup" title="Backup" subtitle="Move this setup to another machine — pairing, tunnels, keys, and statistics travel in one file.">
            {attached && <div className="pb-2 text-xs text-[var(--text-3)]">The background service owns this setup — stop the service to export or import.</div>}
            <div className="py-2"><ExportSetupRow disabled={attached} /></div>
            <div className="pf-sep my-4" />
            <div className="py-2"><ImportSetupFlow disabled={attached} onDone={reload} /></div>
          </Section>

          <AboutSection status={status} />

          {note && !dirty && <div className="pf-fade text-sm text-[var(--good)]">{note}</div>}
        </div>

        {/* Save bar: sticky inside the cards column so it centers on the
            cards, not the viewport. Lives outside the pf-stagger list — a
            late-mounting child would inherit the entrance cascade's delay.
            Opaque menu glass with the accent bleeding through from behind:
            unsaved state should glow, not whisper. */}
        {dirty && (
          <div className="pointer-events-none sticky bottom-6 z-40 -mt-14 flex justify-center pb-1">
            <div
              className="pf-slide-up pf-menu pf-bleed pointer-events-auto relative flex items-center gap-4 rounded-[var(--r-lg)] py-3 pl-5 pr-3"
              style={{
                ['--bleed' as string]: 'var(--accent)',
                ['--bleed-strength' as string]: '28%',
                // The bevel pair comes from .pf-menu's background bands now —
                // restating it as inset shadows specks the corners (glass.css).
                boxShadow: '0 0 0 1px color-mix(in srgb, var(--accent) 40%, var(--border-strong)), 0 12px 48px -12px color-mix(in srgb, var(--accent) 55%, transparent), var(--shadow-pop)',
              }}
            >
              <span className="text-sm font-medium text-[var(--text)]">Unsaved changes</span>
              <div className="flex items-center gap-2">
                <Button variant="ghost" onClick={reload}>Discard</Button>
                <Button onClick={save} disabled={saving}>{saving ? 'Saving…' : 'Save & apply'}</Button>
              </div>
            </div>
          </div>
        )}
        </div>
      </div>
    </div>
  )
}

/** SectionRail: sticky scrollspy nav. Tracks the scroll position of the app's
 * main scroll container and a traveling accent indicator glides to the
 * section under the reading line. */
function SectionRail({sections}: {sections: SectionDef[]}) {
  const [active, setActive] = useState(sections[0]?.id ?? '')
  const railRef = useRef<HTMLElement>(null)
  // Indicator geometry is measured, not tokenized: buttons are content-sized
  // and the list changes with role (Security). The sticky nav is both the
  // offsetParent and the containing block, so offsetTop maps 1:1.
  const [ind, setInd] = useState<{top: number; height: number} | null>(null)
  // Transitions arm one frame after mount — the first measurement must land
  // without animating or the indicator flies in from y=0 on every visit.
  const [armed, setArmed] = useState(false)
  useEffect(() => { setArmed(true) }, [])

  const sectionsKey = sections.map(s => s.id).join(',')
  useLayoutEffect(() => {
    const btn = railRef.current?.querySelector<HTMLElement>(`[data-sec="${active}"]`)
    if (btn) setInd({top: btn.offsetTop, height: btn.offsetHeight})
  }, [active, sectionsKey])

  // Click pinning: a jump highlights its target immediately and holds it
  // while the smooth scroll plays out — otherwise the spy recomputes mid-
  // flight and a bottom-clamped target (Backup) loses to whatever the scroll
  // settles on. The pin releases once scroll events go quiet.
  const pinRef = useRef(false)
  const settleRef = useRef(0)

  useEffect(() => {
    const scroller = railRef.current?.closest('main')
    if (!scroller) return
    const onScroll = () => {
      if (pinRef.current) {
        window.clearTimeout(settleRef.current)
        settleRef.current = window.setTimeout(() => { pinRef.current = false }, 160)
        return
      }
      // The titlebar overlays the scroller; its height is the scroller's top
      // padding, so the reading line starts 96px below the visible chrome.
      const chrome = parseFloat(getComputedStyle(scroller).paddingTop) || 0
      const rectTop = scroller.getBoundingClientRect().top
      const base = rectTop + chrome + 96
      // The line descends with scroll progress, reaching the bottom of the
      // scrollport at full scroll: sections in the last screenful (Backup)
      // whose tops can never climb to a fixed line still get their turn, in
      // order, before the scroller runs out of travel.
      const maxScroll = scroller.scrollHeight - scroller.clientHeight
      const progress = maxScroll > 0 ? Math.min(1, scroller.scrollTop / maxScroll) : 0
      const floor = rectTop + scroller.clientHeight - 24
      const line = base + Math.max(0, floor - base) * progress
      let current = sections[0]?.id ?? ''
      for (const s of sections) {
        const el = document.getElementById(`s-${s.id}`)
        if (el && el.getBoundingClientRect().top <= line) current = s.id
      }
      setActive(current)
    }
    onScroll()
    scroller.addEventListener('scroll', onScroll, {passive: true})
    return () => {
      scroller.removeEventListener('scroll', onScroll)
      window.clearTimeout(settleRef.current)
    }
  }, [sections.map(s => s.id).join(',')])

  const jump = (id: string) => {
    setActive(id)
    pinRef.current = true
    // A jump that needs no scrolling emits no scroll events — release the
    // pin on a timer so the spy doesn't stay frozen.
    window.clearTimeout(settleRef.current)
    settleRef.current = window.setTimeout(() => { pinRef.current = false }, 400)
    document.getElementById(`s-${id}`)?.scrollIntoView({behavior: 'smooth', block: 'start'})
  }

  return (
    <nav ref={railRef} className="sticky top-[var(--chrome-top)] hidden flex-col gap-0.5 md:flex" aria-label="Settings sections">
      {/* Traveling highlight: the same glass the sidebar's active nav wears —
          accent internal glow, lit ring, inset catch-light (pf-nav-glow) —
          gliding between items instead of teleporting. Buttons keep a
          transparent border for identical geometry. Opacity stays outside
          the transition list — it flips instantly on first measure. */}
      <div
        aria-hidden
        className={`pf-nav-glow pointer-events-none absolute left-0 w-full rounded-[var(--r-sm)] ${
          armed ? 'transition-[transform,height] duration-[var(--dur-slow)] [transition-timing-function:var(--ease-spring)]' : ''
        }`}
        style={{height: ind?.height ?? 0, transform: `translateY(${ind?.top ?? 0}px)`, opacity: ind ? 1 : 0}}
      />
      {sections.map(s => (
        <button
          key={s.id}
          data-sec={s.id}
          onClick={() => jump(s.id)}
          className={`relative rounded-[var(--r-sm)] border border-transparent px-3 py-1.5 text-left text-[13px] transition-colors duration-200 ${
            active === s.id ? 'font-medium text-[var(--text)]' : 'text-[var(--text-3)] hover:text-[var(--text)]'
          }`}
        >{s.label}</button>
      ))}
    </nav>
  )
}

/** ThemeRow: Light / Dark / System, applied instantly and persisted. */
function ThemeRow() {
  const {pref, setPref} = useTheme()
  return (
    <div className="flex items-center justify-between gap-4 py-2">
      <div>
        <div className="text-sm font-medium text-[var(--text)]">Theme</div>
        <div className="mt-0.5 text-xs text-[var(--text-3)]">System follows Windows and switches live.</div>
      </div>
      <SegmentedControl<ThemePref>
        value={pref}
        onChange={setPref}
        className="shrink-0"
        options={[
          {value: 'light', label: <><IconSun size={13} /> Light</>},
          {value: 'dark', label: <><IconMoon size={13} /> Dark</>},
          {value: 'system', label: <><IconMonitor size={13} /> System</>},
        ]}
      />
    </div>
  )
}

/** MotionRow: overrides the OS reduced-motion signal (Windows "Animation
 * effects"). On/Off force it; System follows Windows. Client-side only,
 * applied instantly via data-motion (motion.ts). */
function MotionRow() {
  const {pref, setPref} = useMotion()
  return (
    <div className="flex items-center justify-between gap-4 py-2">
      <div>
        <div className="text-sm font-medium text-[var(--text)]">Animations</div>
        <div className="mt-0.5 text-xs text-[var(--text-3)]">System follows Windows' animation-effects setting.</div>
      </div>
      <SegmentedControl<MotionPref>
        value={pref}
        onChange={setPref}
        className="shrink-0"
        options={[
          {value: 'on', label: 'On'},
          {value: 'off', label: 'Off'},
          {value: 'system', label: <><IconMonitor size={13} /> System</>},
        ]}
      />
    </div>
  )
}

/** FxRow: the low-fx escape hatch — solid cards, no moving reflections.
 * Purely client-side (localStorage), applied instantly via data-fx. */
function FxRow() {
  const [low, setLow] = useState(fxPref() === 'low')
  return (
    <Toggle
      checked={low}
      onChange={v => { setLow(v); setFxPref(v ? 'low' : '') }}
      label="Reduce glass effects"
      hint="Turns off card blur and moving reflections. Helps on low-end GPUs."
    />
  )
}

/** CopyPairingCode: copies the full pairing code (the string an agent pairs
 * with — it embeds the token) without having to reveal it on screen. Engine
 * mode only: attached, the service owns the config and the code isn't
 * readable from here. */
function CopyPairingCode({attached}: {attached: boolean}) {
  const [code, setCode] = useState('')
  useEffect(() => {
    if (attached) return
    let cancelled = false
    PairingCode().then(c => { if (!cancelled) setCode(c) }).catch(() => {})
    return () => { cancelled = true }
  }, [attached])
  if (attached || !code) return null
  return <CopyButton text={code} size="sm" label="Copy code" />
}

function TokenRotate({attached, onDone}: {attached: boolean; onDone: () => void}) {
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const [err, setErr] = useState('')
  const rotate = async () => {
    setBusy(true); setErr('')
    try { await RegenerateToken(); setConfirm(false); onDone() }
    catch (e) { setErr(String(e)) }
    finally { setBusy(false) }
  }
  if (attached) return <span className="text-xs text-[var(--text-3)]">Rotate from the service's own config</span>
  return (
    <div className="flex flex-col items-end gap-1">
      {confirm
        ? <div className="pf-fade flex items-center gap-2">
            <span className="text-xs text-[var(--warn)]">Disconnect agents?</span>
            <Button variant="ghost" size="sm" onClick={() => setConfirm(false)}>No</Button>
            <Button variant="danger" size="sm" onClick={rotate} disabled={busy}>{busy ? '…' : 'Rotate'}</Button>
          </div>
        : <Button variant="ghost" size="sm" onClick={() => setConfirm(true)}><IconRefresh size={14} /> Rotate</Button>}
      {err && <span className="text-xs text-[var(--bad)]">{err}</span>}
    </div>
  )
}

/** SystemSection: Windows integration + the config file + build identity. */
function SystemSection({status}: {status: UIStatus}) {
  const [fw, setFw] = useState<boolean | null>(null)
  const [svc, setSvc] = useState<string>('…')
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')

  const refresh = () => {
    FirewallStatus().then(setFw).catch(() => setFw(null))
    ServiceStatus().then(setSvc).catch(() => setSvc('unknown'))
  }
  useEffect(() => { refresh() }, [])

  const wrap = (key: string, fn: () => Promise<void>) => async () => {
    setBusy(key); setErr('')
    try { await fn() } catch (e) { setErr(String(e)) }
    finally { setBusy(''); refresh() }
  }
  const s = svcDisplay(svc)

  return (
    <Section id="system" title="System" subtitle="Windows integration and where this install lives.">
      <WarnWash on={fw === false}>
        <FormRow label="Firewall rule" hint="Allows inbound player and agent connections.">
          <div className="flex items-center gap-2">
            {fw === null ? <Badge tone="neutral">Unknown</Badge> : <Badge tone={fw ? 'good' : 'warn'}>{fw ? 'Present' : 'Missing'}</Badge>}
            {!fw && <Button variant="ghost" size="sm" onClick={wrap('fw', FirewallRepair)} disabled={busy === 'fw'}>{busy === 'fw' ? '…' : 'Add rule'}</Button>}
          </div>
        </FormRow>
      </WarnWash>
      <Divider />
      <WarnWash on={s.tone === 'warn'}>
        <FormRow label="Windows service" hint="Run headless in the background; the app attaches as a viewer.">
          <div className="flex items-center gap-2">
            <Badge tone={s.tone}>{s.label}</Badge>
            {s.installed
              ? <Button variant="danger" size="sm" onClick={wrap('svc', UninstallService)} disabled={busy === 'svc'}>{busy === 'svc' ? '…' : 'Uninstall'}</Button>
              : <Button variant="ghost" size="sm" onClick={wrap('svc', InstallService)} disabled={busy === 'svc'}>{busy === 'svc' ? '…' : 'Install'}</Button>}
          </div>
        </FormRow>
      </WarnWash>
      <Divider />
      {/* Stacked, not a FormRow: FormRow's control side is shrink-0 and this
          <code> was capped at 24rem inside a 44rem column, so a real Windows
          path (C:\Users\…\AppData\Roaming\proxyforward\config.toml) still wrapped
          hard against a needlessly small box. The path gets the whole column;
          a truncated "…" would read as a bug on a value people paste into a
          terminal, so it wraps instead. */}
      <div className="py-2">
        <div className="text-sm font-medium text-[var(--text)]">Config file</div>
        <div className="mt-2 flex items-start gap-2">
          <code className="min-w-0 flex-1 select-text break-all rounded-[var(--r-xs)] bg-[var(--panel-2)] px-2 py-1.5 font-mono text-xs leading-relaxed text-[var(--text-2)]">{status.configPath}</code>
          <Button variant="ghost" size="sm" onClick={() => OpenConfigDir()}><IconExternal size={14} /> Open</Button>
        </div>
      </div>
      {err && <div className="mt-2"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}
    </Section>
  )
}

/** svcDisplay normalizes the backend's raw service state ("running",
 * "stopped", "not-installed", "unknown", or the "…" loading sentinel) into a
 * themed label + tone. Robust to the hyphen/case the backend actually emits —
 * the previous string compare used a space and mis-read "not-installed" as
 * installed, showing a green badge and an Uninstall button. */
function svcDisplay(svc: string): {label: string; tone: 'good' | 'warn' | 'neutral'; installed: boolean} {
  switch (svc.toLowerCase().replace(/[-_ ]/g, '')) {
    case 'running': return {label: 'Running', tone: 'good', installed: true}
    case 'stopped': return {label: 'Stopped', tone: 'warn', installed: true}
    case 'notinstalled': return {label: 'Not installed', tone: 'neutral', installed: false}
    case '…': return {label: '…', tone: 'neutral', installed: false}
    default: return {label: 'Unknown', tone: 'neutral', installed: false}
  }
}

/** AboutSection: creator credit with a small GitHub avatar (weekly-cached by
 * the backend) and a link out to the profile. */
function AboutSection({status}: {status: UIStatus}) {
  const [avatar, setAvatar] = useState('')
  const [info, setInfo] = useState<{name: string; url: string}>({name: 'xeri', url: 'https://github.com/xeri'})
  useEffect(() => {
    CreatorInfo().then(i => setInfo({name: i.name || 'xeri', url: i.url || 'https://github.com/xeri'})).catch(() => {})
    CreatorAvatar().then(setAvatar).catch(() => {})
  }, [])
  return (
    <Section id="about" title="About">
      <div className="flex items-center gap-4 py-1">
        <button
          onClick={() => OpenCreatorURL()}
          title={`Open ${info.url}`}
          className="shrink-0 rounded-full transition-transform duration-150 hover:scale-105"
        >
          {avatar
            ? <img src={avatar} alt={info.name} width={48} height={48} className="h-12 w-12 rounded-full border border-[var(--border)] object-cover" />
            : <span className="grid h-12 w-12 place-items-center rounded-full border border-[var(--border)] bg-[var(--panel-2)] text-lg font-semibold text-[var(--text-2)]">{info.name.slice(0, 1).toUpperCase()}</span>}
        </button>
        <div className="min-w-0">
          <div className="text-sm text-[var(--text-2)]">Created by</div>
          <button onClick={() => OpenCreatorURL()} className="group inline-flex items-center gap-1.5 text-base font-semibold text-[var(--text)] hover:text-[var(--accent)]">
            {info.name}
            <IconExternal size={14} className="text-[var(--text-3)] group-hover:text-[var(--accent)]" />
          </button>
          <div className="mt-0.5 truncate text-xs text-[var(--text-3)]">{info.url}</div>
        </div>
      </div>
      <Divider />
      <FormRow label="Version">
        <span className="select-text text-sm tabular-nums text-[var(--text-2)]">{status.version} · pid {status.pid} · {status.mode}</span>
      </FormRow>
    </Section>
  )
}

/** MmdbField: a GeoLite2 database path with a Browse picker and a live status
 * badge. The badge reflects what the engine actually loaded (from GeoStatus),
 * so it lags a staged edit until the change is saved and the engine restarts —
 * a "Pending" state bridges that gap. Browse is engine-mode only; attached, the
 * path is still editable and applies when the service next hands off. */
function MmdbField({label, path, onChange, loaded, error, attached, browseTitle}: {
  label: string; path: string; onChange: (p: string) => void
  loaded: boolean; error?: string; attached: boolean; browseTitle: string
}) {
  const [busy, setBusy] = useState(false)
  const browse = async () => {
    setBusy(true)
    try { const p = await BrowseMMDB(browseTitle); if (p) onChange(p) }
    catch { /* picker cancelled */ }
    finally { setBusy(false) }
  }
  return (
    <Field label={label}>
      <div className="flex items-center gap-2">
        <div className="min-w-0 flex-1">
          <TextInput mono value={path} onChange={onChange} placeholder="Not set" />
        </div>
        {!attached && <Button variant="ghost" size="sm" onClick={browse} disabled={busy}>{busy ? '…' : 'Browse'}</Button>}
        <MmdbBadge path={path} loaded={loaded} error={error} />
      </div>
    </Field>
  )
}

/** MmdbBadge: nothing when no path is set; otherwise Loaded / Failed / Pending
 * from the engine's view of that path. */
function MmdbBadge({path, loaded, error}: {path: string; loaded: boolean; error?: string}) {
  if (!path.trim()) return null
  if (error) return <span title={error}><Badge tone="warn">Failed</Badge></span>
  if (loaded) return <Badge tone="good">Loaded</Badge>
  return <Badge tone="neutral">Pending</Badge>
}

function Section({id, title, subtitle, action, children}: {
  id: string; title: string; subtitle?: string; action?: ReactNode; children: ReactNode
}) {
  return (
    <div id={`s-${id}`} className="scroll-mt-6">
      <Card title={title} subtitle={subtitle} action={action}>{children}</Card>
    </div>
  )
}

function Divider() { return <div className="pf-sep my-1" /> }
