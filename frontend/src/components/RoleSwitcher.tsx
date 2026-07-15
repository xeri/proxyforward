import {useEffect, useRef, useState} from 'react'
import {GetConfig, RestartEngine, SaveSettings, SetupGateway} from '../../wailsjs/go/app/App'
import {config} from '../../wailsjs/go/models'
import {Badge, Button, ErrorBanner, Menu, MenuItem, Modal, RoleWord} from './ui'
import {Emblem} from './Emblem'
import {IconChevronDown} from './icons'
import {UIStatus} from '../state'

type Role = 'agent' | 'gateway'

const OTHER: Record<Role, Role> = {agent: 'gateway', gateway: 'agent'}

/**
 * RoleSwitcher: the sidebar's mode identity anchor, made live. This machine can
 * be the agent or the gateway, and the config holds BOTH sections
 * independently (`internal/config/config.go`), so flipping is a one-field
 * change — not a reinstall.
 *
 * The backend already refuses the unsafe direction, loudly: SaveSettings
 * validates before it writes, so `Role = agent` without a pairing token is
 * rejected and nothing is persisted. Rather than surface that as an error
 * string, the menu reads the config first and offers the wizard's pairing flow
 * instead.
 *
 * → gateway is always available: `app.go SetupGateway` stops the engine, sets
 *   the role, MINTS a token if there isn't one, saves, and starts. The TLS
 *   keypair is cached in the config dir, so a machine that was a gateway before
 *   comes back with the same certificate fingerprint — previously issued
 *   pairing codes keep working.
 * → agent needs a pairing code it can only get from a gateway. With one already
 *   stored it is GetConfig → Role → SaveSettings → RestartEngine, the same pair
 *   Settings runs on save. Without one, this routes to setup.
 *
 * Attached to a service, the config belongs to the service — RestartEngine
 * refuses in ModeAttached, so the trigger is disabled and says why.
 */
export function RoleSwitcher({status, onPair}: {status: UIStatus; onPair: () => void}) {
  const role: Role = status.role === 'agent' ? 'agent' : 'gateway'
  const attached = status.mode === 'attached'
  const btnRef = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const [confirm, setConfirm] = useState<Role | null>(null)
  const [cfg, setCfg] = useState<config.Config | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  // Re-read on every role change: after a switch the other side's readiness
  // (and the gateway's public host) may be different.
  useEffect(() => {
    let cancelled = false
    GetConfig().then(c => { if (!cancelled) setCfg(c) }).catch(() => {})
    return () => { cancelled = true }
  }, [status.role])

  // "Paired" is exactly what validateAgent demands before it will let the role
  // be saved (config.go): a gateway host and a token.
  const paired = !!(cfg?.Agent?.Token && cfg?.Agent?.GatewayHost)

  // Hovering a role previews its whole world: [data-role] remaps the accent
  // ramp and the registered --accent property (tokens.css) cross-fades every
  // var() consumer down the tree. The wizard's role cards do the same thing.
  const preview = (r: Role | null) => {
    document.documentElement.dataset.role = r ?? (status.role || 'unset')
  }
  // Never leave the app wearing a previewed role.
  useEffect(() => () => preview(null), [])

  const close = () => { setOpen(false); preview(null) }

  const pick = (target: Role) => {
    close()
    if (target === role) return
    if (target === 'agent' && !paired) { onPair(); return }
    setErr('')
    setConfirm(target)
  }

  const doSwitch = async (target: Role) => {
    setBusy(true); setErr('')
    try {
      const c = await GetConfig()
      if (target === 'gateway') {
        // Mints the token itself if this machine has never been a gateway.
        await SetupGateway((c.Gateway?.PublicHost || '').trim())
      } else {
        c.Role = 'agent'
        await SaveSettings(c)
        await RestartEngine()
      }
      setConfirm(null)
    } catch (e) {
      setErr(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <button
        ref={btnRef}
        type="button"
        disabled={attached}
        onClick={() => setOpen(o => { if (o) preview(null); return !o })}
        aria-haspopup="menu"
        aria-expanded={open}
        title={attached
          ? 'The Windows service owns this setup — stop the service to change roles'
          : `Running as ${role} — click to switch`}
        className="mx-3 mt-2 flex items-center gap-2.5 rounded-[var(--r-md)] border border-[color-mix(in_srgb,var(--accent)_22%,var(--border))] bg-[color-mix(in_srgb,var(--accent)_7%,transparent)] px-2.5 py-2 text-left transition-colors duration-500 enabled:hover:border-[color-mix(in_srgb,var(--accent)_40%,var(--border))] enabled:hover:bg-[color-mix(in_srgb,var(--accent)_12%,transparent)] disabled:cursor-default"
      >
        <Emblem role={role} size={26} glow />
        <span className="text-xs font-semibold tracking-tight">{role === 'agent' ? 'Agent' : 'Gateway'}</span>
        <span className="ml-auto flex items-center gap-1">
          {attached && <Badge tone="good">Service</Badge>}
          {!attached && (
            <span className={`text-[var(--text-3)] transition-transform duration-200 ${open ? 'rotate-180' : ''}`}>
              <IconChevronDown size={14} />
            </span>
          )}
        </span>
      </button>

      <Menu open={open} anchor={btnRef} onClose={close} minWidth={228}>
        <div className="px-2 pb-1 pt-1 text-[10px] font-semibold uppercase tracking-[var(--tracking-label)] text-[var(--text-3)]">
          This machine is
        </div>
        {(['gateway', 'agent'] as Role[]).map(r => (
          <MenuItem
            key={r}
            on={r === role}
            lead={<Emblem role={r} size={22} fixed={r !== role} />}
            title={r === 'agent' ? 'Agent' : 'Gateway'}
            hint={r === 'agent'
              ? (paired ? 'Hosts Minecraft — dials out' : 'Not paired yet — set up a pairing code')
              : 'Faces the internet — players connect here'}
            onClick={() => pick(r)}
            onPointerEnter={() => preview(r)}
            onPointerLeave={() => preview(null)}
          />
        ))}
      </Menu>

      {confirm && (
        <Modal
          title={confirm === 'agent' ? 'Switch to agent?' : 'Switch to gateway?'}
          onClose={() => { if (!busy) { setConfirm(null); setErr('') } }}
          footer={
            <>
              <Button variant="ghost" onClick={() => { setConfirm(null); setErr('') }} disabled={busy}>Cancel</Button>
              <Button onClick={() => doSwitch(confirm)} disabled={busy}>
                {busy ? 'Switching…' : confirm === 'agent' ? 'Become the agent' : 'Become the gateway'}
              </Button>
            </>
          }
        >
          <div className="space-y-3 text-sm text-[var(--text-2)]">
            <p>
              This machine stops being the{' '}
              <RoleWord role={OTHER[confirm]}>{OTHER[confirm]}</RoleWord> and becomes the{' '}
              <RoleWord role={confirm}>{confirm}</RoleWord>. The engine restarts, so{' '}
              <b className="text-[var(--text)]">any live player sessions drop</b>.
            </p>
            {confirm === 'gateway' ? (
              <p>
                Players will connect to this machine and it will listen for an agent. Its
                certificate is kept in the config folder, so any pairing code it has already
                handed out keeps working.
              </p>
            ) : (
              <p>
                This machine will dial out to{' '}
                <span className="font-mono text-[var(--text)]">{cfg?.Agent?.GatewayHost}</span>{' '}
                and stop accepting players directly. Its gateway settings stay in the config,
                so you can switch back.
              </p>
            )}
            <p className="text-[var(--text-3)]">
              Analytics keeps one history for this machine, and rows carry no role — sessions
              recorded as the {OTHER[confirm]} and as the {confirm} will sit side by side in
              Traffic and Analytics.
            </p>
            {err && <ErrorBanner message={err} onDismiss={() => setErr('')} />}
          </div>
        </Modal>
      )}
    </>
  )
}
