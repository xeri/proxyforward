import {useState} from 'react'
import {SetPause} from '../../wailsjs/go/app/App'
import {Badge, Button, Modal} from './ui'
import {IconPause, IconPlay} from './icons'

/**
 * The pause verbs and their honesty, shared by the agent's Tunnels cards and
 * the gateway's agent drill-in. Two independent bits exist (each side's own);
 * this side's controls only ever touch this side's bit, so Resume renders only
 * when our bit is set — a pause held by the peer alone is theirs to lift, and
 * the badge says so instead of offering a button that would do nothing.
 *
 * Players are named before they are dropped: pausing with players connected
 * opens the fork (keep them draining, or disconnect now) — the same shape as
 * Stop's confirm. An empty tunnel pauses at once.
 */

/** pausedLabel renders pausedBy ("mine" | "theirs" | "both", from this side's
 * view) for the given peer name ("the gateway" / "the agent"). */
export function pausedLabel(pausedBy: string, peer: string): string {
  switch (pausedBy) {
    case 'mine': return 'Paused by you'
    case 'theirs': return `Paused by ${peer}`
    case 'both': return `Paused by you and ${peer}`
    default: return 'Paused'
  }
}

/** PausedBadge: the state, naming who holds it. */
export function PausedBadge({pausedBy, peer}: {pausedBy: string; peer: string}) {
  return <Badge tone="warn">{pausedLabel(pausedBy, peer)}</Badge>
}

export function PauseControl({agentId = '', tunnelId = '', what, paused, pausedBy, players, unheard, onDone}: {
  agentId?: string   // gateway side: whose tunnel; '' on the agent
  tunnelId?: string  // '' = the whole agent
  what: string       // what the confirm names: 'this tunnel' | 'this agent'
  paused: boolean
  pausedBy: string
  players: number
  /** unheard: the peer's build predates pause and will not display this —
   * enforcement still works here (our bit gates our own side), so the control
   * stays enabled, but the fork says so out loud. */
  unheard?: boolean
  /** onDone: the caller's re-poll hook (the write-then-repoll loop); surfaces
   * that ride the 2 Hz tick can omit it. */
  onDone?: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const mine = pausedBy === 'mine' || pausedBy === 'both'

  const set = (p: boolean, disconnect: boolean) => {
    setBusy(true)
    SetPause(agentId, tunnelId, p, disconnect)
      .then(() => onDone?.())
      .catch(() => {})
      .finally(() => setBusy(false))
  }
  const onPause = () => {
    if (players > 0) { setConfirm(true); return }
    set(true, false)
  }

  if (paused && !mine) return null // the peer's pause is the peer's to lift
  return (
    <>
      {paused ? (
        <Button variant="ghost" size="sm" onClick={() => set(false, false)} disabled={busy}>
          <IconPlay size={13} /> Resume
        </Button>
      ) : (
        <Button variant="ghost" size="sm" onClick={onPause} disabled={busy}>
          <IconPause size={13} /> Pause
        </Button>
      )}

      {confirm && (
        <Modal
          title={`Pause with ${players} ${players === 1 ? 'player' : 'players'} connected?`}
          onClose={() => setConfirm(false)}
          footer={
            <>
              <Button variant="ghost" size="sm" onClick={() => setConfirm(false)}>Cancel</Button>
              <Button variant="danger" size="sm" onClick={() => { setConfirm(false); set(true, true) }}>Pause and disconnect</Button>
              <Button variant="primary" size="sm" onClick={() => { setConfirm(false); set(true, false) }}>Pause, keep them</Button>
            </>
          }
        >
          <p className="text-sm text-[var(--text-2)]">
            New players are turned away while {what} is paused; the port stays yours.
            {players === 1 ? ' The player already on' : ' Players already on'} can stay
            until they leave, or be disconnected now.
            {unheard && ' The other side runs an older build and will not show this pause.'}
          </p>
        </Modal>
      )}
    </>
  )
}
