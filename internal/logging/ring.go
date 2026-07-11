package logging

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Entry is one formatted log line held for the GUI. TimeMs is unix
// milliseconds rather than time.Time so the Wails binding generator can model
// it for the frontend.
type Entry struct {
	Seq    uint64 `json:"seq"`
	TimeMs int64  `json:"timeMs"`
	Level  string `json:"level"`
	Msg    string `json:"msg"`
	Attrs  string `json:"attrs"` // "key=value key=value", preformatted
}

// Ring is a fixed-capacity, thread-safe log buffer. Readers poll
// EntriesSince with the last sequence number they saw — that keeps the hot
// logging path free of subscriber callbacks (the GUI batches at its own
// cadence).
type Ring struct {
	mu   sync.Mutex
	buf  []Entry
	next int // insertion index
	full bool
	seq  uint64
}

func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{buf: make([]Entry, capacity)}
}

func (r *Ring) append(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e.Seq = r.seq
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// EntriesSince returns entries with Seq > since, oldest first.
func (r *Ring) EntriesSince(since uint64) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Entry
	appendIf := func(e Entry) {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	if r.full {
		for _, e := range r.buf[r.next:] {
			appendIf(e)
		}
	}
	for _, e := range r.buf[:r.next] {
		appendIf(e)
	}
	return out
}

// LastSeq returns the sequence number of the newest entry.
func (r *Ring) LastSeq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

type ringHandler struct {
	ring  *Ring
	level slog.Level
	attrs string // preformatted inherited attrs
}

func newRingHandler(ring *Ring, level slog.Level) slog.Handler {
	return &ringHandler{ring: ring, level: level}
}

func (h *ringHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *ringHandler) Handle(_ context.Context, rec slog.Record) error {
	var sb strings.Builder
	sb.WriteString(h.attrs)
	rec.Attrs(func(a slog.Attr) bool {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%s=%v", a.Key, a.Value)
		return true
	})
	h.ring.append(Entry{
		TimeMs: rec.Time.UnixMilli(),
		Level:  rec.Level.String(),
		Msg:    rec.Message,
		Attrs:  sb.String(),
	})
	return nil
}

func (h *ringHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var sb strings.Builder
	sb.WriteString(h.attrs)
	for _, a := range attrs {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%s=%v", a.Key, a.Value)
	}
	return &ringHandler{ring: h.ring, level: h.level, attrs: sb.String()}
}

func (h *ringHandler) WithGroup(name string) slog.Handler {
	// Groups are rare in this codebase; flatten by prefixing nothing.
	return h
}
