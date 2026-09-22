package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// BackfillCursor is one importer's progress through one source file
// (migrations/0004_backfill_cursors.sql). SessionID is the session that
// file's events belong to, minted once on the file's first import.
type BackfillCursor struct {
	SessionID  string
	LastUUID   string
	ByteOffset int64
}

// GetBackfillCursor returns the saved progress for source+path, and whether
// one exists yet. A missing cursor means the file has never been imported.
func (s *Store) GetBackfillCursor(source, path string) (BackfillCursor, bool, error) {
	var c BackfillCursor
	var lastUUID sql.NullString
	err := s.db.QueryRow(`SELECT session_id, last_uuid, byte_offset
		FROM backfill_cursors WHERE source = ? AND path = ?`, source, path).
		Scan(&c.SessionID, &lastUUID, &c.ByteOffset)
	if errors.Is(err, sql.ErrNoRows) {
		return BackfillCursor{}, false, nil
	}
	if err != nil {
		return BackfillCursor{}, false, fmt.Errorf("store: get backfill cursor %s %s: %w", source, path, err)
	}
	c.LastUUID = lastUUID.String
	return c, true, nil
}

// SetBackfillCursor inserts or updates source+path's progress.
func (s *Store) SetBackfillCursor(source, path string, c BackfillCursor) error {
	_, err := s.db.Exec(`INSERT INTO backfill_cursors (source, path, session_id, last_uuid, byte_offset)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source, path) DO UPDATE SET
			session_id  = excluded.session_id,
			last_uuid   = excluded.last_uuid,
			byte_offset = excluded.byte_offset`,
		source, path, c.SessionID, nullable(c.LastUUID), c.ByteOffset)
	if err != nil {
		return fmt.Errorf("store: set backfill cursor %s %s: %w", source, path, err)
	}
	return nil
}
