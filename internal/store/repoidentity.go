package store

import (
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
)

// repoIdentityMerge is one legacy project key that names a repo whose
// canonical key (project.Key: the git common dir alone) is different.
type repoIdentityMerge struct{ legacy, canonical string }

// repoIdentityMerges lists every projects row keyed by a pre-029485ae repo
// spelling — "<common-dir>|<remote>", or the toplevel path of a remote-less
// repo — together with the common-dir key it must move to. Rows already at
// their canonical key, workspace identities and non-repo directories are
// skipped, which is what makes the merge idempotent.
func (s *Store) repoIdentityMerges(git project.Git) ([]repoIdentityMerge, error) {
	rows, err := s.db.Query(`SELECT key, git_common_dir, toplevel FROM projects ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("store: repo identity: list projects: %w", err)
	}
	type row struct {
		key, commonDir, toplevel string
	}
	var all []row
	for rows.Next() {
		var r row
		var gcd sql.NullString
		if err := rows.Scan(&r.key, &gcd, &r.toplevel); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: repo identity: scan project: %w", err)
		}
		r.commonDir = gcd.String
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("store: repo identity: list projects: %w", err)
	}
	_ = rows.Close()

	var out []repoIdentityMerge
	for _, r := range all {
		if project.IsWorkspaceKey(r.key) {
			continue
		}
		if _, isWorkspaceLegacy := s.legacyToCanonical[filepath.Clean(r.key)]; isWorkspaceLegacy {
			continue
		}
		canon := ""
		if dir, ok := project.LegacyRemoteKeyCommonDir(r.key); ok {
			canon = dir
		} else if r.commonDir != "" {
			// Remote-less key: the toplevel path, with the common dir recorded.
			if r.key == r.commonDir {
				continue
			}
			canon = r.commonDir
		} else if git != nil {
			repo, ok := git.Repo(r.key)
			if !ok || filepath.Clean(repo.Toplevel) != filepath.Clean(r.key) {
				continue
			}
			canon = repo.CommonDir
		}
		if canon != "" && canon != r.key {
			out = append(out, repoIdentityMerge{legacy: r.key, canonical: canon})
		}
	}
	return out, nil
}

// mergeRepoIdentities moves every legacy-spelled repo project (see
// repoIdentityMerges) onto its git-common-dir key across projects, sessions,
// records and project_groups, in one transaction, backing up the pre-merge
// state first — the same machinery as the workspace sweep. Idempotent: a
// merged legacy projects row is deleted, so a later Open finds nothing to do
// and rewrites nothing. The legacy spellings are also registered in
// legacyToCanonical so this Store never mints a second row for them.
func (s *Store) mergeRepoIdentities(git project.Git) error {
	merges, err := s.repoIdentityMerges(git)
	if err != nil {
		return err
	}
	if len(merges) == 0 {
		return nil
	}
	sort.Slice(merges, func(i, j int) bool { return merges[i].legacy < merges[j].legacy })

	backupPath := s.path + ".pre-identity-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if _, err := s.db.Exec(`VACUUM INTO ?`, backupPath); err != nil {
		return fmt.Errorf("store: repo identity: backup to %s: %w", backupPath, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: repo identity: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, m := range merges {
		if err := sweepOneTarget(tx, m.legacy, m.canonical, true); err != nil {
			return fmt.Errorf("store: repo identity %s -> %s: %w", m.legacy, m.canonical, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: repo identity: commit: %w", err)
	}
	for _, m := range merges {
		s.legacyToCanonical[filepath.Clean(m.legacy)] = m.canonical
	}
	log.Printf("store: repo identity: merged %d legacy project key(s) onto their git common dir", len(merges))
	return nil
}
