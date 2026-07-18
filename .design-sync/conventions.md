# Building with proxyforward's kit — conventions

## Root wrapper (required)

Wrap every tree in `DSRoot` (a normal export, next to the components). It hosts
the `#pf-refract` SVG filter the popover glass refracts through, stamps the
theme/motion attributes, and clears the host page's opaque body background so
the app's dark canvas gradient (painted on `html` by the shipped CSS) shows.
Without it the page stays white and the near-white text is unreadable.
`<DSRoot>` is the dark instrument look (default); `<DSRoot theme="light">`
switches the whole token set.

## Styling idiom — tokens + `pf-*` material recipes; never invent utility classes

This is NOT a Tailwind-preset system: the shipped stylesheet contains only the
utility classes the app itself happens to use, so a class string you invent may
silently not exist. For your own layout glue use **inline styles with the CSS
custom-property tokens**; reach for components before raw markup.

Tokens (all defined in `_ds_bundle.css`):
- Surfaces: `--bg`, `--bg-base`, `--panel-2`, `--input-bg`, `--border`, `--hairline`
- Text tiers: `--text` (primary), `--text-2` (secondary), `--text-3` (muted)
- Signal: `--accent`, `--good`, `--warn`, `--bad`; roles `--role-agent` (cyan), `--role-gateway` (violet)
- Radii: `--r-xs`, `--r-sm`, `--r-md`, `--r-xl`
- Type: `--fs-hero`, `--fs-title`, `--fs-metric`, `--fs-caption`, `--tracking-label`

Material recipes (material only — positioning stays yours): `pf-card` (quiet
frost card), `pf-signal` + one child `<span class="pf-caustic"/>` (Signal
Glass), `pf-control` (thin-film control), `pf-menu` (opaque popup glass),
`pf-well` (recessed mono well), `pf-sep` (hairline divider), `pf-halo`
(breathing live dot), `pf-skeleton` (shimmer), `pf-press` (press tactility),
`pf-bleed` (ambient alert glow). Prefer `Card`, `SignalCard`, `Codebox`,
`Skeleton` over raw recipes.

## The rules that make it look like proxyforward

- **One identity surface per screen**: exactly one `SignalCard` (it answers the
  pointer); every other panel is a quiet `Card`. Never two.
- **Color is signal, not decoration**: status is a tone prop plus a label
  (`StatusDot state="good" label="Linked"`), never color alone; accent only on
  the primary action and live status.
- **Numerals are tabular**; ports/addresses/counts ride in `MonoChip`; an
  unknown value renders "—", never a fake 0.
- **Whitespace groups things** — metadata is typography on whitespace, not more
  boxes. Vary primitives (hero figure, metric row, divider, table), never
  card-card-card.

Before composing a full screen, read `guidelines/DESIGN.md` (the app's actual
design charter). Per-component usage and props: `components/<group>/<Name>/<Name>.prompt.md`
and `<Name>.d.ts`.

## Idiomatic composition

```jsx
const { DSRoot, SignalCard, StatTile, StatusDot, Button, NumberTicker } = window.Proxyforward;

<DSRoot>
  <div style={{ maxWidth: 720, margin: '0 auto', padding: 24 }}>
    <SignalCard title="Connection">
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 16 }}>
        <StatTile label="Players" value={<NumberTicker value={12} />} accent />
        <StatTile label="RTT" value="23 ms" sub="p95 41 ms" />
        <StatTile label="Uptime" value="4h 12m" />
      </div>
      <div style={{ marginTop: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <StatusDot state="good" label="Linked to gateway" pulse />
        <Button variant="ghost" size="sm">Details</Button>
      </div>
    </SignalCard>
  </div>
</DSRoot>
```
