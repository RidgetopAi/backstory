package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
)

// resolveSweepTargets is Open's sweep set, legacy -> canonical, skipping any
// workspaceDirs entry project.Key treats as a git repo rather than a home.
func resolveSweepTargets(workspaceDirs []string, git project.Git) map[string]string {
	targets := make(map[string]string, len(workspaceDirs))
	for _, w := range workspaceDirs {
		w = filepath.Clean(w)
		if _, ok := targets[w]; ok {
			continue
		}
		if canon := project.Key(w, git, workspaceDirs); project.IsWorkspaceKey(canon) {
			targets[w] = canon
		}
	}
	return targets
}

// canonicalizeProjectKey resolves key to its canonical spelling: key itself,
// unless key (cleaned) is a configured workspace dir's legacy plain form —
// every store write and read funnels through this, so a caller still
// spelling a workspace folder the old way never mints a second row for it.
func (s *Store) canonicalizeProjectKey(key string) string {
	if canon, ok := s.legacyToCanonical[filepath.Clean(key)]; ok {
		return canon
	}
	// A pre-029485ae "<common-dir>|<remote>" spelling names the same repo as
	// its bare common dir.
	if dir, ok := project.LegacyRemoteKeyCommonDir(key); ok {
		return dir
	}
	return key
}

// sweepLegacyWorkspaceKeys merges every target's legacy rows into its
// canonical spelling across all four tables, in one transaction, backing up
// the pre-sweep state first. Idempotent: once a target's legacy projects row
// is gone, nothing can reference that key (all three other tables
// foreign-key into projects(key)), so a later Open rewrites nothing.
func (s *Store) sweepLegacyWorkspaceKeys(targets map[string]string) error {
	// Pre-check so a fully-swept store skips the backup and transaction.
	var anyLegacy bool
	for legacy := range targets {
		var exists int
		err := s.db.QueryRow(`SELECT 1 FROM projects WHERE key = ?`, legacy).Scan(&exists)
		if err == nil {
			anyLegacy = true
			break
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: sweep: check for legacy rows: %w", err)
		}
	}
	if !anyLegacy {
		return nil
	}

	// VACUUM INTO cannot run inside a transaction, so back up before BEGIN.
	backupPath := s.path + ".pre-sweep-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if _, err := s.db.Exec(`VACUUM INTO ?`, backupPath); err != nil {
		return fmt.Errorf("store: sweep: backup to %s: %w", backupPath, err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: sweep: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has run
	for legacy, canonical := range targets {
		if err := sweepOneTarget(tx, legacy, canonical, false); err != nil {
			return fmt.Errorf("store: sweep %s -> %s: %w", legacy, canonical, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: sweep: commit: %w", err)
	}
	return nil
}

// sweepOneTarget merges legacy into canonical inside tx. Every statement is
// scoped by `WHERE project_key = legacy`, so a target with no legacy row is
// a no-op throughout. The canonical projects row must exist before sessions/
// project_groups can point at it (both foreign-key into projects(key)); the
// legacy projects row is deleted last, once nothing references it.
//
// repoIdentity selects the shape of a newly minted canonical row: false is
// the workspace sweep (no common dir, toplevel = the canonical key); true is
// the repo-identity merge (task 029485ae), where canonical is the repo's git
// common dir and the legacy row's remote_url and toplevel carry over.
func sweepOneTarget(tx *sql.Tx, legacy, canonical string, repoIdentity bool) error {
	// Upsert the canonical row, first_seen sourced from the legacy row and
	// kept at whichever of the two is earlier.
	upsert := `INSERT INTO projects (key, git_common_dir, remote_url, toplevel, first_seen)
		SELECT ?, NULL, NULL, ?, first_seen FROM projects WHERE key = ?`
	args := []any{canonical, canonical, legacy}
	if repoIdentity {
		upsert = `INSERT INTO projects (key, git_common_dir, remote_url, toplevel, first_seen)
		SELECT ?, ?, remote_url, toplevel, first_seen FROM projects WHERE key = ?`
		args = []any{canonical, canonical, legacy}
	}
	if _, err := tx.Exec(upsert+`
		ON CONFLICT(key) DO UPDATE SET first_seen = excluded.first_seen WHERE excluded.first_seen < projects.first_seen`,
		args...); err != nil {
		return fmt.Errorf("upsert canonical project row: %w", err)
	}

	sessRes, err := tx.Exec(`UPDATE sessions SET project_key = ? WHERE project_key = ?`, canonical, legacy)
	if err != nil {
		return fmt.Errorf("rewrite sessions: %w", err)
	}
	sessionsRewritten, err := sessRes.RowsAffected()
	if err != nil {
		return fmt.Errorf("rewrite sessions: %w", err)
	}

	var recordsToRewrite int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM records WHERE project_key = ?`, legacy).Scan(&recordsToRewrite); err != nil {
		return fmt.Errorf("count legacy records: %w", err)
	}
	if recordsToRewrite > 0 {
		if _, err := tx.Exec(`DROP TRIGGER records_no_update`); err != nil {
			return fmt.Errorf("drop records_no_update: %w", err)
		}
		if _, err := tx.Exec(`UPDATE records SET project_key = ? WHERE project_key = ?`, canonical, legacy); err != nil {
			return fmt.Errorf("rewrite records: %w", err)
		}
		if _, err := tx.Exec(recordsNoUpdateTriggerSQL); err != nil {
			return fmt.Errorf("recreate records_no_update: %w", err)
		}
	}

	groupsMerged, err := sweepProjectGroups(tx, legacy, canonical)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE key = ?`, legacy); err != nil {
		return fmt.Errorf("delete legacy project row: %w", err)
	}

	log.Printf("store: sweep %s -> %s: projects=1 sessions=%d records=%d project_groups=%d",
		legacy, canonical, sessionsRewritten, recordsToRewrite, groupsMerged)
	return nil
}

// sweepProjectGroups merges legacy's and canonical's project_groups rows:
// the newer set_at wins, moved under canonical; the other is dropped.
// Returns the total rows touched, for the sweep's own log.
func sweepProjectGroups(tx *sql.Tx, legacy, canonical string) (int64, error) {
	dropLegacy, err := tx.Exec(`DELETE FROM project_groups WHERE project_key = ? AND EXISTS (
		SELECT 1 FROM project_groups c WHERE c.project_key = ?
		AND c.set_at >= (SELECT set_at FROM project_groups WHERE project_key = ?))`,
		legacy, canonical, legacy)
	if err != nil {
		return 0, fmt.Errorf("drop superseded legacy project_groups row: %w", err)
	}
	dropCanon, err := tx.Exec(`DELETE FROM project_groups WHERE project_key = ?
		AND EXISTS (SELECT 1 FROM project_groups WHERE project_key = ?)`, canonical, legacy)
	if err != nil {
		return 0, fmt.Errorf("drop superseded canonical project_groups row: %w", err)
	}
	move, err := tx.Exec(`UPDATE project_groups SET project_key = ? WHERE project_key = ?`, canonical, legacy)
	if err != nil {
		return 0, fmt.Errorf("move winning legacy project_groups row: %w", err)
	}
	var total int64
	for _, res := range []sql.Result{dropLegacy, dropCanon, move} {
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
