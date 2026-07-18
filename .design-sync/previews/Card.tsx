import { Badge, Card, MonoChip, SectionHeader } from 'frontend'

const hairline = '1px solid var(--hairline)'

/** The quiet tier-2 panel with `label` — an uppercase overline heading that
 * lets the content dominate (the recommended form for most quiet cards). */
export function LabelledQuiet() {
  return (
    <div style={{ maxWidth: 560 }}>
      <Card label="Latency by country" subtitle="Average ping over the last 30 days">
        <div style={{ display: 'grid', gap: 8 }}>
          {[
            ['Germany', '18 ms'],
            ['Netherlands', '24 ms'],
            ['United States', '96 ms'],
          ].map(([country, ms]) => (
            <div key={country} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13 }}>
              <span style={{ color: 'var(--text-2)' }}>{country}</span>
              <span style={{ color: 'var(--text)', fontVariantNumeric: 'tabular-nums' }}>{ms}</span>
            </div>
          ))}
        </div>
      </Card>
    </div>
  )
}

/** `title` is a real 18px section heading; `pad={false}` lets full-bleed rows
 * own the edges — the Tunnels register shape, count badge in the action slot. */
export function TitledWithRows() {
  const rows = [
    { name: 'minecraft', port: ':25565', state: <Badge tone="good">Bound</Badge> },
    { name: 'bedrock-proxy', port: ':19132', state: <Badge tone="warn">Pending</Badge> },
    { name: 'map-web', port: 'auto', state: <Badge tone="neutral">Disabled</Badge> },
  ]
  return (
    <div style={{ maxWidth: 560 }}>
      <Card title="Registered tunnels" pad={false} action={<div style={{ paddingRight: 16 }}><Badge tone="neutral">3</Badge></div>}>
        <div>
          {rows.map(r => (
            <div key={r.name} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, padding: '10px 20px', borderTop: hairline }}>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--text)' }}>
                {r.name} <MonoChip>{r.port}</MonoChip>
              </span>
              {r.state}
            </div>
          ))}
        </div>
      </Card>
    </div>
  )
}

/** In-card sectioning: a SectionHeader introduces each grouped run of rows —
 * the agent-dossier composition. */
export function SectionedRows() {
  return (
    <div style={{ maxWidth: 560 }}>
      <Card label="Management">
        <div style={{ display: 'grid', gap: 10 }}>
          <SectionHeader title="Enrollment invites" count={2} hint="Live pairing codes this gateway will still honor." />
          {[
            ['XKCD-9214', 'single-use · expires in 9 m'],
            ['ORBIT-7731', 'reusable · no expiry'],
          ].map(([code, meta]) => (
            <div key={code} style={{ display: 'flex', justifyContent: 'space-between', gap: 12, padding: '8px 0', borderTop: hairline, fontSize: 13 }}>
              <span style={{ fontFamily: 'var(--font-mono, ui-monospace, monospace)', color: 'var(--text)' }}>{code}</span>
              <span style={{ color: 'var(--text-3)' }}>{meta}</span>
            </div>
          ))}
        </div>
      </Card>
    </div>
  )
}
