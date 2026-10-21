package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
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
	// A rename another Store (process) made since this one opened.
	if canon, ok := s.persistedAlias(key); ok {
		return canon
	}
	// A pre-029485ae "<common-dir>|<remote>" spelling names the same repo as
	// its bare common dir.
	if dir, ok := project.LegacyRemoteKeyCommonDir(key); ok {
		return dir
	}
	return key
}

// maxAliasHops bounds persistedAlias's chain walk (a key re-keyed twice).
const maxAliasHops = 8

// persistedAlias resolves key through project_key_aliases, the cross-process
// record of sweeps and merges. An alias is ignored while a projects row
// exists under key itself: that spelling is live again (a repo's .git was
// removed and the plain dir re-registered), so it must not be redirected.
// A read error reads as "no alias": canonicalization has no error path, and
// the write that follows reports any real database failure itself.
func (s *Store) persistedAlias(key string) (string, bool) {
	cur, found := key, false
	for i := 0; i < maxAliasHops; i++ {
		var canon string
		err := s.db.QueryRow(`SELECT canonical_key FROM project_key_aliases
			WHERE legacy_key = ? AND NOT EXISTS (SELECT 1 FROM projects WHERE key = ?)`,
			cur, cur).Scan(&canon)
		if err != nil || canon == cur {
			break
		}
		cur, found = canon, true
	}
	return cur, found
}

// CanonicalProjectKey is the key a write under key lands on: key itself
// unless a sweep or merge (in any process) renamed it.
func (s *Store) CanonicalProjectKey(key string) string { return s.canonicalizeProjectKey(key) }

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
	log.Printf("store: sweep: merged legacy workspace key(s) (backup %s, run by %s)", backupPath, processName())
	return nil
}

// processName is the running command's name, for the sweep/merge log lines:
// the daemon logs to its own log, a CLI command to its stderr, and either way
// the line says which process ran it.
func processName() string { return filepath.Base(os.Args[0]) }

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
	var legacyRows int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM projects WHERE key = ?`, legacy).Scan(&legacyRows); err != nil {
		return fmt.Errorf("check legacy project row: %w", err)
	}
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
	if err := sweepProjectLinks(tx, legacy, canonical); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE key = ?`, legacy); err != nil {
		return fmt.Errorf("delete legacy project row: %w", err)
	}
	if legacyRows == 0 {
		return nil // nothing was renamed, so nothing to record or log
	}
	// Record the rename for other processes' Stores, and repoint aliases that
	// targeted the legacy key (a chain) at the new canonical.
	if _, err := tx.Exec(`UPDATE project_key_aliases SET canonical_key = ? WHERE canonical_key = ?`, canonical, legacy); err != nil {
		return fmt.Errorf("repoint aliases: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO project_key_aliases (legacy_key, canonical_key, created_at) VALUES (?, ?, ?)
		ON CONFLICT(legacy_key) DO UPDATE SET canonical_key = excluded.canonical_key, created_at = excluded.created_at`,
		legacy, canonical, tsToNanos(time.Now())); err != nil {
		return fmt.Errorf("record alias: %w", err)
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

// sweepProjectLinks merges legacy's and canonical's project_links rows the
// way sweepProjectGroups does: the newer set_at wins, moved under canonical.
func sweepProjectLinks(tx *sql.Tx, legacy, canonical string) error {
	if _, err := tx.Exec(`DELETE FROM project_links WHERE local_key = ? AND EXISTS (
		SELECT 1 FROM project_links c WHERE c.local_key = ?
		AND c.set_at >= (SELECT set_at FROM project_links WHERE local_key = ?))`,
		legacy, canonical, legacy); err != nil {
		return fmt.Errorf("drop superseded legacy project_links row: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM project_links WHERE local_key = ?
		AND EXISTS (SELECT 1 FROM project_links WHERE local_key = ?)`, canonical, legacy); err != nil {
		return fmt.Errorf("drop superseded canonical project_links row: %w", err)
	}
	if _, err := tx.Exec(`UPDATE project_links SET local_key = ? WHERE local_key = ?`, canonical, legacy); err != nil {
		return fmt.Errorf("move winning legacy project_links row: %w", err)
	}
	return nil
}
