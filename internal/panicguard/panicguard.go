// Package panicguard is the panic net for per-connection goroutines: it turns a
// panic while serving one connection into that one connection closing, rather
// than into the process exiting.
//
// Which goroutines get it is the whole design, and the split is not a matter of
// taste. A per-connection goroutine owns isolated state — one player's two legs,
// its own conntrack entry, its own defers — so the blast radius of continuing is
// that connection. The blast radius of exiting is every tunnel on the box: the
// gateway goes to real lengths to keep agents independent, down to waiting out
// each evicted agent's accept loops before anything else proceeds
// (gateway/actor.go evict), and a nil dereference on one player's goroutine
// discards all of it.
//
// Lifecycle goroutines must NOT be guarded, because for them the usual "process
// state may be corrupt" objection is simply true. The gateway's actor
// (gateway/actor.go run) panicking mid-mutation leaves the listener maps
// half-written AND the caller waiting forever on a done channel that will never
// close. A udp port's read loop (gateway/udp.go readLoop) is its socket's only
// reader, so a recovered one leaves that port permanently unread. The control
// reader, the ping/rtt loops, the health checker and the accept loops are each
// the sole owner of something the whole process depends on. Crashing is the
// correct outcome there, and debug.SetCrashOutput (main.go installCrashLog)
// makes it legible afterwards.
package panicguard

import (
	"io"
	"log/slog"
	"runtime"
)

// stackBytes caps the captured stack. Deep enough for the frames that name the
// bug — the panic site up through the guarded entry function — and bounded by
// construction rather than grown to fit, because this string is handed to the
// GUI's log ring, which holds every attr it is given for 2000 entries.
const stackBytes = 4 << 10

// Conn recovers a panic on the goroutine serving one connection, logs it with
// that connection's identifiers, and closes c.
//
// Defer it FIRST in the goroutine's entry function so it is the outermost
// defer: every defer under it — the conn gate, the conntrack entry, the data
// leg, the local dial — has then already unwound by the time it runs, exactly as
// on the normal return path. attrs are slog key-value pairs naming the
// connection; they are logged ahead of the stack, which is long.
func Conn(logger *slog.Logger, c io.Closer, attrs ...any) {
	r := recover()
	if r == nil {
		return
	}
	var buf [stackBytes]byte
	n := runtime.Stack(buf[:], false)

	args := make([]any, 0, len(attrs)+4)
	args = append(args, attrs...)
	args = append(args, "panic", r, "stack", string(buf[:n]))
	logger.Error("recovered a panic serving one connection; closing that connection only", args...)

	// The entry function's own defer has usually closed c during the unwind
	// already; closing twice is harmless, and closing here is what makes this
	// helper's promise true on its own rather than by convention. A leg that
	// never existed has nothing to close — and the guard must never become the
	// second panic, the one nothing catches.
	if c != nil {
		_ = c.Close()
	}
}
