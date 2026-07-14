---
paths:
  - "**/*_test.go"
---

# Test conventions (moved from CLAUDE.md "Conventions in force" + Footguns)

- Table-driven; requirement IDs in comments ("(3.4)", "(A14)", "(D7)").
- Any test that starts a real Engine / `ipc.Serve` must set a process-private
  `ipc.PipeName` in `init()` (pattern: `app/setup_test.go`) — otherwise parallel
  test binaries and a live daemon deadlock each other.
- e2e is goleak-verified (`e2e_test.go TestMain`); it must stay green through any
  refactor.
- Fuzz targets for every internet-facing parser (`internal/mc`, `internal/control`).
- `waitFor`-style polling with deadlines; no bare sleeps as assertions.
- The burst perf gate is `TestBurstThroughputAndCrossStreamLatency`
  (`internal/e2e/e2e_test.go`); bounds live in `docs/agent/architecture.md`
  "The numbers". Touching it means you're touching the hot-path contract — open the
  `hot-path` skill first.
- Doc citations are tested: `internal/doccheck` asserts every file/symbol/test cited
  by CLAUDE.md, `docs/agent/`, `.claude/rules/`, and `.claude/skills/` exists. If a
  rename breaks it, fix the doc in the same change.
