// Package analytics is the engine's SQLite-backed system of record: the
// persisted bandwidth history (on behalf of internal/stats), connection and
// player history, geo enrichment, and rollups. Exactly one process — the
// engine owner — opens the database; attached GUIs read over IPC. That
// single-process ownership is what makes WAL mode safe on Windows.
package analytics

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const (
	dbFile = "analytics.db"

	// The async writer batches ops into one transaction per batchDelay (or
	// batchMax ops, whichever first). The queue never blocks the data path:
	// under pressure the oldest queued op is dropped.
	writeQueueCap = 4096
	batchMax      = 256
	batchDelay    = 250 * time.Millisecond

	pruneEvery = 24 * time.Hour

	defaultRetentionDays = 180
)

// Options tunes the store.
type Options struct {
	// RetentionDays bounds how long session-grade history lives; <= 0 uses
	// the default (180).
	RetentionDays int
}

// op is one queued write. Ops in a batch share a transaction; done (when
// non-nil) is closed after that transaction commits — the Barrier mechanism.
type op struct {
	name string
	fn   func(tx *sql.Tx) error
	done chan struct{}
}

// DB owns the analytics database and its writer goroutine.
type DB struct {
	sql *sql.DB
	// read is a WAL read pool for queries, so dashboard reads never serialize
	// behind writer batches or the 45 s stats flush. Degrades to sql when the
	// second handle cannot be opened.
	read          *sql.DB
	path          string
	logger        *slog.Logger
	retentionDays int

	writeC    chan op
	qmu       sync.Mutex // serializes producers through Enqueue's full-queue path
	stopC     chan struct{}
	doneC     chan struct{}
	closeOnce sync.Once

	dropped atomic.Int64
}

// Open opens (or creates) <dir>/analytics.db. An unopenable database is
// renamed aside and recreated — analytics must never block engine start,
// exactly like the old stats.json .bad handling.
func Open(dir string, opts Options, logger *slog.Logger) (*DB, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.RetentionDays <= 0 {
		opts.RetentionDays = defaultRetentionDays
	}
	path := filepath.Join(dir, dbFile)
	h, err := openSQLite(path)
	if err != nil {
		logger.Warn("analytics: database unreadable — starting fresh", "path", path, "err", err)
		os.Rename(path, path+".bad")
		// Stale WAL sidecars would resurrect the bad state; drop them.
		os.Remove(path + "-wal")
		os.Remove(path + "-shm")
		if h, err = openSQLite(path); err != nil {
			return nil, fmt.Errorf("analytics: open %s: %w", path, err)
		}
	}
	d := &DB{
		sql:           h,
		read:          openReadPool(path, logger),
		path:          path,
		logger:        logger,
		retentionDays: opts.RetentionDays,
		writeC:        make(chan op, writeQueueCap),
		stopC:         make(chan struct{}),
		doneC:         make(chan struct{}),
	}
	if d.read == nil {
		d.read = h // degrade: reads share the writer connection
	}
	go d.writer()
	return d, nil
}

// openReadPool opens the query-only WAL read handle; nil on failure (the
// caller degrades to the writer connection).
func openReadPool(path string, logger *slog.Logger) *sql.DB {
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=query_only(1)"
	h, err := sql.Open("sqlite", dsn)
	if err != nil {
		logger.Warn("analytics: read pool unavailable — reads share the writer connection", "err", err)
		return nil
	}
	h.SetMaxOpenConns(4)
	// Force one real connection now so a broken pool fails here, not on the
	// first dashboard query.
	if _, err := h.Exec("SELECT 1"); err != nil {
		h.Close()
		logger.Warn("analytics: read pool unavailable — reads share the writer connection", "err", err)
		return nil
	}
	return h
}

func openSQLite(path string) (*sql.DB, error) {
	// modernc's driver applies _pragma parameters per connection; one writer
	// connection (MaxOpenConns 1) plus WAL keeps single-process access simple
	// and crash-safe.
	// No foreign_keys pragma: the schema declares zero FKs; orphaned
	// session_traffic/session_rtt rows are swept by the daily prune instead.
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)"
	h, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	h.SetMaxOpenConns(1)
	// auto_vacuum must be declared before the first table exists; on an
	// already-populated database this is a no-op, which is fine.
	if _, err := h.Exec("PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		h.Close()
		return nil, err
	}
	if err := migrate(h); err != nil {
		h.Close()
		return nil, err
	}
	return h, nil
}

// migrate climbs the schema ladder transactionally; user_version writes are
// part of each transaction, so a crash mid-migration re-runs it cleanly.
func migrate(h *sql.DB) error {
	var v int
	if err := h.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if v > len(migrations) {
		return fmt.Errorf("database schema %d is newer than this build supports (%d)", v, len(migrations))
	}
	for ; v < len(migrations); v++ {
		tx, err := h.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate to schema %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("stamp schema %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schema %d: %w", v+1, err)
		}
	}
	return nil
}

// Enqueue schedules one write on the async writer. It never blocks on the
// writer: when the queue is full the oldest queued non-barrier op is dropped
// (losing a telemetry row beats stalling the data path). The new op always
// lands, barriers are never dropped, and dropped counts exactly one per lost
// op.
func (d *DB) Enqueue(name string, fn func(tx *sql.Tx) error) {
	o := op{name: name, fn: fn}
	select {
	case d.writeC <- o:
		return
	default:
	}
	// Full queue: evict the oldest non-barrier op to make room. qmu
	// serializes producers through this slow path; each loop iteration either
	// lands the new op or drops exactly one victim, so the op always lands
	// and `dropped` stays exact.
	d.qmu.Lock()
	defer d.qmu.Unlock()
	for {
		select {
		case d.writeC <- o:
			return
		default:
		}
		select {
		case victim := <-d.writeC:
			if victim.done != nil {
				// Never drop a barrier — a goroutine is blocked on it.
				// Re-sending moves it later in the queue, which only widens
				// the set of writes it covers. A racing fast-path producer
				// can steal the slot we just freed, making this send block
				// briefly — acceptable: the writer drains continuously, and
				// a queued barrier under overload is a test/shutdown case,
				// never the data path.
				d.writeC <- victim
				continue
			}
			if n := d.dropped.Add(1); n == 1 || n%1000 == 0 {
				d.logger.Warn("analytics: write queue full — dropping oldest", "dropped_total", n)
			}
		default:
			// The writer drained the queue between the two selects; retry.
		}
	}
}

// Barrier blocks until every write enqueued before it has committed. Test
// and shutdown ordering only — not for the data path.
func (d *DB) Barrier() {
	done := make(chan struct{})
	select {
	case d.writeC <- op{name: "barrier", done: done}:
	case <-d.doneC:
		return // writer already stopped; queue was drained by Close
	}
	select {
	case <-done:
	case <-d.doneC:
	}
}

// writer is the single goroutine that owns queued writes: it batches them
// into transactions and runs the daily retention prune.
func (d *DB) writer() {
	defer close(d.doneC)
	prune := time.NewTicker(pruneEvery)
	defer prune.Stop()
	rollup := time.NewTicker(rollupEvery)
	defer rollup.Stop()
	d.prune(time.Now())
	d.runRollup(time.Now())

	var (
		pending []op
		timer   *time.Timer
		timerC  <-chan time.Time
	)
	flush := func() {
		if timer != nil {
			timer.Stop()
			timer, timerC = nil, nil
		}
		if len(pending) == 0 {
			return
		}
		d.runBatch(pending)
		pending = pending[:0]
	}
	for {
		select {
		case o := <-d.writeC:
			pending = append(pending, o)
			if o.done != nil || len(pending) >= batchMax {
				flush()
				continue
			}
			if timer == nil {
				timer = time.NewTimer(batchDelay)
				timerC = timer.C
			}
		case <-timerC:
			timer, timerC = nil, nil
			flush()
		case now := <-prune.C:
			flush()
			d.prune(now)
		case now := <-rollup.C:
			flush()
			d.runRollup(now)
		case <-d.stopC:
			// Drain whatever is already queued, then exit.
			for {
				select {
				case o := <-d.writeC:
					pending = append(pending, o)
				default:
					flush()
					// A final rollup folds this run's last buckets/sessions into
					// the hourly/daily aggregates before the file is checkpointed.
					d.runRollup(time.Now())
					return
				}
			}
		}
	}
}

// runBatch lands one batch in one transaction. Individual op failures are
// logged and skipped; a failed commit loses the batch (telemetry-grade data,
// never worth crashing over).
func (d *DB) runBatch(ops []op) {
	defer func() {
		for _, o := range ops {
			if o.done != nil {
				close(o.done)
			}
		}
	}()
	tx, err := d.sql.Begin()
	if err != nil {
		d.logger.Warn("analytics: begin batch failed", "err", err)
		return
	}
	for _, o := range ops {
		if o.fn == nil {
			continue
		}
		if err := o.fn(tx); err != nil {
			d.logger.Warn("analytics: write failed", "op", o.name, "err", err)
		}
	}
	if err := tx.Commit(); err != nil {
		tx.Rollback()
		d.logger.Warn("analytics: commit batch failed", "ops", len(ops), "err", err)
	}
}

// Close drains the writer, folds the WAL back into the main file, and closes
// the database. Safe to call more than once.
func (d *DB) Close() error {
	d.closeOnce.Do(func() { close(d.stopC) })
	<-d.doneC
	// Read pool first: an open reader would keep WAL frames pinned and defeat
	// the TRUNCATE checkpoint below.
	if d.read != d.sql {
		d.read.Close()
	}
	// Checkpointing on close keeps the config dir copy-friendly (one file,
	// no -wal/-shm sidecars to forget).
	if _, err := d.sql.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		d.logger.Debug("analytics: final checkpoint failed", "err", err)
	}
	return d.sql.Close()
}
