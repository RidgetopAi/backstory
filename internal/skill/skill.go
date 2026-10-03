// Package skill materialises the Backstory skill file
// (AGENT-CONTRACT.md §The skill) under a harness's skills directory:
// symlinked to the packaged copy when it exists (the AUR package's job,
// PLAN.md Lock 10), else written from the embedded copy so a plain `go
// install` still produces a working skill.
package skill

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
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
	// StatusOutdated marks a regular file byte-identical to a past embedded
	// SKILL.md: Backstory's own file, just old. Install replaces it.
	StatusOutdated Status = "outdated"
)

// PastEmbeddedSHA256 lists the sha256 (hex) of every SKILL.md this package
// has ever embedded, except the current one, oldest first. Append the
// outgoing version's hash whenever SKILL.md changes (skill_test.go checks
// the list against git history and rejects the current hash).
var PastEmbeddedSHA256 = []string{
	"18209f1d521d0bb0ef74de150c145ffd8151eb448d58a807c16e0a68c1a849ef", // bc94d31
	"c6003812bf66a715cbdee95c5c41a959803219926bd30f9271411a18f8368822", // 47c25fb
	"0f626aa1b0172683d985881cf9d93e424f7aa7c57f23f54875c8af90cb157abd", // f0616db
	"1d1016ae20d05095fd16712e2fa7be5c8a14b5f2c96685e55288de740ffa8a35", // e887d71
}

// backupSuffixPrefix is appended to the skill path, followed by the first
// backupHashLen hex chars of the replaced bytes' sha256, to name the backup.
const (
	backupSuffixPrefix = ".bak-"
	backupHashLen      = 8
)

func isPastEmbedded(data []byte) bool {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	for _, p := range PastEmbeddedSHA256 {
		if p == h {
			return true
		}
	}
	return false
}

// packagedPath resolves PackagedSkillPath against prefix.
func packagedPath(prefix string) string {
	return filepath.Join(prefix, PackagedSkillPath)
}

// CheckStatus reports whether dest holds the skill this package would
// install: a symlink to the packaged path, or a regular file byte-identical
// to Embedded. A regular file matching a past embedded version is outdated;
// anything else at dest (including a foreign file) is foreign-conflict,
// never clobbered.
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
	data, err := os.ReadFile(dest) //nolint:gosec // dest is the caller-chosen skill path, not external input
	if err == nil && bytes.Equal(data, Embedded) {
		return StatusPresent
	}
	if err == nil && isPastEmbedded(data) {
		return StatusOutdated
	}
	return StatusForeign
}

// Install materialises the skill at dest: a symlink to the packaged path
// when it exists under prefix, else the embedded copy. Idempotent (a second
// call changes zero bytes) and never clobbers a foreign file already at
// dest. An outdated file (a past embedded version) is replaced after its
// bytes are saved to a backup beside it.
func Install(dest, prefix string) (changed bool, err error) {
	switch CheckStatus(dest, prefix) {
	case StatusPresent:
		return false, nil
	case StatusForeign:
		return false, nil
	case StatusOutdated:
		if err := backupAndClear(dest); err != nil {
			return false, err
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return false, err
	}

	if _, err := os.Stat(packagedPath(prefix)); err == nil {
		if err := os.Symlink(packagedPath(prefix), dest); err != nil {
			return false, err
		}
		return true, nil
	}

	if err := os.WriteFile(dest, Embedded, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// backupAndClear copies dest's bytes to a hash-named backup beside it, then
// removes dest so the caller can write the replacement.
func backupAndClear(dest string) error {
	data, err := os.ReadFile(dest) //nolint:gosec // dest is the caller-chosen skill path
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	backup := dest + backupSuffixPrefix + hex.EncodeToString(sum[:])[:backupHashLen]
	if err := os.WriteFile(backup, data, 0o600); err != nil { //nolint:gosec // backup path derives from the caller-chosen skill path
		return err
	}
	return os.Remove(dest)
}

// Remove deletes dest only if it is what Install would have written or an
// older version of it (or already gone); a foreign file at dest is left untouched.
func Remove(dest, prefix string) error {
	switch CheckStatus(dest, prefix) {
	case StatusAbsent, StatusForeign:
		return nil
	default: // present or outdated
		return os.Remove(dest)
	}
}
