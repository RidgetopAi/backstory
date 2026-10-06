// Package capture is the gate every backfill importer applies so SCHEMA.md
// invariant 8 (capture-off is honoured on every write path) holds for
// backfill too: nothing is imported while capture is off, and a session that
// started inside a recorded pause window is never imported, even after
// capture is back on.
package capture

import (
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// OffFunc reports whether capture is currently paused (cmd/backstory's
// captureOff — the flag file). A nil OffFunc means the caller has no
// capture-off flag to consult (library and unit-test use); the CLI and the
// daemon always supply one.
type OffFunc func() (bool, error)

// Off reports whether capture is paused right now.
func Off(off OffFunc) (bool, error) {
	if off == nil {
		return false, nil
	}
	return off()
}

// StartedInPause reports whether a session starting at start falls inside a
// recorded pause window and so must never be imported, even once capture is
// back on. The capture-off-now case is Off's, applied once per Import.
func StartedInPause(st *store.Store, start time.Time) (bool, error) {
	return st.CapturePausedAt(start)
}
