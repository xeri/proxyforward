import { useState } from 'react'
import { Switch } from 'frontend'

const cap = { fontSize: 12, color: 'var(--text-3)' }

/** Both rest states. The accent track + glow marks on; off sits in its seat. */
export function OnOff() {
  const [a, setA] = useState(true)
  const [b, setB] = useState(false)
  return (
    <div style={{ display: 'flex', gap: 24, alignItems: 'center' }}>
      <span style={{ display: 'inline-flex', gap: 8, alignItems: 'center' }}>
        <Switch checked={a} onChange={setA} label="Mojang lookups" />
        <span style={cap}>on</span>
      </span>
      <span style={{ display: 'inline-flex', gap: 8, alignItems: 'center' }}>
        <Switch checked={b} onChange={setB} label="Prometheus metrics" />
        <span style={cap}>off</span>
      </span>
    </div>
  )
}

export function Disabled() {
  return (
    <div style={{ display: 'flex', gap: 24, alignItems: 'center' }}>
      <span style={{ display: 'inline-flex', gap: 8, alignItems: 'center' }}>
        <Switch checked disabled onChange={() => {}} label="Write to log file" />
        <span style={cap}>disabled on</span>
      </span>
      <span style={{ display: 'inline-flex', gap: 8, alignItems: 'center' }}>
        <Switch checked={false} disabled onChange={() => {}} label="Write to log file" />
        <span style={cap}>disabled off</span>
      </span>
    </div>
  )
}
