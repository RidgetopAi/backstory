package sync

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Lock tunables: how long a second sync waits for the first, and how often it
// retries. Variables so tests can shorten them.
var (
	LockWait = 3 * time.Second
	LockPoll = 100 * time.Millisecond
)

// LockFileName sits beside the store database.
const LockFileName = "sync.lock"

// ErrBusy means another sync holds the lock. It is neither an ok nor an error
// attempt: nothing was tried.
var ErrBusy = errors.New("another sync is already running")

// Lock is a held sync lock.
type Lock struct{ f *os.File }

// AcquireLock takes the exclusive sync lock in dir, waiting up to LockWait.
// The kernel drops the lock when the process dies, so a crash never wedges it.
func AcquireLock(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, LockFileName), os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path under the store dir
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(LockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // fd fits int
		if err == nil {
			return &Lock{f: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, ErrBusy
		}
		time.Sleep(LockPoll)
	}
}

// Release drops the lock.
func (l *Lock) Release() { _ = l.f.Close() }
