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
        // Catch-light is a padding-box band, not an `inset 0 1px 0`: an offset
        // inset shadow rounds the corner as a crescent that specks where it
        // ends (glass.css, the rim primitive).
        // backgroundImage, never the `background` shorthand: React warns when a
        // style object updates a shorthand alongside a longhand it subsumes
        // (backgroundClip), and this mark re-tints on every role swap.
        backgroundImage: `linear-gradient(180deg, var(--bevel-top) 0 1px, transparent 1px), linear-gradient(160deg, color-mix(in srgb, ${c} 26%, transparent), color-mix(in srgb, ${c} 8%, transparent))`,
        backgroundClip: 'padding-box, border-box',
        boxShadow: glow
          ? `0 0 ${Math.round(size * 0.8)}px ${-Math.round(size * 0.25)}px color-mix(in srgb, ${c} 70%, transparent)`
          : undefined,
      }}
    >
      <Icon size={Math.round(size * 0.52)} />
    </span>
  )
}
