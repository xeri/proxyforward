/** Pairing-code shape, for display and for typing.
 *
 * A v2 code is `pxf://host:port/v2/pair/` plus 30 Crockford base32 characters in
 * six groups of five:
 *
 *     pxf://mc.example.com:8474/v2/pair/k3m7q-x2vn8-p4rt6-9wzhd-2xkb4-m7q3n
 *
 * This module knows the *shape* only — how to split one for the plaque, and how to
 * tidy one as it is typed. Whether a code is real is the engine's answer
 * (`CheckPairingCode`), deliberately: a checksum reimplemented here would be a
 * second implementation of the format, and the first time the two disagreed this
 * box would confidently reject a code the engine accepts.
 *
 * v1 codes (a free-form token and a `#sha256:` fragment) still pair, and still
 * come out of older gateways, so everything here degrades to "show it whole"
 * rather than failing on one.
 */

export const GROUP_LEN = 5
export const GROUP_COUNT = 6
export const PAYLOAD_LEN = GROUP_LEN * GROUP_COUNT

/** The alphabet the payload is written in — Crockford base32, which has no i, l,
 * o or u, so nothing a human transcribes is ambiguous (`link/cred.go`). */
const ALPHABET = '0123456789abcdefghjkmnpqrstvwxyz'

const V2 = /^pxf:\/\/(\[[^\]]+\]|[^/:]+):(\d+)\/v2\/pair\/([0-9a-zA-Z\- ]+)$/

/** A code split for display: the gateway it names, and its payload groups. */
export type SplitCode = {
  host: string
  port: number
  /** Six groups of five for a v2 code; empty for a v1 one, which has no groups. */
  groups: string[]
  /** The whole code, exactly as it must be copied. */
  text: string
}

/** split parses a code far enough to lay it out. It does not validate: a code
 * that splits may still be rejected by the engine, and a code that does not split
 * is shown whole rather than hidden. */
export function split(code: string): SplitCode | null {
  const m = V2.exec(code.trim())
  if (!m) return null
  const payload = normalize(m[3])
  if (payload.length !== PAYLOAD_LEN) return null
  return {
    host: m[1].replace(/^\[|\]$/g, ''),
    port: parseInt(m[2], 10),
    groups: groups(payload),
    text: code.trim(),
  }
}

/** normalize folds the ways a code comes back from a human onto the one the
 * gateway emitted: any case, any grouping, and Crockford's confusable letters
 * (i and l are 1, o is 0). Lossless — the alphabet has none of those letters to
 * overwrite. Mirrors `link/pairing.go normalizeV2Payload`. */
export function normalize(payload: string): string {
  return payload
    .toLowerCase()
    .replace(/[\s\-_]/g, '')
    .replace(/[il]/g, '1')
    .replace(/o/g, '0')
}

/** groups cuts a normalized payload into its five-character groups. A partial
 * payload yields a short last group, so a half-typed code still reads in the
 * rhythm of a finished one. */
export function groups(payload: string): string[] {
  const out: string[] = []
  for (let i = 0; i < payload.length; i += GROUP_LEN) out.push(payload.slice(i, i + GROUP_LEN))
  return out
}

/** retype is what goes back into the input on every keystroke: the code tidied
 * into its canonical spelling, with the payload folded, re-hyphenated, and clipped
 * at its full length so a doubled paste cannot hide its own tail.
 *
 * Anything that is not a v2 code — a v1 one, a half-typed URL, the wrong clipboard
 * entirely — is returned untouched. Rewriting text we cannot parse is how a paste
 * box eats the one character that would have explained the problem. */
export function retype(input: string): string {
  const s = input.trim()
  const m = /^(pxf:\/\/(?:\[[^\]]+\]|[^/:]+):\d+\/v2\/pair\/)([0-9a-zA-Z\- ]*)$/.exec(s)
  if (!m) return input
  const payload = normalize(m[2]).slice(0, PAYLOAD_LEN)
  return m[1] + groups(payload).join('-')
}

/** isPayloadChar reports whether a character belongs in a payload once folded —
 * used to mark the one that does not, rather than silently dropping it. */
export function isPayloadChar(c: string): boolean {
  return ALPHABET.includes(normalize(c))
}

/** mask renders a code's groups as bullets of the same widths, so revealing one
 * never moves the layout. The mask is real text: a select-all drag off a masked
 * code hands over bullets, not a credential. */
export function mask(groups: string[]): string[] {
  return groups.map(g => '•'.repeat(g.length))
}
