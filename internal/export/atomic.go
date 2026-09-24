package export

import (
	"fmt"
	"os"
	"path/filepath"
)

// mirrorFileMode is the permission WriteAtomic gives a mirror file: readable
// by anyone who can read the directory (another tool ingests it), writable
// only by its owner — reasonable defaults for a file that has no
// pre-existing mode to preserve (unlike internal/install's config files,
// export's --out path is not expected to already exist).
const mirrorFileMode = 0o644

// writeTempData writes data to the still-open temp file WriteAtomic just
// created. It is a package-level var, not a direct f.Write call inline in
// WriteAtomic, purely so a test can swap it for a function that returns an
// error and assert WriteAtomic's failure mode: the destination path must be
// left untouched, because the temp file's random suffix (os.CreateTemp)
// makes injecting a write failure any other way unreliable.
var writeTempData = func(f *os.File, data []byte) error {
	_, err := f.Write(data)
	return err
}

// WriteAtomic writes data to path via a temp file in path's own directory,
// synced and closed, then renamed into place — decision 262cf929: "with
// --out it writes that path atomically (temp + rename)". A failure at any
// step before the rename leaves path exactly as it was (untouched if it
// pre-existed, absent if it did not) and removes the temp file; only a
// successful rename ever changes what path holds, so a caller never
// observes a partially written file at the destination.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".backstory-export-*")
	if err != nil {
		return fmt.Errorf("export: create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeTempData(tmp, data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("export: write %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("export: sync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("export: close %s: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, mirrorFileMode); err != nil {
		return fmt.Errorf("export: chmod %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("export: rename %s to %s: %w", tmpPath, path, err)
	}
	removeTmp = false
	return nil
}
