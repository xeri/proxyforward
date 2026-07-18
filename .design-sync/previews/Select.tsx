import { useState } from 'react'
import { Field, Select } from 'frontend'

/** The log-level filter from Activity — a compact fixed-width Select. The
 * popup is a portal on menu glass; it opens on click (interaction-only, so
 * the cards show the closed control). */
export function LevelFilter() {
  const [level, setLevel] = useState('all')
  return (
    <div style={{ width: 176 }}>
      <Select
        value={level}
        onChange={setLevel}
        options={[
          { value: 'all', label: 'All levels' },
          { value: 'debug', label: 'Debug' },
          { value: 'info', label: 'Info' },
          { value: 'warn', label: 'Warn' },
          { value: 'error', label: 'Error' },
        ]}
      />
    </div>
  )
}

/** Inside a Field, the settings-form shape. */
export function InField() {
  const [region, setRegion] = useState('auto')
  return (
    <div style={{ maxWidth: 320 }}>
      <Field label="History range" hint="How far back the bandwidth chart keeps samples.">
        <Select
          value={region}
          onChange={setRegion}
          options={[
            { value: 'auto', label: 'Automatic' },
            { value: '1h', label: 'Last hour' },
            { value: '24h', label: 'Last 24 hours' },
            { value: '7d', label: 'Last 7 days' },
          ]}
        />
      </Field>
    </div>
  )
}

export function DisabledSelect() {
  return (
    <div style={{ width: 220 }}>
      <Select value="quic" onChange={() => {}} disabled options={[{ value: 'quic', label: 'QUIC (negotiated)' }]} />
    </div>
  )
}
