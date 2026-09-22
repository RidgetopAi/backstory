package store

import (
	"os"
	"path/filepath"
	"testing"
)

// wantMode is the REQUIRED mode, pinned as a literal on purpose: comparing
// against store.FileMode would let a loosened constant move the expectation
// with it (probed 2026-09-21: 0o644 stayed GREEN against the constant).
const wantMode os.FileMode = 0o600

func mustOpen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func TestOpenCreatesFileMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "backstory.db")
	mustOpen(t, path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Fatalf("db file mode = %04o, want %04o", got, wantMode)
	}
}

func TestOpenForcesModeOnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil { //nolint:gosec // deliberately loose: the test proves Open tightens it
		t.Fatal(err)
	}
	mustOpen(t, path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Fatalf("pre-existing db file mode = %04o after Open, want %04o", got, wantMode)
	}
}

func TestPragmasApplied(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	var journal string
	if err := s.DB().QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}
	var fk int
	if err := s.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestFTS5Available(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	db := s.DB()

	if _, err := db.Exec(`CREATE VIRTUAL TABLE notes USING fts5(body)`); err != nil {
		t.Fatalf("fts5 virtual table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes(body) VALUES ('the hyprland session remembered')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notes WHERE notes MATCH 'hyprland'`).Scan(&n); err != nil {
		t.Fatalf("fts5 match: %v", err)
	}
	if n != 1 {
		t.Fatalf("fts5 match count = %d, want 1", n)
	}
}

func TestOpenTwiceIsSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")

	// Sequential: open, close, open again must not re-seed or fail.
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	s := mustOpen(t, path)

	// Concurrent: a second handle on the same file while the first is open.
	other := mustOpen(t, path)

	for _, st := range []*Store{s, other} {
		v, err := st.Version()
		if err != nil {
			t.Fatal(err)
		}
		if v != SchemaVersion {
			t.Errorf("version = %d, want %d", v, SchemaVersion)
		}
		var rows int
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Errorf("schema_version rows = %d, want exactly 1", rows)
		}
	}
}
