package store

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// buildFixtureThroughVersion opens a fresh sqlite database at path and
// applies every embedded migration up to and including throughVersion, in
// order, executed directly (bypassing Store.migrate, so any migration
// numbered above throughVersion — in particular 0006, this punch's own
// migration — is NOT applied and not even recorded in schema_version). It
// returns the open *sql.DB so the caller can seed pre-migration-6 fixture
// rows before the test ever calls Open on the same path — the same shape
// buildV1Fixture/buildV2Fixture/buildV3Fixture (migrate_test.go) use, generalized
// so this file does not repeat their per-version boilerplate a fourth and
// fifth time.
func buildFixtureThroughVersion(t *testing.T, path string, throughVersion int) *sql.DB {
	t.Helper()

	ms, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}

	nowNanos := time.Now().UTC().UnixNano()
	for _, m := range ms {
		if m.version > throughVersion {
			continue
		}
		for _, stmt := range splitStatements(m.sql) {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("apply migration %s: %v\nstatement: %s", m.name, err, stmt)
			}
		}
		if _, err := db.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`, m.version, nowNanos); err != nil {
			t.Fatalf("record schema_version for migration %s: %v", m.name, err)
		}
	}
	return db
}

// seedOldShapeProjectAndSession inserts one project and one backfilled
// session directly (raw SQL, no Store involved yet) for a v5 fixture to
// hang timeline_events and records off of.
func seedOldShapeProjectAndSession(t *testing.T, db *sql.DB, projectKey, sessionID string, nowNanos int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO projects (key, toplevel, first_seen) VALUES (?, ?, ?)`,
		projectKey, "/tmp/"+projectKey, nowNanos); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, agent, cwd, project_key, started_at, origin) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, "claude", "/tmp/"+projectKey, projectKey, nowNanos, "backfilled"); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

// seedOldShapeToolUseEvent inserts one timeline_events row in the
// pre-8ba5487a tool.use shape: a shared `detail` key instead of `path`/
// `command`, exactly what internal/backfill/claude wrote before commit
// 63ab330 unified every writer onto internal/payload.
func seedOldShapeToolUseEvent(t *testing.T, db *sql.DB, sessionID, toolUseID, name, detail string, ts int64) int64 {
	t.Helper()
	type oldShape struct {
		ToolUseID string `json:"tool_use_id,omitempty"`
		Name      string `json:"name"`
		Detail    string `json:"detail,omitempty"`
	}
	b, err := json.Marshal(oldShape{ToolUseID: toolUseID, Name: name, Detail: detail})
	if err != nil {
		t.Fatalf("marshal old-shape tool.use payload: %v", err)
	}
	res, err := db.Exec(`INSERT INTO timeline_events (ts, kind, session_id, source, payload) VALUES (?, ?, ?, ?, ?)`,
		ts, payload.KindToolUse, sessionID, "backfill", string(b))
	if err != nil {
		t.Fatalf("seed old-shape tool.use event: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("old-shape tool.use event last insert id: %v", err)
	}
	return id
}

// seedHumanRecord inserts one human-authored, human-declared record
// directly (raw SQL) — the record this punch's DONE WHEN clause 1 requires
// to survive the migration untouched, since only machine-derived
// timeline_events rows may ever be rewritten.
func seedHumanRecord(t *testing.T, db *sql.DB, id, projectKey, text string, ts int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO records (id, ts, kind, tier, text, project_key) VALUES (?, ?, ?, ?, ?, ?)`,
		id, ts, "note", "human-declared", text, projectKey); err != nil {
		t.Fatalf("seed human record: %v", err)
	}
}

// toolUsePayloadDistinctFiles replicates internal/block's deltaSlot
// counting rule (block.go: unmarshal payload.ToolUse per tool.use event,
// collect distinct non-empty Path values) directly against events read back
// through EventsSinceID — the same store API block.Render calls — without
// internal/store importing internal/block itself (which would be an import
// cycle: block already imports store).
func toolUsePayloadDistinctFiles(t *testing.T, events []TimelineEvent) map[string]bool {
	t.Helper()
	files := map[string]bool{}
	for _, e := range events {
		if e.Kind != payload.KindToolUse {
			continue
		}
		var tu payload.ToolUse
		if err := json.Unmarshal([]byte(e.Payload), &tu); err != nil {
			t.Fatalf("unmarshal tool.use payload %q: %v", e.Payload, err)
		}
		if tu.Path != "" {
			files[tu.Path] = true
		}
	}
	return files
}

// TestOpenMigratesOldShapeToolUseRowsReportsNonZeroDeltaAndPreservesHumanRecord
// is proof (1) for task e96d1a21: a store seeded with pre-8ba5487a tool.use
// events (the file path under `detail`, no `path` key) plus a human-
// authored record, opened the way the daemon does (Open), ends with the
// same distinct-file counting internal/block's delta slot performs
// reporting a correct non-zero count, and the human record unchanged. RED
// against main a79b146: without migration 0006, EventsSinceID still returns
// payloads keyed by `detail`, payload.ToolUse.Path is always "", and the
// distinct-file count this test asserts is 0, not 2.
func TestOpenMigratesOldShapeToolUseRowsReportsNonZeroDeltaAndPreservesHumanRecord(t *testing.T) {
	rawPath := t.TempDir() + "/v5fixture.db"

	db := buildFixtureThroughVersion(t, rawPath, 5)
	nowNanos := time.Now().UTC().UnixNano()

	const projectKey = "proj-detail-shape"
	const sessionID = "sess-detail-shape"
	seedOldShapeProjectAndSession(t, db, projectKey, sessionID, nowNanos)

	seedOldShapeToolUseEvent(t, db, sessionID, "tu1", "Edit", "/tmp/a.go", nowNanos)
	seedOldShapeToolUseEvent(t, db, sessionID, "tu2", "Write", "/tmp/b.go", nowNanos+1)
	seedOldShapeToolUseEvent(t, db, sessionID, "tu3", "Edit", "/tmp/a.go", nowNanos+2) // duplicate path
	bashID := seedOldShapeToolUseEvent(t, db, sessionID, "tu4", "Bash", "go test ./...", nowNanos+3)

	seedHumanRecord(t, db, "human-rec-1", projectKey, "Ridge left a human note before the migration", nowNanos)

	if err := db.Close(); err != nil {
		t.Fatalf("close fixture db before Open: %v", err)
	}

	s := mustOpen(t, rawPath)

	v, err := s.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("Version() after migrating a v5 fixture = %d, want %d (SchemaVersion)", v, SchemaVersion)
	}

	events, err := s.EventsSinceID(projectKey, 0)
	if err != nil {
		t.Fatalf("EventsSinceID: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("EventsSinceID returned %d events, want 4 (migration must not drop or duplicate rows)", len(events))
	}

	files := toolUsePayloadDistinctFiles(t, events)
	if len(files) != 2 {
		t.Errorf("distinct files touched = %d (%v), want 2 (/tmp/a.go, /tmp/b.go) — the delta must report a correct non-zero count, not 0", len(files), files)
	}
	if !files["/tmp/a.go"] || !files["/tmp/b.go"] {
		t.Errorf("distinct files = %v, want exactly {/tmp/a.go, /tmp/b.go}", files)
	}

	// Every payload must have lost its old `detail` key entirely — not just
	// gained a `path`/`command` key alongside it.
	for _, e := range events {
		if strings.Contains(e.Payload, `"detail"`) {
			t.Errorf("event id=%d payload %q still carries the old `detail` key after migrating", e.ID, e.Payload)
		}
	}

	// The Bash row routes to `command`, never `path` — it must not be
	// miscounted as a touched file.
	for _, e := range events {
		if e.ID != bashID {
			continue
		}
		var tu payload.ToolUse
		if err := json.Unmarshal([]byte(e.Payload), &tu); err != nil {
			t.Fatalf("unmarshal bash event payload: %v", err)
		}
		if tu.Command != "go test ./..." {
			t.Errorf("bash event Command = %q, want %q", tu.Command, "go test ./...")
		}
		if tu.Path != "" {
			t.Errorf("bash event Path = %q, want empty (Bash never carries a path)", tu.Path)
		}
	}

	rec, err := s.GetRecord("human-rec-1")
	if err != nil {
		t.Fatalf("GetRecord(human-rec-1) after migrating: %v", err)
	}
	if rec.Text != "Ridge left a human note before the migration" {
		t.Errorf("human record text = %q, want unchanged", rec.Text)
	}
	if rec.Tier != TierHumanDeclared {
		t.Errorf("human record tier = %q, want %q", rec.Tier, TierHumanDeclared)
	}
	if rec.TombstonedAt != nil {
		t.Error("human record was tombstoned by the migration, want untouched")
	}
}

// TestReopenOldShapeStoreIsIdempotent is proof (2) for task e96d1a21:
// opening a store that needed migration 0006 a second time leaves the
// timeline event count, session count and record count exactly as they
// were after the first open, with no duplicated events and byte-identical
// payloads — migration 0006 is schema_version-gated like every other
// migration, so it runs exactly once no matter how many times Open is
// called.
func TestReopenOldShapeStoreIsIdempotent(t *testing.T) {
	rawPath := t.TempDir() + "/v5fixture-idempotent.db"

	db := buildFixtureThroughVersion(t, rawPath, 5)
	nowNanos := time.Now().UTC().UnixNano()

	const projectKey = "proj-idempotent"
	const sessionID = "sess-idempotent"
	seedOldShapeProjectAndSession(t, db, projectKey, sessionID, nowNanos)
	seedOldShapeToolUseEvent(t, db, sessionID, "tu1", "Edit", "/tmp/a.go", nowNanos)
	seedOldShapeToolUseEvent(t, db, sessionID, "tu2", "Bash", "ls", nowNanos+1)
	seedHumanRecord(t, db, "human-rec-idempotent", projectKey, "a human note", nowNanos)
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture db before first Open: %v", err)
	}

	first, err := Open(rawPath, nil, nil)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	firstEvents, err := first.EventsSinceID(projectKey, 0)
	if err != nil {
		t.Fatalf("first EventsSinceID: %v", err)
	}
	var firstSessions, firstRecords int
	if err := first.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&firstSessions); err != nil {
		t.Fatalf("count sessions after first open: %v", err)
	}
	if err := first.DB().QueryRow(`SELECT COUNT(*) FROM records`).Scan(&firstRecords); err != nil {
		t.Fatalf("count records after first open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	second := mustOpen(t, rawPath)
	secondEvents, err := second.EventsSinceID(projectKey, 0)
	if err != nil {
		t.Fatalf("second EventsSinceID: %v", err)
	}
	var secondSessions, secondRecords int
	if err := second.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&secondSessions); err != nil {
		t.Fatalf("count sessions after second open: %v", err)
	}
	if err := second.DB().QueryRow(`SELECT COUNT(*) FROM records`).Scan(&secondRecords); err != nil {
		t.Fatalf("count records after second open: %v", err)
	}

	if len(secondEvents) != len(firstEvents) {
		t.Fatalf("event count after reopen = %d, want unchanged %d (no duplicated events)", len(secondEvents), len(firstEvents))
	}
	if secondSessions != firstSessions {
		t.Errorf("session count after reopen = %d, want unchanged %d", secondSessions, firstSessions)
	}
	if secondRecords != firstRecords {
		t.Errorf("record count after reopen = %d, want unchanged %d", secondRecords, firstRecords)
	}
	for i := range firstEvents {
		if secondEvents[i].ID != firstEvents[i].ID {
			t.Errorf("event[%d].ID after reopen = %d, want unchanged %d", i, secondEvents[i].ID, firstEvents[i].ID)
		}
		if secondEvents[i].Payload != firstEvents[i].Payload {
			t.Errorf("event[%d].Payload after reopen = %q, want byte-identical to first open's %q", i, secondEvents[i].Payload, firstEvents[i].Payload)
		}
	}
}

// TestOpenAlreadyCurrentShapeStoreLeavesToolUseEventsUntouched is proof (4)
// for task e96d1a21: a store already in the current payload shape (built by
// a normal Open plus a normal AppendEvent, never touching the raw fixture
// path) is left alone on a second Open — no rewrite, no re-import — proven
// by the event's row id and payload staying byte-for-byte identical, never
// by timing.
func TestOpenAlreadyCurrentShapeStoreLeavesToolUseEventsUntouched(t *testing.T) {
	path := t.TempDir() + "/current-shape.db"

	first := mustOpen(t, path)
	if err := first.UpsertProject(Project{Key: "proj-current", Toplevel: "/tmp/proj-current", FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	sessionID, err := first.StartSession(StartSessionParams{
		Agent: "claude", CWD: "/tmp/proj-current", ProjectKey: "proj-current",
		StartedAt: time.Now(), Origin: OriginBackfilled,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	currentPayload, err := json.Marshal(payload.ToolUse{ToolUseID: "tu-current", Name: "Edit", Path: "/tmp/current.go"})
	if err != nil {
		t.Fatalf("marshal current-shape payload: %v", err)
	}
	eventID, err := first.AppendEvent(Event{
		TS: time.Now(), Kind: payload.KindToolUse, SessionID: sessionID,
		Source: "backfill", Payload: string(currentPayload),
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	var version6Applied int
	if err := first.DB().QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 6`).Scan(&version6Applied); err != nil {
		t.Fatalf("count schema_version version 6 after first open: %v", err)
	}
	if version6Applied != 1 {
		t.Fatalf("schema_version rows for version 6 after first open = %d, want 1", version6Applied)
	}

	var payloadBefore string
	if err := first.DB().QueryRow(`SELECT payload FROM timeline_events WHERE id = ?`, eventID).Scan(&payloadBefore); err != nil {
		t.Fatalf("read payload before reopen: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	second := mustOpen(t, path)

	var version6AppliedAfterReopen int
	if err := second.DB().QueryRow(`SELECT COUNT(*) FROM schema_version WHERE version = 6`).Scan(&version6AppliedAfterReopen); err != nil {
		t.Fatalf("count schema_version version 6 after reopen: %v", err)
	}
	if version6AppliedAfterReopen != 1 {
		t.Errorf("schema_version rows for version 6 after reopen = %d, want still 1 (migration must not reapply)", version6AppliedAfterReopen)
	}

	var idAfter int64
	var payloadAfter string
	if err := second.DB().QueryRow(`SELECT id, payload FROM timeline_events WHERE kind = ?`, payload.KindToolUse).Scan(&idAfter, &payloadAfter); err != nil {
		t.Fatalf("read tool.use row after reopen: %v", err)
	}
	if idAfter != eventID {
		t.Errorf("event id after reopen = %d, want unchanged %d (an already-current store must not be re-imported)", idAfter, eventID)
	}
	if payloadAfter != payloadBefore {
		t.Errorf("payload after reopen = %q, want byte-identical to before %q (an already-current store must not be rewritten)", payloadAfter, payloadBefore)
	}
}
