import {IconBroadcast, IconChip} from './icons'

/**
 * Emblem: the mode identity mark — a milled glass tile carrying the role
 * motif (agent: chip, the machine room; gateway: beacon, the public front).
 * Both roles share one box, so a role swap never shifts layout.
 *
 * Colors ride var(--accent) by default, so the registered-property role-swap
 * wash animates the mark for free. `fixed` pins the fixed role swatch
 * (--role-agent / --role-gateway) for surfaces that show a role that is NOT
 * the active one — wizard role cards, the peer side of the identity strip.
 */
export function Emblem({role, size = 32, glow = false, fixed = false}: {
  role: 'agent' | 'gateway'
  size?: number
  glow?: boolean
  fixed?: boolean
}) {
  const c = fixed ? `var(--role-${role})` : 'var(--accent)'
  const Icon = role === 'agent' ? IconChip : IconBroadcast
  return (
    <span
      aria-hidden
      className="grid shrink-0 place-items-center transition-[color,background,border-color,box-shadow] duration-500"
      style={{
        width: size,
        height: size,
        borderRadius: Math.max(4, Math.round(size * 0.28)),
        color: c,
        border: `1px solid color-mix(in srgb, ${c} 45%, var(--border))`,
        background: `linear-gradient(160deg, color-mix(in srgb, ${c} 26%, transparent), color-mix(in srgb, ${c} 8%, transparent))`,
        boxShadow: glow
          ? `inset 0 1px 0 var(--bevel-top), 0 0 ${Math.round(size * 0.8)}px ${-Math.round(size * 0.25)}px color-mix(in srgb, ${c} 70%, transparent)`
          : 'inset 0 1px 0 var(--bevel-top)',
      }}
    >
      <Icon size={Math.round(size * 0.52)} />
    </span>
  )
}
