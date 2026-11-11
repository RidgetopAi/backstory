package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// purgeEraseHook, when set (tests only), runs just before the store erase and
// a non-nil return fails the purge as if the erase itself had failed.
var purgeEraseHook func() error

// preCopyInfixes name the copies Backstory keeps beside the store before it
// changes it (restore, the project sweep, the repo-identity and handoff-rehome migrations).
var preCopyInfixes = []string{".pre-restore-", ".pre-sweep-", ".pre-identity-", ".pre-rehome-"}

// backupFileName matches the two names Backstory gives a backup it writes into
// the backup folder: backstory-<UTC ts>.db and backstory-auto-<UTC ts>.db.
var backupFileName = regexp.MustCompile(`^backstory-(auto-)?\d{8}T\d{6}Z\.db$`)

// preCopyStamp is what may follow a .pre-* infix: a UTC stamp, with optional
// fractional seconds.
var preCopyStamp = regexp.MustCompile(`^\d{8}T\d{6}(\.\d+)?Z$`)

// purgeBackupsWrittenElsewhere is the line purge prints about `backup --out`.
const purgeBackupsWrittenElsewhere = "backups written elsewhere with --out are not touched and still hold the text, so delete them yourself."

// purgeBackup is one file purge deletes.
type purgeBackup struct {
	Path string
	At   time.Time
	Size int64
}

// listPurgeBackups finds the backups Backstory wrote that purge removes: the
// named files in backupDir and the .pre-* copies beside the store. Only
// regular files with exactly Backstory's names count — never a directory, a
// symlink or any other file — and nothing outside those two folders.
func listPurgeBackups(dbPath, backupDir string) ([]purgeBackup, error) {
	storeDir := filepath.Dir(dbPath)
	var out []purgeBackup
	seen := map[string]bool{}
	add := func(dir string, match func(name string) bool) error {
		ents, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("list backups in %s: %w", dir, err)
		}
		for _, e := range ents {
			if !match(e.Name()) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			fi, err := os.Lstat(p)
			if err != nil || !fi.Mode().IsRegular() || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, purgeBackup{Path: p, At: fi.ModTime(), Size: fi.Size()})
		}
		return nil
	}
	if err := add(backupDir, backupFileName.MatchString); err != nil {
		return nil, err
	}
	base := filepath.Base(dbPath)
	if err := add(storeDir, func(name string) bool {
		for _, infix := range preCopyInfixes {
			if rest, ok := strings.CutPrefix(name, base+infix); ok && preCopyStamp.MatchString(rest) {
				return true
			}
		}
		return false
	}); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// printPurgeBackups lists what a purge would delete, then the --out note.
func printPurgeBackups(w io.Writer, files []purgeBackup) {
	if len(files) == 0 {
		_, _ = fmt.Fprintln(w, "no backups made by Backstory hold this text")
	} else {
		_, _ = fmt.Fprintf(w, "would also delete %d backups Backstory made (they still hold the text):\n", len(files))
		for _, f := range files {
			_, _ = fmt.Fprintf(w, "  %s  %s  %d bytes\n", f.Path, f.At.Local().Format("2006-01-02 15:04"), f.Size)
		}
	}
	_, _ = fmt.Fprintln(w, purgeBackupsWrittenElsewhere)
}

// cleanBackupsAfterPurge runs after the store erase succeeded: it deletes the
// backups Backstory wrote (re-listed now, so none is missed) and takes a
// fresh one so the user is never left with none. It returns false only when a
// backup could not be deleted; a failed fresh backup is a loud warning, not a
// failure, because the purge itself succeeded.
func cleanBackupsAfterPurge(st *store.Store, dbPath string, stdout, stderr io.Writer) bool {
	ok := true
	dir, err := effectiveBackupDir(st)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory purge: WARNING: the purge worked, but the backup folder could not be read, so old backups still hold the purged text and no fresh backup was taken:", err)
		return false
	}
	files, err := listPurgeBackups(dbPath, dir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory purge: WARNING: the purge worked, but old backups could not be listed, so they still hold the purged text:", err)
		ok = false
	}
	deleted := 0
	for _, f := range files {
		if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(stderr, "backstory purge: WARNING: could not delete %s, it still holds the purged text: %v\n", f.Path, err)
			ok = false
			continue
		}
		deleted++
	}
	_, _ = fmt.Fprintf(stdout, "deleted %d backups that held the purged text\n", deleted)
	_, _ = fmt.Fprintln(stdout, purgeBackupsWrittenElsewhere)

	info, err := writeDefaultBackup(st)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory purge: WARNING: the purge worked, but the fresh backup failed, so you have no backup right now:", err)
		return ok
	}
	_, _ = fmt.Fprintf(stdout, "fresh backup: %s\n", info.Path)
	return ok
}
