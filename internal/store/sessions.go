package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/RidgetopAi/backstory/internal/ident"
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
	// ParentSessionID links a subagent session back to the session that
	// spawned it (decision 3e14db82, task e9cb97dd's Codex importer: a
	// rollout whose session_meta carries a parent_thread_id gets its own
	// full session rather than sharing the parent's, unlike Claude's
	// Task-tool subagents). Empty for every session with no parent.
	ParentSessionID string
}

// StartSession inserts a session row and returns its id.
func (s *Store) StartSession(p StartSessionParams) (string, error) {
	id := p.ID
	if id == "" {
		id = uuid.NewString()
	}
	p.ProjectKey = s.canonicalizeProjectKey(p.ProjectKey)
	var pid any
	if p.PID != nil {
		pid = *p.PID
	}
	_, err := s.db.Exec(`INSERT INTO sessions
		(id, agent, harness_session_id, pid, cwd, project_key, workspace, window, started_at, origin, parent_session_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, p.Agent, nullable(p.HarnessSessionID), pid, p.CWD, nullable(p.ProjectKey),
		nullable(p.Workspace), nullable(p.Window), tsToNanos(p.StartedAt), string(p.Origin),
		nullable(p.ParentSessionID))
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
	ParentSessionID  string
}

// LiveSessionsInProject returns every live session in projectKey (origin
// 'live', no ended_at), most recently started first — the "who else is live
// here" half of status (AGENT-CONTRACT.md §The five tools) and the
// SessionStart block's coordination slot.
func (s *Store) LiveSessionsInProject(projectKey string) ([]Session, error) {
	projectKey = s.canonicalizeProjectKey(projectKey)
	rows, err := s.db.Query(`SELECT id, agent, harness_session_id, pid, cwd, workspace, window, started_at, origin
		FROM sessions WHERE project_key = ? AND ended_at IS NULL AND origin = ? ORDER BY started_at DESC`,
		projectKey, string(OriginLive))
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

// RunSessionByHarnessSessionID returns the session already recorded for the
// run (agent, harnessSessionID), whether it is still live or has ended, so an
// importer re-reading a transcript attaches to it instead of minting a second
// session for the same run (task a757b754: backfill used to match only live
// sessions, so a daemon restart re-imported every finished run). A live-origin
// row wins over an ended one, then the earliest started. ended reports
// whether the matched session already has an ended_at.
func (s *Store) RunSessionByHarnessSessionID(agent, harnessSessionID string) (sess Session, ended, found bool, err error) {
	if agent == "" || harnessSessionID == "" {
		return Session{}, false, false, nil
	}
	var (
		pid               sql.NullInt64
		projectKey        sql.NullString
		workspace, window sql.NullString
		startedAt         int64
		origin            string
		endedAt           sql.NullInt64
		hsid              sql.NullString
	)
	row := s.db.QueryRow(`SELECT id, agent, harness_session_id, pid, cwd, project_key, workspace, window, started_at, origin, ended_at
		FROM sessions WHERE agent = ? AND harness_session_id = ?
		ORDER BY (origin = ?) DESC, started_at ASC, rowid ASC LIMIT 1`,
		agent, harnessSessionID, string(OriginLive))
	if err := row.Scan(&sess.ID, &sess.Agent, &hsid, &pid, &sess.CWD,
		&projectKey, &workspace, &window, &startedAt, &origin, &endedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, false, false, nil
		}
		return Session{}, false, false, fmt.Errorf("store: run session for %s/%s: %w", agent, harnessSessionID, err)
	}
	sess.HarnessSessionID = hsid.String
	if pid.Valid {
		p := int(pid.Int64)
		sess.PID = &p
	}
	sess.ProjectKey = projectKey.String
	sess.Workspace = workspace.String
	sess.Window = window.String
	sess.StartedAt = tsFromNanos(startedAt)
	sess.Origin = SessionOrigin(origin)
	return sess, endedAt.Valid, true, nil
}

// SessionHasEventKind reports whether session id already carries an event of
// kind — importers attaching to an existing session use it to avoid
// re-recording the session.start the run already has.
func (s *Store) SessionHasEventKind(id, kind string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM timeline_events WHERE session_id = ? AND kind = ? LIMIT 1`, id, kind).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: session %s has event %s: %w", id, kind, err)
	}
	return true, nil
}

// SessionByHarnessSessionID returns the session whose harness_session_id
// equals sessionID, any origin and whether it has ended — unlike
// LiveSessionByHarnessSessionID (which exists to attach to a run still being
// captured live), this is the Codex importer's parent lookup (task
// e9cb97dd): a subagent rollout's parent_thread_id names another rollout's
// own session_meta.id, and that parent session is always backfilled, never
// live, and normally already ended by the time the subagent is imported.
// found is false when no session carries that harness_session_id, which the
// caller treats as "no parent to link", never an error.
func (s *Store) SessionByHarnessSessionID(sessionID string) (Session, bool, error) {
	if sessionID == "" {
		return Session{}, false, nil
	}
	row := s.db.QueryRow(`SELECT id, agent, harness_session_id, pid, cwd, project_key, workspace, window, started_at, origin
		FROM sessions WHERE harness_session_id = ? LIMIT 1`, sessionID)

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
		if err == sql.ErrNoRows { //nolint:errorlint // database/sql returns sql.ErrNoRows verbatim from QueryRow.Scan, never wrapped
			return Session{}, false, nil
		}
		return Session{}, false, fmt.Errorf("store: session for harness_session_id %s: %w", sessionID, err)
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

// SessionCountForProject counts every session ever observed for projectKey,
// any origin, ended or still live — recall's empty-project response cites
// this count so an agent can tell "no records yet" (sessions observed, zero
// records) apart from "wrong project" (task b172e778, real use 2026-09-28:
// "recall with no query on a new project returned nothing, and the agent
// could not tell 'no history yet' from 'wrong project'").
func (s *Store) SessionCountForProject(projectKey string) (int, error) {
	projectKey = s.canonicalizeProjectKey(projectKey)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE project_key = ?`, projectKey).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: session count for project %s: %w", projectKey, err)
	}
	return n, nil
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

// LiveSessionIDsLeftOpen returns the id of every live-origin session row that
// has no ended_at, oldest first. The daemon's start-up sweep reads it to end
// each such row through the same path a live end takes (task 78ca0350). Rows
// of every other origin (backfilled transcripts) are not listed.
func (s *Store) LiveSessionIDsLeftOpen() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE ended_at IS NULL AND origin = ? ORDER BY started_at, id`,
		string(OriginLive))
	if err != nil {
		return nil, fmt.Errorf("store: live sessions left open: %w", err)
	}
	return scanIDs(rows)
}

// AgentActivity is one known harness's footprint across a set of sessions:
// its newest activity instant and how many of its sessions were active in
// the window.
type AgentActivity struct {
	Agent        string
	LastActivity time.Time
	SessionCount int
}

// ProjectSessionIDs returns the id of every session whose project_key is
// projectKey.
func (s *Store) ProjectSessionIDs(projectKey string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE project_key = ?`, projectKey)
	if err != nil {
		return nil, fmt.Errorf("store: session ids for project %s: %w", projectKey, err)
	}
	return scanIDs(rows)
}

// AgentActivityForSessions groups ids by agent, newest last_activity first
// (agent name breaks exact ties). A session's activity is its start, its
// timeline events, and its records, bounded above by asOf (the same rule as
// sessionLastActivity); a session counts only when that is at or after
// since. Sessions sharing (agent, harness_session_id) count once (task
// a757b754). Sessions whose agent is not in ident.KnownHarnesses (zero-length
// 'unknown' socket callers) are excluded.
func (s *Store) AgentActivityForSessions(ids []string, since, asOf time.Time) ([]AgentActivity, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	idPH := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	agentPH := strings.TrimSuffix(strings.Repeat("?,", len(ident.KnownHarnesses)), ",")
	//nolint:gosec // only "?" placeholders are concatenated; every value is bound
	query := `
		WITH act AS (
			SELECT COALESCE(NULLIF(harness_session_id, ''), id) AS sid, agent, started_at AS ts FROM sessions
				WHERE id IN (` + idPH + `) AND agent IN (` + agentPH + `)
			UNION ALL
			SELECT COALESCE(NULLIF(s.harness_session_id, ''), s.id), s.agent, e.ts FROM timeline_events e
				JOIN sessions s ON s.id = e.session_id
				WHERE e.session_id IN (` + idPH + `) AND s.agent IN (` + agentPH + `)
			UNION ALL
			SELECT COALESCE(NULLIF(s.harness_session_id, ''), s.id), s.agent, r.ts FROM records r
				JOIN sessions s ON s.id = r.session_id
				WHERE r.session_id IN (` + idPH + `) AND s.agent IN (` + agentPH + `)
		), per AS (
			SELECT sid, agent, MAX(ts) AS m FROM act WHERE ts <= ? GROUP BY sid, agent
		)
		SELECT agent, MAX(m), COUNT(*) FROM per WHERE m >= ?
		GROUP BY agent ORDER BY MAX(m) DESC, agent ASC`
	idArgs := make([]any, len(ids))
	for i, id := range ids {
		idArgs[i] = id
	}
	agentArgs := make([]any, len(ident.KnownHarnesses))
	for i, a := range ident.KnownHarnesses {
		agentArgs[i] = a
	}
	var full []any
	for i := 0; i < 3; i++ {
		full = append(full, idArgs...)
		full = append(full, agentArgs...)
	}
	full = append(full, tsToNanos(asOf), tsToNanos(since))
	rows, err := s.db.Query(query, full...)
	if err != nil {
		return nil, fmt.Errorf("store: agent activity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AgentActivity
	for rows.Next() {
		var (
			a  AgentActivity
			ts int64
		)
		if err := rows.Scan(&a.Agent, &ts, &a.SessionCount); err != nil {
			return nil, fmt.Errorf("store: agent activity: %w", err)
		}
		a.LastActivity = tsFromNanos(ts)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ShellSessionIDs returns the id of every session minted for an interactive
// shell's captured commands (agent ident.HarnessShell). Readers that count
// sessions as units of work use it to leave those out, including the
// per-command shell sessions older daemons left on disk.
func (s *Store) ShellSessionIDs() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE agent = ?`, ident.HarnessShell)
	if err != nil {
		return nil, fmt.Errorf("store: shell session ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: shell session ids: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SessionAgent returns the agent of session id, "" when id is empty or
// names no session.
func (s *Store) SessionAgent(id string) (string, error) {
	if id == "" {
		return "", nil
	}
	var agent string
	err := s.db.QueryRow(`SELECT agent FROM sessions WHERE id = ?`, id).Scan(&agent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: session agent %s: %w", id, err)
	}
	return agent, nil
}
