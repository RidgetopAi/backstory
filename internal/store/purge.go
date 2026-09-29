package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrPurgeRequiresHuman is returned when PurgeSessions is called with an
// Identity that is not IdentityHuman. Purge is a human-only power
// (AGENT-CONTRACT.md §User-only powers).
var ErrPurgeRequiresHuman = errors.New("store: purge requires a human identity")

// PurgeScope selects the sessions to purge: either one session by ID, or a
// project's sessions whose START is in [Since, Until) (a zero bound is
// open). When a window is given (Since or Until non-zero) a project scope
// also selects session-less OS events (session_id NULL) whose ts is in the
// window.
type PurgeScope struct {
	SessionID  string
	ProjectKey string
	Since      time.Time
	Until      time.Time
}

func (sc PurgeScope) windowed() bool { return !sc.Since.IsZero() || !sc.Until.IsZero() }

// PurgeCounts is what a purge removed (or, for PurgePreview, would remove).
type PurgeCounts struct {
	Sessions int
	Events   int
}

// String is the scope text stored in purge_log: identifiers and bounds only.
func (sc PurgeScope) String() string {
	if sc.SessionID != "" {
		return "session=" + sc.SessionID
	}
	out := "project=" + sc.ProjectKey
	if !sc.Since.IsZero() {
		out += " since=" + sc.Since.UTC().Format(time.RFC3339)
	}
	if !sc.Until.IsZero() {
		out += " until=" + sc.Until.UTC().Format(time.RFC3339)
	}
	return out
}

type purgeQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// purgeSelection resolves the scope to session ids and the WHERE clause (with
// args) matching the events to delete.
func (s *Store) purgeSelection(q purgeQuerier, sc PurgeScope) (ids []string, evWhere string, evArgs []any, err error) {
	var rows *sql.Rows
	switch {
	case sc.SessionID != "" && sc.ProjectKey == "":
		rows, err = q.Query(`SELECT id FROM sessions WHERE id = ?`, sc.SessionID)
	case sc.ProjectKey != "" && sc.SessionID == "":
		query := `SELECT id FROM sessions WHERE project_key = ?`
		args := []any{s.canonicalizeProjectKey(sc.ProjectKey)}
		if !sc.Since.IsZero() {
			query += ` AND started_at >= ?`
			args = append(args, tsToNanos(sc.Since))
		}
		if !sc.Until.IsZero() {
			query += ` AND started_at < ?`
			args = append(args, tsToNanos(sc.Until))
		}
		rows, err = q.Query(query, args...)
	default:
		return nil, "", nil, errors.New("store: purge: scope needs exactly one of session id or project key")
	}
	if err != nil {
		return nil, "", nil, fmt.Errorf("store: purge: select sessions: %w", err)
	}
	ids, err = scanIDs(rows)
	if err != nil {
		return nil, "", nil, fmt.Errorf("store: purge: select sessions: %w", err)
	}

	var clauses []string
	if len(ids) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		clauses = append(clauses, "session_id IN ("+marks+")")
		for _, id := range ids {
			evArgs = append(evArgs, id)
		}
	}
	if sc.ProjectKey != "" && sc.windowed() {
		c := "session_id IS NULL"
		if !sc.Since.IsZero() {
			c += " AND ts >= ?"
			evArgs = append(evArgs, tsToNanos(sc.Since))
		}
		if !sc.Until.IsZero() {
			c += " AND ts < ?"
			evArgs = append(evArgs, tsToNanos(sc.Until))
		}
		clauses = append(clauses, "("+c+")")
	}
	if len(clauses) == 0 {
		return ids, "", nil, nil
	}
	return ids, strings.Join(clauses, " OR "), evArgs, nil
}

func countEvents(q purgeQuerier, where string, args []any) (int, error) {
	if where == "" {
		return 0, nil
	}
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM timeline_events WHERE `+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: purge: count events: %w", err)
	}
	return n, nil
}

// PurgePreview reports the exact counts PurgeSessions would remove for sc,
// writing nothing.
func (s *Store) PurgePreview(sc PurgeScope) (PurgeCounts, error) {
	ids, where, args, err := s.purgeSelection(s.db, sc)
	if err != nil {
		return PurgeCounts{}, err
	}
	n, err := countEvents(s.db, where, args)
	if err != nil {
		return PurgeCounts{}, err
	}
	return PurgeCounts{Sessions: len(ids), Events: n}, nil
}

// PurgeSessions erases the timeline events of the sessions sc selects, in
// ONE transaction: drop timeline_events_no_delete, delete the events,
// recreate the trigger from its single definition, stamp each session's
// purged_at (so backfill never re-imports it) and append one purge_log row
// of counts. Session rows, backfill_cursors and records stay. It is a
// human-only power and refuses any other identity, changing nothing.
// Events referenced by records' evidence or event_cursor simply dangle;
// every reader tolerates that.
func (s *Store) PurgeSessions(sc PurgeScope, identity Identity) (PurgeCounts, error) {
	if identity.Kind != IdentityHuman {
		return PurgeCounts{}, ErrPurgeRequiresHuman
	}
	tx, err := s.db.Begin()
	if err != nil {
		return PurgeCounts{}, fmt.Errorf("store: purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ids, where, args, err := s.purgeSelection(tx, sc)
	if err != nil {
		return PurgeCounts{}, err
	}
	n, err := countEvents(tx, where, args)
	if err != nil {
		return PurgeCounts{}, err
	}
	now := tsToNanos(time.Now())
	if where != "" {
		if _, err := tx.Exec(`DROP TRIGGER timeline_events_no_delete`); err != nil {
			return PurgeCounts{}, fmt.Errorf("store: purge: drop trigger: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM timeline_events WHERE `+where, args...); err != nil { //nolint:gosec // where is assembled from fixed fragments in purgeSelection; every value is a bound arg
			return PurgeCounts{}, fmt.Errorf("store: purge: delete events: %w", err)
		}
		if _, err := tx.Exec(timelineEventsNoDeleteTriggerSQL); err != nil {
			return PurgeCounts{}, fmt.Errorf("store: purge: recreate trigger: %w", err)
		}
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE sessions SET purged_at = ? WHERE id = ? AND purged_at IS NULL`, now, id); err != nil {
			return PurgeCounts{}, fmt.Errorf("store: purge: stamp session %s: %w", id, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO purge_log (ts, scope, sessions, events) VALUES (?, ?, ?, ?)`,
		now, sc.String(), len(ids), n); err != nil {
		return PurgeCounts{}, fmt.Errorf("store: purge: log: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PurgeCounts{}, fmt.Errorf("store: purge: %w", err)
	}
	return PurgeCounts{Sessions: len(ids), Events: n}, nil
}

// SessionPurged reports whether session id carries purged_at.
func (s *Store) SessionPurged(id string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE id = ? AND purged_at IS NOT NULL`, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: session purged %s: %w", id, err)
	}
	return true, nil
}

// HarnessSessionPurged reports whether a session with this
// harness_session_id was purged — a transcript never imported (no cursor)
// but whose live-captured session was purged must not come back either.
func (s *Store) HarnessSessionPurged(harnessSessionID string) (bool, error) {
	if harnessSessionID == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE harness_session_id = ? AND purged_at IS NOT NULL LIMIT 1`,
		harnessSessionID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: harness session purged %s: %w", harnessSessionID, err)
	}
	return true, nil
}
