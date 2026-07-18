import { useState } from 'react'
import { Checkbox } from 'frontend'

/** The log console's follow checkbox, checked — from Activity. */
export function Canonical() {
  const [on, setOn] = useState(true)
  return <Checkbox checked={on} onChange={setOn} label="Follow" />
}

/** Unchecked and checked side by side. */
export function States() {
  const [a, setA] = useState(false)
  const [b, setB] = useState(true)
  return (
    <div style={{ display: 'flex', gap: 18, alignItems: 'center', flexWrap: 'wrap' }}>
      <Checkbox checked={a} onChange={setA} label="Also revoke its live invites" />
      <Checkbox checked={b} onChange={setB} label="Wrap long lines" />
    </div>
  )
}
