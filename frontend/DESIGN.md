# ProxyForward Visual Charter — Abyssal Bloom

> **"A deep-sea research instrument: the abyss out the porthole, jade
> bioluminescence for life-signs, and every element an organism drifting in
> the water column."**

ProxyForward is not a SaaS dashboard. It is an instrument lowered into a live
network, read in the dark. The design language is deep-sea bioluminescence:
the page is the abyss (`#02090f`), panels are volumes of trench water you look
into, and light is never decoration — **jade is a life-signal, current blue is
flow, coral is distress**. Every design decision is held to these rules:

1. **Every page has one identity surface.** Everything else recedes around it.
   - Overview — *Connection*: the pipeline is the sculpture (the bloom frame).
   - Traffic — *Motion*: the bandwidth graph itself, bare. The graph is the artwork.
   - Analytics — *Time & place*: the world map, huge and bare.
   - Players — *People*: the wall of faces.
   - Agents (gateway) — *Fleet*: the roster of machine health cards. Quiet
     trench glass, never the bloom frame — N equal cards can't each answer the
     pointer (rule 2). The drill-in borrows Traffic's bare bandwidth hero,
     scoped to one agent.
   - Settings — *Precision*: no identity surface. IDE-quiet.
   - Activity — *Sediment*: the log well, near-zero decoration. Events settle
     downward; the newest surfaces with a rise and holds the light.

2. **Everything is water. The reward is the surface that answers you.**
   Standard cards (`.pf-card`) are **quiet trench glass**: trench water at
   ~60%, a ridge rim, symmetric resting corners. Controls (`.pf-control`) are
   recessed wells of deeper water. They smear what passes behind them — and
   that is all they do. The signature material, the **bloom frame** (Signal
   Glass, `.pf-signal`), is the only surface that *reacts*: the one
   asymmetric 28/10 frame per screen, fed by an accent bloom from its corner,
   standing in its own glow; its rim and surface follow the pointer, caustics
   drift across it while someone is there, and it ignites when the agent
   connects. One per screen, only on surfaces that represent live network
   activity.
   So the hierarchy is not *glass vs. not-glass* — it is **behavior**. Never
   give a card the caustics, the bloom gradient, or the pointer-wake: the
   moment a second surface answers the pointer, the page has two identity
   surfaces and rule 1 is broken.
   **Asymmetry is importance** and it is rationed the same way: the 28/10
   swell belongs to the bloom frame, modals, and the command palette; buttons
   carry 14/6, inputs 10/4, chips 8/3; a quiet card stays symmetric. Nothing
   important is symmetric, and nothing symmetric begs for attention.

3. **Motion is water physics, and it communicates network state — never
   decoration.** Two curves carry everything: the *current*
   (`--ease-out`) for arrivals, hovers, and glow; *propulsion*
   (`--ease-spring`) for presses and toggles. Births bloom, deaths sink,
   nothing moves linearly. Conduit dashes swim when packets flow and stop
   when the link is down. The pipeline ignites when the agent connects. A
   country pulses when a player joins. Organisms (the Emblem, the plankton)
   wobble and drift on individual clocks — alive things move, dormant things
   freeze mid-wobble. Idle UI is still UI. Everything gates on
   `prefersReduced()`: under reduced motion the sea goes completely still —
   plankton included, which mounts nothing at all.
   The sole exception is **tactility** — motion that answers the user's own
   hand: the press squish, the hover lift, the rubber band at a scroller's
   end (`frontend/src/rubberband.ts`). It exists to make the instrument feel
   physical, so it must always be a *reply* to input and must never play on
   its own.

4. **Color is a life-signal, not branding.** Jade (`#2fffc0`) is life: the
   agent role, good, live, the primary flow. Current blue (`#1c8bff`) is the
   open water: the gateway role, the return flow. Coral (`--bad`) is distress
   only — never decoration. The glow fields, the Emblem organism, and the
   role hues are the product's identity — keep them concentrated. Accent
   appears only on: the backdrop glow, the sidebar identity, active
   navigation (the lit lure), primary buttons, live status, and the bloom
   frame. Status hues (`--good/--warn/--bad`) and chart series colors are
   data. Everything else is silt and foam. Only living things glow, and glow
   radius tracks health; a warning's urgency is a tempo (distress breathes
   fast, information breathes slow).

5. **Whitespace is an active design element.** 48px of dark water between
   major page groups, 24px within a group, 16px grid gaps. Metadata lives as
   typography on whitespace, not in boxes. Vary the primitives — hero,
   metric row, divider, chart, list, code block — never card-card-card.
   Tables are hairline rows, never zebra.

6. **Two voices, strictly cast.** The working voice is **Spline Sans Mono**
   (variable, self-hosted): body, data, labels, chrome — lowercase by
   default, tabular numerals. The display voice is **Cormorant Unicase**
   (500/700, self-hosted): page titles, section headings, and headline
   metrics — and nothing else; never body copy. Buttons are the one place
   type raises its voice: uppercase, 0.14em tracking, bold (the `.pf-btn`
   recipe carries it). 30px page titles, 34px for the one hero figure per
   page, 26px standard metrics, 12.5px body, 10.5px uppercase tracked labels
   — design-width sizes; `--ui-scale` (tokens.css) steps the whole scale
   with the viewport. The jump between levels — and between the two faces —
   is what creates hierarchy.

7. **Users should remember one composition from every page.** One deliberate
   grid break (the Overview pipeline runs full-bleed), one bloom of light,
   one place the eye lands first. The atmosphere holds the rest: depth
   gradient, dot-grid seafloor, drifting plankton, scanlines over the top —
   all pointer-transparent, all deferential to the content.
