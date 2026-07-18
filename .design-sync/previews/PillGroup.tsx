import { useState } from 'react'
import { PillGroup } from 'frontend'

const dot = (
  <span style={{ display: 'inline-flex', width: 6, height: 6, borderRadius: '50%', background: 'var(--good)' }} />
)

/** The player-filter chips from Players — the indicator sits on "All". */
export function PlayerFilters() {
  const [v, setV] = useState('all')
  return (
    <PillGroup
      value={v} onChange={setV}
      options={[
        { value: 'all', label: 'All' },
        { value: 'online', label: <>{dot}Online</> },
        { value: 'survival', label: 'Survival' },
        { value: 'creative', label: 'Creative' },
      ]}
    />
  )
}

/** Active on the mixed-content chip — the dot and text stay aligned. */
export function ActiveWithDot() {
  const [v, setV] = useState('online')
  return (
    <PillGroup
      value={v} onChange={setV}
      options={[
        { value: 'all', label: 'All' },
        { value: 'online', label: <>{dot}Online</> },
        { value: 'survival', label: 'Survival' },
      ]}
    />
  )
}
