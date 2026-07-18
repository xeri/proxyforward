import { Button, EmptyState, IconAgents, IconPlayers, IconPlus, IconTunnels } from 'frontend'

/** The written empty state with a way forward — the Agents roster before any
 * machine enrolls (Agents.tsx). */
export function NoAgents() {
  return (
    <div style={{ maxWidth: 560 }}>
      <EmptyState
        icon={<IconAgents size={28} />}
        title="No agents enrolled yet"
        hint="Issue a pairing code (here, on Overview, or in the setup wizard) and share it with a machine. Every agent that enrolls with it appears here — online or off."
        action={<Button onClick={() => {}}>Issue pairing code</Button>}
      />
    </div>
  )
}

/** Tunnels before the first one exists — icon, one-line hint, primary action
 * with a leading icon (Tunnels.tsx). */
export function NoTunnels() {
  return (
    <div style={{ maxWidth: 560 }}>
      <EmptyState
        icon={<IconTunnels size={28} />}
        title="No tunnels yet"
        hint="Add one to publish a local Minecraft server through the gateway."
        action={<Button onClick={() => {}}><IconPlus size={16} /> Add tunnel</Button>}
      />
    </div>
  )
}

/** The honest unavailable state: no action, because there is nothing the user
 * can click to fix it here (Players.tsx AnalyticsUnavailable). */
export function Unavailable() {
  return (
    <div style={{ maxWidth: 560 }}>
      <EmptyState
        icon={<IconPlayers size={28} />}
        title="Analytics isn't available"
        hint="The connected daemon is an older build without the analytics store, or the analytics database failed to open (see logs)."
      />
    </div>
  )
}
