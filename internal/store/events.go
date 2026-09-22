package store

import (
	"database/sql"
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

// TimelineEvent is a row read back from timeline_events, including its id —
// the rowid, and the only ordering timeline_events carries (SCHEMA.md
// invariant 10).
type TimelineEvent struct {
	ID int64
	Event
}

// EventsSinceID returns every timeline event belonging to a session in
// projectKey with id > sinceID, ordered by id ascending — never by ts
// (SCHEMA.md invariant 10: "Recall and the SessionStart delta order events
// by timeline_events.id, never by ts; backfilled sessions carry file clocks
// that lie"). sinceID == 0 returns every event for the project, from the
// beginning. An event with no session (session_id NULL) belongs to no
// project and is never returned.
func (s *Store) EventsSinceID(projectKey string, sinceID int64) ([]TimelineEvent, error) {
	rows, err := s.db.Query(`SELECT e.id, e.ts, e.kind, e.session_id, e.source, e.payload, e.workspace, e.window
		FROM timeline_events e
		JOIN sessions sess ON sess.id = e.session_id
		WHERE sess.project_key = ? AND e.id > ?
		ORDER BY e.id ASC`, projectKey, sinceID)
	if err != nil {
		return nil, fmt.Errorf("store: events since %d for project %s: %w", sinceID, projectKey, err)
	}
	defer func() { _ = rows.Close() }()

	out := []TimelineEvent{}
	for rows.Next() {
		var (
			id                int64
			ts                int64
			kind, source      string
			payload           string
			sessionID         sql.NullString
			workspace, window sql.NullString
		)
		if err := rows.Scan(&id, &ts, &kind, &sessionID, &source, &payload, &workspace, &window); err != nil {
			return nil, fmt.Errorf("store: scan timeline event: %w", err)
		}
		out = append(out, TimelineEvent{
			ID: id,
			Event: Event{
				TS:        tsFromNanos(ts),
				Kind:      kind,
				SessionID: sessionID.String,
				Source:    source,
				Payload:   payload,
				Workspace: workspace.String,
				Window:    window.String,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: events since %d for project %s: %w", sinceID, projectKey, err)
	}
	return out, nil
}
