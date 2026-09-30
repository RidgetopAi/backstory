package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
)

// sweepT1/sweepT2 are this file's two fixed timestamps: t1 is always
// earlier, so "earliest first_seen wins" and "newer set_at wins" tests have
// an unambiguous expected direction.
var (
	sweepT1 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sweepT2 = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
)

// fakeSweepGit is a project.Git whose Repo answers are configured per
// directory — resolveSweepTargets' own clause 6 test needs a workspace dir
// that IS a git repo (Repo(w) ok=true), which every other sweep test needs
// to answer false for (an ordinary workspace dir, never a repo).
type fakeSweepGit struct {
	repos map[string]project.Repo
}

func (g fakeSweepGit) Repo(cwd string) (project.Repo, bool) {
	r, ok := g.repos[filepath.Clean(cwd)]
	return r, ok
}

func (g fakeSweepGit) State(string) (project.State, bool) { return project.State{}, false }

func rawInsertProject(t *testing.T, db *sql.DB, key, toplevel string, firstSeen time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO projects (key, git_common_dir, remote_url, toplevel, first_seen)
		VALUES (?, NULL, NULL, ?, ?)`, key, toplevel, tsToNanos(firstSeen)); err != nil {
		t.Fatalf("raw insert project %s: %v", key, err)
	}
}

func rawInsertSession(t *testing.T, db *sql.DB, id, key, cwd string, startedAt time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO sessions (id, agent, cwd, project_key, started_at, origin)
		VALUES (?, 'claude', ?, ?, ?, 'live')`, id, cwd, key, tsToNanos(startedAt)); err != nil {
		t.Fatalf("raw insert session %s: %v", id, err)
	}
}

func rawInsertRecord(t *testing.T, db *sql.DB, id, key, sessionID string, kind RecordKind, ts time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO records (id, ts, kind, tier, text, about, session_id, project_key, evidence, event_cursor)
		VALUES (?, ?, ?, 'agent-declared', ?, '[]', ?, ?, '[]', 0)`,
		id, tsToNanos(ts), string(kind), "text for "+id, sessionID, key); err != nil {
		t.Fatalf("raw insert record %s: %v", id, err)
	}
}

func rawInsertGroup(t *testing.T, db *sql.DB, key, groupName string, setAt time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO project_groups (project_key, group_name, set_at) VALUES (?, ?, ?)`,
		key, groupName, tsToNanos(setAt)); err != nil {
		t.Fatalf("raw insert group %s: %v", key, err)
	}
}

func countWhere(t *testing.T, db *sql.DB, table, column, value string) int {
	t.Helper()
	var n int
	//nolint:gosec // table/column are this file's own literals, never caller input
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, value).Scan(&n); err != nil {
		t.Fatalf("count %s.%s = %s: %v", table, column, value, err)
	}
	return n
}

// seedLegacySweepFixture builds the punch's DONE WHEN clause 1 fixture: a
// legacy plain W row AND a workspace:W row, each already present in
// projects/sessions/records/project_groups (a handoff and a decision under
// each in records), plus a repo key nested under W and a key outside any
// workspace — both of which the sweep's exact-match rule must leave byte
// for byte alone. Everything is seeded via raw SQL against a migrated-but-
// unswept store (workspaceDirs nil), the only way to get a legacy row
// stored at all: the moment workspaceDirs includes W, every write funnels
// through Store.canonicalizeProjectKey, which would rewrite a legacy write
// to its canonical form before it ever reached the table.
func seedLegacySweepFixture(t *testing.T) (dbPath, w string) {
	t.Helper()
	dbPath = tempDBPath(t)
	w = filepath.Join(t.TempDir(), "projects")
	wsKey := "workspace:" + w

	seed, err := Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("Open (seed): %v", err)
	}
	db := seed.DB()

	rawInsertProject(t, db, w, w, sweepT1)
	rawInsertProject(t, db, wsKey, w, sweepT2)

	rawInsertSession(t, db, "sess-legacy-1", w, w, sweepT1)
	rawInsertSession(t, db, "sess-legacy-2", w, w, sweepT1.Add(time.Hour))
	rawInsertSession(t, db, "sess-ws-1", wsKey, w, sweepT2)

	rawInsertRecord(t, db, "rec-legacy-handoff", w, "sess-legacy-1", KindHandoff, sweepT1)
	rawInsertRecord(t, db, "rec-legacy-decision", w, "sess-legacy-1", KindDecision, sweepT1)
	rawInsertRecord(t, db, "rec-ws-handoff", wsKey, "sess-ws-1", KindHandoff, sweepT2)
	rawInsertRecord(t, db, "rec-ws-decision", wsKey, "sess-ws-1", KindDecision, sweepT2)

	rawInsertGroup(t, db, w, "old", sweepT1)
	rawInsertGroup(t, db, wsKey, "new", sweepT2)

	// A real repo living under W, key text-prefixed by W: exact-match only,
	// must never be swept just because it starts with W's own string.
	repoKey := w + "/omarcade/.git"
	rawInsertProject(t, db, repoKey, w+"/omarcade", sweepT1)
	rawInsertSession(t, db, "sess-repo", repoKey, w+"/omarcade", sweepT1)
	rawInsertRecord(t, db, "rec-repo-handoff", repoKey, "sess-repo", KindHandoff, sweepT1)

	// A key entirely outside any configured workspace.
	rawInsertProject(t, db, "/home/u/Work", "/home/u/Work", sweepT1)

	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}
	return dbPath, w
}

// TestSweepMergesLegacyIntoCanonicalAcrossAllFourTables is DONE WHEN clause
// 1: after Open, count(project_key = W) is 0 in all four tables;
// workspace:W's sessions/records counts equal the pre-sweep sums;
// PRAGMA foreign_key_check is empty; records_no_update still exists and
// still refuses a text mutation.
func TestSweepMergesLegacyIntoCanonicalAcrossAllFourTables(t *testing.T) {
	dbPath, w := seedLegacySweepFixture(t)
	wsKey := "workspace:" + w

	s, err := Open(dbPath, []string{w}, fakeSweepGit{})
	if err != nil {
		t.Fatalf("Open (sweep): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	db := s.DB()

	for _, table := range []string{"projects", "sessions", "records", "project_groups"} {
		col := "key"
		if table != "projects" {
			col = "project_key"
		}
		if n := countWhere(t, db, table, col, w); n != 0 {
			t.Errorf("%s: count(%s = %q) = %d, want 0", table, col, w, n)
		}
	}

	if n := countWhere(t, db, "sessions", "project_key", wsKey); n != 3 {
		t.Errorf("sessions: count(project_key = %q) = %d, want 3 (2 legacy + 1 workspace, summed)", wsKey, n)
	}
	if n := countWhere(t, db, "records", "project_key", wsKey); n != 4 {
		t.Errorf("records: count(project_key = %q) = %d, want 4 (2 legacy + 2 workspace, summed)", wsKey, n)
	}

	// Earliest first_seen wins (legacy's sweepT1, earlier than the
	// pre-existing canonical row's sweepT2).
	var firstSeenNanos int64
	if err := db.QueryRow(`SELECT first_seen FROM projects WHERE key = ?`, wsKey).Scan(&firstSeenNanos); err != nil {
		t.Fatalf("read canonical first_seen: %v", err)
	}
	if got := tsFromNanos(firstSeenNanos); !got.Equal(sweepT1) {
		t.Errorf("canonical project first_seen = %s, want %s (the earlier, legacy value)", got, sweepT1)
	}

	// project_groups: exactly one merged row, the newer (canonical) one won.
	if n := countWhere(t, db, "project_groups", "project_key", wsKey); n != 1 {
		t.Errorf("project_groups: count(project_key = %q) = %d, want exactly 1 (merged)", wsKey, n)
	}
	var groupName string
	if err := db.QueryRow(`SELECT group_name FROM project_groups WHERE project_key = ?`, wsKey).Scan(&groupName); err != nil {
		t.Fatalf("read merged group: %v", err)
	}
	if groupName != "new" {
		t.Errorf("merged project_groups group_name = %q, want %q (canonical's set_at was later)", groupName, "new")
	}

	// The repo key and the outside key are byte for byte untouched — exact
	// match only, never confused with W just because the repo key's text
	// happens to start with W's own string.
	repoKey := w + "/omarcade/.git"
	assertProjectRowUnchanged(t, db, repoKey, w+"/omarcade", sweepT1)
	assertProjectRowUnchanged(t, db, "/home/u/Work", "/home/u/Work", sweepT1)
	if n := countWhere(t, db, "sessions", "project_key", repoKey); n != 1 {
		t.Errorf("sessions: count(project_key = %q) = %d, want 1 (untouched)", repoKey, n)
	}
	if n := countWhere(t, db, "records", "project_key", repoKey); n != 1 {
		t.Errorf("records: count(project_key = %q) = %d, want 1 (untouched)", repoKey, n)
	}

	assertNoForeignKeyViolations(t, db)
	assertRecordsNoUpdateStillEnforced(t, db, "rec-ws-handoff")
}

func assertProjectRowUnchanged(t *testing.T, db *sql.DB, key, wantToplevel string, wantFirstSeen time.Time) {
	t.Helper()
	var toplevel string
	var firstSeenNanos int64
	if err := db.QueryRow(`SELECT toplevel, first_seen FROM projects WHERE key = ?`, key).Scan(&toplevel, &firstSeenNanos); err != nil {
		t.Fatalf("read project %s: %v", key, err)
	}
	if toplevel != wantToplevel {
		t.Errorf("project %s toplevel = %q, want unchanged %q", key, toplevel, wantToplevel)
	}
	if got := tsFromNanos(firstSeenNanos); !got.Equal(wantFirstSeen) {
		t.Errorf("project %s first_seen = %s, want unchanged %s", key, got, wantFirstSeen)
	}
}

func assertNoForeignKeyViolations(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var violations int
	for rows.Next() {
		violations++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	if violations != 0 {
		t.Errorf("foreign_key_check found %d violation(s), want 0", violations)
	}
}

func assertRecordsNoUpdateStillEnforced(t *testing.T, db *sql.DB, recordID string) {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = 'records_no_update'`).Scan(&name); err != nil {
		t.Fatalf("records_no_update trigger missing from sqlite_master: %v", err)
	}
	if _, err := db.Exec(`UPDATE records SET text = 'mutated after sweep' WHERE id = ?`, recordID); err == nil {
		t.Fatal("UPDATE records SET text ... succeeded after the sweep, want records_no_update to RAISE(ABORT)")
	}
}

// TestSweepIsIdempotentAndBacksUpOnlyOnce is DONE WHEN clause 3: a second
// Open rewrites nothing and writes no new backup; the first Open that
// rewrote rows wrote exactly one <db>.pre-sweep-* backup that opens (read
// directly, never through store.Open, which would sweep it too) and holds
// the pre-sweep legacy rows.
func TestSweepIsIdempotentAndBacksUpOnlyOnce(t *testing.T) {
	dbPath, w := seedLegacySweepFixture(t)

	s1, err := Open(dbPath, []string{w}, fakeSweepGit{})
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	backups := globPreSweepBackups(t, dbPath)
	if len(backups) != 1 {
		t.Fatalf("pre-sweep backups after first Open = %v, want exactly 1", backups)
	}

	rawBackup, err := sql.Open("sqlite", "file:"+backups[0])
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer func() { _ = rawBackup.Close() }()
	if n := countWhere(t, rawBackup, "projects", "key", w); n != 1 {
		t.Errorf("backup: count(projects.key = %q) = %d, want 1 (the pre-sweep legacy row)", w, n)
	}
	if n := countWhere(t, rawBackup, "sessions", "project_key", w); n != 2 {
		t.Errorf("backup: count(sessions.project_key = %q) = %d, want 2 (pre-sweep)", w, n)
	}

	s2, err := Open(dbPath, []string{w}, fakeSweepGit{})
	if err != nil {
		t.Fatalf("Open (second): %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("close second: %v", err)
	}

	backupsAfter := globPreSweepBackups(t, dbPath)
	if len(backupsAfter) != 1 {
		t.Fatalf("pre-sweep backups after second Open = %v, want still exactly 1 (no new backup)", backupsAfter)
	}
}

func globPreSweepBackups(t *testing.T, dbPath string) []string {
	t.Helper()
	matches, err := filepath.Glob(dbPath + ".pre-sweep-*")
	if err != nil {
		t.Fatalf("glob pre-sweep backups: %v", err)
	}
	return matches
}

// TestSweepCatchesStaleWriterAfterFirstOpen is DONE WHEN clause 4: a raw-SQL
// insert of a legacy session and a legacy project_groups row after the
// first Open (simulating a daemon that has not yet restarted after an
// upgrade, still minting the plain key) leaves 0 legacy rows after the next
// Open.
func TestSweepCatchesStaleWriterAfterFirstOpen(t *testing.T) {
	dbPath, w := seedLegacySweepFixture(t)
	wsKey := "workspace:" + w

	s1, err := Open(dbPath, []string{w}, fakeSweepGit{})
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	raw, err := sql.Open("sqlite", dsn(dbPath))
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	// A stale writer re-creates the legacy projects row (its own
	// UpsertProject-equivalent, computing the pre-79f7b20e plain key) before
	// starting a session under it — sessions.project_key is a real foreign
	// key, so nothing could reference W without this.
	rawInsertProject(t, raw, w, w, sweepT2.Add(time.Hour))
	rawInsertSession(t, raw, "sess-stale", w, w, sweepT2.Add(time.Hour))
	rawInsertGroup(t, raw, w, "stale-group", sweepT2.Add(2*time.Hour))
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw connection: %v", err)
	}

	s2, err := Open(dbPath, []string{w}, fakeSweepGit{})
	if err != nil {
		t.Fatalf("Open (second, after stale writer): %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	db := s2.DB()

	if n := countWhere(t, db, "projects", "key", w); n != 0 {
		t.Errorf("projects: count(key = %q) = %d, want 0", w, n)
	}
	if n := countWhere(t, db, "sessions", "project_key", w); n != 0 {
		t.Errorf("sessions: count(project_key = %q) = %d, want 0", w, n)
	}
	if n := countWhere(t, db, "project_groups", "project_key", w); n != 0 {
		t.Errorf("project_groups: count(project_key = %q) = %d, want 0", w, n)
	}
	if n := countWhere(t, db, "sessions", "project_key", wsKey); n != 4 {
		t.Errorf("sessions: count(project_key = %q) = %d, want 4 (3 from round 1 + the stale writer's)", wsKey, n)
	}
}

// TestSweepGroupMergeNewerSetAtWins is DONE WHEN clause 5: legacy `old`@t1
// vs workspace `new`@t2>t1 merges to `new`; reversed timestamps merges to
// `old`.
func TestSweepGroupMergeNewerSetAtWins(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		legacyAt, canonicalAt time.Time
		wantGroup             string
	}{
		{"canonical newer wins", sweepT1, sweepT2, "new"},
		{"legacy newer wins (reversed timestamps)", sweepT2, sweepT1, "old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := tempDBPath(t)
			w := filepath.Join(t.TempDir(), "projects")
			wsKey := "workspace:" + w

			seed, err := Open(dbPath, nil, nil)
			if err != nil {
				t.Fatalf("Open (seed): %v", err)
			}
			db := seed.DB()
			rawInsertProject(t, db, w, w, sweepT1)
			rawInsertProject(t, db, wsKey, w, sweepT1)
			rawInsertGroup(t, db, w, "old", tc.legacyAt)
			rawInsertGroup(t, db, wsKey, "new", tc.canonicalAt)
			if err := seed.Close(); err != nil {
				t.Fatalf("close seed: %v", err)
			}

			s, err := Open(dbPath, []string{w}, fakeSweepGit{})
			if err != nil {
				t.Fatalf("Open (sweep): %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })

			var got string
			if err := s.DB().QueryRow(`SELECT group_name FROM project_groups WHERE project_key = ?`, wsKey).Scan(&got); err != nil {
				t.Fatalf("read merged group: %v", err)
			}
			if got != tc.wantGroup {
				t.Errorf("merged group_name = %q, want %q", got, tc.wantGroup)
			}
			if n := countWhere(t, s.DB(), "project_groups", "project_key", wsKey); n != 1 {
				t.Errorf("count(project_groups.project_key = %q) = %d, want exactly 1", wsKey, n)
			}
			if n := countWhere(t, s.DB(), "project_groups", "project_key", w); n != 0 {
				t.Errorf("count(project_groups.project_key = %q) = %d, want 0", w, n)
			}
		})
	}
}

// TestSweepSkipsWorkspaceDirThatIsItselfAGitRepo is DONE WHEN clause 6: a
// fake Git reporting Repo(W) ok means W is NOT rewritten (0 rows touched)
// and project.Key(W) still returns the plain W key — the writer's own rule
// (a workspace dir that is itself a git repo keeps its repo identity).
func TestSweepSkipsWorkspaceDirThatIsItselfAGitRepo(t *testing.T) {
	dbPath := tempDBPath(t)
	w := filepath.Join(t.TempDir(), "repo-workspace")

	seed, err := Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("Open (seed): %v", err)
	}
	db := seed.DB()
	rawInsertProject(t, db, w, w, sweepT1)
	rawInsertSession(t, db, "sess-repo", w, w, sweepT1)
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	git := fakeSweepGit{repos: map[string]project.Repo{
		w: {Toplevel: w, CommonDir: filepath.Join(w, ".git")},
	}}
	s, err := Open(dbPath, []string{w}, git)
	if err != nil {
		t.Fatalf("Open (sweep): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// W is a git repo: never swept to workspace:W. Its plain-path row is the
	// repo-identity merge's business (task 029485ae) and moves to W's common dir.
	repoKey := filepath.Join(w, ".git")
	if n := countWhere(t, s.DB(), "projects", "key", repoKey); n != 1 {
		t.Errorf("count(projects.key = %q) = %d, want 1 (merged onto the repo's common dir)", repoKey, n)
	}
	if n := countWhere(t, s.DB(), "sessions", "project_key", repoKey); n != 1 {
		t.Errorf("count(sessions.project_key = %q) = %d, want 1", repoKey, n)
	}
	if n := countWhere(t, s.DB(), "projects", "key", "workspace:"+w); n != 0 {
		t.Errorf("count(projects.key = %q) = %d, want 0: a git-repo workspace dir must never gain a workspace: row", "workspace:"+w, n)
	}

	if key := project.Key(w, git, []string{w}); key != repoKey {
		t.Errorf("project.Key(%s) = %q, want %q (repo identity, unaffected by workspace config)", w, key, w)
	}
}
