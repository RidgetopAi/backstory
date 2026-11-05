package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DoneMarkerText is the text of the human-declared confirm record that marks
// a handoff done. A done record is a confirm with an informs edge into the
// handoff and no promoter, exactly like an affirm; this fixed text is the
// disambiguator (no schema column), so the edge enum and schema stay as-is.
const DoneMarkerText = "marked done"

var (
	// ErrDoneHumanOnly is returned when a non-human identity tries to mark a
	// handoff done.
	ErrDoneHumanOnly = errors.New("store: only a human can mark a handoff done")
	// ErrDoneNotAHandoff is returned when done's target is not a handoff.
	ErrDoneNotAHandoff = errors.New("store: done requires the target record to be a handoff")
)

// HandoffDone reports whether handoffID has a live (non-tombstoned) human
// done record, and when the latest one was written. Tombstoning the done
// record (backstory delete) makes the handoff not-done again.
func (s *Store) HandoffDone(handoffID string) (time.Time, bool, error) {
	var nanos int64
	err := s.db.QueryRow(`
		SELECT c.ts FROM edges e
		JOIN records c ON c.id = e.from_id
		WHERE e.to_id = ? AND e.type = ? AND c.kind = ? AND c.tier = ?
		AND c.promoter IS NULL AND c.text = ? AND c.tombstoned_at IS NULL
		ORDER BY c.rowid DESC LIMIT 1`,
		handoffID, string(EdgeInforms), string(KindConfirm), string(TierHumanDeclared), DoneMarkerText).Scan(&nanos)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: handoff done %s: %w", handoffID, err)
	}
	return tsFromNanos(nanos), true, nil
}
