import {Component, ErrorInfo, ReactNode} from 'react'
import {Button, Disclosure, FatalStatement} from './ui'
import {useAlert} from './Announcer'
import {CloseDialog} from './CloseDialog'
import {WindowControls} from '../layout/WindowControls'

/**
 * Where the Go side writes fatal runtime crashes and top-level startup errors
 * (`installCrashLog`, `main.go`). The production binary is a windowsgui app
 * with no usable stderr, so that file is the only trace an engine crash leaves.
 * Both fallbacks below name it, because from the outside a render throw and an
 * engine panic look identical: a window that stopped being a window.
 */
export const CRASH_LOG_PATH = '%APPDATA%\\proxyforward\\logs\\crash.log'

export type Fault = {
  /** The thrown value's message, verbatim — the one diagnostic a WebView2
   *  window with no devtools can still show its user. */
  message: string
  /** React's component stack: which subtree died. Empty until componentDidCatch. */
  stack: string
  /** Discard the fault and re-render the children. */
  reset: () => void
}

type Props = {children: ReactNode; fallback: (fault: Fault) => ReactNode}
type State = {failed: boolean; message: string; stack: string}

/**
 * The repo's one class component, and a deliberate exception to
 * `.claude/rules/frontend.md` ("React 19 with classic hooks"): React has never
 * shipped a hook form of `componentDidCatch`, so an error boundary is a class
 * or it does not exist. The exception ends at this declaration — every fallback
 * below is an ordinary function component.
 *
 * Without it, any render throw unmounts the whole tree, and in a WebView2
 * window with no devtools that is a blank frame with nothing to read and no way
 * out. The boundary is a render-time reply, not a logger: React already prints
 * the throw to the console, so this only has to make it *visible*.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = {failed: false, message: '', stack: ''}

  static getDerivedStateFromError(err: unknown): Partial<State> {
    const msg = err instanceof Error ? err.message : String(err ?? '')
    return {failed: true, message: msg.trim()}
  }

  componentDidCatch(_err: Error, info: ErrorInfo) {
    // The component stack arrives only here — getDerivedStateFromError never
    // sees it — and it is what turns a screenshot into a bug report.
    this.setState({stack: (info.componentStack || '').trim()})
  }

  render() {
    if (!this.state.failed) return this.props.children
    return this.props.fallback({
      message: this.state.message,
      stack: this.state.stack,
      reset: () => this.setState({failed: false, message: '', stack: ''}),
    })
  }
}

/** The evidence, in the working voice under the statement: the message, the
 *  stack folded away in a well, and where an engine crash would have gone. */
function FaultEvidence({fault}: {fault: Fault}) {
  return (
    <div className="max-w-prose space-y-2.5">
      <p className="text-sub text-[var(--text-3)]">
        {fault.message || 'The error carried no message.'}
      </p>
      {fault.stack && (
        <Disclosure label="Where it happened" hint="The component stack, for a bug report.">
          <pre className="whitespace-pre-wrap break-words text-caption leading-relaxed text-[var(--text-3)]">
            {fault.stack}
          </pre>
        </Disclosure>
      )}
      <p className="text-caption italic text-[var(--text-3)]">
        If the engine crashed too, it writes to {CRASH_LOG_PATH}.
      </p>
    </div>
  )
}

/**
 * The inner fallback: one screen stopped drawing and the console around it did
 * not. Type on whitespace behind FatalStatement's coral rule, never a card —
 * a fatal is a statement, and the page already spends its one identity surface
 * elsewhere (DESIGN rule 2). It announces through `useAlert` rather than wearing
 * `role="alert"` itself: the evidence below is in the same subtree, and an alert
 * would recite the whole component stack.
 *
 * Navigating away resets it for free — App keys the boundary by `nav` — so
 * "Try again" is only for the screen you are already on.
 */
export function ScreenFault({fault}: {fault: Fault}) {
  useAlert(`This screen stopped drawing. ${fault.message}`)
  return (
    <div className="pf-rise py-2">
      <FatalStatement
        eyebrow="crash"
        title="This screen stopped drawing"
        detail="The tunnel is unaffected — this is a fault in the window, not the engine. Every other screen still opens."
        action={
          <div className="space-y-5">
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="ghost" size="sm" onClick={fault.reset}>Try again</Button>
              <Button variant="subtle" size="sm" onClick={() => window.location.reload()}>Reload the window</Button>
            </div>
            <FaultEvidence fault={fault} />
          </div>
        }
      />
    </div>
  )
}

/**
 * The outer fallback: the console itself threw, so Shell — sidebar, title bar,
 * scroller and all — is gone and this has to be its own frame. Deliberately
 * thin: a fallback that throws takes the whole tree with it, so this is plain
 * layout over tokens plus the two pieces the window genuinely cannot do
 * without. WindowControls, because a frameless window has no other way to be
 * moved out of the way or closed; CloseDialog with it, because BeforeClose (Go)
 * swallows any close it cannot honor silently and waits for that dialog to
 * answer — without it the close button would be the next dead end.
 */
export function ShellFault({fault}: {fault: Fault}) {
  useAlert(`The console stopped drawing. ${fault.message}`)
  return (
    <div className="grid h-full" style={{gridTemplateRows: 'var(--chrome-top) 1fr'}}>
      <header className="pf-drag flex items-stretch justify-end" style={{paddingTop: 'var(--titlebar-inset)'}}>
        <div className="pf-no-drag flex items-stretch" style={{height: 'var(--titlebar-h)'}}>
          <WindowControls />
        </div>
      </header>
      <main className="min-h-0 overflow-y-auto">
        <div data-band-content className="mx-auto w-full max-w-[var(--content-max)] px-[var(--page-pad)] py-10">
          <FatalStatement
            eyebrow="crash"
            title="The console stopped drawing"
            detail="The tunnel is unaffected — this is a fault in the window, not the engine. Reloading rebuilds it from scratch; the run keeps going either way."
            action={
              <div className="space-y-5">
                <Button variant="primary" size="sm" onClick={() => window.location.reload()}>Reload the window</Button>
                <FaultEvidence fault={fault} />
              </div>
            }
          />
        </div>
      </main>
      <CloseDialog />
    </div>
  )
}

/**
 * The devmock `&crash=` axis's only footprint in the app. The mock installs a
 * fake Wails bridge; it cannot reach React, so the throw has to come from a
 * component inside the boundary being walked. `import.meta.env.DEV` drops this
 * whole function — string included — from the shipped bundle, exactly like the
 * mock it serves.
 */
export function CrashProbe({where}: {where: 'screen' | 'shell'}) {
  if (import.meta.env.DEV && (window as {__pfCrash?: string}).__pfCrash === where) {
    throw new Error(`devmock &crash=${where}: a deliberate render throw, so the ${where} boundary can be walked`)
  }
  return null
}
