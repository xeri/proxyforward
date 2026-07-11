import {useState} from 'react'
import {ChooseAndInspectSetupFile, ExportSetup, ImportSetup} from '../../wailsjs/go/app/App'
import {app} from '../../wailsjs/go/models'
import {Badge, Button, ErrorBanner, Field, TextInput, Toggle} from './ui'
import {IconCheck} from './icons'

/** ExportSetupRow drives ExportSetup: optional passphrase, then the native
 * save dialog. Used in Settings (the wizard has nothing to export yet). */
export function ExportSetupRow({disabled}: {disabled?: boolean}) {
  const [encrypt, setEncrypt] = useState(true)
  const [pass, setPass] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [done, setDone] = useState('')

  const doExport = async () => {
    setBusy(true); setErr(''); setDone('')
    try {
      const path = await ExportSetup(encrypt ? pass : '')
      if (path) setDone(path)
    } catch (e) { setErr(String(e)) }
    finally { setBusy(false) }
  }

  return (
    <div className={disabled ? 'pointer-events-none opacity-50' : ''}>
      <Toggle checked={encrypt} onChange={v => { setEncrypt(v); setDone('') }}
        label="Encrypt with passphrase"
        hint={encrypt
          ? 'You will need this passphrase to import the file. There is no recovery if you lose it.'
          : 'Without a passphrase, anyone who gets this file can join or impersonate your setup — it contains your token and keys.'} />
      {encrypt && (
        <div className="mt-1">
          <Field label="Passphrase">
            <TextInput type="password" value={pass} onChange={v => { setPass(v); setDone('') }}
              placeholder="Choose a passphrase" onEnter={doExport} />
          </Field>
        </div>
      )}
      <div className="mt-3 flex items-center justify-between gap-3">
        <span className="text-xs text-[var(--text-3)]">
          Everything travels in one .pfsetup file: pairing, tunnels, keys, and statistics.
        </span>
        <Button size="sm" onClick={doExport} disabled={busy || (encrypt && !pass)}>
          {busy ? 'Exporting…' : 'Export setup…'}
        </Button>
      </div>
      {done && (
        <div className="mt-2 flex items-center gap-1.5 text-xs text-[var(--good)]">
          <IconCheck size={14} /> Exported to <code className="max-w-xs truncate font-mono">{done}</code>
        </div>
      )}
      {err && <div className="mt-2"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}
    </div>
  )
}

/** ImportSetupFlow drives the whole restore path: pick a .pfsetup file, show
 * what it is (role, date, encrypted), take a passphrase when needed, confirm
 * the overwrite, then ImportSetup. Shared by Settings and the wizard. */
export function ImportSetupFlow({disabled, isWizard, onDone}: {
  disabled?: boolean
  /** Wizard installs have nothing to lose, so the overwrite warning softens. */
  isWizard?: boolean
  onDone?: () => void
}) {
  const [info, setInfo] = useState<app.SetupFileInfo | null>(null)
  const [pass, setPass] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const choose = async () => {
    setErr('')
    try {
      const i = await ChooseAndInspectSetupFile()
      if (i) { setInfo(i); setPass('') }
    } catch (e) { setErr(String(e)) }
  }

  const doImport = async () => {
    if (!info) return
    setBusy(true); setErr('')
    try {
      await ImportSetup(info.path, pass)
      setInfo(null)
      onDone?.()
    } catch (e) { setErr(String(e)) }
    finally { setBusy(false) }
  }

  return (
    <div className={disabled ? 'pointer-events-none opacity-50' : ''}>
      {!info ? (
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs text-[var(--text-3)]">
            Restore a setup exported on another machine or OS install.
          </span>
          <Button variant="ghost" size="sm" onClick={choose}>Import setup…</Button>
        </div>
      ) : (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--panel-2)] p-3.5">
          <div className="flex items-center gap-2">
            <Badge tone="accent">{info.role}</Badge>
            {info.encrypted && <Badge tone="neutral">encrypted</Badge>}
            <span className="min-w-0 truncate text-xs text-[var(--text-3)]">
              exported {new Date(info.exportedAtMs).toLocaleString()} · v{info.appVersion}
            </span>
          </div>
          <div className="mt-1.5 truncate font-mono text-[11px] text-[var(--text-3)]">{info.path}</div>
          {info.encrypted && (
            <div className="mt-3">
              <Field label="Passphrase">
                <TextInput type="password" value={pass} onChange={setPass}
                  placeholder="Passphrase used at export" autoFocus onEnter={doImport} />
              </Field>
            </div>
          )}
          <p className="mt-3 text-xs leading-relaxed text-[var(--warn)]">
            {isWizard
              ? `This machine becomes the ${info.role} from the backup and connects with its identity.`
              : `This replaces this machine's pairing, tunnels, and statistics with the ${info.role} setup from the backup, then restarts the engine.`}
          </p>
          <div className="mt-3 flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => { setInfo(null); setErr('') }}>Cancel</Button>
            <Button size="sm" onClick={doImport} disabled={busy || (info.encrypted && !pass)}>
              {busy ? 'Importing…' : 'Import & restart'}
            </Button>
          </div>
        </div>
      )}
      {err && <div className="mt-2"><ErrorBanner message={err} onDismiss={() => setErr('')} /></div>}
    </div>
  )
}
