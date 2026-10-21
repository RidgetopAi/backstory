package store

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
)

// fillMissingRemotes repairs stores written before UpsertProject stopped
// clobbering remote_url: every git-repo project (key == its git common dir)
// whose remote_url is NULL gets it from that repo's config, so it gains a sync
// identity. Run once per Open, not lazily at identity time, and it only ever
// fills NULLs (no backup needed). Each project is decided ONCE: a repo whose
// config was read and had no remote (or git refused) is stamped
// remote_checked_at and not asked again by later Opens — every CLI call and
// panel refresh is an Open, so re-asking would cost a git subprocess per
// remote-less project forever. The stamp gates only this repair: a live
// session start or backfill upsert that observes a remote still records it
// (UpsertProject never consults the stamp). A repo whose common dir has
// vanished is skipped without shelling out and without a stamp, so it is
// checked if the directory ever returns.
func (s *Store) fillMissingRemotes(git project.Git) error {
	if git == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT key, toplevel FROM projects
		WHERE remote_url IS NULL AND remote_checked_at IS NULL ORDER BY key`)
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
		if _, err := os.Stat(r.key); err != nil {
			continue
		}
		repo, ok := git.Repo(r.toplevel)
		if !ok || repo.RemoteURL == "" || filepath.Clean(repo.CommonDir) != filepath.Clean(r.key) {
			if _, err := s.db.Exec(`UPDATE projects SET remote_checked_at = ?
				WHERE key = ? AND remote_url IS NULL`, tsToNanos(time.Now()), r.key); err != nil {
				return fmt.Errorf("store: fill remotes: mark %s checked: %w", r.key, err)
			}
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
