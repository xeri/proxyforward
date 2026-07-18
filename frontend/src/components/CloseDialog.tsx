import {useEffect, useState} from 'react'
import {HideToTray, QuitAndDisconnect, StopEngine} from '../../wailsjs/go/app/App'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Button, Modal} from './ui'

type Prompt = {kind: 'notice' | 'asktray' | 'askquit' | 'stop'; players: number}

/**
 * The close fork. BeforeClose (Go) swallows any close it cannot silently honor
 * and emits pxf:close; this renders the named-count confirm. One dialog shape
 * serves close-with-players (both tray variants), the tray menu's Stop, and
 * the one-time first-minimize notice — the caller-owned Modal pattern
 * (Agents.tsx precedent), mounted once in App so it overlays any screen.
 */
export function CloseDialog() {
  const [prompt, setPrompt] = useState<Prompt | null>(null)
  useEffect(() => EventsOn('pxf:close', (p: Prompt) => setPrompt(p)), [])
  if (!prompt) return null

  const dismiss = () => setPrompt(null)
  const {kind, players} = prompt
  const who = players === 1 ? 'One player is' : `${players} players are`
  const them = players === 1 ? 'them' : 'them all'

  if (kind === 'notice') {
    return (
      <Modal
        title="proxyforward keeps running"
        onClose={dismiss}
        footer={<>
          <Button variant="ghost" size="sm" onClick={dismiss}>Cancel</Button>
          <Button variant="primary" size="sm" onClick={() => { dismiss(); HideToTray() }}>Minimize to tray</Button>
        </>}
      >
        <p className="text-sm text-[var(--text-2)]">
          Closing the window minimizes proxyforward to the tray, and the tunnel
          stays up. You can change this in Settings. This notice is shown once.
        </p>
      </Modal>
    )
  }
  if (kind === 'asktray') {
    return (
      <Modal
        title={`${players} ${players === 1 ? 'player is' : 'players are'} connected`}
        onClose={dismiss}
        footer={<>
          <Button variant="danger" size="sm" onClick={() => { dismiss(); QuitAndDisconnect() }}>Quit and disconnect</Button>
          <Button variant="primary" size="sm" onClick={() => { dismiss(); HideToTray() }}>Minimize</Button>
        </>}
      >
        <p className="text-sm text-[var(--text-2)]">
          proxyforward will keep running in the tray, so {players === 1 ? 'they stay' : 'they all stay'} connected.
        </p>
      </Modal>
    )
  }
  if (kind === 'stop') {
    return (
      <Modal
        title={`Disconnect ${players} ${players === 1 ? 'player' : 'players'}?`}
        onClose={dismiss}
        footer={<>
          <Button variant="ghost" size="sm" onClick={dismiss}>Cancel</Button>
          <Button variant="danger" size="sm" onClick={() => { dismiss(); StopEngine(false).catch(() => {}) }}>Stop</Button>
        </>}
      >
        <p className="text-sm text-[var(--text-2)]">
          {who} connected right now. Stopping the tunnel drops {them} immediately.
        </p>
      </Modal>
    )
  }
  return (
    <Modal
      title={`Disconnect ${players} ${players === 1 ? 'player' : 'players'}?`}
      onClose={dismiss}
      footer={<>
        <Button variant="ghost" size="sm" onClick={dismiss}>Cancel</Button>
        <Button variant="danger" size="sm" onClick={() => { dismiss(); QuitAndDisconnect() }}>Quit and disconnect</Button>
      </>}
    >
      <p className="text-sm text-[var(--text-2)]">
        Closing will disconnect {them} immediately.
      </p>
    </Modal>
  )
}
