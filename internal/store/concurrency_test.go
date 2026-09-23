package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestInsertRecordSucceedsUnderConcurrentTimelineWriter reproduces the
// production failure behind "database is locked (SQLITE_BUSY)" on the
// `note` write path (task 825225ee): the daemon's backfill and live-capture
// paths append timeline_events in a tight loop on one connection while the
// `note` command repeatedly calls InsertRecord against the same database
// file from a second connection, exactly as two separate OS processes do in
// production.
//
// On unfixed store.go, dsn() leaves _txlock at its modernc.org/sqlite
// default (deferred), so s.db.Begin() in InsertRecordWithEdges opens a
// DEFERRED transaction. When a concurrent writer commits new WAL frames
// between this transaction's implicit read snapshot and its write-lock
// request, SQLite fails the read->write upgrade with SQLITE_BUSY
// immediately — busy_timeout's busy handler is never invoked for that
// failure, because no amount of waiting fixes a stale snapshot. Every write
// path must instead take its write lock at BEGIN (_txlock=immediate) so the
// only busy case left is ordinary lock contention, which busy_timeout does
// resolve by retrying.
//
// Mutation probe (removed `q.Add("_txlock", "immediate")` from store.go's
// dsn(), restoring the deferred default): RED, 4/10 runs
// (-run TestInsertRecordSucceedsUnderConcurrentTimelineWriter -count=10),
// e.g. "InsertRecord 0/300: store: insert record: database is locked
// (517)" (SQLITE_BUSY_SNAPSHOT) -- restoring the _txlock=immediate line
// turns it back GREEN, 10/10.
func TestInsertRecordSucceedsUnderConcurrentTimelineWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")

	writer := mustOpen(t, path)
	reader := mustOpen(t, path)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var writerErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := writer.AppendEvent(Event{
				TS:      time.Now(),
				Kind:    "tool_use",
				Source:  "backfill",
				Payload: "{}",
			}); err != nil {
				writerErr = err
				return
			}
		}
	}()

	const n = 300
	for i := 0; i < n; i++ {
		if _, err := reader.InsertRecord(InsertRecordParams{
			Identity: Identity{Kind: IdentityAgent},
			Kind:     KindNote,
			Text:     "concurrent note",
		}); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("InsertRecord %d/%d: %v", i, n, err)
		}
	}
	close(stop)
	wg.Wait()
	if writerErr != nil {
		t.Fatalf("concurrent timeline writer: %v", writerErr)
	}
}
