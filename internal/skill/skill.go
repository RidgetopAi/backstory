// Package skill materialises the Backstory skill file
// (AGENT-CONTRACT.md §The skill) under a harness's skills directory:
// symlinked to the packaged copy when it exists (the AUR package's job,
// PLAN.md Lock 10), else written from the embedded copy so a plain `go
// install` still produces a working skill.
package skill

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

// Embedded is the skill file's content, byte-for-byte the eight lines of
// AGENT-CONTRACT.md §The skill (skill_test.go proves it against that file).
//
//go:embed SKILL.md
var Embedded []byte

// PackagedSkillPath is where the AUR package (PLAN.md Lock 10) installs the
// skill file system-wide. Resolve it against a prefix (the empty string in
// production, a temp dir in tests) with filepath.Join.
const PackagedSkillPath = "/usr/share/backstory/skills/backstory/SKILL.md"

// Status is a --check item's reported state.
type Status string

const (
	StatusAbsent  Status = "absent"
	StatusPresent Status = "present"
	StatusForeign Status = "foreign-conflict"
)

// packagedPath resolves PackagedSkillPath against prefix.
func packagedPath(prefix string) string {
	return filepath.Join(prefix, PackagedSkillPath)
}

// CheckStatus reports whether dest holds the skill this package would
// install: a symlink to the packaged path, or a regular file byte-identical
// to Embedded. Anything else at dest (including a foreign file) is
// foreign-conflict, never clobbered.
func CheckStatus(dest, prefix string) Status {
	fi, err := os.Lstat(dest)
	if err != nil {
		return StatusAbsent
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(dest)
		if err == nil && target == packagedPath(prefix) {
			return StatusPresent
		}
		return StatusForeign
	}
	data, err := os.ReadFile(dest)
	if err == nil && bytes.Equal(data, Embedded) {
		return StatusPresent
	}
	return StatusForeign
}

// Install materialises the skill at dest: a symlink to the packaged path
// when it exists under prefix, else the embedded copy. Idempotent (a second
// call changes zero bytes) and never clobbers a foreign file already at
// dest.
func Install(dest, prefix string) (changed bool, err error) {
	switch CheckStatus(dest, prefix) {
	case StatusPresent:
		return false, nil
	case StatusForeign:
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}

	if _, err := os.Stat(packagedPath(prefix)); err == nil {
		if err := os.Symlink(packagedPath(prefix), dest); err != nil {
			return false, err
		}
		return true, nil
	}

	if err := os.WriteFile(dest, Embedded, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// Remove deletes dest only if it is what Install would have written (or
// already gone); a foreign file at dest is left untouched.
func Remove(dest, prefix string) error {
	switch CheckStatus(dest, prefix) {
	case StatusAbsent, StatusForeign:
		return nil
	default:
		return os.Remove(dest)
	}
}
