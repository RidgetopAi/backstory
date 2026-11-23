package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// User config is often a symlink into a dotfiles tree (GNU stow, chezmoi's
// symlink mode, home-manager). Every write and delete this package makes
// under $HOME goes through the helpers below, so the link the user made
// always survives:
//
//   - a write lands on the link's final target (writeAtomic resolves it and
//     renames the temp file over the target, in the target's directory);
//   - a symlink whose target does not exist is refused with
//     ErrDanglingSymlink, never replaced by a plain file;
//   - a file Remove would delete (nothing but Backstory's text is left) is,
//     when its path is a symlink, emptied in place instead: the user made the
//     link, so it existed before install, and neither it nor its target is
//     ever deleted;
//   - a directory is pruned only when it is a real directory, never a link
//     to one (os.Remove unlinks a symlink whatever its target holds).

// ErrDanglingSymlink is returned when a config path is a symlink whose target
// does not exist (or is not a regular file). Install refuses rather than
// replace the link with a plain file; fix or remove the link and re-run.
var ErrDanglingSymlink = errors.New("install: config path is a symlink to a missing or non-regular file")

// resolveWriteTarget returns the file a write to path must replace: path
// itself when it is absent or not a symlink, else the symlink's final target,
// which must exist and be a regular file.
func resolveWriteTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrDanglingSymlink, path, err)
	}
	ti, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("%w: %s -> %s: %v", ErrDanglingSymlink, path, target, err)
	}
	if !ti.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s -> %s is not a regular file", ErrDanglingSymlink, path, target)
	}
	return target, nil
}

// checkWriteTargets resolves every path before an installer writes any of
// them, so a dangling symlink fails the install with nothing written.
func checkWriteTargets(paths ...string) error {
	for _, p := range paths {
		if _, err := resolveWriteTarget(p); err != nil {
			return err
		}
	}
	return nil
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path) //nolint:gosec // G703: a harness config path under the user's own home, by design
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// removeOrEmpty deletes a config file install created once nothing but
// Backstory's content is left in it; a symlinked path is instead rewritten
// through to its target with empty (the file's empty state: "" for text,
// "{}\n" for a JSON object), keeping both the link and the target.
func removeOrEmpty(path string, empty []byte) error {
	if isSymlink(path) {
		return writeAtomic(path, empty, fileMode(path))
	}
	return os.Remove(path)
}

// removeOwnedWholeFile deletes a file whose entire content is Backstory's
// (an extension or plugin file). A symlink at path was put there by the user,
// not by install, so it is left untouched.
func removeOwnedWholeFile(path string) error {
	if isSymlink(path) {
		return nil
	}
	return os.Remove(path)
}

// pruneDir removes d when it is an empty real directory. A symlink to a
// directory is never removed (os.Remove would unlink it even when its target
// is full); that, or a non-empty directory, returns an error so a pruning
// loop stops there.
func pruneDir(d string) error {
	if isSymlink(d) {
		return fmt.Errorf("%s is a symlink; not pruned", d)
	}
	return os.Remove(d)
}
