import {useEffect, useRef, useState} from 'react'
import {CheckPairingCode, GetConfig, IssuePairingCode, SetupAgent, SetupGateway} from '../../wailsjs/go/app/App'
import {Button, CopyButton, Disclosure, ErrorBanner, FatalStatement, Field, Overline, Spinner, TextInput, Toggle} from '../components/ui'
import {PairingPlaque} from '../components/PairingPlaque'
import {Emblem} from '../components/Emblem'
import {ImportSetupFlow} from '../components/SetupBackup'
import {IconCheck, IconGlobe, IconRefresh, IconServer, IconShield, IconSpark} from '../components/icons'
import {retype} from '../pairing'
import {fmtRtt, hasRtt, UIStatus} from '../state'

const DEFAULT_CONTROL_PORT = 8474

type Act = 'role' | 'gateway' | 'agent' | 'import' | 'live'
type Kind = 'gateway' | 'agent'

/** First-run setup in three acts: choose a role, configure it, go live. The
 * final act is driven by real ticks — it listens for the actual handshake
 * before handing over to the console. Designed so a non-technical user can
 * finish in under a minute; the pairing code is the only thing that moves
 * between machines. */
export function Wizard({status, onDone, deepLink}: {status: UIStatus | null; onDone: () => void; deepLink?: string}) {
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

  // A pxf:// deep link (a clicked pairing invite) drops the user straight onto the
  // agent paste step with the code prefilled — parsed inline below, still
  // confirm-to-connect. A pairing code always means "make this machine the agent".
  useEffect(() => {
    if (deepLink && deepLink.startsWith('pxf://')) {
      setErr(''); setKind('agent'); setPairing(deepLink); setAct('agent')
    }
  }, [deepLink])

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

  const verdict = usePairingVerdict(pairing)

  return (
    <div className="flex h-full items-center justify-center overflow-y-auto overscroll-y-contain p-6">
      <div className="pf-stagger w-full max-w-xl" data-band-content>
        {/* Brand hero: the one colossal moment the app allows itself. The
            two-line break is designed — 56px never fits max-w-xl on one line,
            so each clause owns a line instead of wrapping ragged. */}
        <div className="mb-7 text-center">
          <div className="pf-breathe mx-auto mb-5 flex h-14 w-14 items-center justify-center rounded-[var(--r-xl)] bg-[var(--accent)] text-[var(--accent-contrast)]">
            <IconServer size={28} />
          </div>
          <Overline variant="eyebrow" className="mb-2">proxyforward</Overline>
          <h1 className="font-display text-colossal font-bold leading-[var(--lh-display)]">
            <span className="block">Two machines.</span>
            <span className="block">One address.</span>
          </h1>
          <p className="mx-auto mt-3 max-w-md text-body text-[var(--text-2)]">
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
            <Field label="Public address" hint="The address players and the agent will use — a plain IP works, but a stable DNS name (DDNS is fine) survives IP changes. You can change this later.">
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
                outbound connection{verdict.kind === 'ok' ? <> to <span className="font-mono">{verdict.host}:{verdict.port}</span></> : ' to the gateway'} —
                router and firewall changes happen on the gateway side only.
              </span>
            </div>
            <PairingField value={pairing} onChange={setPairing} verdict={verdict} />
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
              <Button onClick={doAgent} disabled={busy || verdict.kind !== 'ok'}>{busy ? 'Connecting…' : 'Connect'}</Button>
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

/** GatewayLive: the gateway is up — issue a single-use enrollment code, hand it
 * over, and listen for the real handshake on the live ticks. Each machine gets a
 * fresh code; the reusable toggle (advanced) trades that for a multi-agent code. */
function GatewayLive({status, controlPort, onDone}: {
  status: UIStatus | null; controlPort: number; onDone: () => void
}) {
  const [code, setCode] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [reusable, setReusable] = useState(false)
  const [gen, setGen] = useState(0)

  // (Re)issue the enrollment ticket on mount and whenever "New code" bumps
  // `gen`. A single-use code auto-issues — this screen exists to pair one
  // machine right now, and its 10-minute code shows honestly as a pending
  // invite row in the roster meanwhile. A REUSABLE code never auto-issues:
  // it is a standing credential, so it must be an explicit click (gen > 0),
  // never a toggle's side effect — flipping the toggle and back must mint
  // nothing. Retries briefly in case the gateway's listener isn't ready yet,
  // and cancels cleanly on the next issue.
  useEffect(() => {
    if (reusable && gen === 0) { setCode(''); setErr(''); setBusy(false); return }
    let cancelled = false
    setBusy(true); setCode(''); setErr('')
    const poll = (n: number) => {
      // Single-use expires in 10 min; a reusable code lives until it is revoked (ttl 0).
      IssuePairingCode(reusable, reusable ? 0 : 600, [], [], '').then(c => { if (!cancelled) { setCode(c); setBusy(false) } })
        .catch(e => { if (!cancelled) { if (n < 20) setTimeout(() => poll(n + 1), 250); else { setErr(String((e as Error)?.message ?? e)); setBusy(false) } } })
    }
    poll(0)
    return () => { cancelled = true }
  }, [reusable, gen])
  // The toggle resets the mint counter both ways, so switching modes never
  // inherits the other mode's "already asked" state.
  const pickReusable = (v: boolean) => { setReusable(v); setGen(0) }

  const paired = !!status?.agentConnected
  return (
    <Panel>
      <h2 className="mb-2 font-display text-metric-hero font-bold leading-[var(--lh-display)] text-[var(--good)]">
        Gateway is live
      </h2>
      <p className="mb-2 text-sm text-[var(--text-2)]">
        Copy this one-time pairing code and paste it into proxyforward on your Minecraft machine:
      </p>
      {code
        ? <PairingPlaque code={code} actions={<CopyButton text={code} />} />
        : err
          ? <ErrorBanner message={err} onDismiss={() => setErr('')} />
          : reusable
            ? <Button size="sm" variant="ghost" onClick={() => setGen(1)}>Mint reusable code</Button>
            : <div className="pf-well px-3 py-2.5 text-sm text-[var(--text-3)]">Generating code…</div>}
      <div className="mt-2 flex items-center justify-between gap-3">
        <span className="min-w-0 text-xs text-[var(--text-3)]">
          {reusable
            ? 'Reusable — a standing code, listed under Agents until you revoke it.'
            : 'Single-use — enrolls one agent, then expires. Valid for 10 minutes.'}
        </span>
        {!(reusable && gen === 0) && (
          <Button variant="ghost" size="sm" onClick={() => setGen(g => g + 1)} disabled={busy}>
            <IconRefresh size={13} /> New code
          </Button>
        )}
      </div>

      <div className="mt-3">
        <Disclosure label="Advanced" hint="Reuse this code for more than one agent">
          <Toggle
            checked={reusable} onChange={pickReusable}
            label="Reusable code"
            hint="Let this code enroll more than one machine. Nothing is minted until you ask — a standing code should never be a toggle's side effect." />
        </Disclosure>
      </div>

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

  // Distress leaves the box: FatalStatement's contract is type on whitespace,
  // so the fatal state drops the Panel entirely and stands on the backdrop.
  if (fatal) {
    return (
      <div className="pf-rise px-1 py-2">
        <FatalStatement
          eyebrow="refused" title="The gateway said no" detail={fatal}
          action={<Button variant="ghost" onClick={onBack}>Fix the pairing code</Button>}
        />
      </div>
    )
  }
  return (
    <Panel>
      <div className="flex flex-col items-center py-6 text-center">
        <div
          className={`grid h-14 w-14 place-items-center rounded-[var(--r-xl)] border transition-all duration-500 ${up ? 'pf-breathe' : ''}`}
          style={{
            color: up ? 'var(--good)' : 'var(--accent)',
            borderColor: `color-mix(in srgb, ${up ? 'var(--good)' : 'var(--accent)'} 40%, var(--border))`,
            background: `color-mix(in srgb, ${up ? 'var(--good)' : 'var(--accent)'} 9%, transparent)`,
          }}
        >
          {up ? <IconSpark size={26} /> : <Spinner size={24} />}
        </div>
        <div className={`mt-5 text-balance font-display font-bold leading-[var(--lh-display)] ${
          up ? 'text-colossal' : 'text-metric-hero'}`}>
          {up ? 'Handshake complete.' : 'Dialing the gateway…'}
        </div>
        <div className="mt-2.5 max-w-sm text-sm text-[var(--text-2)]">
          {up
            ? `Connected to ${status?.peerHostname || 'the gateway'}${status && hasRtt(status.rttMillis) ? ` in ${fmtRtt(status.rttMillis)}` : ''}. Opening your console…`
            : 'Establishing the tunnel link. Reconnects retry automatically with backoff.'}
        </div>
      </div>
      <div className="mt-2 flex justify-between">
        <Button variant="ghost" onClick={onBack}>Back</Button>
        <Button variant={up ? 'primary' : 'ghost'} onClick={onDone}>Open the console</Button>
      </div>
    </Panel>
  )
}

/** Stepper: the wizard's three acts, numbered like a playbill — 01 02 03 at
 * eyebrow tracking. Accent ink marks acts reached; the future stays silt.
 * Wizard steps are a true sequence, so ordinals are honest here (nowhere else
 * in the app numbers its sections). */
function Stepper({act}: {act: Act}) {
  const idx = act === 'role' ? 0 : act === 'live' ? 2 : 1
  const labels = ['choose role', act === 'import' ? 'restore' : 'configure', 'go live']
  return (
    <div className="mb-6 flex items-center justify-center gap-2.5 text-caption">
      {labels.map((label, i) => (
        <div key={i} className="flex items-center gap-2.5">
          <span className="flex items-baseline gap-1.5">
            <span className={`font-semibold tabular-nums tracking-[var(--tracking-eyebrow)] transition-colors duration-300 ${
              i <= idx ? 'text-[var(--accent)]' : 'text-[var(--text-3)]'}`}>
              0{i + 1}
            </span>
            <span className={`lowercase tracking-[var(--tracking-label)] transition-colors duration-300 ${
              i <= idx ? 'text-[var(--text)]' : 'text-[var(--text-3)]'}`}>{label}</span>
          </span>
          {i < labels.length - 1 && (
            <span className={`h-px w-8 transition-colors duration-300 ${i < idx ? 'bg-[color-mix(in_srgb,var(--accent)_55%,var(--border))]' : 'bg-[var(--border)]'}`} />
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
      // Quiet trench glass, like every other card the pointer can pick up
      // (Agents' roster, the player tiles). These were .pf-signal, which put two
      // pointer-answering surfaces on one screen — DESIGN.md rules 1 and 2, and
      // a role picker is not live network activity in the first place. The
      // wizard now uses the bloom frame nowhere: its ceremony is scale and type
      // (the lockup, "Handshake complete."), which is the sheet's other lever.
      // The role still reads — the Emblem inside carries its hue.
      className="group pf-card pf-lift pf-press p-5 text-left"
    >
      <div className="mb-3 inline-flex transition-transform duration-300 group-hover:scale-110">
        <Emblem role={role} fixed size={40} />
      </div>
      <div className="font-display text-title font-bold text-[var(--text)]">{title}</div>
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
            <code className="shrink-0 select-text rounded-[var(--r-sm)] border border-[var(--border)] bg-[var(--panel)] px-2 py-0.5 font-mono text-body font-semibold text-[var(--text)]">
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

type Verdict = {kind: 'incomplete' | 'ok' | 'typo' | 'unusable'; host: string; port: number; problem: string; legacy: boolean}

/** Go error strings are lowercase by convention; a sentence shown to someone is
 * not. The parser's messages are written as prose for exactly this spot, so the
 * only thing missing is the capital. */
const sentence = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

const BLANK: Verdict = {kind: 'incomplete', host: '', port: 0, problem: '', legacy: false}

/** usePairingVerdict asks the engine what a half-typed code is. The judgment is
 * deliberately not reimplemented here: a checksum written twice is two
 * implementations of the format, and the first time they disagreed this box would
 * confidently reject a code the engine accepts (`app/pairing.go CheckPairingCode`).
 *
 * Debounced, because it runs per keystroke, and sequence-guarded, because two
 * answers can land out of order and the older one must not win. */
function usePairingVerdict(code: string): Verdict {
  const [verdict, setVerdict] = useState<Verdict>(BLANK)
  const seq = useRef(0)
  useEffect(() => {
    if (!code.trim()) { seq.current++; setVerdict(BLANK); return }
    const mine = ++seq.current
    const t = setTimeout(() => {
      CheckPairingCode(code)
        .then(v => { if (seq.current === mine) setVerdict(v as Verdict) })
        .catch(() => { if (seq.current === mine) setVerdict(BLANK) })
    }, 120)
    return () => clearTimeout(t)
  }, [code])
  return verdict
}

/** PairingField: the box someone pastes or retypes a pairing code into.
 *
 * It forgives how the code arrives — any case, any grouping, Crockford's
 * confusable letters — and tidies it back into the canonical spelling as it is
 * typed. Two rules keep that from fighting the typist: a deletion is never
 * reformatted (otherwise a backspace over a hyphen looks stuck), and anything that
 * is not recognisably a v2 code is left exactly as typed, because rewriting text
 * we cannot parse is how a paste box eats the one character that would have
 * explained the problem.
 *
 * Four states, all written: nothing yet, still typing (silent — a half-typed code
 * is not a mistake), one character wrong, and the wrong string entirely. */
function PairingField({value, onChange, verdict}: {
  value: string; onChange: (v: string) => void; verdict: Verdict
}) {
  const prev = useRef(value)
  const handle = (next: string) => {
    // Shrinking means a deletion; leave it alone so the caret can get past a
    // separator this would otherwise put straight back.
    const tidy = next.length < prev.current.length ? next : retype(next)
    prev.current = tidy
    onChange(tidy)
  }
  return (
    <>
      <Field label="Pairing code" hint="Shown by the gateway right after you set it up. Starts with pxf://">
        <TextInput value={value} onChange={handle} placeholder="pxf://host:8474/v2/pair/…" mono autoFocus />
      </Field>
      {value.trim() && verdict.kind !== 'incomplete' && (
        verdict.kind === 'ok'
          ? <div className="pf-fade mt-2 flex items-center gap-2 text-xs text-[var(--good)]">
              <IconCheck size={14} />
              Gateway {verdict.host}:{verdict.port} · certificate pinned
              {verdict.legacy && <span className="text-[var(--text-3)]">&nbsp;· older-format code, still fine</span>}
            </div>
          : <div className={`pf-fade mt-2 text-xs ${verdict.kind === 'typo' ? 'text-[var(--warn)]' : 'text-[var(--text-3)]'}`}>
              {sentence(verdict.problem)}
            </div>
      )}
    </>
  )
}
