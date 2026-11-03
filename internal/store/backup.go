package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// BackupDirMode and BackupFileMode are the modes a backup directory the
// command creates and every backup file carry; a snapshot holds the whole
// memory, so it is as private as the store itself.
const (
	BackupDirMode  os.FileMode = 0o700
	BackupFileMode os.FileMode = FileMode
)

// ErrBackupExists is returned by Backup when dest exists and overwrite is
// false.
var ErrBackupExists = errors.New("store: backup target already exists")

// BackupInfo describes a snapshot file: where it is and what it holds.
type BackupInfo struct {
	Path          string `json:"path"`
	Size          int64  `json:"size"`
	SchemaVersion int    `json:"schema_version"`
	Projects      int    `json:"projects"`
	Sessions      int    `json:"sessions"`
	Records       int    `json:"records"`
	// Newest is the ts of the newest record (UTC); zero, and omitted from
	// JSON as RFC 3339, when there are none.
	Newest time.Time `json:"newest,omitzero"`
}

// Backup writes a consistent snapshot of the store to dest with VACUUM INTO,
// which reads one WAL snapshot and so is safe while another process writes
// (a plain file copy can miss the -wal or tear). The snapshot is built beside
// dest under a temporary name, opened read-only and checked with PRAGMA
// integrity_check (VerifyBackup); only a file that passes is moved to dest, so
// dest is never a partial or bad copy. Without overwrite an existing dest is
// refused (ErrBackupExists); the check is atomic (a hard link), not a stat.
// The parent directory is created 0700 when missing; the file is 0600.
func (s *Store) Backup(dest string, overwrite bool) (BackupInfo, error) {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, BackupDirMode); err != nil {
		return BackupInfo{}, fmt.Errorf("store: backup: create dir: %w", err)
	}
	if !overwrite {
		if _, err := os.Lstat(dest); err == nil {
			return BackupInfo{}, fmt.Errorf("%w: %s", ErrBackupExists, dest)
		}
	}
	// VACUUM INTO requires a target that does not exist, so reserve a unique
	// name in dir and remove the placeholder (it is created 0600 by CreateTemp).
	tmpf, err := os.CreateTemp(dir, ".backstory-backup-*.tmp")
	if err != nil {
		return BackupInfo{}, fmt.Errorf("store: backup: temp file: %w", err)
	}
	tmp := tmpf.Name()
	_ = tmpf.Close()
	_ = os.Remove(tmp)
	defer func() { _ = os.Remove(tmp) }() // no-op once moved

	if _, err := s.db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		return BackupInfo{}, fmt.Errorf("store: backup: snapshot: %w", err)
	}
	if err := os.Chmod(tmp, BackupFileMode); err != nil {
		return BackupInfo{}, fmt.Errorf("store: backup: chmod: %w", err)
	}
	if _, err := VerifyBackup(tmp); err != nil {
		return BackupInfo{}, err
	}
	if overwrite {
		err = os.Rename(tmp, dest)
	} else {
		err = os.Link(tmp, dest) // fails if dest appeared meanwhile
		if errors.Is(err, os.ErrExist) {
			return BackupInfo{}, fmt.Errorf("%w: %s", ErrBackupExists, dest)
		}
	}
	if err != nil {
		return BackupInfo{}, fmt.Errorf("store: backup: place %s: %w", dest, err)
	}
	return VerifyBackup(dest)
}

// VerifyBackup opens the file at path read-only, runs PRAGMA integrity_check
// (anything but "ok" is an error), and reports its size, highest schema
// version and project/session/record counts.
func VerifyBackup(path string) (BackupInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("store: verify backup: %w", err)
	}
	q := url.Values{}
	q.Set("mode", "ro")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return BackupInfo{}, fmt.Errorf("store: verify backup: open: %w", err)
	}
	defer func() { _ = db.Close() }()

	var verdict string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&verdict); err != nil {
		return BackupInfo{}, fmt.Errorf("store: verify backup: integrity_check: %w", err)
	}
	if verdict != "ok" {
		return BackupInfo{}, fmt.Errorf("store: verify backup: integrity_check: %s", verdict)
	}
	info := BackupInfo{Path: path, Size: fi.Size()}
	for _, c := range []struct {
		query string
		dst   *int
	}{
		{`SELECT COALESCE(MAX(version), 0) FROM schema_version`, &info.SchemaVersion},
		{`SELECT COUNT(*) FROM projects`, &info.Projects},
		{`SELECT COUNT(*) FROM sessions`, &info.Sessions},
		{`SELECT COUNT(*) FROM records`, &info.Records},
	} {
		if err := db.QueryRow(c.query).Scan(c.dst); err != nil {
			return BackupInfo{}, fmt.Errorf("store: verify backup: %w", err)
		}
	}
	var newest sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(ts) FROM records`).Scan(&newest); err != nil {
		return BackupInfo{}, fmt.Errorf("store: verify backup: %w", err)
	}
	if newest.Valid {
		info.Newest = tsFromNanos(newest.Int64)
	}
	return info, nil
}
