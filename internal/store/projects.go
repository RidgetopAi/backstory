package store

import (
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
