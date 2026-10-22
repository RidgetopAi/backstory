package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentOpenMigratesTriggerMigrationOnce (task bfb4cdff): the captured
// daemon exit was `apply migration 0020_sync_session_purged.sql (statement 1):
// trigger sync_session_purged already exists`. Cause: two Opens both read
// schema_version before either migrated, then the loser re-ran the
// non-idempotent CREATE TRIGGER once it got the write lock. Open must
// re-check schema_version under that lock, so every racer succeeds and the
// migration is recorded exactly once.
func TestConcurrentOpenMigratesTriggerMigrationOnce(t *testing.T) {
	if SchemaVersion < 20 {
		t.Fatalf("SchemaVersion = %d, want >= 20", SchemaVersion)
	}
	const rounds, openers = 10, 8
	for r := 0; r < rounds; r++ {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("v19-%d.db", r))
		// A partly migrated store: fully migrated, then 0020 un-applied.
		// (buildFixtureThroughVersion skips data hooks, so it cannot build v19.)
		seed := mustOpen(t, path)
		for _, q := range []string{
			`DROP TRIGGER sync_session_purged`,
			`DELETE FROM schema_version WHERE version = 20`,
		} {
			if _, err := seed.DB().Exec(q); err != nil {
				t.Fatalf("un-apply 0020 (%s): %v", q, err)
			}
		}
		if err := seed.Close(); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, openers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < openers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s, err := Open(path, nil, nil)
				if err == nil {
					err = s.Close()
				}
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: concurrent Open of a store with 0020 pending: %v", r, err)
			}
		}
		s := mustOpen(t, path)
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 20`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("round %d: schema_version rows for 20 = %d, %v; want 1", r, n, err)
		}
		_ = s.Close()
	}
}
