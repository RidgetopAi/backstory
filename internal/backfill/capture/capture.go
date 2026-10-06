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

// ValidTS reports whether t is a usable event time: strictly after the unix
// epoch. Go's zero time.Time converts to a large negative unix-nanosecond
// value (rendered as year 1754), so a missing or unparseable transcript
// timestamp must never reach the store as-is.
func ValidTS(t time.Time) bool {
	return store.ValidTS(t)
}

// FillTimes repairs every invalid time in ts in place so no event is ever
// written with a zero or negative time: an invalid entry takes the nearest
// preceding valid time (the last observed event time of the session), and a
// leading run of invalid entries takes the first valid time. It reports
// false, leaving ts untouched, when no entry is valid — the caller then has
// no time to anchor on and must write nothing rather than invent one.
func FillTimes(ts []*time.Time) bool {
	first := -1
	for i, t := range ts {
		if ValidTS(*t) {
			first = i
			break
		}
	}
	if first < 0 {
		return false
	}
	last := *ts[first]
	for _, t := range ts {
		if ValidTS(*t) {
			last = *t
		} else {
			*t = last
		}
	}
	return true
}
