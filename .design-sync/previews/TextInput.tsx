import { useState } from 'react'
import { TextInput, IconSearch } from 'frontend'

/** The md text field — the gateway's public address, from the wizard. */
export function Canonical() {
  const [v, setV] = useState('play.hexvale.net')
  return (
    <div style={{ maxWidth: 420 }}>
      <TextInput value={v} onChange={setV} placeholder="play.example.com" />
    </div>
  )
}

/** mono — addresses, ports, and pairing codes ride in the mono form. */
export function Mono() {
  const [addr, setAddr] = useState('127.0.0.1:25565')
  const [code, setCode] = useState('')
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10, maxWidth: 480 }}>
      <TextInput value={addr} onChange={setAddr} mono />
      <TextInput value={code} onChange={setCode} placeholder="pxf://host:8474/v1/pair/…#sha256:…" mono />
    </div>
  )
}

/** size="sm" + icon — the list-toolbar filter box from Agents and Players. */
export function SmallWithIcon() {
  const [q, setQ] = useState('')
  return (
    <div style={{ width: 200 }}>
      <TextInput
        size="sm" icon={<IconSearch size={14} />} value={q} onChange={setQ}
        placeholder="Filter agents…" ariaLabel="Filter agents by name, host, or id"
      />
    </div>
  )
}

/** type="password" grows the reveal eye on the right (metrics auth token). */
export function Password() {
  const [v, setV] = useState('s3cret-scrape-token')
  return (
    <div style={{ maxWidth: 420 }}>
      <TextInput value={v} onChange={setV} type="password" />
    </div>
  )
}
