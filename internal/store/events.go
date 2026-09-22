package store

import (
	"fmt"
	"time"
)

// Event is a row to append to timeline_events. Only the daemon ever calls
// AppendEvent (AGENT-CONTRACT.md §The never-list, item 4) — the socket API
// exposes no event write.
type Event struct {
	TS        time.Time
	Kind      string
	SessionID string // empty for OS events with no session
	Source    string // posttooluse | shell | socket2 | notification | clipboard | backfill | daemon
	Payload   string // JSON
	Workspace string
	Window    string
}

// AppendEvent inserts a timeline event and returns its id (the rowid, and
// the only ordering timeline_events carries — SCHEMA.md §timeline_events).
func (s *Store) AppendEvent(e Event) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO timeline_events (ts, kind, session_id, source, payload, workspace, window)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tsToNanos(e.TS), e.Kind, nullable(e.SessionID), e.Source, e.Payload,
		nullable(e.Workspace), nullable(e.Window))
	if err != nil {
		return 0, fmt.Errorf("store: append event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: append event: %w", err)
	}
	return id, nil
}
