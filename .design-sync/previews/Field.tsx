import { useState } from 'react'
import { Field, TextInput } from 'frontend'

/** Label + hint over a mono input — the pairing-code field from the wizard. */
export function PairingCode() {
  const [v, setV] = useState('')
  return (
    <div style={{ maxWidth: 460 }}>
      <Field label="Pairing code" hint="Shown by the gateway right after you set it up. Starts with pxf://">
        <TextInput value={v} onChange={setV} placeholder="pxf://host:8474/v1/pair/…#sha256:…" mono />
      </Field>
    </div>
  )
}

/** A filled field with a long hint — the gateway's public address. */
export function PublicAddress() {
  const [v, setV] = useState('play.hexvale.net')
  return (
    <div style={{ maxWidth: 460 }}>
      <Field label="Public address" hint="The address players and the agent will use — a plain IP works, but a stable DNS name (DDNS is fine) survives IP changes. You can change this later.">
        <TextInput value={v} onChange={setV} placeholder="play.example.com" />
      </Field>
    </div>
  )
}

/** Label only, no hint — the tunnel name field. */
export function LabelOnly() {
  const [v, setV] = useState('Survival')
  return (
    <div style={{ maxWidth: 460 }}>
      <Field label="Name">
        <TextInput value={v} onChange={setV} />
      </Field>
    </div>
  )
}
