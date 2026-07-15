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

2. **Everything is glass. The reward is the glass that answers you.**
   Standard cards (`.pf-card`) are **frost**: heavy blur, low transmission, a
   milled rim. Controls (`.pf-control`) are thin films. They refract what passes
   behind them — and that is all they do. The signature material, **Signal
   Glass** (`.pf-signal`), is the only surface that *reacts*: it is clear where
   cards are frosted, its rim and surface follow the pointer, caustics drift
   across it while someone is there, a reflection streak crosses it, and it
   ignites when the agent connects. One per screen, only on surfaces that
   represent live network activity.
   So the hierarchy is no longer *glass vs. not-glass* — it is **behavior**.
   Never give a card the caustics, the streak, the arc, or the pointer-wake:
   the moment a second surface answers the pointer, the page has two identity
   surfaces and rule 1 is broken.

3. **Motion communicates network state, never decoration.** Conduits flow
   when packets flow and stop when the link is down. The pipeline ignites
   when the agent connects. A country pulses when a player joins. Idle UI is
   still UI. Everything gates on `prefersReduced()`.
   The sole exception is **tactility** — motion that answers the user's own
   hand: the press, the hover lift, the rubber band at a scroller's end
   (`frontend/src/rubberband.ts`). It exists to make the instrument feel
   physical, so it must always be a *reply* to input and must never play on
   its own.

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

6. **Type contrast over type size.** The face is Inter (variable, self-hosted).
   26px page titles, 36px for the one hero figure per page, 26px standard
   metrics, 13.5px body, 11px uppercase tracked labels — design-width sizes;
   `--ui-scale` (tokens.css) steps the whole scale with the viewport. The jump
   between levels is what creates hierarchy.

7. **Users should remember one composition from every page.** One deliberate
   grid break (the Overview pipeline runs full-bleed), one moment of light,
   one place the eye lands first.
