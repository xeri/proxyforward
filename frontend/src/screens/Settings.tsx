import {ReactNode, useEffect, useRef, useState} from 'react'
import {
  CreatorAvatar, CreatorInfo, FirewallRepair, FirewallStatus, GetConfig, InstallService,
  OpenConfigDir, OpenCreatorURL, RegenerateToken, RestartEngine, SaveSettings, ServiceStatus,
  UninstallService,
} from '../../wailsjs/go/app/App'
import {config} from '../../wailsjs/go/models'
import {
  Badge, Banner, Button, Card, ErrorBanner, Field, PageHeader, SegmentedControl,
  Select, Spinner, TextInput, Toggle,
} from '../components/ui'
import {ExportSetupRow, ImportSetupFlow} from '../components/SetupBackup'
import {IconExternal, IconMonitor, IconMoon, IconRefresh, IconSun} from '../components/icons'
import {UIStatus} from '../state'
import {ThemePref, useTheme} from '../theme'
import {fxPref, setFxPref} from '../fx'

type Cfg = config.Config

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
    {id: 'telemetry', label: 'Telemetry'},
    {id: 'system', label: 'System'},
    {id: 'backup', label: 'Backup'},
    {id: 'about', label: 'About'},
  ]

  const reload = () => GetConfig().then(c => { setCfg(c); setDirty(false) }).catch(e => setErr(String(e)))
  useEffect(() => { reload() }, [])

  if (!cfg) return <div className="flex justify-center py-16 text-[var(--text-3)]"><Spinner size={22} /></div>

  const patch = (fn: (c: Cfg) => void) => {
    const next = config.Config.createFrom(JSON.parse(JSON.stringify(cfg)))
    fn(next)
    setCfg(next); setDirty(true); setNote('')
  }

  const save = async () => {
    setSaving(true); setErr(''); setNote('')
    try {
      await SaveSettings(cfg)
      if (!attached) { try { await RestartEngine() } catch (e) { setErr(String(e)) } }
      setDirty(false)
      setNote('Saved. The engine reconnected with the new settings.')
    } catch (e) { setErr(String(e)) }
    finally { setSaving(false) }
  }

  return (
    <div>
      <PageHeader title="Settings" subtitle="Appearance, connection, and system integration." />
      {err && <div className="mb-4"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}
      {attached && (
        <div className="mb-4">
          <Banner tone="info">
            A background service owns this configuration. Changes save to this user's config and take effect when the service stops and this app runs the engine directly.
          </Banner>
        </div>
      )}

      <div className="grid grid-cols-1 items-start gap-6 md:grid-cols-[150px_minmax(0,1fr)]">
        <SectionRail sections={sections} />

        <div className="pf-stagger min-w-0 space-y-4 pb-24">
          <Section id="appearance" title="Appearance">
            <ThemeRow />
            <Divider />
            <FxRow />
            <Divider />
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
                <Field label="Control port"><TextInput mono value={String(cfg.Agent.GatewayPort)}
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
                <Field label="Control port"><TextInput mono value={String(cfg.Gateway.ControlPort)}
                  onChange={v => patch(c => { c.Gateway.ControlPort = parseInt(v, 10) || 0 })} /></Field>
              </div>
              <div className="mt-3">
                <Field label="Bind address" hint="0.0.0.0 listens on all interfaces.">
                  <TextInput mono value={cfg.Gateway.BindAddr} onChange={v => patch(c => { c.Gateway.BindAddr = v })} /></Field>
              </div>
            </Section>
          )}

          {!isAgent && (
            <Section id="security" title="Security" subtitle="Pairing and abuse limits, enforced at the gateway.">
              <Row label="Pairing token" hint="Rotating it disconnects agents until they re-pair with the new code.">
                <TokenRotate attached={attached} onDone={reload} />
              </Row>
              <Divider />
              <div className="pt-1 text-sm font-medium text-[var(--text)]">Abuse limits</div>
              <div className="mt-2 grid grid-cols-3 gap-3">
                <Field label="Max connections"><TextInput mono value={String(cfg.Gateway.MaxConnsGlobal)}
                  onChange={v => patch(c => { c.Gateway.MaxConnsGlobal = parseInt(v, 10) || 0 })} /></Field>
                <Field label="Max per client IP"><TextInput mono value={String(cfg.Gateway.MaxConnsPerIP)}
                  onChange={v => patch(c => { c.Gateway.MaxConnsPerIP = parseInt(v, 10) || 0 })} /></Field>
                <Field label="Auth attempts / min"><TextInput mono value={String(cfg.Gateway.AuthAttemptsPerMin)}
                  onChange={v => patch(c => { c.Gateway.AuthAttemptsPerMin = parseInt(v, 10) || 0 })} /></Field>
              </div>
            </Section>
          )}

          <Section id="telemetry" title="Telemetry" subtitle="Logging detail and the optional metrics endpoint.">
            <div className="grid grid-cols-2 gap-3">
              <Field label="Log level">
                <Select value={cfg.Logging.Level} onChange={v => patch(c => { c.Logging.Level = v })} options={[
                  {value: 'debug', label: 'Debug'}, {value: 'info', label: 'Info'},
                  {value: 'warn', label: 'Warn'}, {value: 'error', label: 'Error'},
                ]} />
              </Field>
              <div className="flex items-end pb-1">
                <Toggle checked={cfg.Logging.FileEnabled} onChange={v => patch(c => { c.Logging.FileEnabled = v })}
                  label="Write to log file" />
              </div>
            </div>
            <Divider />
            <Toggle checked={cfg.Metrics.PrometheusEnabled} onChange={v => patch(c => { c.Metrics.PrometheusEnabled = v })}
              label="Prometheus endpoint" hint="Expose /metrics for scraping. Off by default." />
            {cfg.Metrics.PrometheusEnabled && (
              <div className="mt-2"><Field label="Listen address"><TextInput mono value={cfg.Metrics.PrometheusAddr}
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
      </div>

      {/* Save bar: floats above everything while changes are staged. */}
      {dirty && (
        <div className="pf-slide-up pf-glass fixed bottom-6 left-1/2 z-40 flex items-center gap-3 rounded-[var(--r-lg)] px-4 py-2.5">
          <span className="text-sm text-[var(--text-2)]">Unsaved changes</span>
          <Button variant="ghost" size="sm" onClick={reload}>Discard</Button>
          <Button size="sm" onClick={save} disabled={saving}>{saving ? 'Saving…' : 'Save & apply'}</Button>
        </div>
      )}
    </div>
  )
}

/** SectionRail: sticky scrollspy nav. Tracks the scroll position of the app's
 * main scroll container and highlights the section under the reading line. */
function SectionRail({sections}: {sections: SectionDef[]}) {
  const [active, setActive] = useState(sections[0]?.id ?? '')
  const railRef = useRef<HTMLElement>(null)

  useEffect(() => {
    const scroller = railRef.current?.closest('main')
    if (!scroller) return
    const onScroll = () => {
      // The titlebar overlays the scroller; its height is the scroller's top
      // padding, so the reading line sits 96px below the visible chrome.
      const chrome = parseFloat(getComputedStyle(scroller).paddingTop) || 0
      const line = scroller.getBoundingClientRect().top + chrome + 96
      let current = sections[0]?.id ?? ''
      for (const s of sections) {
        const el = document.getElementById(`s-${s.id}`)
        if (el && el.getBoundingClientRect().top <= line) current = s.id
      }
      setActive(current)
    }
    onScroll()
    scroller.addEventListener('scroll', onScroll, {passive: true})
    return () => scroller.removeEventListener('scroll', onScroll)
  }, [sections.map(s => s.id).join(',')])

  const jump = (id: string) => {
    document.getElementById(`s-${id}`)?.scrollIntoView({behavior: 'smooth', block: 'start'})
  }

  return (
    <nav ref={railRef} className="sticky top-[var(--chrome-top)] hidden flex-col gap-0.5 md:flex" aria-label="Settings sections">
      {sections.map(s => (
        <button
          key={s.id}
          onClick={() => jump(s.id)}
          className={`rounded-[var(--r-sm)] border-l-2 px-3 py-1.5 text-left text-[13px] transition-all duration-200 ${
            active === s.id
              ? 'border-[var(--accent)] bg-[color-mix(in_srgb,var(--accent)_8%,transparent)] font-medium text-[var(--text)]'
              : 'border-transparent text-[var(--text-3)] hover:text-[var(--text)]'
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
        className="w-64"
        options={[
          {value: 'light', label: <><IconSun size={13} /> Light</>},
          {value: 'dark', label: <><IconMoon size={13} /> Dark</>},
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
    <Section id="system" title="System" subtitle="Windows integration and this install's identity.">
      <WarnWash on={fw === false}>
        <Row label="Firewall rule" hint="Allows inbound player and agent connections.">
          <div className="flex items-center gap-2">
            {fw === null ? <Badge tone="neutral">Unknown</Badge> : <Badge tone={fw ? 'good' : 'warn'}>{fw ? 'Present' : 'Missing'}</Badge>}
            {!fw && <Button variant="ghost" size="sm" onClick={wrap('fw', FirewallRepair)} disabled={busy === 'fw'}>{busy === 'fw' ? '…' : 'Add rule'}</Button>}
          </div>
        </Row>
      </WarnWash>
      <Divider />
      <WarnWash on={s.tone === 'warn'}>
        <Row label="Windows service" hint="Run headless in the background; the app attaches as a viewer.">
          <div className="flex items-center gap-2">
            <Badge tone={s.tone}>{s.label}</Badge>
            {s.installed
              ? <Button variant="danger" size="sm" onClick={wrap('svc', UninstallService)} disabled={busy === 'svc'}>{busy === 'svc' ? '…' : 'Uninstall'}</Button>
              : <Button variant="ghost" size="sm" onClick={wrap('svc', InstallService)} disabled={busy === 'svc'}>{busy === 'svc' ? '…' : 'Install'}</Button>}
          </div>
        </Row>
      </WarnWash>
      <Divider />
      <Row label="Config file">
        <div className="flex items-center gap-2">
          <code className="max-w-xs select-text truncate rounded-[var(--r-xs)] bg-[var(--panel-2)] px-2 py-1 font-mono text-xs text-[var(--text-2)]">{status.configPath}</code>
          <Button variant="ghost" size="sm" onClick={() => OpenConfigDir()}><IconExternal size={14} /> Open</Button>
        </div>
      </Row>
      <Divider />
      <Row label="Version"><span className="text-sm tabular-nums text-[var(--text-2)]">{status.version} · pid {status.pid} · {status.mode}</span></Row>
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
          <div className="mt-0.5 truncate text-xs text-[var(--text-3)]">{info.url} · {status.version}</div>
        </div>
      </div>
    </Section>
  )
}

function Section({id, title, subtitle, action, children}: {
  id: string; title: string; subtitle?: string; action?: ReactNode; children: ReactNode
}) {
  return (
    <div id={`s-${id}`} className="scroll-mt-6">
      <Card dot title={title} subtitle={subtitle} action={action}>{children}</Card>
    </div>
  )
}

/** WarnWash: an amber internal glow behind a settings row that needs eyes on
 * it — the light seeps into the glass right where the problem is. */
function WarnWash({on, children}: {on: boolean; children: ReactNode}) {
  if (!on) return <>{children}</>
  return (
    <div
      className="relative -mx-2 rounded-[var(--r-md)] px-2"
      style={{
        background: 'color-mix(in srgb, var(--warn) 6%, transparent)',
        boxShadow: 'inset 0 0 24px -8px color-mix(in srgb, var(--warn) 25%, transparent)',
      }}
    >
      {children}
    </div>
  )
}
function Row({label, hint, children}: {label: string; hint?: string; children: ReactNode}) {
  return (
    <div className="flex items-center justify-between gap-4 py-2">
      <div className="min-w-0">
        <div className="text-sm font-medium text-[var(--text)]">{label}</div>
        {hint && <div className="mt-0.5 text-xs text-[var(--text-3)]">{hint}</div>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}
function Divider() { return <div className="pf-sep my-1" /> }
