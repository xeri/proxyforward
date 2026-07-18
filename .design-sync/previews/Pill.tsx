import { useState } from 'react'
import { Pill, IconClose } from 'frontend'

/** The filter chip both ways — on carries the accent tint, off stays quiet. */
export function OnAndOff() {
  const [a, setA] = useState(true)
  const [b, setB] = useState(false)
  const [c, setC] = useState(false)
  return (
    <div style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
      <Pill on={a} onClick={() => setA(v => !v)}>Survival</Pill>
      <Pill on={b} onClick={() => setB(v => !v)}>Creative</Pill>
      <Pill on={c} onClick={() => setC(v => !v)}>Hardcore</Pill>
    </div>
  )
}

/** The active country filter from Players — flag slot, code, and a dismiss
 * glyph. Uses the app's own 🌐 fallback: Windows renders flag emoji as bare
 * regional-indicator letters, which reads as a duplicated country code. */
export function CountryChip() {
  return (
    <Pill on onClick={() => {}}>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        🌐 DE <IconClose size={11} />
      </span>
    </Pill>
  )
}
