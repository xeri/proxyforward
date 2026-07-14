# ProxyForward Visual Charter

> **"If Apple designed a network appliance for developers — the quiet confidence
> of lab equipment, with just enough motion to make the network feel alive."**

ProxyForward is not a SaaS dashboard. It is a precision instrument for
operating a live network. Every design decision is held to these rules:

1. **Every page has one identity surface.** Everything else recedes around it.
   - Overview — *Connection*: the pipeline is the sculpture (Signal Glass).
   - Traffic — *Motion*: the bandwidth graph itself, bare. The graph is the artwork.
   - Analytics — *Time & place*: the world map, huge and bare.
   - Players — *People*: the wall of faces.
   - Settings — *Precision*: no identity surface. IDE-quiet.
   - Activity — *Terminal*: the log well, near-zero decoration.

2. **Glass is a reward, not a default.** The signature material — **Signal
   Glass** (`.pf-signal`: soft optical distortion, directional reflection,
   chromatic edge, internal glow, pointer-wake caustics) — appears only on
   surfaces that represent live network activity. Standard cards
   (`.pf-card`) are quiet, near-solid panels: one subtle border, one soft
   shadow, nothing else.

3. **Motion communicates network state, never decoration.** Conduits flow
   when packets flow and stop when the link is down. The pipeline ignites
   when the agent connects. A country pulses when a player joins. Idle UI is
   still UI. Everything gates on `prefersReduced()`.

4. **Color represents signal, not branding.** The role aurora, the Emblem,
   and the role hues are the product's identity — keep them, concentrated.
   Accent appears only on: the aurora backdrop, the sidebar identity, active
   navigation, primary buttons, live status, and the identity surface.
   Status hues (`--good/--warn/--bad`) and chart series colors are data, not
   decoration. Everything else is neutral.

5. **Whitespace is an active design element.** 48px of silence between major
   page groups, 24px within a group, 16px grid gaps. Metadata lives as
   typography on whitespace, not in boxes. Vary the primitives — hero,
   metric row, divider, chart, list, code block — never card-card-card.

6. **Type contrast over type size.** 26px page titles, 36px for the one hero
   figure per page, 26px standard metrics, 13.5px body, 11px uppercase
   tracked labels. The jump between levels is what creates hierarchy.

7. **Users should remember one composition from every page.** One deliberate
   grid break (the Overview pipeline runs full-bleed), one moment of light,
   one place the eye lands first.
