# Imported design documents

Pulled from the claude.ai/design project **"Abyssal Bloom Design System"**
(`904776f9-751d-42cd-a0e7-f9591bcf28fb`) on 2026-07-20 via DesignSync `get_file`.
This is NOT the push target pinned in `.design-sync/config.json` (that is
`15c99c85-29eb-46c5-bc9c-2f100e3950e8`, the component-library project); it is a
separate project holding the original theme-brainstorm uploads.

`Radical UI Theme Brainstorm/` mirrors the remote `uploads/Radical UI Theme
Brainstorm/` folder:

- `Theme Brainstorm.dc.html` — the multi-theme brainstorm canvas Abyssal Bloom
  was chosen from.
- `Abyssal Bloom.dc.html` — first full-screen mockup of the theme.
- `Abyssal Bloom Elements.dc.html` / `Abyssal Bloom Elements v2.dc.html` — the
  element/component sheets (v2 is the current reference).
- `support.js` — the `.dc.html` runtime (generated; the HTML files load it via
  a relative `<script src="./support.js">`, so keep them side by side). Open a
  `.dc.html` directly in a browser to view it.

These are upstream design references, not app code: the shipped charter derived
from them is `frontend/DESIGN.md`, which stays the source of truth for the app.
