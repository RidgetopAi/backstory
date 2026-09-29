package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
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
		tsToNanos(e.TS), e.Kind, nullable(e.SessionID), e.Source, Redact(e.Payload),
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
	projectKey = s.canonicalizeProjectKey(projectKey)
	rows, err := s.db.Query(`SELECT e.id, e.ts, e.kind, e.session_id, e.source, e.payload, e.workspace, e.window
		FROM timeline_events e
		JOIN sessions sess ON sess.id = e.session_id
		WHERE sess.project_key = ? AND e.id > ?
		ORDER BY e.id ASC`, projectKey, sinceID)
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
	projectKey = s.canonicalizeProjectKey(projectKey)
	args := []any{projectKey}
	query := `SELECT id, ts, kind, session_id, source, payload, workspace, window FROM (
		SELECT e.id, e.ts, e.kind, e.session_id, e.source, e.payload, e.workspace, e.window
		FROM timeline_events e
		JOIN sessions sess ON sess.id = e.session_id
		WHERE sess.project_key = ?`
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

// EventsForSessionTimeline returns timeline events belonging to sessionID
// alone, filtered by since (a zero time.Time means no lower bound;
// otherwise only events with ts >= since) and kind (empty means any kind),
// always ordered by id ascending — sequence, never ts (SCHEMA.md invariant
// 10), the same rule EventsForTimeline applies to its project-scoped query,
// applied here to a single session instead of a project's join. limit <= 0
// means no limit; a positive limit keeps the most recent limit events (by
// sequence), still returned oldest-to-newest. This is the mcp `timeline`
// tool's scope=session query (AGENT-CONTRACT.md's timeline tool): unlike
// EventsForTimeline's project_key join, a session's own events carry no
// ambiguity about which project they belong to, so no join is needed.
func (s *Store) EventsForSessionTimeline(sessionID string, since time.Time, kind string, limit int) ([]TimelineEvent, error) {
	args := []any{sessionID}
	query := `SELECT id, ts, kind, session_id, source, payload, workspace, window FROM (
		SELECT id, ts, kind, session_id, source, payload, workspace, window
		FROM timeline_events
		WHERE session_id = ?`
	if !since.IsZero() {
		query += ` AND ts >= ?`
		args = append(args, tsToNanos(since))
	}
	if kind != "" {
		query += ` AND kind = ?`
		args = append(args, kind)
	}
	query += ` ORDER BY id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	query += `) ORDER BY id ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: timeline events for session %s: %w", sessionID, err)
	}
	out, err := scanTimelineEvents(rows)
	if err != nil {
		return nil, fmt.Errorf("store: timeline events for session %s: %w", sessionID, err)
	}
	return out, nil
}

// eventsForSessionID returns every timeline event belonging to sessionID
// alone, ordered by id ascending — LABELS' own event source (decision
// f3fa04c7, store.sessionLabels): unlike EventsSinceID/EventsForTimeline,
// this is scoped by session id directly, never by project_key, since a
// session's work-location labels must be derived from what THAT session
// itself touched regardless of which project_key its records happen to
// carry.
func (s *Store) eventsForSessionID(sessionID string) ([]TimelineEvent, error) {
	rows, err := s.db.Query(`SELECT id, ts, kind, session_id, source, payload, workspace, window
		FROM timeline_events WHERE session_id = ? ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: events for session %s: %w", sessionID, err)
	}
	out, err := scanTimelineEvents(rows)
	if err != nil {
		return nil, fmt.Errorf("store: events for session %s: %w", sessionID, err)
	}
	return out, nil
}

// eventsSinceIDForSessions returns every timeline event whose session_id is
// one of sessionIDs, with id > sinceID, ordered by id ascending — never by
// ts (SCHEMA.md invariant 10) — the same shape EventsSinceID returns but
// scoped to an explicit session set rather than a project_key join:
// HandoffFreshness's home-scoped later-activity check (decision f3fa04c7)
// needs every session whose folder resolves to a handoff's home, which can
// span many distinct project_key values (one per repo under the
// workspace), not just a single canonicalized key. An empty sessionIDs
// returns no rows without querying.
func (s *Store) eventsSinceIDForSessions(sessionIDs []string, sinceID int64) ([]TimelineEvent, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(sessionIDs))
	args := make([]any, 0, len(sessionIDs)+1)
	for i, id := range sessionIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, sinceID)
	//nolint:gosec // the concatenated part is only "?" placeholders (one per session id), every value is still bound as a query arg below
	query := `SELECT id, ts, kind, session_id, source, payload, workspace, window FROM timeline_events
		WHERE session_id IN (` + strings.Join(placeholders, ",") + `) AND id > ? ORDER BY id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: events since %d for sessions: %w", sinceID, err)
	}
	out, err := scanTimelineEvents(rows)
	if err != nil {
		return nil, fmt.Errorf("store: events since %d for sessions: %w", sinceID, err)
	}
	return out, nil
}

// EventsForSessionsSince returns every timeline event belonging to one of
// sessionIDs with ts >= since, ordered by id ascending — This Week's
// per-label week-grid event source (decision f3fa04c7 clause 5, task
// 482b2320): scoped by an explicit session set, the same reason
// eventsSinceIDForSessions exists, but bounded by wall-clock time (the
// window's own since bound) rather than a sequence cursor. An empty
// sessionIDs returns no rows without querying.
func (s *Store) EventsForSessionsSince(sessionIDs []string, since time.Time) ([]TimelineEvent, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(sessionIDs))
	args := make([]any, 0, len(sessionIDs)+1)
	for i, id := range sessionIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, tsToNanos(since))
	//nolint:gosec // the concatenated part is only "?" placeholders (one per session id), every value is still bound as a query arg below
	query := `SELECT id, ts, kind, session_id, source, payload, workspace, window FROM timeline_events
		WHERE session_id IN (` + strings.Join(placeholders, ",") + `) AND ts >= ? ORDER BY id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: events for sessions since %s: %w", since, err)
	}
	out, err := scanTimelineEvents(rows)
	if err != nil {
		return nil, fmt.Errorf("store: events for sessions since %s: %w", since, err)
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

// ToolOutputExcerpt is the bounded, redacted form of a tool's output that
// every tool.result writer stores (task c9ab6d28). Redaction runs over the
// FULL text BEFORE the cut, so a secret straddling the cut point is replaced
// whole by its marker instead of surviving as a partial key.
func ToolOutputExcerpt(full string) string {
	return payload.ElideMiddle(Redact(full), payload.ToolOutputExcerptMaxRunes)
}

// ToolResultEvent is a stored tool.result event found by tool_use_id.
type ToolResultEvent struct {
	ID      int64
	Payload payload.ToolResult
}

// FindToolResult returns the tool.result event carrying toolUseID, if any.
func (s *Store) FindToolResult(toolUseID string) (ToolResultEvent, bool, error) {
	var (
		id  int64
		raw string
	)
	err := s.db.QueryRow(`SELECT id, payload FROM timeline_events
		WHERE kind = ? AND json_extract(payload, '$.tool_use_id') = ? ORDER BY id LIMIT 1`,
		payload.KindToolResult, toolUseID).Scan(&id, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolResultEvent{}, false, nil
	}
	if err != nil {
		return ToolResultEvent{}, false, fmt.Errorf("store: find tool.result %s: %w", toolUseID, err)
	}
	var p payload.ToolResult
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return ToolResultEvent{}, false, fmt.Errorf("store: decode tool.result %s: %w", toolUseID, err)
	}
	return ToolResultEvent{ID: id, Payload: p}, true, nil
}

// EnrichToolResult rewrites the payload of the tool.result event id with
// outcome fields observed after the event was first written (live capture
// records a tool.result before a transcript's is_error/Exit code line
// exists). timeline_events is append-only by trigger; this is the single
// sanctioned exception — only a tool.result's payload, only inside one
// transaction that restores the trigger — following the precedent of the
// project-key sweep's records rewrite (sweep.go).
func (s *Store) EnrichToolResult(id int64, p payload.ToolResult) error {
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("store: enrich tool.result %d: %w", id, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: enrich tool.result %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DROP TRIGGER timeline_events_no_update`); err != nil {
		return fmt.Errorf("store: enrich tool.result %d: drop trigger: %w", id, err)
	}
	if _, err := tx.Exec(`UPDATE timeline_events SET payload = ? WHERE id = ? AND kind = ?`,
		Redact(string(b)), id, payload.KindToolResult); err != nil {
		return fmt.Errorf("store: enrich tool.result %d: %w", id, err)
	}
	if _, err := tx.Exec(timelineEventsNoUpdateTriggerSQL); err != nil {
		return fmt.Errorf("store: enrich tool.result %d: recreate trigger: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: enrich tool.result %d: %w", id, err)
	}
	return nil
}

// ReconcileToolResult is backfill's tool.result writer half: when a
// tool.result for in.ToolUseID already exists it is never duplicated. If
// that event carries no outcome yet (live capture recorded only the id) it
// is ENRICHED in place with in's is_error, exit code and excerpt, keeping
// anything the existing event already observed (its Exit, Interrupted). It
// reports whether an existing event was found — the caller then appends
// nothing. in.Content must already be excerpted (ToolOutputExcerpt).
func (s *Store) ReconcileToolResult(in payload.ToolResult) (found bool, err error) {
	existing, ok, err := s.FindToolResult(in.ToolUseID)
	if err != nil || !ok {
		return false, err
	}
	old := existing.Payload
	if old.Content != "" || old.IsError {
		return true, nil
	}
	merged := old
	merged.IsError = in.IsError
	merged.Content = in.Content
	if merged.Exit == nil {
		merged.Exit = in.Exit
	}
	if merged.AgentID == "" {
		merged.AgentID = in.AgentID
	}
	if merged.IsError == old.IsError && merged.Content == old.Content && merged.Exit == old.Exit {
		return true, nil
	}
	return true, s.EnrichToolResult(existing.ID, merged)
}
