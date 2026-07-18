import { CopyButton, Codebox } from 'frontend'

const code = 'pxf://play.hexvale.net:8474/v1/pair/K7QF-2MRT#sha256:9f31c2ab'

/** Both sizes at rest. Clicking flips to a checkmark + "Copied" for 1.5 s. */
export function Sizes() {
  return (
    <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
      <CopyButton text={code} />
      <CopyButton text={code} size="sm" label="Copy code" />
    </div>
  )
}

/** The canonical composition — riding a Codebox as its action, per the wizard
 * and the invite roster. */
export function InCodebox() {
  return (
    <div style={{ maxWidth: 560 }}>
      <Codebox text={code} action={<CopyButton text={code} />} />
    </div>
  )
}
