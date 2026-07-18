import { Codebox, CopyButton, IconButton, IconEye } from 'frontend'

const CODE =
  'pxf://play.example.com:8474/v1/pair/tkt_9f3ce8a1b7124d06a5c2#sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08'

/** A freshly minted pairing code in its well, breaking across lines, with the
 * copy affordance riding beside it (Agents.tsx IssuePairingModal). */
export function PairingCode() {
  return (
    <div style={{ maxWidth: 560 }}>
      <Codebox text={CODE} action={<CopyButton text={CODE} />} />
    </div>
  )
}

/** Masked (selectable={false}): the bullets are real text, so selection is
 * off — a drag must never hand out a string of dots that looks like the code.
 * Copy still copies the real value; the eye reveals (Overview.tsx
 * PairingCodeRow). */
export function Masked() {
  return (
    <div style={{ maxWidth: 560 }}>
      <Codebox
        text={'•'.repeat(28)}
        selectable={false}
        action={
          <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
            <IconButton title="Reveal pairing code" onClick={() => {}}>
              <IconEye size={16} />
            </IconButton>
            <CopyButton text={CODE} />
          </div>
        }
      />
    </div>
  )
}

/** The short form: the public address players paste into Minecraft
 * (Overview.tsx). */
export function PublicAddress() {
  return (
    <div style={{ maxWidth: 420 }}>
      <Codebox text="play.example.net:25565" action={<CopyButton text="play.example.net:25565" label="Copy" />} />
    </div>
  )
}
