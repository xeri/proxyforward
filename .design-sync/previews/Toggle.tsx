import { useState } from 'react'
import { Toggle } from 'frontend'

/** The settings row, on — Minecraft-aware sniffing, ported from Tunnels. */
export function Canonical() {
  const [on, setOn] = useState(true)
  return (
    <div style={{ width: 480, maxWidth: '100%' }}>
      <Toggle
        checked={on} onChange={setOn}
        label="Minecraft-aware"
        hint="Sniff player usernames from the login handshake for the traffic and players views."
      />
    </div>
  )
}

/** Off, with a rich hint (markup rides through the hint slot). */
export function Off() {
  const [on, setOn] = useState(false)
  return (
    <div style={{ width: 480, maxWidth: '100%' }}>
      <Toggle
        checked={on} onChange={setOn}
        label="PROXY protocol v2"
        hint={<>Send the real client IP to the local server (Paper/Velocity). <b>Mutually exclusive</b> with BungeeCord/Velocity IP-forwarding.</>}
      />
    </div>
  )
}

/** Disabled dims the whole row, not just the switch. */
export function Disabled() {
  return (
    <div style={{ width: 480, maxWidth: '100%' }}>
      <Toggle
        checked disabled onChange={() => {}}
        label="Mojang lookups"
        hint="Resolve player names and skins through Mojang's API. The only outbound call analytics makes."
      />
    </div>
  )
}
