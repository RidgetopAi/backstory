package store

import (
	"path/filepath"
	"sync"
	"testing"
)

// Concurrent Opens of one fresh database (the daemon plus a CLI call) must
// all succeed: the migration that loses the race to the write lock skips.
func TestConcurrentOpenFreshDatabaseMigratesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	const openers = 8
	errs := make(chan error, openers)
	var wg sync.WaitGroup
	for i := 0; i < openers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(path, nil, nil)
			if err == nil {
				err = s.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Open: %v", err)
		}
	}
}
