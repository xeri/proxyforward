import {Button, FatalStatement} from './ui'
import {useAlert} from './Announcer'
import {OpenConfigDir} from '../../wailsjs/go/app/App'

/**
 * The config on disk could not be read, and the document this window is running
 * on is a substituted default.
 *
 * This surface exists to stand in front of the setup wizard, which is what a
 * defaulted config would otherwise present: an empty document has no role, and
 * no role means first run. That reading is wrong and expensive — the real config
 * is still on disk, and finishing the wizard saves over it, taking the gateway
 * address, every tunnel, the scope and the caps with it. So the fault is a stop,
 * not a banner over a working screen: every route out of here is deliberate.
 *
 * "Set this machine up again" is offered because it is sometimes the right
 * answer — a config from a build this one cannot read is not always worth
 * recovering. It says what it costs before it does it, and `config.Save` keeps a
 * copy beside the original either way (quarantineUnreadable), so the button is
 * survivable rather than final.
 */
export function ConfigFault({reason, configPath, onStartOver}: {
  reason: string
  configPath: string
  onStartOver: () => void
}) {
  useAlert(`The configuration could not be read. ${reason}`)
  return (
    <div className="mx-auto flex h-full w-full max-w-[var(--content-max)] items-center px-[var(--page-pad)]">
      <div className="pf-rise w-full">
        <FatalStatement
          eyebrow="unreadable"
          title="This machine's configuration could not be read"
          detail={
            <>
              Nothing has been changed or lost — the file is still where it was, and the
              tunnel has not been started from it. Fix it and reopen the app, or start
              over and set this machine up again.
              <span className="mt-2 block break-all font-mono text-sub text-[var(--text-3)]">{reason}</span>
            </>
          }
          action={
            <div className="space-y-5">
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="primary" size="sm" onClick={() => OpenConfigDir().catch(() => {})}>
                  Open the config folder
                </Button>
                <Button variant="ghost" size="sm" onClick={onStartOver}>
                  Set this machine up again
                </Button>
              </div>
              <p className="max-w-prose text-caption italic text-[var(--text-3)]">
                Setting up again replaces {configPath || 'the configuration'}. A copy of the
                unreadable file is kept beside it, ending in <span className="not-italic">.corrupt</span>.
              </p>
            </div>
          }
        />
      </div>
    </div>
  )
}
