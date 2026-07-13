// Package tcpinfo reads the kernel's smoothed round-trip estimate for a live
// TCP connection — the same number ss(8) and netstat surface — without sending
// any probe traffic. The gateway samples it on each public player connection
// to attribute a real network RTT to that player, distinct from the
// agent↔gateway control-link RTT.
//
// RTT is best-effort: it returns ok=false on platforms without a supported
// syscall, on connections the kernel has no sample for yet, and on any error.
// Callers treat a miss as "unknown" and move on — latency is enrichment, never
// a dependency.
package tcpinfo
