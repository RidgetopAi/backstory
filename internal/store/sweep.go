package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
)

// sweepTarget is one configured workspace dir Open's sweep merges: legacy
// is the plain path a pre-79f7b20e build wrote for it (before project.Key
// started prefixing a workspace identity with "workspace:"); canonical is
// project.Key's own output for the same dir today.
type sweepTarget struct {
	legacy    string
	canonical string
}

// resolveSweepTargets computes Open's sweep set: one entry per
// workspaceDirs entry project.Key itself treats as a workspace identity
// rather than a git repo (task d65ef8ff clause 6 — a workspace dir that IS
// a git repo keeps its own repo key forever; project.Key(dir, git, dirs)
// still returns that repo key, not "workspace:"+dir, so this never treats
// it as a sweep target and it is never rewritten). Duplicate workspaceDirs
// entries collapse to one target.
func resolveSweepTargets(workspaceDirs []string, git project.Git) []sweepTarget {
	seen := make(map[string]bool, len(workspaceDirs))
	var out []sweepTarget
	for _, w := range workspaceDirs {
		w = filepath.Clean(w)
		if seen[w] {
			continue
		}
		seen[w] = true
		canon := project.Key(w, git, workspaceDirs)
		if !project.IsWorkspaceKey(canon) {
			continue
		}
		out = append(out, sweepTarget{legacy: w, canonical: canon})
	}
	return out
}

// legacyToCanonicalMap is targets indexed by their own legacy spelling —
// Store.canonicalizeProjectKey's lookup table.
func legacyToCanonicalMap(targets []sweepTarget) map[string]string {
	m := make(map[string]string, len(targets))
	for _, t := range targets {
		m[t.legacy] = t.canonical
	}
	return m
}

// canonicalizeProjectKey resolves key to its canonical spelling: key
// itself, unless key (cleaned) IS one of the store's configured workspace
// dirs' legacy plain form, in which case the "workspace:"-prefixed key
// Open's own sweep already merged that folder's history into. Every store
// write and read that takes a project key funnels through this ONE
// function (task d65ef8ff) — UpsertProject, StartSession,
// InsertRecordWithEdges, SetProjectGroup, ClearProjectGroup, GroupOf, and
// every project-scoped query — so a caller that still spells a workspace
// folder the old way (a human typing the old path on the CLI, a stale
// daemon that has not yet restarted after an upgrade) can never mint a
// second row for it: the write lands under the same canonical key Open's
// sweep already established, and a read by the old spelling finds exactly
// what a read by the new one finds.
func (s *Store) canonicalizeProjectKey(key string) string {
	if canon, ok := s.legacyToCanonical[filepath.Clean(key)]; ok {
		return canon
	}
	return key
}

// sweepLegacyWorkspaceKeys merges every target's legacy rows into its
// canonical spelling, across projects/sessions/records/project_groups, in
// ONE transaction — Open's own one-spelling-per-workspace-folder pass (task
// d65ef8ff). It is idempotent: once a target's legacy projects row is gone
// (this sweep's own last step, or a previous Open's), nothing references
// that key anymore — sessions.project_key and records.project_key are both
// foreign keys into projects(key), and nothing else in this codebase
// deletes a projects row — so a later Open finds no legacy row for any
// target, rewrites nothing, and writes no backup.
func (s *Store) sweepLegacyWorkspaceKeys(targets []sweepTarget) error {
	if len(targets) == 0 {
		return nil
	}

	legacyKeys := make([]string, len(targets))
	for i, t := range targets {
		legacyKeys[i] = t.legacy
	}
	anyLegacy, err := s.anyProjectRowExists(legacyKeys)
	if err != nil {
		return fmt.Errorf("store: sweep: check for legacy rows: %w", err)
	}
	if !anyLegacy {
		return nil
	}

	// Back up the pre-sweep state BEFORE any row is rewritten. VACUUM INTO
	// cannot run inside an explicit transaction, so this happens on its own,
	// ahead of the sweep's own BEGIN below.
	backupPath := s.path + ".pre-sweep-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if _, err := s.db.Exec(`VACUUM INTO ?`, backupPath); err != nil {
		return fmt.Errorf("store: sweep: backup to %s: %w", backupPath, err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: sweep: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has run

	for _, t := range targets {
		if err := sweepOneTarget(tx, t); err != nil {
			return fmt.Errorf("store: sweep %s -> %s: %w", t.legacy, t.canonical, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: sweep: commit: %w", err)
	}
	return nil
}

// anyProjectRowExists reports whether any of keys still has a projects row
// — sweepLegacyWorkspaceKeys' cheap pre-check for "is there anything to
// sweep at all", so a fully-swept store (every Open after the first) skips
// the backup and the transaction entirely rather than opening one to find
// zero rows to touch.
func (s *Store) anyProjectRowExists(keys []string) (bool, error) {
	placeholders := make([]string, len(keys))
	args := make([]any, len(keys))
	for i, k := range keys {
		placeholders[i] = "?"
		args[i] = k
	}
	//nolint:gosec // the concatenated part is only "?" placeholders (one per key), every value is still bound as a query arg below
	query := `SELECT COUNT(*) FROM projects WHERE key IN (` + strings.Join(placeholders, ",") + `)`
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// sweepOneTarget merges t.legacy's rows into t.canonical inside tx, in the
// order the FIX requires: (a) the canonical projects row (earliest
// first_seen) must exist before (b)/(d) can point sessions/project_groups
// at it (both foreign-key into projects(key)); (c) records only drops and
// recreates records_no_update when there is actually a legacy row to
// rewrite; (e) the legacy projects row is deleted last, once nothing still
// references it.
func sweepOneTarget(tx *sql.Tx, t sweepTarget) error {
	legacy, legacyOK, err := getProjectTx(tx, t.legacy)
	if err != nil {
		return fmt.Errorf("read legacy project row: %w", err)
	}
	if !legacyOK {
		// Nothing can reference a projects row that does not exist
		// (sessions.project_key, records.project_key and
		// project_groups.project_key are all foreign keys into
		// projects(key)), so there is nothing to merge for this target.
		return nil
	}

	canonical, canonicalOK, err := getProjectTx(tx, t.canonical)
	if err != nil {
		return fmt.Errorf("read canonical project row: %w", err)
	}
	switch {
	case !canonicalOK:
		if _, err := tx.Exec(`INSERT INTO projects (key, git_common_dir, remote_url, toplevel, first_seen)
			VALUES (?, NULL, NULL, ?, ?)`, t.canonical, t.canonical, tsToNanos(legacy.FirstSeen)); err != nil {
			return fmt.Errorf("insert canonical project row: %w", err)
		}
	case legacy.FirstSeen.Before(canonical.FirstSeen):
		if _, err := tx.Exec(`UPDATE projects SET first_seen = ? WHERE key = ?`,
			tsToNanos(legacy.FirstSeen), t.canonical); err != nil {
			return fmt.Errorf("advance canonical project row's first_seen: %w", err)
		}
	}

	sessRes, err := tx.Exec(`UPDATE sessions SET project_key = ? WHERE project_key = ?`, t.canonical, t.legacy)
	if err != nil {
		return fmt.Errorf("rewrite sessions: %w", err)
	}
	sessionsRewritten, err := sessRes.RowsAffected()
	if err != nil {
		return fmt.Errorf("rewrite sessions: %w", err)
	}

	var recordsToRewrite int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM records WHERE project_key = ?`, t.legacy).Scan(&recordsToRewrite); err != nil {
		return fmt.Errorf("count legacy records: %w", err)
	}
	if recordsToRewrite > 0 {
		if _, err := tx.Exec(`DROP TRIGGER records_no_update`); err != nil {
			return fmt.Errorf("drop records_no_update: %w", err)
		}
		if _, err := tx.Exec(`UPDATE records SET project_key = ? WHERE project_key = ?`, t.canonical, t.legacy); err != nil {
			return fmt.Errorf("rewrite records: %w", err)
		}
		if _, err := tx.Exec(recordsNoUpdateTriggerSQL); err != nil {
			return fmt.Errorf("recreate records_no_update: %w", err)
		}
	}

	groupsMerged, err := sweepProjectGroups(tx, t.legacy, t.canonical)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM projects WHERE key = ?`, t.legacy); err != nil {
		return fmt.Errorf("delete legacy project row: %w", err)
	}

	log.Printf("store: sweep %s -> %s: projects=1 sessions=%d records=%d project_groups=%d",
		t.legacy, t.canonical, sessionsRewritten, recordsToRewrite, groupsMerged)
	return nil
}

// getProjectTx reads back a single projects row by key inside tx — the same
// shape Store.GetProject reads, but usable from within the sweep's own
// transaction rather than a fresh connection.
func getProjectTx(tx *sql.Tx, key string) (Project, bool, error) {
	var (
		p                    Project
		commonDir, remoteURL sql.NullString
		firstSeen            int64
	)
	err := tx.QueryRow(`SELECT key, git_common_dir, remote_url, toplevel, first_seen
		FROM projects WHERE key = ?`, key).
		Scan(&p.Key, &commonDir, &remoteURL, &p.Toplevel, &firstSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, err
	}
	p.GitCommonDir = commonDir.String
	p.RemoteURL = remoteURL.String
	p.FirstSeen = tsFromNanos(firstSeen)
	return p, true, nil
}

// sweepGroupRow is one project_groups row's mergeable fields.
type sweepGroupRow struct {
	groupName string
	setAt     time.Time
}

func getGroupTx(tx *sql.Tx, key string) (sweepGroupRow, bool, error) {
	var (
		g      sweepGroupRow
		setAt  int64
		exists bool
	)
	err := tx.QueryRow(`SELECT group_name, set_at FROM project_groups WHERE project_key = ?`, key).Scan(&g.groupName, &setAt)
	if errors.Is(err, sql.ErrNoRows) {
		return sweepGroupRow{}, false, nil
	}
	if err != nil {
		return sweepGroupRow{}, false, err
	}
	exists = true
	g.setAt = tsFromNanos(setAt)
	return g, exists, nil
}

// sweepProjectGroups merges legacy's and canonical's project_groups rows
// (task d65ef8ff clause 5): the newer set_at wins, moved (or left in place)
// under canonical; the other is dropped. A project is in at most one group
// (project_key is project_groups' primary key), so this can never leave two
// rows for the same folder. Returns 1 when a row existed under legacy to
// merge, else 0.
func sweepProjectGroups(tx *sql.Tx, legacy, canonical string) (int, error) {
	legacyGroup, legacyOK, err := getGroupTx(tx, legacy)
	if err != nil {
		return 0, fmt.Errorf("read legacy project_groups row: %w", err)
	}
	if !legacyOK {
		return 0, nil
	}

	canonicalGroup, canonicalOK, err := getGroupTx(tx, canonical)
	if err != nil {
		return 0, fmt.Errorf("read canonical project_groups row: %w", err)
	}

	switch {
	case !canonicalOK:
		if _, err := tx.Exec(`UPDATE project_groups SET project_key = ? WHERE project_key = ?`, canonical, legacy); err != nil {
			return 0, fmt.Errorf("move legacy project_groups row: %w", err)
		}
	case legacyGroup.setAt.After(canonicalGroup.setAt):
		if _, err := tx.Exec(`DELETE FROM project_groups WHERE project_key = ?`, canonical); err != nil {
			return 0, fmt.Errorf("drop superseded canonical project_groups row: %w", err)
		}
		if _, err := tx.Exec(`UPDATE project_groups SET project_key = ? WHERE project_key = ?`, canonical, legacy); err != nil {
			return 0, fmt.Errorf("move winning legacy project_groups row: %w", err)
		}
	default:
		if _, err := tx.Exec(`DELETE FROM project_groups WHERE project_key = ?`, legacy); err != nil {
			return 0, fmt.Errorf("drop superseded legacy project_groups row: %w", err)
		}
	}
	return 1, nil
}
