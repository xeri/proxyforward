import {useState} from 'react'
import {runStateOf, UIStatus} from '../state'
import {powerStop, useServiceRunning} from '../power'
import {Button, Modal} from './ui'
import {IconStop} from './icons'

/**
 * The engine's Stop control for the page header. It renders only while the run
 * is up (or draining) — when the run is off the stopped banner owns Start, so
 * each state has exactly one control and they never both show. It never lives
 * on the pipeline (DESIGN rule 2); the caller places it in the header.
 *
 * Players are named before they are dropped: a Stop with clients connected
 * opens a confirm modal counting them; an empty tunnel stops at once.
 */
export function PowerControls({status, players}: {status: UIStatus; players: number}) {
  const runState = runStateOf(status)
  const stopping = runState === 'stopping'
  const svcRunning = useServiceRunning(status)
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)

  const doStop = () => {
    setBusy(true)
    powerStop(svcRunning, false).catch(() => {}).finally(() => setBusy(false))
  }
  const onStopClick = () => {
    if (players > 0) { setConfirm(true); return }
    doStop()
  }

  return (
    <>
      <Button variant="ghost" size="sm" onClick={onStopClick} disabled={busy || stopping}>
        <IconStop size={13} /> {stopping ? 'Stopping…' : 'Stop'}
      </Button>

      {confirm && (
        <Modal
          title={`Disconnect ${players} ${players === 1 ? 'player' : 'players'}?`}
          onClose={() => setConfirm(false)}
          footer={
            <>
              <Button variant="ghost" size="sm" onClick={() => setConfirm(false)}>Cancel</Button>
              <Button variant="danger" size="sm" onClick={() => { setConfirm(false); doStop() }}>Stop</Button>
            </>
          }
        >
          <p className="text-sm text-[var(--text-2)]">
            {players === 1 ? 'One player is' : `${players} players are`} connected right now.
            Stopping the tunnel drops {players === 1 ? 'them' : 'them all'} immediately.
          </p>
        </Modal>
      )}
    </>
  )
}
