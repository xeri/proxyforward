import { useState } from 'react'
import { SegmentedControl } from 'frontend'

/** The fleet sort control from Agents — four segments, thumb on the first. */
export function SortKeys() {
  const [v, setV] = useState('health')
  return (
    <div style={{ display: 'flex' }}>
      <SegmentedControl
        value={v} onChange={setV}
        options={[
          { value: 'health', label: 'Health' }, { value: 'traffic', label: 'Traffic' },
          { value: 'players', label: 'Players' }, { value: 'name', label: 'Name' },
        ]}
      />
    </div>
  )
}

/** Two segments — the roster density toggle, thumb on the second. */
export function Density() {
  const [v, setV] = useState('list')
  return (
    <div style={{ display: 'flex' }}>
      <SegmentedControl
        value={v} onChange={setV}
        options={[{ value: 'grid', label: 'Grid' }, { value: 'list', label: 'List' }]}
      />
    </div>
  )
}

/** The theme picker from Settings — mixed label widths share the widest. */
export function Theme() {
  const [v, setV] = useState('system')
  return (
    <div style={{ display: 'flex' }}>
      <SegmentedControl
        value={v} onChange={setV}
        options={[
          { value: 'dark', label: 'Dark' }, { value: 'light', label: 'Light' },
          { value: 'system', label: 'System' },
        ]}
      />
    </div>
  )
}
