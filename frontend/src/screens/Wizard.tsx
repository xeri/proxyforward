import {useEffect, useMemo, useState} from 'react'
import {GetConfig, PairingCode, SetupAgent, SetupGateway} from '../../wailsjs/go/app/App'
import {Button, Codebox, CopyButton, ErrorBanner, Field, TextInput} from '../components/ui'
import {ImportSetupFlow} from '../components/SetupBackup'
import {IconCheck, IconGlobe, IconRefresh, IconServer, IconShield} from '../components/icons'

const DEFAULT_CONTROL_PORT = 8474

type Step = 'role' | 'gateway' | 'gateway-done' | 'agent' | 'import'

/** First-run setup: pick a role, then show the pairing code (gateway) or
 * paste one (agent). Designed so a non-technical user can finish in under a
 * minute — the pairing code is the only thing that moves between machines. */
export function Wizard() {
  const [step, setStep] = useState<Step>('role')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const [publicHost, setPublicHost] = useState('')
  const [code, setCode] = useState('')

  const [pairing, setPairing] = useState('')
  const [localAddr, setLocalAddr] = useState('127.0.0.1:25565')
  const [publicPort, setPublicPort] = useState('25565')

  // The gateway's control port, for the port-forwarding checklist. Read from
  // config so a non-default port shows the truth, not the default.
  const [controlPort, setControlPort] = useState(DEFAULT_CONTROL_PORT)
  useEffect(() => {
    GetConfig()
      .then(c => { if (c.Gateway?.ControlPort) setControlPort(c.Gateway.ControlPort) })
      .catch(() => {})
  }, [])

  useEffect(() => {
    if (step !== 'gateway-done' || code) return
    let cancelled = false
    const poll = async (attempt: number) => {
      try {
        const c = await PairingCode()
        if (!cancelled) setCode(c)
      } catch (e) {
        if (!cancelled && attempt < 20) setTimeout(() => poll(attempt + 1), 250)
        else if (!cancelled) setErr(String(e))
      }
    }
    poll(0)
    return () => { cancelled = true }
  }, [step, code])

  const doGateway = async () => {
    setBusy(true); setErr('')
    try { await SetupGateway(publicHost.trim()); setStep('gateway-done') }
    catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }
  const doAgent = async () => {
    setBusy(true); setErr('')
    try { await SetupAgent(pairing.trim(), localAddr.trim(), parseInt(publicPort, 10) || 25565) }
    catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  const parsed = useMemo(() => parsePairing(pairing), [pairing])

  return (
    <div className="flex h-full items-center justify-center p-6">
      <div className="pf-stagger w-full max-w-xl">
        {/* Brand */}
        <div className="mb-7 text-center">
          <div
            className="pf-breathe mx-auto mb-3 flex h-14 w-14 items-center justify-center rounded-2xl text-white"
            style={{background: 'linear-gradient(135deg, var(--accent), var(--accent-2))'}}
          >
            <IconServer size={28} />
          </div>
          <h1 className="text-2xl font-semibold tracking-tight">Welcome to proxyforward</h1>
          <p className="mt-1.5 text-sm text-[var(--text-2)]">
            Expose a Minecraft server behind NAT through a machine that can port-forward.
          </p>
        </div>

        <Stepper step={step} />

        {err && <div className="mb-4"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}

        {step === 'role' && (
          <>
            <div className="pf-stagger grid grid-cols-1 gap-3 sm:grid-cols-2">
              <RoleCard
                icon={<IconServer size={22} />} title="This hosts Minecraft"
                sub="Agent — dials out to the gateway. No port forwarding needed here."
                onClick={() => { setErr(''); setStep('agent') }} />
              <RoleCard
                icon={<IconGlobe size={22} />} title="This faces the internet"
                sub="Gateway — players connect here; it relays traffic to the agent."
                onClick={() => { setErr(''); setStep('gateway') }} />
            </div>
            <button onClick={() => { setErr(''); setStep('import') }}
              className="mt-3 flex w-full items-center justify-center gap-2 rounded-xl border border-dashed border-[var(--border)] px-4 py-2.5 text-sm text-[var(--text-2)] transition-colors hover:border-[var(--border-strong)] hover:bg-[var(--panel)] hover:text-[var(--text)]">
              <IconRefresh size={15} /> Restore from another machine — import a .pfsetup backup
            </button>
          </>
        )}

        {step === 'import' && (
          <Panel>
            <p className="mb-4 text-sm text-[var(--text-2)]">
              Already set up on another machine or OS install? Import the setup file exported there
              (Settings → Backup) — pairing, tunnels, and statistics carry over, and the gateway keeps
              recognizing this identity.
            </p>
            <ImportSetupFlow isWizard />
            <div className="mt-5">
              <Button variant="ghost" onClick={() => setStep('role')}>Back</Button>
            </div>
          </Panel>
        )}

        {step === 'gateway' && (
          <Panel>
            <Field label="Public address" hint="The hostname or IP players and the agent will use. A plain IP works; a stable DNS name (DDNS works) survives IP changes — Minecraft clients cache DNS, so stability matters. You can change this later.">
              <TextInput value={publicHost} onChange={setPublicHost} placeholder="play.example.com" autoFocus onEnter={doGateway} />
            </Field>

            <PortChecklist
              title="On your router, forward these ports to this machine"
              intro="Router admin pages call this “port forwarding” or “virtual server”. Both rules are TCP and point at this machine's LAN IP."
              rules={[
                {port: controlPort, label: 'Control link', why: 'the agent (Minecraft machine) connects here'},
                {port: 25565, label: 'Minecraft', why: 'players connect here — or the public port you pick on the agent'},
              ]}
              footnote="Windows Firewall also needs to allow these inbound — the app offers a one-click rule after setup (Settings → Windows integration)."
            />

            <div className="mt-5 flex justify-between">
              <Button variant="ghost" onClick={() => setStep('role')}>Back</Button>
              <Button onClick={doGateway} disabled={busy}>{busy ? 'Starting…' : 'Start gateway'}</Button>
            </div>
          </Panel>
        )}

        {step === 'gateway-done' && (
          <Panel>
            <div className="mb-3 flex items-center gap-2 text-[var(--good)]">
              <IconCheck size={18} /> <span className="font-medium">Gateway is running</span>
            </div>
            <p className="mb-2 text-sm text-[var(--text-2)]">
              Copy this pairing code and paste it into proxyforward on your Minecraft machine:
            </p>
            {code
              ? <Codebox text={code} action={<CopyButton text={code} />} />
              : <div className="rounded-lg border border-[var(--border)] bg-[var(--panel-2)] px-3 py-2.5 text-sm text-[var(--text-3)]">Generating code…</div>}
            <ol className="mt-4 space-y-1.5 text-sm text-[var(--text-2)]">
              <li>1. Open proxyforward on the Minecraft machine.</li>
              <li>2. Choose <b>“This hosts Minecraft”</b>.</li>
              <li>3. Paste the code and connect.</li>
            </ol>

            <PortChecklist
              title="Don't forget the router"
              rules={[
                {port: controlPort, label: 'Control link', why: 'agent → gateway'},
                {port: 25565, label: 'Minecraft', why: 'players → gateway'},
              ]}
              footnote="Forward both as TCP to this machine, and allow them through Windows Firewall (one-click in Settings). The agent's “Test public reachability” button verifies the whole path."
            />

            <p className="mt-4 text-xs text-[var(--text-3)]">
              The dashboard will show the agent as soon as it connects. Keep this window open.
            </p>
          </Panel>
        )}

        {step === 'agent' && (
          <Panel>
            <div className="mb-4 flex items-start gap-2.5 rounded-xl border border-[color-mix(in_srgb,var(--good)_30%,var(--border))] bg-[color-mix(in_srgb,var(--good)_8%,transparent)] px-3 py-2.5 text-sm text-[var(--text-2)]">
              <span className="mt-0.5 shrink-0 text-[var(--good)]"><IconShield size={16} /></span>
              <span>
                <b className="text-[var(--text)]">Nothing to forward on this machine.</b> The agent only makes an
                outbound connection{parsed ? <> to <span className="font-mono">{parsed.host}:{parsed.port}</span></> : ' to the gateway'} —
                router and firewall changes happen on the gateway side only.
              </span>
            </div>
            <Field label="Pairing code" hint="Shown by the gateway right after you set it up. Starts with pf1://">
              <TextInput value={pairing} onChange={setPairing} placeholder="pf1://host:8474/…#sha256:…" mono autoFocus />
            </Field>
            {pairing.trim() && (
              parsed
                ? <div className="mt-2 flex items-center gap-2 text-xs text-[var(--good)]">
                    <IconCheck size={14} /> Gateway {parsed.host}:{parsed.port} · certificate pinned
                  </div>
                : <div className="mt-2 text-xs text-[var(--warn)]">That doesn’t look like a complete pairing code yet.</div>
            )}
            <div className="mt-4 grid grid-cols-2 gap-3">
              <Field label="Local Minecraft address" hint="Usually the default.">
                <TextInput value={localAddr} onChange={setLocalAddr} mono />
              </Field>
              <Field label="Public port" hint="Port players will use.">
                <TextInput value={publicPort} onChange={setPublicPort} mono />
              </Field>
            </div>
            <div className="mt-5 flex justify-between">
              <Button variant="ghost" onClick={() => setStep('role')}>Back</Button>
              <Button onClick={doAgent} disabled={busy || !parsed}>{busy ? 'Connecting…' : 'Connect'}</Button>
            </div>
          </Panel>
        )}
      </div>
    </div>
  )
}

function Stepper({step}: {step: Step}) {
  const idx = step === 'role' ? 0 : 1
  return (
    <div className="mb-5 flex items-center justify-center gap-2">
      {['Choose role', step === 'gateway-done' ? 'Share code' : step === 'import' ? 'Restore' : 'Connect'].map((label, i) => (
        <div key={i} className="flex items-center gap-2">
          <span className={`flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold ${
            i <= idx ? 'bg-[var(--accent)] text-white' : 'bg-[var(--panel-2)] text-[var(--text-3)]'}`}>{i + 1}</span>
          <span className={`text-xs ${i <= idx ? 'text-[var(--text)]' : 'text-[var(--text-3)]'}`}>{label}</span>
          {i === 0 && <span className="mx-1 h-px w-8 bg-[var(--border)]" />}
        </div>
      ))}
    </div>
  )
}

function RoleCard({icon, title, sub, onClick}: {icon: React.ReactNode; title: string; sub: string; onClick: () => void}) {
  return (
    <button onClick={onClick}
      className="group rounded-2xl border border-[var(--border)] bg-[var(--panel)] p-5 text-left transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)] hover:-translate-y-1 hover:border-[color-mix(in_srgb,var(--accent)_55%,var(--border))] hover:shadow-[0_16px_40px_-16px_color-mix(in_srgb,var(--accent)_40%,transparent)] active:translate-y-0 active:scale-[0.99]">
      <div className="mb-3 flex h-10 w-10 items-center justify-center rounded-xl bg-[var(--panel-2)] text-[var(--text-2)] transition-all duration-300 group-hover:scale-110 group-hover:bg-[linear-gradient(135deg,var(--accent),var(--accent-2))] group-hover:text-white">
        {icon}
      </div>
      <div className="text-base font-semibold">{title}</div>
      <div className="mt-1 text-sm text-[var(--text-2)]">{sub}</div>
    </button>
  )
}

/** PortChecklist spells out exactly which ports must be forwarded/allowed —
 * the #1 thing gateway users get wrong. Ports render as copy-friendly chips. */
function PortChecklist({title, intro, rules, footnote}: {
  title: string
  intro?: string
  rules: {port: number; label: string; why: string}[]
  footnote?: string
}) {
  return (
    <div className="mt-4 rounded-xl border border-[var(--border)] bg-[var(--panel-2)] p-3.5">
      <div className="flex items-center gap-2 text-sm font-medium text-[var(--text)]">
        <span className="text-[var(--accent)]"><IconGlobe size={15} /></span>
        {title}
      </div>
      {intro && <p className="mt-1.5 text-xs leading-relaxed text-[var(--text-3)]">{intro}</p>}
      <div className="mt-2.5 space-y-1.5">
        {rules.map(r => (
          <div key={r.port} className="flex items-start gap-2.5 text-sm">
            <code className="shrink-0 rounded-md border border-[var(--border)] bg-[var(--panel)] px-2 py-0.5 font-mono text-[12px] font-semibold text-[var(--text)]">
              TCP {r.port}
            </code>
            <span className="min-w-0 leading-snug">
              <span className="font-medium text-[var(--text-2)]">{r.label}</span>
              <span className="text-xs text-[var(--text-3)]"> — {r.why}</span>
            </span>
          </div>
        ))}
      </div>
      {footnote && <p className="mt-2.5 text-xs leading-relaxed text-[var(--text-3)]">{footnote}</p>}
    </div>
  )
}

function Panel({children}: {children: React.ReactNode}) {
  return <div className="pf-rise rounded-2xl border border-[var(--border)] bg-[var(--panel)] p-5 shadow-[var(--shadow-soft)]">{children}</div>
}

/** Lightweight client-side parse for instant feedback; the Go side does the
 * authoritative validation on submit. */
function parsePairing(s: string): {host: string; port: number} | null {
  const t = s.trim()
  const m = /^pf1:\/\/(\[[^\]]+\]|[^/:]+):(\d+)\/([^#]+)#sha256:[0-9a-fA-F]{64}$/.exec(t)
  if (!m) return null
  const port = parseInt(m[2], 10)
  if (port < 1 || port > 65535) return null
  return {host: m[1].replace(/^\[|\]$/g, ''), port}
}
