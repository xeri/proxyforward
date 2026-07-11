import {ReactNode, useEffect, useState} from 'react'
import {
  FirewallRepair, FirewallStatus, GetConfig, InstallService, OpenConfigDir,
  RegenerateToken, RestartEngine, SaveSettings, ServiceStatus, UninstallService,
} from '../../wailsjs/go/app/App'
import {config} from '../../wailsjs/go/models'
import {
  Badge, Button, Card, ErrorBanner, Field, Select, Spinner, TextInput, Toggle,
} from '../components/ui'
import {ExportSetupRow, ImportSetupFlow} from '../components/SetupBackup'
import {IconExternal, IconMoon, IconRefresh, IconShield, IconSun} from '../components/icons'
import {UIStatus} from '../state'

type Cfg = config.Config

export function Settings({status, theme, onThemeToggle}: {
  status: UIStatus; theme: 'dark' | 'light'; onThemeToggle: () => void
}) {
  const isAgent = status.role === 'agent'
  const attached = status.mode === 'attached'
  const [cfg, setCfg] = useState<Cfg | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')
  const [note, setNote] = useState('')

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
    <div className="pf-stagger space-y-4 pb-20">
      {err && <ErrorBanner message={err} onDismiss={() => setErr('')} />}
      {attached && (
        <div className="rounded-lg border border-[var(--border)] bg-[var(--panel-2)] px-3 py-2 text-sm text-[var(--text-2)]">
          A background service owns this configuration. Changes here save to this user's config but only take effect when the service is stopped and this app runs the engine directly.
        </div>
      )}

      {/* Appearance */}
      <Section title="Appearance">
        <Row label="Theme" hint="Applies instantly and is remembered.">
          <Button variant="ghost" size="sm" onClick={onThemeToggle}>
            {theme === 'dark' ? <IconSun size={15} /> : <IconMoon size={15} />}
            {theme === 'dark' ? 'Light' : 'Dark'}
          </Button>
        </Row>
        <Divider />
        <Toggle checked={cfg.UI.MinimizeToTray} onChange={v => patch(c => { c.UI.MinimizeToTray = v })}
          label="Minimize to tray" hint="Keep running in the background when the window is closed." />
        <Toggle checked={cfg.UI.Autostart} onChange={v => patch(c => { c.UI.Autostart = v })}
          label="Start on login" hint="Launch proxyforward automatically when you sign in to Windows." />
      </Section>

      {/* Connection */}
      {isAgent ? (
        <Section title="Gateway connection" subtitle="Editable without re-pairing — DNS is re-resolved on every reconnect.">
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
        <Section title="Gateway" subtitle="Where players and agents reach this machine.">
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

      {/* Security / limits (gateway) */}
      {!isAgent && (
        <>
          <Section title="Security">
            <Row label="Pairing token" hint="Rotating it disconnects existing agents until they re-pair with the new code.">
              <TokenRotate attached={attached} onDone={reload} />
            </Row>
          </Section>
          <Section title="Abuse limits" subtitle="Enforced at the gateway on public listeners and the control port.">
            <div className="grid grid-cols-3 gap-3">
              <Field label="Max connections (global)"><TextInput mono value={String(cfg.Gateway.MaxConnsGlobal)}
                onChange={v => patch(c => { c.Gateway.MaxConnsGlobal = parseInt(v, 10) || 0 })} /></Field>
              <Field label="Max per client IP"><TextInput mono value={String(cfg.Gateway.MaxConnsPerIP)}
                onChange={v => patch(c => { c.Gateway.MaxConnsPerIP = parseInt(v, 10) || 0 })} /></Field>
              <Field label="Auth attempts / min"><TextInput mono value={String(cfg.Gateway.AuthAttemptsPerMin)}
                onChange={v => patch(c => { c.Gateway.AuthAttemptsPerMin = parseInt(v, 10) || 0 })} /></Field>
            </div>
          </Section>
        </>
      )}

      {/* Metrics */}
      <Section title="Metrics">
        <Toggle checked={cfg.Metrics.PrometheusEnabled} onChange={v => patch(c => { c.Metrics.PrometheusEnabled = v })}
          label="Prometheus endpoint" hint="Expose /metrics for scraping. Off by default." />
        {cfg.Metrics.PrometheusEnabled && (
          <div className="mt-2"><Field label="Listen address"><TextInput mono value={cfg.Metrics.PrometheusAddr}
            onChange={v => patch(c => { c.Metrics.PrometheusAddr = v })} /></Field></div>
        )}
      </Section>

      {/* Logging */}
      <Section title="Logging">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Level">
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
      </Section>

      {/* Windows integration */}
      <WindowsSection />

      {/* Backup / restore */}
      <Section title="Backup" subtitle="Move this setup to another machine or a dual-boot OS install — pairing, tunnels, keys, and statistics travel in one file.">
        {attached && <div className="pb-2 text-xs text-[var(--text-3)]">The background service owns this setup — stop the service to export or import.</div>}
        <ExportSetupRow disabled={attached} />
        <Divider />
        <ImportSetupFlow disabled={attached} onDone={reload} />
      </Section>

      {/* Config location */}
      <Section title="Configuration">
        <Row label="Config file">
          <div className="flex items-center gap-2">
            <code className="max-w-xs truncate rounded bg-[var(--panel-2)] px-2 py-1 font-mono text-xs text-[var(--text-2)]">{status.configPath}</code>
            <Button variant="ghost" size="sm" onClick={() => OpenConfigDir()}><IconExternal size={14} /> Open</Button>
          </div>
        </Row>
        <Divider />
        <Row label="Version"><span className="text-sm text-[var(--text-2)]">{status.version} · pid {status.pid} · {status.mode}</span></Row>
      </Section>

      {/* Sticky save bar */}
      {dirty && (
        <div className="pf-slide-up fixed bottom-6 left-1/2 z-40 flex items-center gap-3 rounded-full border border-[var(--border)] bg-[var(--glass)] px-4 py-2.5 shadow-[var(--shadow-pop)] backdrop-blur-xl">
          <span className="text-sm text-[var(--text-2)]">Unsaved changes</span>
          <Button variant="ghost" size="sm" onClick={reload}>Discard</Button>
          <Button size="sm" onClick={save} disabled={saving}>{saving ? 'Saving…' : 'Save & apply'}</Button>
        </div>
      )}
      {note && !dirty && <div className="text-sm text-[var(--good)]">{note}</div>}
    </div>
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
        ? <div className="flex items-center gap-2">
            <span className="text-xs text-[var(--warn)]">Disconnect agents?</span>
            <Button variant="ghost" size="sm" onClick={() => setConfirm(false)}>No</Button>
            <Button variant="danger" size="sm" onClick={rotate} disabled={busy}>{busy ? '…' : 'Rotate'}</Button>
          </div>
        : <Button variant="ghost" size="sm" onClick={() => setConfirm(true)}><IconRefresh size={14} /> Regenerate</Button>}
      {err && <span className="text-xs text-[var(--bad)]">{err}</span>}
    </div>
  )
}

function WindowsSection() {
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
  const installed = svc.toLowerCase() !== 'not installed' && svc !== 'unknown' && svc !== '…'

  return (
    <Section title="Windows integration" action={<IconShield size={18} />}>
      <Row label="Firewall rule" hint="Allows inbound player and agent connections.">
        <div className="flex items-center gap-2">
          {fw === null ? <Badge tone="neutral">unknown</Badge> : <Badge tone={fw ? 'good' : 'warn'}>{fw ? 'Present' : 'Missing'}</Badge>}
          {!fw && <Button variant="ghost" size="sm" onClick={wrap('fw', FirewallRepair)} disabled={busy === 'fw'}>{busy === 'fw' ? '…' : 'Add rule'}</Button>}
        </div>
      </Row>
      <Divider />
      <Row label="Windows service" hint="Run headless in the background; the app attaches as a thin client.">
        <div className="flex items-center gap-2">
          <Badge tone={installed ? 'good' : 'neutral'}>{svc}</Badge>
          {installed
            ? <Button variant="danger" size="sm" onClick={wrap('svc', UninstallService)} disabled={busy === 'svc'}>{busy === 'svc' ? '…' : 'Uninstall'}</Button>
            : <Button variant="ghost" size="sm" onClick={wrap('svc', InstallService)} disabled={busy === 'svc'}>{busy === 'svc' ? '…' : 'Install'}</Button>}
        </div>
      </Row>
      {err && <div className="mt-2"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}
    </Section>
  )
}

function Section({title, subtitle, action, children}: {
  title: string; subtitle?: string; action?: ReactNode; children: ReactNode
}) {
  return <Card title={title} subtitle={subtitle} action={action}>{children}</Card>
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
function Divider() { return <div className="my-1 border-t border-[var(--border)]" /> }
