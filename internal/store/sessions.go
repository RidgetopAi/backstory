package store

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SessionOrigin is sessions.origin.
type SessionOrigin string

const (
	OriginLive       SessionOrigin = "live"
	OriginBackfilled SessionOrigin = "backfilled"
)

// StartSessionParams is the input to StartSession. ID is optional: an empty
// ID is minted by the store (sessions are always daemon-minted, never
// caller-chosen for a live session), but a backfill importer may supply a
// stable id of its own.
type StartSessionParams struct {
	ID               string
	Agent            string
	HarnessSessionID string
	PID              *int
	CWD              string
	ProjectKey       string
	Workspace        string
	Window           string
	StartedAt        time.Time
	Origin           SessionOrigin
}

// StartSession inserts a session row and returns its id.
func (s *Store) StartSession(p StartSessionParams) (string, error) {
	id := p.ID
	if id == "" {
		id = uuid.NewString()
	}
	var pid any
	if p.PID != nil {
		pid = *p.PID
	}
	_, err := s.db.Exec(`INSERT INTO sessions
		(id, agent, harness_session_id, pid, cwd, project_key, workspace, window, started_at, origin)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, p.Agent, nullable(p.HarnessSessionID), pid, p.CWD, nullable(p.ProjectKey),
		nullable(p.Workspace), nullable(p.Window), p.StartedAt.UTC().Format(time.RFC3339Nano), string(p.Origin))
	if err != nil {
		return "", fmt.Errorf("store: start session: %w", err)
	}
	return id, nil
}

// EndSession records a session's end time and exit kind.
func (s *Store) EndSession(id string, endedAt time.Time, exitKind string) error {
	res, err := s.db.Exec(`UPDATE sessions SET ended_at = ?, exit_kind = ? WHERE id = ?`,
		endedAt.UTC().Format(time.RFC3339Nano), nullable(exitKind), id)
	if err != nil {
		return fmt.Errorf("store: end session %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: end session %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: end session %s: not found", id)
	}
	return nil
}
