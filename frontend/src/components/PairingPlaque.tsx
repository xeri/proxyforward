import {ReactNode, useEffect, useState} from 'react'
import {PairingQR} from '../../wailsjs/go/app/App'
import {IconButton} from './ui'
import {IconQR} from './icons'
import {mask as maskGroups, split} from '../pairing'

/** PairingPlaque: the one artifact this app asks a human to carry to another
 * machine, laid out to be carried.
 *
 * A pairing code is read aloud over a call, typed into a second laptop, or
 * photographed. So it is not a paragraph of monospace: the gateway it names sits
 * on its own line, and the payload is spaced into its six groups of five — the
 * unit someone says, hears, and checks off. Never case-transformed, at any size:
 * codes are credentials (DESIGN.md rule 6).
 *
 * Material is the recessed well every control wears, not the bloom frame — one
 * surface per screen answers the pointer, and on the screens this appears on that
 * surface is something else (rule 2).
 *
 * A v1 code has no groups to lay out, so it renders whole. That is not a
 * degradation to apologise for: an older gateway's code is still a working code.
 */
export function PairingPlaque({code, actions, masked = false, footnote}: {
  code: string
  /** Copy, reveal, re-issue — whatever the surface owns. The QR toggle joins them. */
  actions?: ReactNode
  /** Render the payload as bullets of the same widths, so revealing never moves
   * the layout. The bullets are real text: dragging them off hands over bullets. */
  masked?: boolean
  footnote?: ReactNode
}) {
  const parts = split(code)
  const [qrOpen, setQrOpen] = useState(false)
  // Masking exists for screen-sharing; a QR is the code in a form a camera reads,
  // so it closes with the mask rather than outliving it.
  useEffect(() => { if (masked) setQrOpen(false) }, [masked])

  const groups = parts ? (masked ? maskGroups(parts.groups) : parts.groups) : []

  return (
    <div className="pf-well px-4 py-3.5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          {parts && (
            <div className="mb-2 truncate font-mono text-sub text-[var(--text-3)]">
              {parts.host}:{parts.port}
            </div>
          )}
          {parts ? (
            <div className={`flex flex-wrap gap-x-3.5 gap-y-1 font-mono text-code font-medium leading-relaxed tracking-[var(--tracking-code)] text-[var(--text)] ${
              masked ? 'select-none' : 'select-text'
            }`}>
              {groups.map((g, i) => <span key={i}>{g}</span>)}
            </div>
          ) : (
            <code className={`block break-all font-mono text-emphasis leading-relaxed text-[var(--text)] ${
              masked ? 'select-none' : 'select-text'
            }`}>
              {masked ? '•'.repeat(28) : code}
            </code>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {!masked && (
            <IconButton
              title={qrOpen ? 'Hide the QR code' : 'Show a QR code to scan'}
              onClick={() => setQrOpen(o => !o)}
            >
              <IconQR size={16} />
            </IconButton>
          )}
          {actions}
        </div>
      </div>
      {qrOpen && !masked && <QRPanel code={code} />}
      {footnote && <div className="mt-2.5 text-xs text-[var(--text-3)]">{footnote}</div>}
    </div>
  )
}

/** QRPanel renders the code as a symbol a phone can read, so the second machine
 * needs no typing at all. All four states are designed: loading, the symbol, an
 * honest unavailable (the encoder refuses codes past its largest symbol rather
 * than emitting one no camera resolves), and — via the parent — not asked for.
 *
 * The plate is light in both themes on purpose. A scanner needs dark modules on a
 * light field; trench water behind a QR is a symbol that looks right and reads to
 * nothing. */
function QRPanel({code}: {code: string}) {
  const [qr, setQr] = useState<{size: number; path: string} | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    let cancelled = false
    setQr(null); setErr('')
    PairingQR(code)
      .then(r => { if (!cancelled) setQr({size: r.size, path: r.path}) })
      .catch(e => { if (!cancelled) setErr(String((e as Error)?.message ?? e)) })
    return () => { cancelled = true }
  }, [code])

  return (
    <div className="pf-fade mt-3.5 flex items-center gap-3.5">
      <div className="grid size-[8.5rem] shrink-0 place-items-center rounded-[var(--r-sm)] bg-[#f4f7f7] p-2.5">
        {qr ? (
          // viewBox is the module grid plus a two-module quiet zone on each side,
          // which is what a reader needs to find the symbol's edges.
          <svg viewBox={`-2 -2 ${qr.size + 4} ${qr.size + 4}`} className="size-full" shapeRendering="crispEdges" role="img" aria-label="Pairing code as a QR code">
            <path d={qr.path} fill="#02090f" />
          </svg>
        ) : err ? (
          <span className="px-1 text-center font-mono text-micro leading-snug text-[#5a6a6a]">no QR for this one</span>
        ) : (
          <span className="font-mono text-micro text-[#5a6a6a]">…</span>
        )}
      </div>
      <p className="min-w-0 text-xs leading-relaxed text-[var(--text-3)]">
        {err
          ? <>This code is too long to render as a QR — {err}. Copy it instead; the code itself is fine.</>
          : <>Scan this on the other machine to skip the typing. It carries the same code, so anyone who photographs it can pair.</>}
      </p>
    </div>
  )
}
