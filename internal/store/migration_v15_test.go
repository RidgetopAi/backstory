package store

import (
	"path/filepath"
	"testing"
)

// TestMigrationV15AddsProjectKeyAliases (task 725021e4): a v14 store gains an
// empty project_key_aliases table, keeps its rows, and reaches SchemaVersion.
func TestMigrationV15AddsProjectKeyAliases(t *testing.T) {
	if SchemaVersion < 15 {
		t.Fatalf("SchemaVersion = %d, want >= 15", SchemaVersion)
	}
	rawPath := filepath.Join(t.TempDir(), "v14fixture.db")
	db := buildFixtureThroughVersion(t, rawPath, 14)
	if _, err := db.Exec(`INSERT INTO projects (key, toplevel, first_seen) VALUES ('/p', '/p', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := mustOpen(t, rawPath)
	if v, err := s.Version(); err != nil || v != SchemaVersion {
		t.Fatalf("Version() = %d, %v; want %d", v, err, SchemaVersion)
	}
	var n, p int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM project_key_aliases`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("aliases = %d, %v; want empty table", n, err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&p); err != nil || p != 1 {
		t.Fatalf("projects = %d, %v; want 1", p, err)
	}
}
