package store

import "fmt"

// SessionHasRecord reports whether sessionID has written a live
// (non-tombstoned) record of any kind — a note, outcome or handoff. The
// post-commit ask stays quiet for a session that has already said something.
func (s *Store) SessionHasRecord(sessionID string) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM records
		WHERE session_id = ? AND tombstoned_at IS NULL`, sessionID).Scan(&n); err != nil {
		return false, fmt.Errorf("store: count records for session %s: %w", sessionID, err)
	}
	return n > 0, nil
}
