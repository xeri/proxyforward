---
name: overhaul
description: Protocol for wholesale replacement of a proxyforward subsystem (transport, storage, GUI). Use when a change replaces an implementation rather than editing it.
---

# Overhaul protocol

An overhaul may replace any implementation wholesale — transport, storage, even the
GUI — but:

1. The root CLAUDE.md **Invariants** section is the contract: anything it names must
   be re-embodied and its symbol references updated (`internal/doccheck` will fail
   until they are).
2. Wire changes still follow the `wire-protocol` skill against *deployed* peers.
3. Storage changes append to `analytics.migrations` (`schema.go` — append-only,
   `user_version` ladder) or import-and-rename like `ImportLegacyStats`. Never edit
   an applied migration step.
4. Characterize first: the e2e suite must pass unmodified before and after, or the
   change to it is part of the review.
5. Root CLAUDE.md is updated **in the same change** — run the razor over every
   edited instruction: *"which file enforces this tomorrow?"* No file → cut it or
   build the enforcement.
