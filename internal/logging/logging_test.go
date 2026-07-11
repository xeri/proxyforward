package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRingSequenceAndWraparound(t *testing.T) {
	r := NewRing(4)
	for i := 1; i <= 6; i++ {
		r.append(Entry{Msg: fmt.Sprintf("m%d", i)})
	}
	got := r.EntriesSince(0)
	if len(got) != 4 {
		t.Fatalf("expected capacity-limited 4 entries, got %d", len(got))
	}
	for i, e := range got {
		want := fmt.Sprintf("m%d", i+3) // m3..m6 survive
		if e.Msg != want {
			t.Errorf("entry %d: got %q want %q", i, e.Msg, want)
		}
		if e.Seq != uint64(i+3) {
			t.Errorf("entry %d: seq %d want %d", i, e.Seq, i+3)
		}
	}
	// Incremental read picks up only what's new.
	last := got[len(got)-1].Seq
	r.append(Entry{Msg: "m7"})
	inc := r.EntriesSince(last)
	if len(inc) != 1 || inc[0].Msg != "m7" {
		t.Fatalf("incremental read wrong: %+v", inc)
	}
}

func TestLoggerFanout(t *testing.T) {
	dir := t.TempDir()
	ring := NewRing(16)
	logPath := filepath.Join(dir, "test.log")
	logger, closeFn, err := New(Options{Level: "debug", FilePath: logPath, Ring: ring})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("hello", "player", "steve")
	logger.Debug("fine detail")
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}

	entries := ring.EntriesSince(0)
	if len(entries) != 2 {
		t.Fatalf("ring: got %d entries, want 2", len(entries))
	}
	if entries[0].Msg != "hello" || !strings.Contains(entries[0].Attrs, "player=steve") {
		t.Errorf("ring entry wrong: %+v", entries[0])
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello") || !strings.Contains(string(data), "player=steve") {
		t.Errorf("file sink missing content: %s", data)
	}
}

func TestLevelFiltering(t *testing.T) {
	ring := NewRing(16)
	logger, _, err := New(Options{Level: "warn", Ring: ring})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("quiet")
	logger.Warn("loud")
	entries := ring.EntriesSince(0)
	if len(entries) != 1 || entries[0].Msg != "loud" {
		t.Fatalf("expected only the warn entry, got %+v", entries)
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.log")
	w, err := newRotatingWriter(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 40) + "\n"
	for i := 0; i < 8; i++ { // 328 bytes total -> should rotate
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("expected rotated file .1: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 100 {
		t.Errorf("live file exceeds max: %d bytes", info.Size())
	}
}
