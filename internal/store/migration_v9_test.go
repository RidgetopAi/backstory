package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestMigrationV9AddsNullGitHeadToV8FixtureWithExistingRecords is task
// 5615ddae DONE WHEN 4: migration 0009 applies on a store at schema version
// 8, keeps the existing record intact with git_head NULL, and a new record
// can carry a stamp.
func TestMigrationV9AddsNullGitHeadToV8FixtureWithExistingRecords(t *testing.T) {
	rawPath := filepath.Join(t.TempDir(), "v8fixture.db")
	db := buildFixtureThroughVersion(t, rawPath, 8)
	nowNanos := time.Now().UTC().UnixNano()

	const projectKey = "proj-v8"
	if _, err := db.Exec(`INSERT INTO projects (key, toplevel, first_seen) VALUES (?, ?, ?)`,
		projectKey, "/tmp/"+projectKey, nowNanos); err != nil {
		t.Fatalf("seed v8 project: %v", err)
	}
	seedHumanRecord(t, db, "rec-v8", projectKey, "pre-migration record", nowNanos)
	if err := db.Close(); err != nil {
		t.Fatalf("close v8 fixture: %v", err)
	}

	s := mustOpen(t, rawPath)
	if v, err := s.Version(); err != nil || v != SchemaVersion || v < 9 {
		t.Fatalf("Version() = %d, %v; want SchemaVersion %d (>= 9)", v, err, SchemaVersion)
	}
	rec, err := s.GetRecord("rec-v8")
	if err != nil {
		t.Fatalf("GetRecord after migrating: %v", err)
	}
	if rec.Text != "pre-migration record" || rec.GitHead != "" {
		t.Errorf("record after migration = %+v, want text intact and git_head NULL", rec)
	}
}
