# ProxyForward Visual Charter — Abyssal Bloom, direction 3a

> **"The Sounding Line, Trued."** A deep-sea instrument read in the dark: strict
> hairline structure, one metric ladder, and exactly one living, glowing
> organism per screen.

Locked direction: **3a**, from `Layout Brainstorm.dc.html#3a`, specified in
`Abyssal Bloom Spec.dc.html` (v2, the Build Constitution). This file is the
binding restatement of both. Where this file and a screen disagree, this file
wins.

## What changed, and why there are two charters

The previous charter — preserved verbatim as `DESIGN-glass-legacy.md` — is also
called Abyssal Bloom, and shares its palette floor and its two display voices.
It is nonetheless a different system. It is the **glass** one: trench-water
panel fills, a seven-step blur ladder, card shadows, a light theme, and an
accent ramp that re-tints per role.

3a retires all of that. The box budget for a whole screen is now:

- the bloom,
- its code wells,
- the chrome pod.

Nothing else may be a box. No card grids, no panel backgrounds, no nested
containers. Everything that is not the bloom is **typography on 1px rules**.

Those two systems cannot both be true at once, so they live in separate files
and screens cross over one at a time:

| | glass (legacy) | 3a (this charter) |
| --- | --- | --- |
| tokens | `src/styles/tokens.css` | `src/styles/abyssal-3a.css` |
| classes | `.pf-*` | `.a3-*` |
| status | what the 9 screens still render | the target |

A screen is "3a" only once it is rebuilt on the `.a3-*` primitives **and**
passes the growth checklist at the bottom of this file. Until then it keeps the
glass look, and that is expected rather than a bug. The 3a stylesheet declares
no bare element selectors, so it cannot half-apply itself to a screen nobody has
reviewed yet.

## 1. The closed palette. Never add a hue.

Seven colors and two text tiers, each with exactly one job.

| token | value | job |
| --- | --- | --- |
| `--a3-abyss` | `#02090f` | the only page background |
| `--a3-hairline` | `#0d2b33` | structure — every rule, 1px, always |
| `--a3-border` | `#124450` | the outline of an interactive thing, 1px, always |
| `--a3-mint` | `#2fffc0` | **alive**: agents, live data, the one primary action |
| `--a3-current` | `#1c8bff` | **machine**: gateway, this device, secondary signal |
| `--a3-waiting` | `#e8b34b` | pending states, and nothing else |
| `--a3-coral` | `#ff5d73` | danger, stop, close, POOR, and nothing else |
| `--a3-text` | `#bdfbe6` | primary ink |
| `--a3-muted` | `#3e6a74` | secondary ink |

**Two text tiers only.** Anything reaching for a third is over-nested — flatten
it instead. **Status is never color alone**: every colored dot carries its word
beside it. Ambient light is capped at two radial washes per screen, painted on
the frame and never on content. At most **one solid-mint filled element per
screen** — copy buttons sitting near codes stay hairline.

## 2. Six voices, fixed jobs, no seventh

| voice | font | job |
| --- | --- | --- |
| ceremonial | Cormorant Unicase 700 | titles 44px, stat words 28px, names 18px. Never below 16px, never a number, never mid-sentence. |
| workhorse | Gruppo | labels, captions, nav, tags, 11–14px. Lowercase, always. |
| human | MuseoModerno | helper sentences only. The one font allowed a marine metaphor. |
| machine-verbatim | Red Hat Mono | codes, hosts, urls, commands, log lines, config. Lowercase, always. |
| instrument | Doto 900 | **every free-standing number** — telemetry, counts, countdowns, ports, PIDs, versions, percentages. |
| action | Genos 700 | button labels only. Genos on something unclickable is a bug. |

Only numerals locked inside verbatim mono text escape Doto. Mint 600 + glow in a
code well means **secret** — a command is the same plaque with the mint switched
off, and nothing else earns the glow.

## 3. One metric ladder. Nothing off-scale.

`4` micro · `7` dot-to-word · `12` in-band · `14` band padding · `20` block
rhythm · `24` gutter · `28` page margin (top 24, bottom 20).

**Nothing touches a border**: 14px clearance at every hairline, 24px at every
vertical rule. The content grid is `1.55fr / 1fr` with a 24px gutter, **two
columns maximum** — a would-be third becomes a band below.

A feature that "needs" 17px of padding is a feature whose type scale is wrong.
Fix the type; keep the ladder.

## 4. Shape: grown, not stamped

Asymmetric radii, mirrored on the diagonal, A B A B: frame `34/14` · bloom
`26/10` · code well `24/10` · chrome pod `0 0 18/8` · buttons `12/5` (secondary
`10/4`, paired icon buttons mirror `10/4` ↔ `4/10`) · state badge `7/3` · `999px`
pills for tiny role tags only, never a container.

No symmetric rounded rectangles except those pills. No 90° corners except
hairline rules. The dashed well inside the bloom is **retired** — 3a proved the
code well alone is enough. One box fewer, always.

## 5. The one-bloom rule

Exactly one breathing, glowing organism per screen. Its anatomy is fixed:

1. header line — label left, state word right in Doto, never floating over content
2. organisms, **top-aligned** so the tether meets both blob centers regardless of caption length
3. code well pinned to the bottom, full bloom width

Capacity is capped at header + one organism pair + one code well + one hairline
footer row. **A new feature never adds a second bloom.** If it genuinely deserves
the bloom it takes over and the previous occupant demotes to the ladder — never
both. Overflow migrates to its own station.

Living entities wobble. An absent one is a dashed muted outline at 0.55 opacity,
because "not here yet" is a real state and should look like one.

## 6. The instrument ladder

Everything that is not the bloom is typography on 1px `--a3-hairline` rules:
14px band padding, one shared baseline grid, **≤3 stats per band**, one concern
per rung (link health gets its own rung rather than crowding the hero numerals).
A 4th stat opens a new band below. **Ladders grow down, never sideways.**

## 7. Nav — the sounding line

96px rail. Doto 12px depth marks stepping 200 from `0200`. Rotated Gruppo 14px
station labels. **Hard cap 8 stations** — a 9th nests inside an existing station
as hairline tabs rather than lengthening the rail. Exactly one active, ever
(2px mint left border + wash). Settings anchors the foot.

The chrome pod is **absolutely molded to the frame**: `position:absolute; top:0;
right:26px`. Never negative margins, never inline in the strip. The surface strip
owes it ≥240px of right padding so a long title can never collide with it.

## 8. Glow and motion budgets

Glow rides ≤2 hero numerals and the code groups. Dot glows ≤8px.

**Three keyframes exist** — `lb-wobble` (7–8s, living blobs), `lb-breathe` (6s,
the bloom), `lb-pulse` (2.5–3s, status dots). A fourth never ships. Nothing
slides, scales, spins, or enters. Hover is a brightness or border shift, ≤160ms.
Under `prefers-reduced-motion` the sea goes completely still.

## 9. Data honesty

Unmeasured renders `—` in muted. A measured zero renders as a real `0 B/S` in its
signal color. **Never a fake number.** Addresses in flowing text are Gruppo 12px
muted; anything copyable is Red Hat Mono inside a code well.

## 10. Resilience — nothing breaks by growth

Every label, stat, and button is `white-space:nowrap`. The flexible child of a
row is always the text column (`min-width:0`); actions are `flex-shrink:0` with
`margin-left:auto`. Chrome never depends on a flow neighbour. Page actions center
to the title block, never to its baseline.

Assigned overflow exits: 4th stat → new band · 9th station → nested tabs · 2nd
secret → footer row, not a second well · bloom overflow → its own station.

**The squeeze test:** narrow any screen 20%. No button may wrap, no code group
may split mid-group, no text may touch a rule.

## Growth checklist — run before adding any feature

Every "no" is a redesign, not a patch.

1. Exactly one bloom, one solid-mint action, one active station?
2. Did the feature enter as hairline typography rather than as its own box?
3. Is every color one of the seven, used for its assigned meaning?
4. Every number Doto, every label lowercase Gruppo, every button Genos, every code lowercase Red Hat Mono in a well?
5. Every gap and padding off the §3 ladder, with nothing touching a border?
6. Glow within budget (≤2 hero numerals), nothing new moving?
7. Nav still ≤8 stations?
8. Squeeze test passed?
9. Every value real — `—` when unmeasured, signal-colored when a measured zero?
10. Delete-test: strip the feature's decoration. Does it still read on type and hairlines alone?
