// ConnectionPill drives itself from app tick events; previews have no Wails
// runtime, so stub the minimal surface before the component module evaluates.
const w = window as any
w.runtime ??= { EventsOn: () => () => {} }
w.go ??= { app: { App: { Status: async () => ({}) } } }

import { useEffect, useState } from 'react'
import { ConnectionPill } from 'frontend'

// useTickStale flips to "Syncing…" when the status prop identity sits still
// for >2.5 s (in the app every 2 Hz tick is a fresh object). Re-mint the
// object each second to simulate live tick delivery — same data, live pulse.
function Live({ make }: { make: () => any }) {
  const [s, setS] = useState(make)
  useEffect(() => {
    const t = setInterval(() => setS(make()), 1000)
    return () => clearInterval(t)
  }, [])
  return <ConnectionPill status={s} />
}

const base = {
  mode: 'engine', version: 'dev', hostname: 'mc-basement', pid: 4242,
  configPath: '', runState: 'running', transport: 'quic',
  jitterMillis: 2, peerHostname: '', publicIp: '', peerPublicIp: '',
  localLanIps: [], peerLanIps: [], tunnels: [], connections: [],
  totalBytesIn: 0, totalBytesOut: 0, processStartMs: 0, peerAddr: '',
  linkBytesIn: 0, linkBytesOut: 0, allTimeBytesIn: 0, allTimeBytesOut: 0,
  cumulativeUptimeMs: 0, linkSessions: 1,
}

const agentUp = (over: any = {}) => ({
  ...base, role: 'agent', linkUp: true, agentConnected: false,
  rttMillis: 23, packetLossPct: 0, healthScore: 'good', agents: [],
  linkUpSinceMs: Date.now() - (2 * 3600_000 + 14 * 60_000), ...over,
})

const mkAgent = (over: any = {}) => ({
  agentId: 'a1b2c3d4', hostname: 'mc-basement', lanIps: [], remoteIp: '',
  linkUpSinceMs: Date.now() - 3600_000, rttMillis: 23, jitterMillis: 2,
  packetLossPct: 0, healthScore: 'good', linkBytesIn: 0, linkBytesOut: 0,
  tunnels: 1, players: 3, ...over,
})

/** The healthy agent link: green breathing dot, RTT and uptime tail. */
export function AgentConnected() {
  return <Live make={() => agentUp()} />
}

/** Degraded link: the health rollup turns the dot amber and packet loss joins
 * the readouts — state plus consequence at a glance. */
export function AgentDegraded() {
  return <Live make={() => agentUp({ rttMillis: 87, packetLossPct: 2.4, healthScore: 'warn' })} />
}

/** Gateway fleet rollups: one agent shows its own tail; three agents roll up
 * to a count with the worst-of-fleet health driving the dot; zero waits. */
export function GatewayFleet() {
  const gw = (agents: any[], up = true) => ({
    ...base, role: 'gateway', linkUp: false, agentConnected: up,
    rttMillis: agents.length === 1 ? agents[0].rttMillis : 0,
    packetLossPct: 0, healthScore: 'good', agents,
    linkUpSinceMs: up ? Date.now() - 47 * 60_000 : 0,
  })
  return (
    <div style={{ display: 'grid', gap: 10, justifyItems: 'start' }}>
      <Live make={() => gw([mkAgent()])} />
      <Live make={() => gw([mkAgent(), mkAgent({ agentId: 'e5f6', hostname: 'attic-pi', healthScore: 'warn' }), mkAgent({ agentId: '9a8b', hostname: 'vps-2' })])} />
      <Live make={() => gw([], false)} />
    </div>
  )
}

/** Deliberately off vs down: "Stopped" is a quiet grey (no alarm); a lost
 * link while running reads as the fault it is. */
export function StoppedAndReconnecting() {
  return (
    <div style={{ display: 'grid', gap: 10, justifyItems: 'start' }}>
      <Live make={() => agentUp({ runState: 'stopped', linkUp: false, linkUpSinceMs: 0 })} />
      <Live make={() => agentUp({ linkUp: false, healthScore: 'bad', linkUpSinceMs: 0 })} />
    </div>
  )
}
