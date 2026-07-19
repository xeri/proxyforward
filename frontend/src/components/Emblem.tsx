import {IconBroadcast, IconChip} from './icons'

/**
 * Emblem: the mode identity mark — a living organism in the role's hue
 * carrying the role motif (agent: chip, the machine room; gateway: beacon,
 * the public front). It wobbles and drifts on its own clock (motion.css
 * .pf-organism), keyed off its size so no two marks move alike; under
 * reduced motion the kill switch freezes it mid-wobble, which is exactly
 * what a still organism looks like. Both roles share one box, so a role
 * swap never shifts layout.
 *
 * Colors ride var(--accent) by default, so the registered-property role-swap
 * wash re-tints the body for free. `fixed` pins the fixed role swatch
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
  // Unique-ish clocks per scale: 5.5–8.2s wobble, 9–12.2s drift.
  const wobble = 5.5 + (size % 4) * 0.9
  const drift = 9 + (size % 5) * 0.8
  return (
    <span
      aria-hidden
      className="pf-organism grid shrink-0 place-items-center transition-[color,box-shadow] duration-500"
      style={{
        width: size,
        height: size,
        ['--organism-c' as string]: c,
        // Every role hue is bright over the abyss and deep over the shallows,
        // so the glyph reads in the same ink as filled controls.
        color: 'var(--accent-contrast)',
        animationDuration: `${wobble}s, ${drift}s`,
        boxShadow: glow
          ? `0 0 ${Math.round(size * 0.9)}px ${-Math.round(size * 0.2)}px color-mix(in srgb, ${c} 65%, transparent)`
          : undefined,
      }}
    >
      <Icon size={Math.round(size * 0.5)} />
    </span>
  )
}
