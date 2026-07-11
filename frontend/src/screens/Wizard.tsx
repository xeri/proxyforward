import {useEffect, useMemo, useState} from 'react'
import {GetConfig, PairingCode, SetupAgent, SetupGateway} from '../../wailsjs/go/app/App'
import {Button, Codebox, CopyButton, ErrorBanner, Field, Spinner, TextInput} from '../components/ui'
import {Emblem} from '../components/Emblem'
import {ImportSetupFlow} from '../components/SetupBackup'
import {IconCheck, IconGlobe, IconRefresh, IconServer, IconShield, IconSpark} from '../components/icons'
import {UIStatus} from '../state'

const DEFAULT_CONTROL_PORT = 8474

type Act = 'role' | 'gateway' | 'agent' | 'import' | 'live'
type Kind = 'gateway' | 'agent'

/** First-run setup in three acts: choose a role, configure it, go live. The
 * final act is driven by real ticks — it listens for the actual handshake
 * before handing over to the console. Designed so a non-technical user can
 * finish in under a minute; the pairing code is the only thing that moves
 * between machines. */
export function Wizard({status, onDone}: {status: UIStatus | null; onDone: () => void}) {
  const [act, setAct] = useState<Act>('role')
  const [kind, setKind] = useState<Kind>('gateway')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const [publicHost, setPublicHost] = useState('')
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

  // Preview a role's ambient hue on hover — the whole backdrop leans in.
  const preview = (role: '' | Kind) => {
    document.documentElement.dataset.role = role || 'unset'
  }

  const doGateway = async () => {
    setBusy(true); setErr('')
    try { await SetupGateway(publicHost.trim()); setKind('gateway'); setAct('live') }
    catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }
  const doAgent = async () => {
    setBusy(true); setErr('')
    try {
      await SetupAgent(pairing.trim(), localAddr.trim(), parseInt(publicPort, 10) || 25565)
      setKind('agent'); setAct('live')
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  const parsed = useMemo(() => parsePairing(pairing), [pairing])

  return (
    <div className="flex h-full items-center justify-center overflow-y-auto p-6">
      <div className="pf-stagger w-full max-w-xl">
        {/* Brand hero */}
        <div className="mb-7 text-center">
          <div className="pf-breathe mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-[var(--r-xl)] bg-[var(--accent)] text-[var(--accent-contrast)]">
            <IconServer size={28} />
          </div>
          <h1 className="text-[26px] font-semibold leading-tight tracking-tight">Two machines. One address.</h1>
          <p className="mt-2 text-sm text-[var(--text-2)]">
            Publish a Minecraft server from behind NAT through any machine that can port-forward.
          </p>
        </div>

        <Stepper act={act} />

        {err && <div className="mb-4"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}

        {act === 'role' && (
          <>
            <div className="pf-stagger grid grid-cols-1 gap-3 sm:grid-cols-2">
              <RoleCard
                role="agent"
                title="This hosts Minecraft"
                sub="Agent — dials out to the gateway. Nothing to forward here."
                onHover={on => preview(on ? 'agent' : '')}
                onClick={() => { setErr(''); setAct('agent') }} />
              <RoleCard
                role="gateway"
                title="This faces the internet"
                sub="Gateway — players connect here; it relays traffic to the agent."
                onHover={on => preview(on ? 'gateway' : '')}
                onClick={() => { setErr(''); setAct('gateway') }} />
            </div>
            <button onClick={() => { setErr(''); setAct('import') }}
              className="mt-3 flex w-full items-center justify-center gap-2 rounded-[var(--r-lg)] border border-dashed border-[var(--border)] px-4 py-2.5 text-sm text-[var(--text-2)] transition-colors hover:border-[var(--border-strong)] hover:bg-[var(--panel)] hover:text-[var(--text)]">
              <IconRefresh size={15} /> Restore from another machine — import a .pfsetup backup
            </button>
          </>
        )}

        {act === 'import' && (
          <Panel>
            <p className="mb-4 text-sm text-[var(--text-2)]">
              Already set up elsewhere? Import the setup file exported there (Settings → Backup) —
              pairing, tunnels, and statistics carry over, and the gateway keeps recognizing this identity.
            </p>
            <ImportSetupFlow isWizard />
            <div className="mt-5">
              <Button variant="ghost" onClick={() => setAct('role')}>Back</Button>
            </div>
          </Panel>
        )}

        {act === 'gateway' && (
          <Panel>
            <Field label="Public address" hint="The hostname or IP players and the agent will use. A plain IP works; a stable DNS name (DDNS is fine) survives IP changes — Minecraft clients cache DNS, so stability matters. You can change this later.">
              <TextInput value={publicHost} onChange={setPublicHost} placeholder="play.example.com" autoFocus onEnter={doGateway} />
            </Field>

            <PortChecklist
              title="On your router, forward these ports to this machine"
              intro="Router admin pages call this “port forwarding” or “virtual server”. Both rules are TCP and point at this machine's LAN IP."
              rules={[
                {port: controlPort, label: 'Control link', why: 'the agent (Minecraft machine) connects here'},
                {port: 25565, label: 'Minecraft', why: 'players connect here — or the public port you pick on the agent'},
              ]}
              footnote="Windows Firewall also needs to allow these inbound — the app offers a one-click rule after setup (Settings → System)."
            />

            <div className="mt-5 flex justify-between">
              <Button variant="ghost" onClick={() => setAct('role')}>Back</Button>
              <Button onClick={doGateway} disabled={busy}>{busy ? 'Starting…' : 'Start gateway'}</Button>
            </div>
          </Panel>
        )}

        {act === 'agent' && (
          <Panel>
            <div className="mb-4 flex items-start gap-2.5 rounded-[var(--r-md)] border border-[color-mix(in_srgb,var(--good)_30%,var(--border))] bg-[color-mix(in_srgb,var(--good)_8%,transparent)] px-3 py-2.5 text-sm text-[var(--text-2)]">
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
                ? <div className="pf-fade mt-2 flex items-center gap-2 text-xs text-[var(--good)]">
                    <IconCheck size={14} /> Gateway {parsed.host}:{parsed.port} · certificate pinned
                  </div>
                : <div className="mt-2 text-xs text-[var(--warn)]">That doesn't look like a complete pairing code yet.</div>
            )}
            <div className="mt-4 grid grid-cols-2 gap-3">
              <Field label="Local server address" hint="Usually the default.">
                <TextInput value={localAddr} onChange={setLocalAddr} mono />
              </Field>
              <Field label="Public port" hint="Port players will use.">
                <TextInput value={publicPort} onChange={setPublicPort} mono />
              </Field>
            </div>
            <div className="mt-5 flex justify-between">
              <Button variant="ghost" onClick={() => setAct('role')}>Back</Button>
              <Button onClick={doAgent} disabled={busy || !parsed}>{busy ? 'Connecting…' : 'Connect'}</Button>
            </div>
          </Panel>
        )}

        {act === 'live' && kind === 'gateway' && (
          <GatewayLive status={status} controlPort={controlPort} onDone={onDone} />
        )}
        {act === 'live' && kind === 'agent' && (
          <AgentLive status={status} onDone={onDone} onBack={() => { setErr(''); setAct('agent') }} />
        )}
      </div>
    </div>
  )
}

/** GatewayLive: the gateway is up — hand over the pairing code and listen for
 * the real handshake on the live ticks. */
function GatewayLive({status, controlPort, onDone}: {
  status: UIStatus | null; controlPort: number; onDone: () => void
}) {
  const [code, setCode] = useState('')
  const [err, setErr] = useState('')
  useEffect(() => {
    let cancelled = false
    const poll = (n: number) => {
      PairingCode().then(c => { if (!cancelled) setCode(c) })
        .catch(e => { if (!cancelled) { if (n < 20) setTimeout(() => poll(n + 1), 250); else setErr(String(e)) } })
    }
    poll(0)
    return () => { cancelled = true }
  }, [])

  const paired = !!status?.agentConnected
  return (
    <Panel>
      <div className="mb-3 flex items-center gap-2 text-[var(--good)]">
        <IconCheck size={18} /> <span className="font-medium">Gateway is live</span>
      </div>
      <p className="mb-2 text-sm text-[var(--text-2)]">
        Copy this pairing code and paste it into proxyforward on your Minecraft machine:
      </p>
      {code
        ? <Codebox text={code} action={<CopyButton text={code} />} />
        : err
          ? <ErrorBanner message={err} />
          : <div className="pf-well px-3 py-2.5 text-sm text-[var(--text-3)]">Generating code…</div>}
      <ol className="mt-4 space-y-1.5 text-sm text-[var(--text-2)]">
        <li>1. Open proxyforward on the Minecraft machine.</li>
        <li>2. Choose <b>"This hosts Minecraft"</b>.</li>
        <li>3. Paste the code and connect.</li>
      </ol>

      <PortChecklist
        title="Don't forget the router"
        rules={[
          {port: controlPort, label: 'Control link', why: 'agent → gateway'},
          {port: 25565, label: 'Minecraft', why: 'players → gateway'},
        ]}
        footnote="Forward both as TCP to this machine, and allow them through Windows Firewall (one-click in Settings). The agent's “Test player path” verifies the whole route."
      />

      {/* Live handshake state, straight from the ticks. */}
      <div className={`mt-4 flex items-center gap-2.5 rounded-[var(--r-md)] border px-3 py-2.5 text-sm transition-colors duration-500 ${
        paired
          ? 'border-[color-mix(in_srgb,var(--good)_35%,var(--border))] bg-[color-mix(in_srgb,var(--good)_9%,transparent)] text-[var(--good)]'
          : 'border-[var(--border)] bg-[var(--panel-2)] text-[var(--text-2)]'
      }`}>
        {paired
          ? <><IconSpark size={16} /> <span className="font-medium">Handshake complete — {status?.peerHostname || 'your agent'} is online.</span></>
          : <><Spinner size={14} /> Listening for your agent…</>}
      </div>

      <div className="mt-5 flex justify-end">
        <Button onClick={onDone}>{paired ? 'Open the console' : 'Open the console — keep pairing later'}</Button>
      </div>
    </Panel>
  )
}

/** AgentLive: dialing the gateway for real. Success auto-advances into the
 * console; a fatal engine error (bad token, conflict) offers a way back. */
function AgentLive({status, onDone, onBack}: {
  status: UIStatus | null; onDone: () => void; onBack: () => void
}) {
  const up = !!status?.linkUp
  const fatal = status?.engineFatal || ''

  useEffect(() => {
    if (!up) return
    const t = setTimeout(onDone, 1600)
    return () => clearTimeout(t)
  }, [up])

  return (
    <Panel>
      <div className="flex flex-col items-center py-6 text-center">
        <div
          className={`grid h-14 w-14 place-items-center rounded-[var(--r-xl)] border transition-all duration-500 ${up ? 'pf-breathe' : ''}`}
          style={{
            color: fatal ? 'var(--bad)' : up ? 'var(--good)' : 'var(--accent)',
            borderColor: `color-mix(in srgb, ${fatal ? 'var(--bad)' : up ? 'var(--good)' : 'var(--accent)'} 40%, var(--border))`,
            background: `color-mix(in srgb, ${fatal ? 'var(--bad)' : up ? 'var(--good)' : 'var(--accent)'} 9%, transparent)`,
          }}
        >
          {fatal ? <IconRefresh size={26} /> : up ? <IconSpark size={26} /> : <Spinner size={24} />}
        </div>
        <div className="mt-4 text-lg font-semibold">
          {fatal ? 'The gateway said no' : up ? 'Handshake complete.' : 'Dialing the gateway…'}
        </div>
        <div className="mt-1 max-w-sm text-sm text-[var(--text-2)]">
          {fatal
            ? fatal
            : up
              ? `Connected to ${status?.peerHostname || 'the gateway'} in ${status?.rttMillis ?? '—'} ms. Opening your console…`
              : 'Establishing the tunnel link. Reconnects retry automatically with backoff.'}
        </div>
      </div>
      <div className="flex justify-between">
        <Button variant="ghost" onClick={onBack}>{fatal ? 'Fix the pairing code' : 'Back'}</Button>
        {!fatal && <Button variant={up ? 'primary' : 'ghost'} onClick={onDone}>Open the console</Button>}
      </div>
    </Panel>
  )
}

function Stepper({act}: {act: Act}) {
  const idx = act === 'role' ? 0 : act === 'live' ? 2 : 1
  const labels = ['Choose role', act === 'import' ? 'Restore' : 'Configure', 'Go live']
  return (
    <div className="mb-5 flex items-center justify-center gap-2">
      {labels.map((label, i) => (
        <div key={i} className="flex items-center gap-2">
          <span className={`flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold transition-colors duration-300 ${
            i <= idx ? 'bg-[var(--accent)] text-[var(--accent-contrast)]' : 'bg-[var(--panel-2)] text-[var(--text-3)]'}`}>
            {i < idx ? <IconCheck size={12} /> : i + 1}
          </span>
          <span className={`text-xs transition-colors duration-300 ${i <= idx ? 'text-[var(--text)]' : 'text-[var(--text-3)]'}`}>{label}</span>
          {i < labels.length - 1 && (
            <span className={`mx-1 h-px w-8 transition-colors duration-300 ${i < idx ? 'bg-[color-mix(in_srgb,var(--accent)_55%,var(--border))]' : 'bg-[var(--border)]'}`} />
          )}
        </div>
      ))}
    </div>
  )
}

function RoleCard({role, title, sub, onClick, onHover}: {
  role: 'agent' | 'gateway'; title: string; sub: string
  onClick: () => void; onHover: (on: boolean) => void
}) {
  return (
    <button
      onClick={() => { onHover(false); onClick() }}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      style={{['--hue' as string]: `var(--role-${role})`}}
      className="group pf-card p-5 text-left transition-all duration-300 [transition-timing-function:var(--ease-out)] hover:-translate-y-1 hover:shadow-[inset_0_1px_0_var(--bevel-top),inset_0_-1px_0_var(--bevel-bot),0_16px_40px_-16px_color-mix(in_srgb,var(--hue)_40%,transparent)] active:translate-y-0 active:scale-[0.99]"
    >
      <div className="mb-3 inline-flex transition-transform duration-300 group-hover:scale-110">
        <Emblem role={role} fixed size={40} />
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
    <div className="mt-4 rounded-[var(--r-md)] border border-[var(--border)] bg-[var(--panel-2)] p-3.5">
      <div className="flex items-center gap-2 text-sm font-medium text-[var(--text)]">
        <span className="text-[var(--accent)]"><IconGlobe size={15} /></span>
        {title}
      </div>
      {intro && <p className="mt-1.5 text-xs leading-relaxed text-[var(--text-3)]">{intro}</p>}
      <div className="mt-2.5 space-y-1.5">
        {rules.map(r => (
          <div key={r.port} className="flex items-start gap-2.5 text-sm">
            <code className="shrink-0 select-text rounded-[var(--r-sm)] border border-[var(--border)] bg-[var(--panel)] px-2 py-0.5 font-mono text-[12px] font-semibold text-[var(--text)]">
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
  return <div className="pf-rise pf-card p-5">{children}</div>
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
