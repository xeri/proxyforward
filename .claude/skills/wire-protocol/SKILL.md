---
name: wire-protocol
description: Playbook for modifying proxyforward's wire protocol (internal/control) — capabilities, new fields, message types. Use before ANY change to frames, hello exchange, or agent↔gateway messages. Protocol changes are an escalation trigger: confirm with the human first.
---

# Modify the wire protocol (playbook C)

Stop and confirm with the human before starting — any `ProtocolVersion` bump,
capability semantic change, or wire-field meaning change is an escalation trigger
(`docs/agent/reasoning.md` §5).

1. Features = a new capability: const in `control.go`, append to
   `SupportedCapabilities`, gate **all** behavior on `session.Has(cap)` **both
   sides**. `ProtocolVersion` bumps only for hello-breaking changes.
2. New fields: `omitempty`, zero value must mean "legacy peer"; extend
   `hello_compat_test.go` to prove legacy frames stay byte-identical.
3. Keep unknown-type tolerance (default arms ignore, never error); keep frames ≤
   `MaxFrame` — chunk like `MaxConnStatsPerFrame` does; never raise the cap.
4. Test both mixed-version directions: e2e `harnessOpts.offerCaps: []string{}`
   simulates a legacy agent (`TestLegacyRegisterFallback`).
5. Implement fully before advertising the capability — `CapTunnelUDP` is the
   standing counterexample (advertised, unimplemented, live protocol-bug risk).
6. Protocol and implementation never change in the same commit.
7. Gate: control + e2e suites; a mixed-version manual run if the change is risky.
