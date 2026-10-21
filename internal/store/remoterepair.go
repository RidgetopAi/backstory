package store

import (
	"database/sql"
	"fmt"
	"log"
	"path/filepath"

	"github.com/RidgetopAi/backstory/internal/project"
)

// fillMissingRemotes repairs stores written before UpsertProject stopped
// clobbering remote_url: every git-repo project (key == its git common dir)
// whose remote_url is NULL gets it from that repo's config, so it gains a sync
// identity. Run once per Open, not lazily at identity time: it is a handful of
// git calls only for rows still missing a remote, and it only ever fills NULLs
// (no backup needed). Idempotent — a filled row is no longer selected, and a
// repo with no remote or that has vanished from disk stays NULL.
func (s *Store) fillMissingRemotes(git project.Git) error {
	if git == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT key, toplevel FROM projects
		WHERE remote_url IS NULL ORDER BY key`)
	if err != nil {
		return fmt.Errorf("store: fill remotes: list projects: %w", err)
	}
	type row struct{ key, toplevel string }
	var todo []row
	for rows.Next() {
		var r row
		var top sql.NullString
		if err := rows.Scan(&r.key, &top); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: fill remotes: scan project: %w", err)
		}
		r.toplevel = top.String
		todo = append(todo, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("store: fill remotes: list projects: %w", err)
	}
	_ = rows.Close()

	filled := 0
	for _, r := range todo {
		if project.IsWorkspaceKey(r.key) || !filepath.IsAbs(r.key) {
			continue
		}
		repo, ok := git.Repo(r.toplevel)
		if !ok || repo.RemoteURL == "" || filepath.Clean(repo.CommonDir) != filepath.Clean(r.key) {
			continue
		}
		if err := s.UpsertProject(Project{Key: r.key, GitCommonDir: repo.CommonDir, RemoteURL: repo.RemoteURL, Toplevel: r.toplevel}); err != nil {
			return err
		}
		filled++
	}
	if filled > 0 {
		log.Printf("store: filled remote_url for %d git project(s) from their repo config (run by %s)", filled, processName())
	}
	return nil
}
