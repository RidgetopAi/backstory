package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// BashrcMarkerComment and BashrcEvalLine are the two lines `backstory
// install bash` appends to ~/.bashrc together, as one unit (the task's
// "ONE marked line"): a comment identifying the block, followed by the
// eval that wires `backstory shell init bash` (task 7fe84ffb) into the
// interactive shell. The comment alone is what idempotency checks look
// for; the pair together is what gets removed, verbatim, by BashrcBlock.
const (
	BashrcMarkerComment = "# backstory shell capture"
	BashrcEvalLine      = `command -v backstory >/dev/null && eval "$(backstory shell init bash)"`
)

// DefaultBashrcPath returns ~/.bashrc under home, mirroring DefaultPaths'
// role for the Claude Code installer.
func DefaultBashrcPath(home string) string {
	return filepath.Join(home, ".bashrc")
}

// BashrcBlock is the exact text InstallBashrc appends and RemoveBashrc
// deletes: the marker comment, the eval line, and a single trailing
// newline. RemoveBashrc matches this whole string verbatim — never a
// prefix or substring match on "backstory" alone, which would also delete
// unrelated lines that merely mention the word (e.g. a user's own comment
// or alias).
var BashrcBlock = BashrcMarkerComment + "\n" + BashrcEvalLine + "\n"

// ErrBashrcNotWritable is returned when ~/.bashrc (or, for a symlink, its
// target) cannot be safely written: it is not a regular file, it is not
// owned by the current user, or it lacks the owner write bit. InstallBashrc
// and RemoveBashrc leave the file completely untouched whenever this error
// applies.
var ErrBashrcNotWritable = errors.New("install: ~/.bashrc is not a writable regular file owned by the current user")

// InstallBashrc appends BashrcBlock to path, creating path (and its parent
// directory) if it does not exist. It is idempotent: if BashrcMarkerComment
// is already present anywhere in the file, InstallBashrc changes nothing
// and returns nil. A path that is read-only, a symlink to a non-regular
// file, or a symlink to a file not owned by the current user returns
// ErrBashrcNotWritable without writing anything. A symlink to a regular
// file the current user owns is written through: the symlink itself is
// left in place, and its target file is updated.
func InstallBashrc(path string) error { return InstallBashrcBinary(path, "") }

// BashrcBlockFor is BashrcBlock with the eval invoking binary (an absolute
// path) instead of looking `backstory` up on PATH; "" is BashrcBlock itself.
func BashrcBlockFor(binary string) string {
	if binary == "" {
		return BashrcBlock
	}
	q := shellQuote(binary)
	return BashrcMarkerComment + "\n" + "[ -x " + q + ` ] && eval "$(` + q + ` shell init bash)"` + "\n"
}

// bashrcBlockRE matches the block under any binary: the marker line and the
// eval line that follows it.
var bashrcBlockRE = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(BashrcMarkerComment) + `\n(?:[^\n]*shell init bash[^\n]*)\n`)

var bashrcInstalledBinaryRE = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(BashrcMarkerComment) + `\n\[ -x (.+?) \] && eval `)

// BashrcInstalledBinary returns the binary path the installed snippet in path
// invokes, or "" when the snippet is absent or uses the PATH lookup default.
func BashrcInstalledBinary(path string) string {
	target, _, err := resolveBashrcTarget(path)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(target) //nolint:gosec // target is resolved by resolveBashrcTarget, which already checked ownership/regularity
	if err != nil {
		return ""
	}
	m := bashrcInstalledBinaryRE.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return unquoteShell(string(m[1]))
}

// InstallBashrcBinary is InstallBashrc with the snippet invoking binary.
func InstallBashrcBinary(path, binary string) error {
	target, mode, err := resolveBashrcTarget(path)
	if err != nil {
		return err
	}

	data, readErr := os.ReadFile(target) //nolint:gosec // target is resolved by resolveBashrcTarget, which already checked ownership/regularity
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	content := string(data)

	if strings.Contains(content, BashrcMarkerComment) {
		return nil // already installed
	}

	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += BashrcBlockFor(binary)

	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	return writeAtomic(target, []byte(content), mode)
}

// RemoveBashrc deletes exactly BashrcBlock from path (through a symlink's
// target, under the same ownership/regularity rules as InstallBashrc), byte
// for byte, leaving everything else in the file untouched. If path does not
// exist, or exists but does not contain BashrcBlock, RemoveBashrc is a
// no-op. If removing BashrcBlock leaves the file empty, the file itself is
// removed (restoring the pre-install "no ~/.bashrc" state exactly).
func RemoveBashrc(path string) error {
	target, mode, err := resolveBashrcTarget(path)
	if err != nil {
		return err
	}

	data, readErr := os.ReadFile(target) //nolint:gosec // target is resolved by resolveBashrcTarget, which already checked ownership/regularity
	if os.IsNotExist(readErr) {
		return nil
	}
	if readErr != nil {
		return readErr
	}
	content := string(data)

	loc := bashrcBlockRE.FindStringIndex(content)
	if loc == nil {
		return nil // not installed; nothing to remove
	}
	newContent := content[:loc[0]] + content[loc[1]:]

	if newContent == "" {
		return os.Remove(target)
	}
	return writeAtomic(target, []byte(newContent), mode)
}

// BashrcStatus reports whether path currently contains BashrcMarkerComment.
// It never writes, and a path that cannot be resolved (missing, or blocked
// by ErrBashrcNotWritable) reports StatusAbsent.
func BashrcStatus(path string) ItemStatus {
	target, _, err := resolveBashrcTarget(path)
	if err != nil {
		return StatusAbsent
	}
	data, err := os.ReadFile(target) //nolint:gosec // target is resolved by resolveBashrcTarget, which already checked ownership/regularity
	if err != nil {
		return StatusAbsent
	}
	if strings.Contains(string(data), BashrcMarkerComment) {
		return StatusPresent
	}
	return StatusAbsent
}

// resolveBashrcTarget resolves the real file InstallBashrc/RemoveBashrc
// must read and write for path, and the file mode to preserve (0o644 for a
// brand new file). A path that does not exist yet resolves to itself with
// no error, ready for os.MkdirAll+writeAtomic to create. A symlink resolves
// to its target, checked for regularity and ownership; anything else
// (a non-regular file, a symlink to one, read-only, or not owned by the
// current user) returns ErrBashrcNotWritable and never touches path.
func resolveBashrcTarget(path string) (target string, mode os.FileMode, err error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, 0o644, nil
	}
	if err != nil {
		return "", 0, err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", 0, fmt.Errorf("%w: %s: broken symlink: %v", ErrBashrcNotWritable, path, err)
		}
		targetInfo, err := os.Lstat(resolved)
		if err != nil {
			return "", 0, fmt.Errorf("%w: %s -> %s: %v", ErrBashrcNotWritable, path, resolved, err)
		}
		if err := checkRegularWritableOwnedByCurrentUser(resolved, targetInfo); err != nil {
			return "", 0, err
		}
		return resolved, targetInfo.Mode().Perm(), nil
	}

	if err := checkRegularWritableOwnedByCurrentUser(path, info); err != nil {
		return "", 0, err
	}
	return path, info.Mode().Perm(), nil
}

// checkRegularWritableOwnedByCurrentUser is the shared guard behind
// resolveBashrcTarget's two branches (a plain file, and a symlink's
// resolved target): both must be a regular file, owned by the process's
// own uid, with the owner write bit set, or InstallBashrc/RemoveBashrc must
// refuse rather than clobber or write through to something the user does
// not control.
func checkRegularWritableOwnedByCurrentUser(path string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrBashrcNotWritable, path)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%w: %s is not owned by the current user", ErrBashrcNotWritable, path)
	}
	if info.Mode().Perm()&0o200 == 0 {
		return fmt.Errorf("%w: %s is read-only", ErrBashrcNotWritable, path)
	}
	return nil
}
