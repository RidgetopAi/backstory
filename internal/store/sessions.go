package store

import (
	"database/sql"
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
		nullable(p.Workspace), nullable(p.Window), tsToNanos(p.StartedAt), string(p.Origin))
	if err != nil {
		return "", fmt.Errorf("store: start session: %w", err)
	}
	return id, nil
}

// Session is a row read back from sessions.
type Session struct {
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

// LiveSessionsInProject returns every live session in projectKey (origin
// 'live', no ended_at), most recently started first — the "who else is live
// here" half of status (AGENT-CONTRACT.md §The five tools) and the
// SessionStart block's coordination slot.
func (s *Store) LiveSessionsInProject(projectKey string) ([]Session, error) {
	k1, k2 := projectKeyIN(projectKey)
	rows, err := s.db.Query(`SELECT id, agent, harness_session_id, pid, cwd, workspace, window, started_at, origin
		FROM sessions WHERE project_key IN (?, ?) AND ended_at IS NULL AND origin = ? ORDER BY started_at DESC`,
		k1, k2, string(OriginLive))
	if err != nil {
		return nil, fmt.Errorf("store: live sessions in project %s: %w", projectKey, err)
	}
	defer func() { _ = rows.Close() }()

	sessions := []Session{}
	for rows.Next() {
		var (
			sess              Session
			harnessSessionID  sql.NullString
			pid               sql.NullInt64
			workspace, window sql.NullString
			startedAt         int64
			origin            string
		)
		if err := rows.Scan(&sess.ID, &sess.Agent, &harnessSessionID, &pid, &sess.CWD,
			&workspace, &window, &startedAt, &origin); err != nil {
			return nil, fmt.Errorf("store: scan session: %w", err)
		}
		sess.ProjectKey = projectKey
		sess.HarnessSessionID = harnessSessionID.String
		if pid.Valid {
			p := int(pid.Int64)
			sess.PID = &p
		}
		sess.Workspace = workspace.String
		sess.Window = window.String
		sess.StartedAt = tsFromNanos(startedAt)
		sess.Origin = SessionOrigin(origin)
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: live sessions in project %s: %w", projectKey, err)
	}
	return sessions, nil
}

// LiveSessionByHarnessSessionID returns the still-live (origin 'live',
// ended_at IS NULL) session whose harness_session_id equals sessionID, if
// one exists. The Claude backfill importer uses this to attach to a run
// that is still being captured live instead of minting a second,
// backfilled-origin session for the same run (task 25b74537) — matching on
// harness_session_id alone, never pid, since the importer never observes a
// pid at all.
func (s *Store) LiveSessionByHarnessSessionID(sessionID string) (Session, bool, error) {
	if sessionID == "" {
		return Session{}, false, nil
	}
	row := s.db.QueryRow(`SELECT id, agent, harness_session_id, pid, cwd, project_key, workspace, window, started_at, origin
		FROM sessions WHERE harness_session_id = ? AND ended_at IS NULL AND origin = ? LIMIT 1`,
		sessionID, string(OriginLive))

	var (
		sess              Session
		harnessSessionID  sql.NullString
		pid               sql.NullInt64
		projectKey        sql.NullString
		workspace, window sql.NullString
		startedAt         int64
		origin            string
	)
	if err := row.Scan(&sess.ID, &sess.Agent, &harnessSessionID, &pid, &sess.CWD,
		&projectKey, &workspace, &window, &startedAt, &origin); err != nil {
		if err == sql.ErrNoRows {
			return Session{}, false, nil
		}
		return Session{}, false, fmt.Errorf("store: live session for harness_session_id %s: %w", sessionID, err)
	}
	sess.HarnessSessionID = harnessSessionID.String
	if pid.Valid {
		p := int(pid.Int64)
		sess.PID = &p
	}
	sess.ProjectKey = projectKey.String
	sess.Workspace = workspace.String
	sess.Window = window.String
	sess.StartedAt = tsFromNanos(startedAt)
	sess.Origin = SessionOrigin(origin)
	return sess, true, nil
}

// SessionOrigin returns session id's origin — a caller deciding whether it
// may end a session (e.g. the Claude backfill importer, which must never
// end a still-live-origin session: that lifecycle belongs exclusively to
// the daemon, task 25b74537) needs this without reading back the whole row.
func (s *Store) SessionOrigin(id string) (SessionOrigin, error) {
	var origin string
	err := s.db.QueryRow(`SELECT origin FROM sessions WHERE id = ?`, id).Scan(&origin)
	if err != nil {
		return "", fmt.Errorf("store: session origin %s: %w", id, err)
	}
	return SessionOrigin(origin), nil
}

// SessionCWD returns the cwd session id was started in. The daemon's
// session-end path needs the ENDED session's own cwd, not the cwd of
// whichever connection happened to trigger the end (a registry sweep ends
// other, dead harnesses' sessions — task c2573b35).
func (s *Store) SessionCWD(id string) (string, error) {
	var cwd string
	if err := s.db.QueryRow(`SELECT cwd FROM sessions WHERE id = ?`, id).Scan(&cwd); err != nil {
		return "", fmt.Errorf("store: session cwd %s: %w", id, err)
	}
	return cwd, nil
}

// EndSession records a session's end time and exit kind.
func (s *Store) EndSession(id string, endedAt time.Time, exitKind string) error {
	res, err := s.db.Exec(`UPDATE sessions SET ended_at = ?, exit_kind = ? WHERE id = ?`,
		tsToNanos(endedAt), nullable(exitKind), id)
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
