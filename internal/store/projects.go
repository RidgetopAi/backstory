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
