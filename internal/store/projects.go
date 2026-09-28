package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Project is a row in projects. Deriving Key from git common dir + first
// remote (AGENT-CONTRACT.md §Project = git repository identity) is a later
// punch's job; UpsertProject just persists whatever key the caller passes.
type Project struct {
	Key          string
	GitCommonDir string
	RemoteURL    string
	Toplevel     string
	FirstSeen    time.Time
}

// UpsertProject inserts a project, or updates its identity fields if the
// key already exists. first_seen is preserved on update.
func (s *Store) UpsertProject(p Project) error {
	p.Key = s.canonicalizeProjectKey(p.Key)
	_, err := s.db.Exec(`INSERT INTO projects (key, git_common_dir, remote_url, toplevel, first_seen)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			git_common_dir = excluded.git_common_dir,
			remote_url     = excluded.remote_url,
			toplevel       = excluded.toplevel`,
		p.Key, nullable(p.GitCommonDir), nullable(p.RemoteURL), p.Toplevel, tsToNanos(p.FirstSeen))
	if err != nil {
		return fmt.Errorf("store: upsert project %s: %w", p.Key, err)
	}
	return nil
}

// ProjectsSeenSince returns the distinct project keys with at least one
// session started at or after since, ordered by key. projects.first_seen
// never advances past a project's first sighting (UpsertProject preserves
// it on update), so it cannot answer "seen recently" on its own — this
// reads activity off sessions.started_at instead, the signal `backstory
// group list`'s "ungrouped projects seen this week" needs.
func (s *Store) ProjectsSeenSince(since time.Time) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT project_key FROM sessions
		WHERE project_key IS NOT NULL AND started_at >= ?
		ORDER BY project_key`, tsToNanos(since))
	if err != nil {
		return nil, fmt.Errorf("store: projects seen since %s: %w", since, err)
	}
	defer func() { _ = rows.Close() }()

	out := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("store: scan project key: %w", err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: projects seen since %s: %w", since, err)
	}
	return out, nil
}

// AllProjectKeys returns every project key backstory has ever recorded a
// projects row for, ordered by key — `backstory group list --json`'s full
// `projects` array candidate set (task 4fe02e30 round 5): every project the
// group editor can offer to group, not just ProjectsSeenSince's "has a
// session started in the last week" subset (a project grouped once and
// quiet since must still show up so its group membership stays visible and
// editable).
func (s *Store) AllProjectKeys() ([]string, error) {
	rows, err := s.db.Query(`SELECT key FROM projects ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("store: all project keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("store: scan project key: %w", err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: all project keys: %w", err)
	}
	return out, nil
}

// GetProject reads back a single projects row by key. found is false when
// no session or backfill has ever upserted this key.
func (s *Store) GetProject(key string) (Project, bool, error) {
	var (
		p                    Project
		commonDir, remoteURL sql.NullString
		firstSeen            int64
	)
	err := s.db.QueryRow(`SELECT key, git_common_dir, remote_url, toplevel, first_seen
		FROM projects WHERE key = ?`, key).
		Scan(&p.Key, &commonDir, &remoteURL, &p.Toplevel, &firstSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, fmt.Errorf("store: get project %s: %w", key, err)
	}
	p.GitCommonDir = commonDir.String
	p.RemoteURL = remoteURL.String
	p.FirstSeen = tsFromNanos(firstSeen)
	return p, true, nil
}

// ActiveProjectKeys returns every distinct project_key with a session
// started, a timeline event, or a record inserted at or after since —
// This Week's "one row per project with activity in the last 7 days"
// (decision 9be5c1d5, task 56d8c63d). It returns whatever project_key
// values appear, workspace identities (project.IsWorkspaceKey) included:
// filtering those out is the caller's job, so this package keeps dealing
// only in opaque project_key strings rather than importing internal/project.
func (s *Store) ActiveProjectKeys(since time.Time) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT project_key FROM (
			SELECT project_key, started_at AS ts FROM sessions WHERE project_key IS NOT NULL
			UNION ALL
			SELECT s.project_key, e.ts FROM timeline_events e
				JOIN sessions s ON s.id = e.session_id
				WHERE s.project_key IS NOT NULL
			UNION ALL
			SELECT project_key, ts FROM records WHERE project_key IS NOT NULL
		)
		WHERE ts >= ?`, tsToNanos(since))
	if err != nil {
		return nil, fmt.Errorf("store: active project keys since %s: %w", since, err)
	}
	return scanIDs(rows)
}

// LastActivity returns the latest of projectKey's own session starts,
// timeline events, and record inserts — This Week's Where-you-left-off
// "last activity time" (task 56d8c63d). ok is false when projectKey has no
// activity of any of those three kinds at all.
// Activity after asOf is ignored, so the view is "as of" its own clock.
func (s *Store) LastActivity(projectKey string, asOf time.Time) (t time.Time, ok bool, err error) {
	var maxTS sql.NullInt64
	err = s.db.QueryRow(`
		SELECT MAX(ts) FROM (
			SELECT started_at AS ts FROM sessions WHERE project_key = ?
			UNION ALL
			SELECT e.ts FROM timeline_events e
				JOIN sessions s ON s.id = e.session_id
				WHERE s.project_key = ?
			UNION ALL
			SELECT ts FROM records WHERE project_key = ?
		) WHERE ts <= ?`, projectKey, projectKey, projectKey, tsToNanos(asOf)).Scan(&maxTS)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: last activity for project %s: %w", projectKey, err)
	}
	if !maxTS.Valid {
		return time.Time{}, false, nil
	}
	return tsFromNanos(maxTS.Int64), true, nil
}

// LatestSessionForProject returns projectKey's most recently started
// session, any origin, live or ended — This Week's Where-you-left-off "cwd
// to reopen" (task 56d8c63d): the cwd rarely moves week to week, so the
// most recent session overall, not just one started within the window, is
// the right one to reopen.
func (s *Store) LatestSessionForProject(projectKey string) (Session, bool, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM sessions WHERE project_key = ?
		ORDER BY started_at DESC LIMIT 1`, projectKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("store: latest session for project %s: %w", projectKey, err)
	}
	sess, err := s.getSession(id)
	if err != nil {
		return Session{}, false, err
	}
	return sess, true, nil
}

// getSession reads back a single sessions row by id, the shared scan logic
// LatestSessionForProject uses.
func (s *Store) getSession(id string) (Session, error) {
	var (
		sess              Session
		harnessSessionID  sql.NullString
		pid               sql.NullInt64
		projectKey        sql.NullString
		workspace, window sql.NullString
		startedAt         int64
		origin            string
	)
	err := s.db.QueryRow(`SELECT id, agent, harness_session_id, pid, cwd, project_key, workspace, window, started_at, origin
		FROM sessions WHERE id = ?`, id).
		Scan(&sess.ID, &sess.Agent, &harnessSessionID, &pid, &sess.CWD,
			&projectKey, &workspace, &window, &startedAt, &origin)
	if err != nil {
		return Session{}, fmt.Errorf("store: get session %s: %w", id, err)
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
	return sess, nil
}
