package store

import (
	"path/filepath"
	"testing"
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

	first, err := Open(path)
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
