package store

import (
	"database/sql"
	"errors"
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
// Payload is redacted before storage (redact.go), the same rule
// InsertRecord applies to records.text (SCHEMA.md invariant 6: redaction on
// every payload, not just the ledger).
func (s *Store) AppendEvent(e Event) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO timeline_events (ts, kind, session_id, source, payload, workspace, window)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tsToNanos(e.TS), e.Kind, nullable(e.SessionID), e.Source, redact(e.Payload),
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

// HasEventWithToolUseID reports whether a timeline event of kind already
// carries toolUseID as its payload's tool_use_id. It is scoped by kind and
// tool_use_id only, never by session: a tool use captured live and the same
// tool use later replayed from a Claude transcript land in two different
// sessions (a live PostToolUse connection mints its own ad hoc session; a
// backfill import mints a separate backfilled one for the transcript file),
// so a session-scoped check would never see the live event backfill must
// dedup against (internal/backfill/claude's appendToolEvents). toolUseID
// must be non-empty — the caller skips the check entirely for a tool_use
// block with no id, since every such row's payload omits the key
// (json:"tool_use_id,omitempty") and would otherwise all compare equal
// under json_extract's NULL.
func (s *Store) HasEventWithToolUseID(kind, toolUseID string) (bool, error) {
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM timeline_events
		WHERE kind = ? AND json_extract(payload, '$.tool_use_id') = ? LIMIT 1`,
		kind, toolUseID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: has event with tool_use_id %s/%s: %w", kind, toolUseID, err)
	}
	return true, nil
}

// EventsSinceID returns every timeline event belonging to a session in
// projectKey with id > sinceID, ordered by id ascending — never by ts
// (SCHEMA.md invariant 10: "Recall and the SessionStart delta order events
// by timeline_events.id, never by ts; backfilled sessions carry file clocks
// that lie"). sinceID == 0 returns every event for the project, from the
// beginning. An event with no session (session_id NULL) belongs to no
// project and is never returned.
func (s *Store) EventsSinceID(projectKey string, sinceID int64) ([]TimelineEvent, error) {
	k1, k2 := projectKeyIN(projectKey)
	rows, err := s.db.Query(`SELECT e.id, e.ts, e.kind, e.session_id, e.source, e.payload, e.workspace, e.window
		FROM timeline_events e
		JOIN sessions sess ON sess.id = e.session_id
		WHERE sess.project_key IN (?, ?) AND e.id > ?
		ORDER BY e.id ASC`, k1, k2, sinceID)
	if err != nil {
		return nil, fmt.Errorf("store: events since %d for project %s: %w", sinceID, projectKey, err)
	}
	out, err := scanTimelineEvents(rows)
	if err != nil {
		return nil, fmt.Errorf("store: events since %d for project %s: %w", sinceID, projectKey, err)
	}
	return out, nil
}

// EventsForTimeline returns timeline events belonging to a session in
// projectKey, filtered by since (a zero time.Time means no lower bound;
// otherwise only events with ts >= since) and kind (empty means any kind),
// always ordered by id ascending — sequence, never ts (SCHEMA.md invariant
// 10) — even when since's bound is itself a ts comparison and a backfilled
// session's clock disagrees with sequence order: two events that both pass
// the ts bound keep their sequence order relative to each other in the
// result, never reordered by ts. limit <= 0 means no limit; a positive
// limit keeps the most recent limit events (by sequence), still returned
// oldest-to-newest. It is `backstory timeline`'s query, read directly from
// the store on the human path (PLAN.md §Phase 4 CLI, decision d9d456e7) —
// unlike EventsSinceID's sequence-position boundary (the SessionStart
// delta's own use), this bound is the caller's wall-clock --since value.
func (s *Store) EventsForTimeline(projectKey string, since time.Time, kind string, limit int) ([]TimelineEvent, error) {
	k1, k2 := projectKeyIN(projectKey)
	args := []any{k1, k2}
	query := `SELECT id, ts, kind, session_id, source, payload, workspace, window FROM (
		SELECT e.id, e.ts, e.kind, e.session_id, e.source, e.payload, e.workspace, e.window
		FROM timeline_events e
		JOIN sessions sess ON sess.id = e.session_id
		WHERE sess.project_key IN (?, ?)`
	if !since.IsZero() {
		query += ` AND e.ts >= ?`
		args = append(args, tsToNanos(since))
	}
	if kind != "" {
		query += ` AND e.kind = ?`
		args = append(args, kind)
	}
	query += ` ORDER BY e.id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	query += `) ORDER BY id ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: timeline events for project %s: %w", projectKey, err)
	}
	out, err := scanTimelineEvents(rows)
	if err != nil {
		return nil, fmt.Errorf("store: timeline events for project %s: %w", projectKey, err)
	}
	return out, nil
}

// scanTimelineEvents drains rows of the (id, ts, kind, session_id, source,
// payload, workspace, window) shape both EventsSinceID and
// EventsForTimeline select, into TimelineEvent values in the rows' own
// order. It always closes rows itself, even on a scan error.
func scanTimelineEvents(rows *sql.Rows) ([]TimelineEvent, error) {
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
			return nil, fmt.Errorf("scan timeline event: %w", err)
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
		return nil, err
	}
	return out, nil
}
