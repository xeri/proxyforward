package panicguard

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// nilPtr is dereferenced by one of the panic cases below. A package-level
// variable rather than a local one so the compiler cannot fold the fault away.
var nilPtr *int

// syncBuf is a writer the guard's logger and the test goroutine may both touch:
// the panic is logged on the server goroutine and read here.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// echoServer runs one goroutine per accepted connection, shaped exactly like the
// guarded handlers: the guard is the FIRST defer (so it unwinds last), the
// connection's own Close is the second, and the body is the connection's work —
// echo a byte, except 'p', which fires the panic under test.
func echoServer(t *testing.T, logger *slog.Logger, fire func()) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	serve := func(c net.Conn) {
		defer Conn(logger, c, "client", c.RemoteAddr().String())
		defer c.Close()
		b := make([]byte, 1)
		for {
			if _, err := c.Read(b); err != nil {
				return
			}
			if b[0] == 'p' {
				fire()
			}
			if _, err := c.Write(b); err != nil {
				return
			}
		}
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(c)
		}
	}()
	return ln
}

// echo writes one byte and reads the answer back, failing the test if the
// connection does not round-trip it.
func echo(t *testing.T, c net.Conn, b byte, desc string) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte{b}); err != nil {
		t.Fatalf("%s: write: %v", desc, err)
	}
	got := make([]byte, 1)
	if _, err := c.Read(got); err != nil {
		t.Fatalf("%s: read: %v", desc, err)
	}
	if got[0] != b {
		t.Fatalf("%s: echoed %q, want %q", desc, got[0], b)
	}
}

// A panic while serving one connection must cost exactly that connection: the
// connections beside it keep serving, the listener keeps accepting, and the
// process stays up. That is the trade the guard exists to make — per-connection
// state is isolated, so continuing costs one player, while exiting costs every
// tunnel on the box.
func TestConnContainsAPanicToOneConnection(t *testing.T) {
	cases := []struct {
		name string
		fire func()
	}{
		{"explicit panic", func() { panic("handler exploded") }},
		{"nil pointer dereference", func() { _ = *nilPtr }},
		{"nil map write", func() { var m map[string]int; m["x"] = 1 }},
		{"index out of range", func() { s := make([]int, 1); i := len(s) + 1; _ = s[i] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := &syncBuf{}
			logger := slog.New(slog.NewTextHandler(logs, nil))
			ln := echoServer(t, logger, tc.fire)

			dial := func() net.Conn {
				t.Helper()
				c, err := net.Dial("tcp", ln.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
				return c
			}

			bystander := dial()
			echo(t, bystander, 'a', "bystander before the panic")

			victim := dial()
			_ = victim.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := victim.Write([]byte{'p'}); err != nil {
				t.Fatalf("victim: write: %v", err)
			}
			// The guard closes this connection, so the victim's read ends rather
			// than hanging on a server goroutine that quietly went away.
			if _, err := victim.Read(make([]byte, 1)); err == nil {
				t.Fatal("victim: read succeeded; a panicking handler must close its connection")
			}

			// The other two promises: an established neighbour is undisturbed, and
			// the listener still accepts.
			echo(t, bystander, 'b', "bystander after the panic")
			echo(t, dial(), 'c', "connection accepted after the panic")

			// The panic has to be legible afterwards: the connection's identifiers
			// and a stack, on one error record. Polled because the guard runs after
			// the close the victim already observed.
			deadline := time.Now().Add(5 * time.Second)
			for {
				out := logs.String()
				if strings.Contains(out, "stack=") {
					if n := strings.Count(out, "level=ERROR"); n != 1 {
						t.Fatalf("logged %d error records, want exactly 1:\n%s", n, out)
					}
					if !strings.Contains(out, "client=") || !strings.Contains(out, "panic=") {
						t.Fatalf("panic log names neither the client nor the panic value:\n%s", out)
					}
					return
				}
				if !time.Now().Before(deadline) {
					t.Fatalf("guard never logged the panic:\n%s", out)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

// The guard is deferred unconditionally on paths that mostly do not panic, so
// the ordinary case — no panic — must be a no-op: it must not close the
// connection it was handed.
func TestConnLeavesAHealthyConnectionAlone(t *testing.T) {
	logs := &syncBuf{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	ln := echoServer(t, logger, func() { t.Error("panic case fired on the healthy path") })

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	echo(t, c, 'a', "healthy connection")
	echo(t, c, 'b', "healthy connection, second byte")
	if logs.String() != "" {
		t.Fatalf("guard logged on a connection that never panicked:\n%s", logs.String())
	}
}
