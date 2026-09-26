// Package store owns the SQLite database that backs Backstory.
//
// The store is opened with mode 0600 (the file may hold private memory),
// WAL journaling, and foreign keys enforced on every connection.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/RidgetopAi/backstory/internal/project"
	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// FileMode is the permission bits every Backstory database file is held at.
const FileMode os.FileMode = 0o600

// Store is an open Backstory database.
type Store struct {
	db   *sql.DB
	path string
	// legacyToCanonical maps a configured workspace dir's plain pre-79f7b20e
	// key to its "workspace:" key (canonicalizeProjectKey's lookup table).
	legacyToCanonical map[string]string
}

// Open opens (creating if needed) the SQLite database at path, and merges
// every configured workspace dir's legacy plain-key rows into its
// "workspace:"-prefixed spelling: ONE spelling per workspace folder, not a
// read-side alias over two (task d65ef8ff). workspaceDirs and git are the
// same facts every caller already resolves for internal/project.Key; nil
// workspaceDirs is safe.
//
// The file is created with, and forced to, FileMode; every pooled connection
// has WAL journaling and foreign_keys enabled via the DSN. Safe to call
// repeatedly: migration and the sweep are both idempotent.
func Open(path string, workspaceDirs []string, git project.Git) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	// Create the file ourselves so the mode is ours, not the driver's default.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, FileMode) //nolint:gosec // path is the caller-chosen DB location
	if err != nil {
		return nil, fmt.Errorf("store: create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: close %s: %w", path, err)
	}
	// OpenFile only applies the mode on creation; a pre-existing file keeps
	// whatever it had. Force it so an old 0644 file is never left readable.
	if err := os.Chmod(path, FileMode); err != nil {
		return nil, fmt.Errorf("store: chmod %s: %w", path, err)
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	targets := resolveSweepTargets(workspaceDirs, git)
	s := &Store{db: db, path: path, legacyToCanonical: targets}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.sweepLegacyWorkspaceKeys(targets); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// dsn builds the modernc.org/sqlite connection string. The _pragma parameters
// are applied to every new connection in the pool. _txlock=immediate makes
// every explicit transaction (every s.db.Begin() in this package) take its
// write lock at BEGIN instead of deferring it: in WAL mode, a DEFERRED
// transaction that turns out to write can fail its read->write lock upgrade
// with SQLITE_BUSY (or SQLITE_BUSY_SNAPSHOT) the instant a concurrent writer
// commits between the transaction's read snapshot and its write attempt —
// busy_timeout's busy handler is never invoked for that case, because
// waiting cannot fix a stale snapshot. Acquiring the lock at BEGIN reduces
// the failure to ordinary lock contention, which busy_timeout does resolve
// by retrying. This is a single, DSN-wide setting: it covers every write
// path in the package (InsertRecord, migrations, data migration) without
// each call site having to opt in separately.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_txlock", "immediate")
	return "file:" + path + "?" + q.Encode()
}

// DB exposes the underlying connection pool.
func (s *Store) DB() *sql.DB { return s.db }

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Version returns the highest schema migration version applied to this
// database.
func (s *Store) Version() (int, error) {
	var v int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: read version: %w", err)
	}
	return v, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}
	return nil
}
