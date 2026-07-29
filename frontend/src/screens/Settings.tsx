import {ReactNode, useEffect, useLayoutEffect, useRef, useState} from 'react'
import {
  AutostartEnabled, BrowseMMDB, CheckForUpdate, CreatorAvatar, CreatorInfo, FirewallRepair, FirewallStatus, GeoStatus, GetConfig,
  InstallService, OpenConfigDir, OpenCreatorURL, OpenExternal,
  RestartEngine, SaveSettings, ServiceStatus, SetAutostart, SetMinimizeToTray, SetUpdateCheck, StartService, StopService, UninstallService,
} from '../../wailsjs/go/app/App'
import {app, config, geo} from '../../wailsjs/go/models'
import {
  Badge, Banner, Button, Card, CopyButton, Disclosure, ErrorBanner, Field, FormRow, Modal,
  PageHeader, SegmentedControl, Select, Skeleton, Switch, TextInput, Toggle, WarnWash,
} from '../components/ui'
import {ExportSetupRow, ImportSetupFlow} from '../components/SetupBackup'
import {IconExternal, IconMonitor, IconMoon, IconRefresh, IconSun} from '../components/icons'
import {fmtRelative, isEngineOff, runStateOf, TRANSPORT_LABEL, UIStatus} from '../state'
import {StopEngine} from '../../wailsjs/go/app/App'
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
  maxTunnelsPerAgent: 32,
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
        <PageHeader eyebrow="precision" title="Settings" subtitle="Appearance, behavior, connection, and system integration." />
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
      out.Gateway.MaxTunnelsPerAgent = out.Gateway.MaxTunnelsPerAgent || DEF.maxTunnelsPerAgent
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
      <PageHeader eyebrow="precision" title="Settings" subtitle="Appearance, behavior, connection, and system integration." />
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
                <Field label="Transport" hint="Automatic prefers QUIC — one connection where each player's packet loss can't stall the others — and falls back to per-connection or multiplexed if UDP is blocked. QUIC gives that isolation over a single connection; per-connection gives each player a dedicated connection; multiplexed shares one connection across all players (simplest, but one player's loss stalls everyone).">
                  <Select value={cfg.Agent.Transport} onChange={v => patch(c => { c.Agent.Transport = v })} options={[
                    {value: 'auto', label: 'Automatic (recommended) — prefer QUIC, fall back'},
                    {value: 'quic', label: 'QUIC — one connection, no head-of-line blocking'},
                    {value: 'per-conn', label: 'Per-connection — one per player'},
                    {value: 'mux', label: 'Multiplexed — one shared connection'},
                  ]} />
                </Field>
                {status.linkUp && status.transport && (
                  <div className="mt-1.5 text-xs text-[var(--text-3)]">
                    Currently connected via <span className="text-[var(--text-2)]">{TRANSPORT_LABEL[status.transport] ?? status.transport}</span>
                  </div>
                )}
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
            <Section id="security" title="Security" subtitle="Abuse limits, enforced at the gateway. Pairing codes live on Overview and the Agents screen.">
              <div className="pt-1 text-sm font-medium text-[var(--text)]">Abuse limits</div>
              <div className="mt-2 grid grid-cols-3 gap-3">
                <Field label="Max connections"><TextInput mono value={numStr(cfg.Gateway.MaxConnsGlobal)} placeholder={String(DEF.maxConnsGlobal)}
                  onChange={v => patch(c => { c.Gateway.MaxConnsGlobal = parseInt(v, 10) || 0 })} /></Field>
                <Field label="Max per client IP"><TextInput mono value={numStr(cfg.Gateway.MaxConnsPerIP)} placeholder={String(DEF.maxConnsPerIP)}
                  onChange={v => patch(c => { c.Gateway.MaxConnsPerIP = parseInt(v, 10) || 0 })} /></Field>
                <Field label="Auth attempts / min"><TextInput mono value={numStr(cfg.Gateway.AuthAttemptsPerMin)} placeholder={String(DEF.authAttemptsPerMin)}
                  onChange={v => patch(c => { c.Gateway.AuthAttemptsPerMin = parseInt(v, 10) || 0 })} /></Field>
              </div>
              <Field label="Max tunnels per agent" hint="How many public ports one agent may hold open at once. The three above bound the public internet; this one bounds an agent you've already paired.">
                <div className="w-40">
                  <TextInput mono value={numStr(cfg.Gateway.MaxTunnelsPerAgent)} placeholder={String(DEF.maxTunnelsPerAgent)}
                    onChange={v => patch(c => { c.Gateway.MaxTunnelsPerAgent = parseInt(v, 10) || 0 })} />
                </div>
              </Field>
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

          <SystemSection
            status={status}
            trayOn={!!cfg.UI.MinimizeToTray}
            onTrayToggle={v => {
              // Immediate like the theme — a UI preference, not a settings
              // apply. The staged copy is kept in sync (without dirtying) so a
              // later Save can't revert what the toggle already wrote.
              const next = config.Config.createFrom(JSON.parse(JSON.stringify(cfg)))
              next.UI.MinimizeToTray = v
              setCfg(next)
              SetMinimizeToTray(v).catch(e => setErr(String(e)))
            }}
            // Immediate, and the staged copy follows for the same reason the
            // tray one does — with teeth here: SaveSettings reconciles the Run
            // key from this document, so a stale staged value would undo the
            // toggle on the next unrelated save. The promise is returned so the
            // section can show the failure beside the control that caused it.
            onAutostartToggle={v => {
              const next = config.Config.createFrom(JSON.parse(JSON.stringify(cfg)))
              next.UI.Autostart = v
              setCfg(next)
              return SetAutostart(v)
            }}
          />

          <Section id="backup" title="Backup" subtitle="Move this setup to another machine — pairing, tunnels, keys, and statistics travel in one file.">
            {attached && <div className="pb-2 text-xs text-[var(--text-3)]">The background service owns this setup — stop the service to export or import.</div>}
            <div className="py-2"><ExportSetupRow disabled={attached} /></div>
            <div className="pf-sep my-4" />
            <div className="py-2"><ImportSetupFlow disabled={attached} onDone={reload} /></div>
          </Section>

          <AboutSection
            status={status}
            updateOn={!!cfg.UI.UpdateCheck}
            // Immediate, and the staged copy follows, exactly like the tray and
            // autostart toggles above: a later Save of an unrelated field must
            // not carry a stale value back over what this already wrote.
            onUpdateToggle={v => {
              const next = config.Config.createFrom(JSON.parse(JSON.stringify(cfg)))
              next.UI.UpdateCheck = v
              setCfg(next)
              return SetUpdateCheck(v)
            }}
          />

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
              className="pf-slide-up pf-menu pf-bleed pointer-events-auto relative flex items-center gap-4 rounded-[var(--r-btn)] py-3 pl-5 pr-3"
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
          className={`relative rounded-[var(--r-sm)] border border-transparent px-3 py-1.5 text-left text-emphasis transition-colors duration-200 ${
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

/** ForceStopRow: the escape hatch for a stop that will not land. A normal Stop
 * waits for the link to close cleanly; force skips that wait, cutting the run
 * where it stands. Kept behind Advanced because it is only ever the answer when
 * the graceful Stop is stuck — and it still names the players it drops. Hidden
 * while the run is already off; nothing to force then. */
function ForceStopRow({status}: {status: UIStatus}) {
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const players = (status.connections ?? []).length

  if (runStateOf(status) === 'stopped') return null

  const force = () => {
    setBusy(true)
    StopEngine(true).catch(() => {}).finally(() => setBusy(false))
  }
  const onClick = () => { if (players > 0) { setConfirm(true); return } force() }

  return (
    <>
      <FormRow label="Force stop" hint="Cut the run immediately instead of waiting for a clean shutdown.">
        <Button variant="danger" size="sm" onClick={onClick} disabled={busy}>Force stop</Button>
      </FormRow>
      {confirm && (
        <Modal
          title={`Disconnect ${players} ${players === 1 ? 'player' : 'players'}?`}
          onClose={() => setConfirm(false)}
          footer={
            <>
              <Button variant="ghost" size="sm" onClick={() => setConfirm(false)}>Cancel</Button>
              <Button variant="danger" size="sm" onClick={() => { setConfirm(false); force() }}>Force stop</Button>
            </>
          }
        >
          <p className="text-sm text-[var(--text-2)]">
            {players === 1 ? 'One player is' : `${players} players are`} connected. Force stop cuts the tunnel now, without waiting for the link to close cleanly.
          </p>
        </Modal>
      )}
    </>
  )
}

/** SystemSection: Windows integration + the config file + build identity. */
function SystemSection({status, trayOn, onTrayToggle, onAutostartToggle}: {
  status: UIStatus; trayOn: boolean; onTrayToggle: (v: boolean) => void
  onAutostartToggle: (v: boolean) => Promise<void>
}) {
  const [fw, setFw] = useState<boolean | null>(null)
  const [svc, setSvc] = useState<string>('…')
  // The startup entry is read from the registry, not from the config document:
  // it is shared state an uninstaller or another install can change, so the
  // machine answers for it. undefined = still asking, null = could not read.
  const [auto, setAuto] = useState<boolean | null | undefined>(undefined)
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')

  const refresh = () => {
    FirewallStatus().then(setFw).catch(() => setFw(null))
    ServiceStatus().then(setSvc).catch(() => setSvc('unknown'))
    AutostartEnabled().then(setAuto).catch(() => setAuto(null))
  }
  useEffect(() => { refresh() }, [])

  const wrap = (key: string, fn: () => Promise<void>) => async () => {
    setBusy(key); setErr('')
    try { await fn() } catch (e) { setErr(String(e)) }
    finally { setBusy(''); refresh() }
  }
  const s = svcDisplay(svc)

  // Optimistic, then confirmed: the switch answers the pointer immediately and
  // the re-read puts it back if the registry refused.
  const toggleAuto = (v: boolean) => {
    setAuto(v); setErr('')
    onAutostartToggle(v).catch(e => setErr(String(e))).finally(refresh)
  }

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
            {s.installed ? (
              <>
                {/* Start/Stop control the running service; each needs an elevated
                    prompt (SCM control is admin-only), which the binding raises. */}
                {s.label === 'Running'
                  ? <Button variant="ghost" size="sm" onClick={wrap('svc-run', StopService)} disabled={busy === 'svc-run'}>{busy === 'svc-run' ? '…' : 'Stop'}</Button>
                  : <Button variant="ghost" size="sm" onClick={wrap('svc-run', StartService)} disabled={busy === 'svc-run'}>{busy === 'svc-run' ? '…' : 'Start'}</Button>}
                <Button variant="danger" size="sm" onClick={wrap('svc', UninstallService)} disabled={busy === 'svc'}>{busy === 'svc' ? '…' : 'Uninstall'}</Button>
              </>
            ) : (
              <Button variant="ghost" size="sm" onClick={wrap('svc', InstallService)} disabled={busy === 'svc'}>{busy === 'svc' ? '…' : 'Install'}</Button>
            )}
          </div>
        </FormRow>
      </WarnWash>
      {/* A stopped service is a broken promise: it exists to run without a
          window open, so nobody may be watching to notice players can't
          connect. Louder than the badge, quieter than the crash banner. */}
      {s.installed && s.label === 'Stopped' && (
        <div className="mt-2">
          <Banner tone="warn">
            The Windows service is installed but stopped — it should be running so the tunnel stays up without this window open. Start it above.
          </Banner>
        </div>
      )}
      {/* Both preferences below live with the other Windows integration, and
          only where this process owns the run: an attached viewer's close never
          touches the daemon, and a daemon someone else starts needs no sign-in
          from us — so neither is a preference this window gets to hold.

          Start on login sits directly under the service because they answer the
          same question — what brings proxyforward up without a person launching
          it — and the service is the better answer: it starts at boot, with
          nobody signed in, and survives logout. So when the service is
          installed this says so rather than quietly offering a second launch.
          It stays operable while it is ON, though: someone who already has both
          must be able to turn this one off, and a disabled switch would trap
          them in the overlap it is warning about. */}
      {status.mode !== 'attached' && (
        <>
          <Divider />
          <WarnWash on={s.installed && auto === true}>
            <Toggle
              checked={auto === true}
              onChange={toggleAuto}
              disabled={auto == null || (s.installed && auto === false)}
              label="Start on login"
              hint={autostartHint(auto, s.installed)}
            />
          </WarnWash>
          <Divider />
          <Toggle
            checked={trayOn}
            onChange={onTrayToggle}
            label="Close to tray"
            hint="Closing the window keeps the tunnel running in the tray. Turn off to quit on close."
          />
        </>
      )}
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
      {/* Advanced: the sharp controls, folded away so the common path stays
          calm. Only shown when this process owns the engine — an attached
          viewer forces nothing (and force stop is hidden anyway while off). */}
      {status.mode !== 'attached' && !isEngineOff(status) && (
        <>
          <Divider />
          <Disclosure label="Advanced">
            <ForceStopRow status={status} />
          </Disclosure>
        </>
      )}
      {err && <div className="mt-2"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}
    </Section>
  )
}

/** autostartHint writes the four states of the sign-in-launch row: still
 * asking, unreadable, overlapping the Windows service (on or off), and the
 * plain case. The overlap wording is deliberately concrete about what the
 * second launch actually does — it does not start a second tunnel, it opens a
 * window that attaches to the running one — because "conflict" would send
 * people hunting for a problem the pipe already solved. */
function autostartHint(auto: boolean | null | undefined, serviceInstalled: boolean): string {
  if (auto === undefined) return 'Checking the Windows startup entry…'
  if (auto === null) return 'The Windows startup entry could not be read, so this cannot be changed here.'
  if (serviceInstalled) {
    return auto
      ? 'The Windows service already starts the tunnel at boot, with nobody signed in — this only adds a window that attaches to it. Turn it off, or uninstall the service.'
      : 'Not needed: the Windows service already starts the tunnel at boot, with nobody signed in.'
  }
  return 'Signing in to Windows launches proxyforward, and the tunnel starts with it.'
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
 * the backend), a link out to the profile, and the release check.
 *
 * The release check lives here and NOT under Telemetry, whose subtitle promises
 * "nothing is sent anywhere" — a switch that makes an outbound call cannot sit
 * under that sentence without making it false. About is where this build's own
 * identity is already stated, and asking whether a newer one exists is a
 * question about exactly that. */
function AboutSection({status, updateOn, onUpdateToggle}: {
  status: UIStatus; updateOn: boolean; onUpdateToggle: (v: boolean) => Promise<void>
}) {
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
      <Divider />
      <Toggle
        checked={updateOn}
        onChange={v => { onUpdateToggle(v).catch(() => {}) }}
        label="Check for new releases"
        hint="Off by default. Asks GitHub once a day whether a newer version was published — nothing about you, this machine, or your tunnels is sent, and nothing installs itself. GitHub sees only that some copy asked, from this IP address."
      />
      <UpdateRow on={updateOn} />
    </Section>
  )
}

/** UpdateRow: the release check's states — asking, a newer version, up to date,
 * nothing published yet, and a failure that says so.
 *
 * Off renders nothing at all rather than an unknown: a row is a report, and
 * there is nothing to report about a question that was never asked.
 *
 * The verdict and the fact are separated on purpose. `comparable` false means
 * the two version strings could not be ranked (a hand-built binary, a tag
 * shaped like nothing) — so the tag is shown as a plain fact with no badge,
 * because "Up to date" there would be the one confident lie this feature is
 * built to avoid. */
function UpdateRow({on}: {on: boolean}) {
  const [info, setInfo] = useState<app.UpdateInfo | null>(null)
  const [err, setErr] = useState('')
  const [asking, setAsking] = useState(false)

  useEffect(() => {
    if (!on) { setInfo(null); setErr(''); return }
    // Guarded against a toggle flipped twice quickly: the first answer must not
    // land after the second and describe a state that is no longer current.
    let live = true
    setAsking(true); setErr('')
    CheckForUpdate()
      .then(i => { if (live) setInfo(i) })
      .catch(e => { if (live) setErr(String(e)) })
      .finally(() => { if (live) setAsking(false) })
    return () => { live = false }
  }, [on])

  if (!on) return null
  if (asking) return <FormRow label="Releases"><Skeleton className="h-4 w-24" /></FormRow>
  if (err) {
    return (
      <FormRow label="Releases" hint={err}>
        <Badge tone="warn">Could not check</Badge>
      </FormRow>
    )
  }
  if (!info) return null
  if (!info.latest) {
    return <FormRow label="Releases" hint="Nothing has been published yet.">
      <span className="text-sm text-[var(--text-3)]">—</span>
    </FormRow>
  }
  const checked = `Checked ${fmtRelative(info.checkedMs)}.`
  if (info.newer) {
    return (
      <FormRow label="Releases" hint={`${info.latest} is newer than this build. ${checked}`}>
        {/* Ghost, not primary. An available update is worth saying, not worth
            the one accent-filled call to action on a page whose real work is
            elsewhere — and nothing here installs anything, so the button only
            opens a page. */}
        <Button variant="ghost" size="sm" onClick={() => OpenExternal(info.url)}>
          View {info.latest} <IconExternal size={13} />
        </Button>
      </FormRow>
    )
  }
  if (!info.comparable) {
    return (
      <FormRow label="Releases" hint={`This build's version can't be compared to a release tag, so there is no verdict here. ${checked}`}>
        <Button variant="ghost" size="sm" onClick={() => OpenExternal(info.url)}>
          Latest is {info.latest} <IconExternal size={13} />
        </Button>
      </FormRow>
    )
  }
  return <FormRow label="Releases" hint={`${info.latest} is the newest published release. ${checked}`}>
    <Badge tone="good">Up to date</Badge>
  </FormRow>
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
