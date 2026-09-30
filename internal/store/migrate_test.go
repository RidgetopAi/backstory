package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wantTables are every table SCHEMA.md names plus the FTS5 index, expected
// to exist after Open on a fresh path.
var wantTables = []string{
	"schema_version",
	"projects",
	"sessions",
	"timeline_events",
	"records",
	"edges",
	"settings",
	"records_fts",
	"backfill_cursors",
	"project_groups",
}

func tableExists(t *testing.T, s *Store, name string) bool {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name).Scan(&n); err != nil {
		t.Fatalf("query sqlite_master for %s: %v", name, err)
	}
	return n > 0
}

func TestOpenFreshPathCreatesEverySchemaTable(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	for _, name := range wantTables {
		if !tableExists(t, s, name) {
			t.Errorf("table %s: not created", name)
		}
	}

	v, err := s.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("Version() = %d, want %d (SchemaVersion)", v, SchemaVersion)
	}

	var rows int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&rows); err != nil {
		t.Fatalf("count schema_version rows: %v", err)
	}
	if rows == 0 {
		t.Fatal("schema_version has no rows after a fresh Open")
	}
}

// TestReopenAppliesNothing proves the second Open on the same file is a
// no-op: identical schema_version row count and version, and every table
// from the first open is still there and empty.
func TestReopenAppliesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")

	first, err := Open(path, nil, nil)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	firstVersion, err := first.Version()
	if err != nil {
		t.Fatalf("first Version: %v", err)
	}
	var firstRows int
	if err := first.DB().QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&firstRows); err != nil {
		t.Fatalf("count schema_version rows: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	second := mustOpen(t, path)
	secondVersion, err := second.Version()
	if err != nil {
		t.Fatalf("second Version: %v", err)
	}
	var secondRows int
	if err := second.DB().QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&secondRows); err != nil {
		t.Fatalf("count schema_version rows: %v", err)
	}

	if secondVersion != firstVersion {
		t.Errorf("Version() after reopen = %d, want unchanged %d", secondVersion, firstVersion)
	}
	if secondRows != firstRows {
		t.Errorf("schema_version rows after reopen = %d, want unchanged %d", secondRows, firstRows)
	}

	for _, name := range wantTables {
		if !tableExists(t, second, name) {
			t.Errorf("table %s: missing after reopen", name)
		}
	}
}

// wantIntegerTSColumns are every ts-like column migration 0002 converts
// from RFC3339Nano TEXT to INTEGER unix nanoseconds (PLAN.md §Phase 1,
// critic T2 on 7c4dfc90). edges carries no timestamp column.
var wantIntegerTSColumns = []struct{ table, column string }{
	{"schema_version", "applied_at"},
	{"projects", "first_seen"},
	{"sessions", "started_at"},
	{"sessions", "ended_at"},
	{"timeline_events", "ts"},
	{"records", "ts"},
	{"records", "expires_at"},
	{"records", "tombstoned_at"},
}

func columnType(t *testing.T, s *Store, table, column string) (string, bool) {
	t.Helper()
	rows, err := s.DB().Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if name == column {
			return colType, true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	return "", false
}

// TestTsColumnsAreIntegerInLiveSchema is proof (2) for task 05b03d4a: every
// ts-like column is INTEGER in a freshly migrated database, not the
// RFC3339Nano TEXT v0 used (whose lexical comparison sorts a whole-second
// timestamp after one a fraction of a second later).
func TestTsColumnsAreIntegerInLiveSchema(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	for _, want := range wantIntegerTSColumns {
		colType, found := columnType(t, s, want.table, want.column)
		if !found {
			t.Errorf("%s.%s: column not found", want.table, want.column)
			continue
		}
		if colType != "INTEGER" {
			t.Errorf("%s.%s type = %q, want INTEGER", want.table, want.column, colType)
		}
	}
}

// buildV1Fixture creates a database at path with only migration 0001
// applied (SQLite executed directly, bypassing Store.migrate so migration
// 0002 is NOT applied), then seeds one RFC3339Nano-TEXT-timestamped row into
// each ts-bearing table — a stand-in for a real pre-0002 Backstory database
// on disk.
func buildV1Fixture(t *testing.T, path string) {
	t.Helper()

	ms, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	var m0001 migration
	for _, m := range ms {
		if m.version == 1 {
			m0001 = m
		}
	}
	if m0001.sql == "" {
		t.Fatal("migration version 1 not found among embedded migrations")
	}

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open v1 fixture: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, stmt := range splitStatements(m0001.sql) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply v1 schema: %v\nstatement: %s", err, stmt)
		}
	}

	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create v1 schema_version: %v", err)
	}
	nowText := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (1, ?)`, nowText); err != nil {
		t.Fatalf("seed v1 schema_version: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO projects (key, toplevel, first_seen) VALUES (?, ?, ?)`,
		"proj1", "/tmp/proj1", nowText); err != nil {
		t.Fatalf("seed v1 projects: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, agent, cwd, started_at, ended_at, origin) VALUES (?, ?, ?, ?, ?, ?)`,
		"sess1", "test-harness", "/tmp/proj1", nowText, nowText, "live"); err != nil {
		t.Fatalf("seed v1 sessions: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO timeline_events (ts, kind, source, payload) VALUES (?, ?, ?, ?)`,
		nowText, "probe", "daemon", "{}"); err != nil {
		t.Fatalf("seed v1 timeline_events: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO records (id, ts, kind, tier, text, expires_at, tombstoned_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"rec1", nowText, "note", "agent-declared", "v1 fixture record", nowText, nowText); err != nil {
		t.Fatalf("seed v1 records: %v", err)
	}
	// rec2 is NOT tombstoned: it exercises clause 3's requirement that a
	// live pre-migration record is still findable by SearchRecords after
	// migrating, i.e. records_fts's external-content rowid link to records
	// survives the create-copy-drop-rename rewrite. migration 0001's
	// records_ai AFTER INSERT trigger populates records_fts for it.
	if _, err := db.Exec(`INSERT INTO records (id, ts, kind, tier, text) VALUES (?, ?, ?, ?, ?)`,
		"rec2", nowText, "note", "agent-declared", "v1 fixture pre-migration searchable record"); err != nil {
		t.Fatalf("seed v1 records rec2: %v", err)
	}
}

// TestMigrationV2AppliesOnV1Fixture is proof (2) for task 05b03d4a:
// migration 0002 applies cleanly to a v1 database and preserves row counts
// — the rewrite is destructive to sub-second timestamp precision (the
// migration comment says so), never to rows.
func TestMigrationV2AppliesOnV1Fixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1fixture.db")
	buildV1Fixture(t, path)

	wantCounts := map[string]int{
		"projects":        1,
		"sessions":        1,
		"timeline_events": 1,
		"records":         2,
	}

	s := mustOpen(t, path)

	v, err := s.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("Version() after migrating a v1 fixture = %d, want %d (SchemaVersion)", v, SchemaVersion)
	}

	for table, want := range wantCounts {
		var got int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Errorf("%s row count after migrating a v1 fixture = %d, want %d (migration must preserve row counts)", table, got, want)
		}
	}

	for _, want := range wantIntegerTSColumns {
		colType, found := columnType(t, s, want.table, want.column)
		if !found {
			t.Errorf("%s.%s: column not found after migrating a v1 fixture", want.table, want.column)
			continue
		}
		if colType != "INTEGER" {
			t.Errorf("%s.%s type after migrating a v1 fixture = %q, want INTEGER", want.table, want.column, colType)
		}
	}

	rec, err := s.GetRecord("rec1")
	if err != nil {
		t.Fatalf("GetRecord(rec1) after migrating a v1 fixture: %v", err)
	}
	if rec.TS.IsZero() {
		t.Error("GetRecord(rec1).TS is zero after migrating a v1 fixture")
	}
	if rec.ExpiresAt == nil || rec.ExpiresAt.IsZero() {
		t.Error("GetRecord(rec1).ExpiresAt is nil/zero after migrating a v1 fixture")
	}
	if rec.TombstonedAt == nil || rec.TombstonedAt.IsZero() {
		t.Error("GetRecord(rec1).TombstonedAt is nil/zero after migrating a v1 fixture")
	}

	// Clause 3: a non-tombstoned pre-migration record (rec2) is still
	// returned by SearchRecords after migrating — proof that records_fts's
	// external-content rowid link to records survived the create-copy-
	// drop-rename rewrite of the records table.
	results, err := s.SearchRecords("searchable", 10)
	if err != nil {
		t.Fatalf("SearchRecords after migrating a v1 fixture: %v", err)
	}
	var foundRec2 bool
	for _, r := range results {
		if r.ID == "rec2" {
			foundRec2 = true
		}
	}
	if !foundRec2 {
		t.Errorf("SearchRecords(%q) after migrating a v1 fixture = %+v, want rec2 among the results (FTS rowid must survive the migration)", "searchable", results)
	}
}

// nulProjectKey is a stand-in for a real pre-0003 project_key: internal/
// project.Key used to join CommonDir and RemoteURL with a literal NUL byte.
const nulProjectKey = "/tmp/proj2/.git\x00git@example.com:ridgetopai/proj2.git"

// wantPrintableProjectKey is nulProjectKey rewritten with the printable "|"
// separator migration 0003 must produce.
const wantPrintableProjectKey = "/tmp/proj2/.git" // migration 3 yields "<dir>|<remote>"; Open then merges it onto the bare common dir (task 029485ae)

// buildV2Fixture creates a database at path with migrations 0001 and 0002
// applied (SQLite executed directly, bypassing Store.migrate so migration
// 0003 is NOT applied), then seeds a NUL-separated project_key into
// projects, sessions and records — a stand-in for a real pre-0003 Backstory
// database on disk.
func buildV2Fixture(t *testing.T, path string) {
	t.Helper()

	ms, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	var m0001, m0002 migration
	for _, m := range ms {
		switch m.version {
		case 1:
			m0001 = m
		case 2:
			m0002 = m
		}
	}
	if m0001.sql == "" || m0002.sql == "" {
		t.Fatal("migration version 1 or 2 not found among embedded migrations")
	}

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open v2 fixture: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, stmt := range splitStatements(m0001.sql) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply v1 schema: %v\nstatement: %s", err, stmt)
		}
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create v1 schema_version: %v", err)
	}
	nowNanos := time.Now().UTC().UnixNano()
	if _, err := db.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (1, ?)`, nowNanos); err != nil {
		t.Fatalf("seed v1 schema_version: %v", err)
	}

	for _, stmt := range splitStatements(m0002.sql) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply v2 schema: %v\nstatement: %s", err, stmt)
		}
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (2, ?)`, nowNanos); err != nil {
		t.Fatalf("seed v2 schema_version: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO projects (key, toplevel, first_seen) VALUES (?, ?, ?)`,
		nulProjectKey, "/tmp/proj2", nowNanos); err != nil {
		t.Fatalf("seed v2 projects: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, agent, cwd, project_key, started_at, ended_at, origin) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sess2", "test-harness", "/tmp/proj2", nulProjectKey, nowNanos, nowNanos, "live"); err != nil {
		t.Fatalf("seed v2 sessions: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO records (id, ts, kind, tier, text, project_key) VALUES (?, ?, ?, ?, ?, ?)`,
		"rec-v2", nowNanos, "note", "agent-declared", "v2 fixture record", nulProjectKey); err != nil {
		t.Fatalf("seed v2 records: %v", err)
	}
}

// TestMigrationV3RewritesNulProjectKeys is proof (3) for task e7951178: a v2
// fixture DB with NUL-separated keys migrates to v3 with the same row count
// and every key rewritten to the printable separator.
func TestMigrationV3RewritesNulProjectKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2fixture.db")
	buildV2Fixture(t, path)

	wantCounts := map[string]int{
		"projects": 1,
		"sessions": 1,
		"records":  1,
	}

	s := mustOpen(t, path)

	v, err := s.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("Version() after migrating a v2 fixture = %d, want %d (SchemaVersion)", v, SchemaVersion)
	}

	for table, want := range wantCounts {
		var got int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Errorf("%s row count after migrating a v2 fixture = %d, want %d (migration must preserve row counts)", table, got, want)
		}
	}

	var projectKey string
	if err := s.DB().QueryRow(`SELECT key FROM projects WHERE toplevel = ?`, "/tmp/proj2").Scan(&projectKey); err != nil {
		t.Fatalf("read projects.key after migrating a v2 fixture: %v", err)
	}
	if projectKey != wantPrintableProjectKey {
		t.Errorf("projects.key after migrating a v2 fixture = %q, want %q", projectKey, wantPrintableProjectKey)
	}
	if strings.ContainsRune(projectKey, 0) {
		t.Errorf("projects.key after migrating a v2 fixture still carries a NUL byte: %q", projectKey)
	}

	var sessionProjectKey string
	if err := s.DB().QueryRow(`SELECT project_key FROM sessions WHERE id = ?`, "sess2").Scan(&sessionProjectKey); err != nil {
		t.Fatalf("read sessions.project_key after migrating a v2 fixture: %v", err)
	}
	if sessionProjectKey != wantPrintableProjectKey {
		t.Errorf("sessions.project_key after migrating a v2 fixture = %q, want %q", sessionProjectKey, wantPrintableProjectKey)
	}

	rec, err := s.GetRecord("rec-v2")
	if err != nil {
		t.Fatalf("GetRecord(rec-v2) after migrating a v2 fixture: %v", err)
	}
	if rec.ProjectKey != wantPrintableProjectKey {
		t.Errorf("records.project_key after migrating a v2 fixture = %q, want %q", rec.ProjectKey, wantPrintableProjectKey)
	}

	// The append-only trigger the migration drops and recreates around its
	// own UPDATE must still guard records after migrating.
	_, err = s.DB().Exec(`UPDATE records SET text = 'mutated' WHERE id = ?`, "rec-v2")
	if err == nil {
		t.Fatal("UPDATE records.text after migrating a v2 fixture succeeded, want records_no_update to abort it")
	}
}

// TestRecordsHaveEventCursorColumn is proof (2) for task 214eb30e:
// records.event_cursor exists in a freshly migrated database.
func TestRecordsHaveEventCursorColumn(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	colType, found := columnType(t, s, "records", "event_cursor")
	if !found {
		t.Fatal("records.event_cursor: column not found")
	}
	if colType != "INTEGER" {
		t.Errorf("records.event_cursor type = %q, want INTEGER", colType)
	}
}

// buildV3Fixture creates a database at path with migrations 0001-0003
// applied (SQLite executed directly, bypassing Store.migrate so migration
// 0004 is NOT applied), then seeds three timeline_events and two records —
// one with a ts before every event (no preceding event: backfill must land
// on 0) and one with a ts between the second and third event (backfill must
// land on the second event's id, the nearest preceding one) — a stand-in
// for a real pre-0004 Backstory database on disk.
func buildV3Fixture(t *testing.T, path string) (event2ID int64) {
	t.Helper()

	ms, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	var m0001, m0002, m0003 migration
	for _, m := range ms {
		switch m.version {
		case 1:
			m0001 = m
		case 2:
			m0002 = m
		case 3:
			m0003 = m
		}
	}
	if m0001.sql == "" || m0002.sql == "" || m0003.sql == "" {
		t.Fatal("migration version 1, 2 or 3 not found among embedded migrations")
	}

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open v3 fixture: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, stmt := range splitStatements(m0001.sql) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply v1 schema: %v\nstatement: %s", err, stmt)
		}
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create v1 schema_version: %v", err)
	}
	nowNanos := time.Now().UTC().UnixNano()
	if _, err := db.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (1, ?)`, nowNanos); err != nil {
		t.Fatalf("seed v1 schema_version: %v", err)
	}
	for _, stmt := range splitStatements(m0002.sql) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply v2 schema: %v\nstatement: %s", err, stmt)
		}
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (2, ?)`, nowNanos); err != nil {
		t.Fatalf("seed v2 schema_version: %v", err)
	}
	for _, stmt := range splitStatements(m0003.sql) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply v3 schema: %v\nstatement: %s", err, stmt)
		}
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (3, ?)`, nowNanos); err != nil {
		t.Fatalf("seed v3 schema_version: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO projects (key, toplevel, first_seen) VALUES (?, ?, ?)`,
		"proj-v3", "/tmp/proj-v3", nowNanos); err != nil {
		t.Fatalf("seed v3 projects: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, agent, cwd, project_key, started_at, origin) VALUES (?, ?, ?, ?, ?, ?)`,
		"sess-v3", "test-harness", "/tmp/proj-v3", "proj-v3", nowNanos, "live"); err != nil {
		t.Fatalf("seed v3 sessions: %v", err)
	}

	base := time.Now().UTC()
	event1TS := base.UnixNano()
	event2TS := base.Add(time.Minute).UnixNano()
	event3TS := base.Add(2 * time.Minute).UnixNano()
	if _, err := db.Exec(`INSERT INTO timeline_events (ts, kind, session_id, source, payload) VALUES (?, 'probe', ?, 'shell', '{}')`,
		event1TS, "sess-v3"); err != nil {
		t.Fatalf("seed v3 event 1: %v", err)
	}
	res, err := db.Exec(`INSERT INTO timeline_events (ts, kind, session_id, source, payload) VALUES (?, 'probe', ?, 'shell', '{}')`,
		event2TS, "sess-v3")
	if err != nil {
		t.Fatalf("seed v3 event 2: %v", err)
	}
	event2ID, err = res.LastInsertId()
	if err != nil {
		t.Fatalf("v3 event 2 last insert id: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO timeline_events (ts, kind, session_id, source, payload) VALUES (?, 'probe', ?, 'shell', '{}')`,
		event3TS, "sess-v3"); err != nil {
		t.Fatalf("seed v3 event 3: %v", err)
	}

	// rec-before-all: ts earlier than every event -> no preceding event ->
	// backfill must land on 0.
	if _, err := db.Exec(`INSERT INTO records (id, ts, kind, tier, text, project_key) VALUES (?, ?, ?, ?, ?, ?)`,
		"rec-before-all", base.Add(-time.Hour).UnixNano(), "note", "agent-declared", "before every event", "proj-v3"); err != nil {
		t.Fatalf("seed v3 rec-before-all: %v", err)
	}
	// rec-between-2-and-3: ts between event 2 and event 3 -> nearest
	// preceding event is event 2 -> backfill must land on event2ID.
	if _, err := db.Exec(`INSERT INTO records (id, ts, kind, tier, text, project_key) VALUES (?, ?, ?, ?, ?, ?)`,
		"rec-between-2-and-3", base.Add(90*time.Second).UnixNano(), "note", "agent-declared", "between event 2 and 3", "proj-v3"); err != nil {
		t.Fatalf("seed v3 rec-between-2-and-3: %v", err)
	}

	return event2ID
}

// TestMigrationV4BackfillsEventCursorFromNearestPrecedingEvent is proof (2)
// for task 214eb30e: migration 0004 applies cleanly to a v3 fixture,
// preserves row counts, and backfills every existing record's event_cursor
// from the nearest preceding timeline event by ts, best effort (0004's own
// migration comment says so; there is no sequence position recorded at
// insert time for a pre-0004 row).
func TestMigrationV4BackfillsEventCursorFromNearestPrecedingEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3fixture.db")
	event2ID := buildV3Fixture(t, path)

	wantCounts := map[string]int{
		"projects":        1,
		"sessions":        1,
		"timeline_events": 3,
		"records":         2,
	}

	s := mustOpen(t, path)

	v, err := s.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("Version() after migrating a v3 fixture = %d, want %d (SchemaVersion)", v, SchemaVersion)
	}

	for table, want := range wantCounts {
		var got int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Errorf("%s row count after migrating a v3 fixture = %d, want %d (migration must preserve row counts)", table, got, want)
		}
	}

	recBeforeAll, err := s.GetRecord("rec-before-all")
	if err != nil {
		t.Fatalf("GetRecord(rec-before-all) after migrating a v3 fixture: %v", err)
	}
	if recBeforeAll.EventCursor != 0 {
		t.Errorf("rec-before-all.EventCursor after migrating a v3 fixture = %d, want 0 (no preceding event)", recBeforeAll.EventCursor)
	}

	recBetween, err := s.GetRecord("rec-between-2-and-3")
	if err != nil {
		t.Fatalf("GetRecord(rec-between-2-and-3) after migrating a v3 fixture: %v", err)
	}
	if recBetween.EventCursor != event2ID {
		t.Errorf("rec-between-2-and-3.EventCursor after migrating a v3 fixture = %d, want %d (event 2, the nearest preceding event)", recBetween.EventCursor, event2ID)
	}

	// The append-only trigger the migration drops and recreates around its
	// own backfill UPDATE must still guard records, including the new
	// column, after migrating.
	_, err = s.DB().Exec(`UPDATE records SET event_cursor = 999 WHERE id = ?`, "rec-before-all")
	if err == nil {
		t.Fatal("UPDATE records.event_cursor after migrating a v3 fixture succeeded, want records_no_update to abort it")
	}
}

// TestLoadMigrationsRefusesDuplicateVersion guards the loader against two
// files sharing a version prefix (task 214eb30e, found when 0004_backfill_cursors
// and 0004_event_cursor met on main): the runner would skip the second as
// already applied. Distinct versions load; a duplicate is an error naming both.
func TestLoadMigrationsRefusesDuplicateVersion(t *testing.T) {
	read := func(string) ([]byte, error) { return []byte("SELECT 1;"), nil }
	if _, err := migrationsFromNames([]string{"0001_a.sql", "0002_b.sql"}, read); err != nil {
		t.Fatalf("distinct versions: unexpected error: %v", err)
	}
	_, err := migrationsFromNames([]string{"0004_a.sql", "0004_b.sql"}, read)
	if err == nil {
		t.Fatalf("duplicate version 4 loaded without error")
	}
	if !strings.Contains(err.Error(), "0004_a.sql") || !strings.Contains(err.Error(), "0004_b.sql") {
		t.Errorf("error does not name both files: %v", err)
	}
}
