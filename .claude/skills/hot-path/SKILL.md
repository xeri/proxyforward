---
name: hot-path
description: Playbook for touching proxyforward's data hot path — internal/relay, internal/transport, stats/counting.go, or mcsniff. Use BEFORE editing any per-connection or per-copy-iteration code; it is burst-gated.
---

# Touch the hot relay path (playbook B)

1. Record a baseline first: `go test -run TestBurst -v ./internal/e2e/` ×3, note
   MiB/s + worst RTT (floor values: `docs/agent/architecture.md` "The numbers").
2. Rules while editing:
   - No allocation or lock per Read/Write; atomics only.
   - Preserve half-close order (EOF → `CloseWrite`, both directions drain) and the
     2-min progress deadline (`relay.go Splice`).
   - No logging below connection granularity.
   - Nothing outside `internal/transport` may import yamux.
3. Re-run the burst ×3. A >10 % throughput drop or worst-RTT near the 500 ms bound
   is a regression; revert or justify in the commit message.
4. goleak (`e2e_test.go TestMain`) must stay green.
