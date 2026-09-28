package install_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// seededBashrc is a realistic pre-existing ~/.bashrc: unrelated content on
// both sides of where the block will land, and — like virtually every
// editor-written file — a trailing newline, so InstallBashrc's "append
// directly, no separator" path and RemoveBashrc's "strip exactly the
// appended bytes" path are exact inverses of one another (see bashrc.go's
// own doc comments for the general, not-fully-invertible case of a file
// with no trailing newline, deferred the same way the pre-existing
// InstallStub/removeStub pair already defers it).
const seededBashrc = "# my prompt\nPS1='$ '\nalias ll='ls -la'\n"

// TestInstallBashAppendsMarkedBlockOnceAndIsIdempotent is the punch's DONE
// WHEN clause 1: install under a temp HOME with an existing ~/.bashrc
// appends the marked block exactly once, leaves the rest of the file
// unchanged, and a second install leaves ~/.bashrc byte-identical.
func TestInstallBashAppendsMarkedBlockOnceAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	path := install.DefaultBashrcPath(home)
	if err := os.WriteFile(path, []byte(seededBashrc), 0o644); err != nil {
		t.Fatalf("seed ~/.bashrc: %v", err)
	}

	if err := install.InstallBashrc(path); err != nil {
		t.Fatalf("InstallBashrc: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ~/.bashrc after install: %v", err)
	}
	want := seededBashrc + install.BashrcBlock
	if string(got) != want {
		t.Fatalf("~/.bashrc after install =\n%q\nwant\n%q", got, want)
	}
	if n := strings.Count(string(got), install.BashrcMarkerComment); n != 1 {
		t.Fatalf("marker comment appears %d times after install, want exactly 1:\n%s", n, got)
	}

	// A second install leaves the file byte-identical.
	if err := install.InstallBashrc(path); err != nil {
		t.Fatalf("second InstallBashrc: %v", err)
	}
	got2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ~/.bashrc after second install: %v", err)
	}
	if string(got2) != string(got) {
		t.Fatalf("~/.bashrc changed on second install:\nfirst:  %q\nsecond: %q", got, got2)
	}
	if n := strings.Count(string(got2), install.BashrcMarkerComment); n != 1 {
		t.Fatalf("marker comment appears %d times after second install, want exactly 1 (no duplicate):\n%s", n, got2)
	}
}

// TestUninstallBashRemovesExactlyTheMarkedBlock is the punch's DONE WHEN
// clause 2 (uninstall half) and clause 4's second mutation ("make uninstall
// delete by prefix match on 'backstory' instead of the exact marked
// line"): uninstall restores ~/.bashrc to its exact pre-install bytes, and
// decoy lines that merely mention "backstory" — one as a mid-line
// substring, one as the line's own literal prefix — both survive, proving
// removal matches the marked block exactly rather than any line containing,
// or starting with, that word.
func TestUninstallBashRemovesExactlyTheMarkedBlock(t *testing.T) {
	home := t.TempDir()
	path := install.DefaultBashrcPath(home)
	seeded := seededBashrc +
		"# I really like backstory, the memory tool\n" +
		"alias bs='which backstory'\n" +
		"backstory-notes: see the wiki for setup tips\n"
	if err := os.WriteFile(path, []byte(seeded), 0o644); err != nil {
		t.Fatalf("seed ~/.bashrc: %v", err)
	}

	if err := install.InstallBashrc(path); err != nil {
		t.Fatalf("InstallBashrc: %v", err)
	}
	if err := install.RemoveBashrc(path); err != nil {
		t.Fatalf("RemoveBashrc: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ~/.bashrc after remove: %v", err)
	}
	if string(got) != seeded {
		t.Fatalf("~/.bashrc after install+remove =\n%q\nwant pre-install bytes\n%q", got, seeded)
	}
}

// TestInstallBashWithNoBashrcCreatesOneWithOnlyTheMarkedBlock is the punch's
// DONE WHEN clause 2 (no-prior-file half): install with no ~/.bashrc
// creates one containing only the marked block, and uninstalling it again
// removes the file entirely (restoring the true pre-install "no ~/.bashrc"
// state).
func TestInstallBashWithNoBashrcCreatesOneWithOnlyTheMarkedBlock(t *testing.T) {
	home := t.TempDir()
	path := install.DefaultBashrcPath(home)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("~/.bashrc unexpectedly exists before install: err=%v", err)
	}

	if err := install.InstallBashrc(path); err != nil {
		t.Fatalf("InstallBashrc: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ~/.bashrc after install: %v", err)
	}
	if string(got) != install.BashrcBlock {
		t.Fatalf("~/.bashrc after install on no-prior-file =\n%q\nwant only\n%q", got, install.BashrcBlock)
	}

	if err := install.RemoveBashrc(path); err != nil {
		t.Fatalf("RemoveBashrc: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("~/.bashrc still exists after remove (err=%v), want removed (pre-install state was no file)", err)
	}
}

// TestBashrcStatusReflectsPresence is a --check-level smoke test: absent on
// a fresh home, present after install, absent again after remove.
func TestBashrcStatusReflectsPresence(t *testing.T) {
	home := t.TempDir()
	path := install.DefaultBashrcPath(home)

	if got := install.BashrcStatus(path); got != install.StatusAbsent {
		t.Errorf("BashrcStatus on fresh home = %s, want absent", got)
	}
	if err := install.InstallBashrc(path); err != nil {
		t.Fatalf("InstallBashrc: %v", err)
	}
	if got := install.BashrcStatus(path); got != install.StatusPresent {
		t.Errorf("BashrcStatus after install = %s, want present", got)
	}
	if err := install.RemoveBashrc(path); err != nil {
		t.Fatalf("RemoveBashrc: %v", err)
	}
	if got := install.BashrcStatus(path); got != install.StatusAbsent {
		t.Errorf("BashrcStatus after remove = %s, want absent", got)
	}
}

// TestInstallBashRefusesReadOnlyBashrc covers the original description's
// "a read-only ~/.bashrc is reported, not clobbered" clause: InstallBashrc
// returns ErrBashrcNotWritable and changes zero bytes.
func TestInstallBashRefusesReadOnlyBashrc(t *testing.T) {
	home := t.TempDir()
	path := install.DefaultBashrcPath(home)
	if err := os.WriteFile(path, []byte(seededBashrc), 0o400); err != nil {
		t.Fatalf("seed read-only ~/.bashrc: %v", err)
	}

	err := install.InstallBashrc(path)
	if err == nil {
		t.Fatalf("InstallBashrc on a read-only ~/.bashrc returned nil error, want ErrBashrcNotWritable")
	}
	if !errors.Is(err, install.ErrBashrcNotWritable) {
		t.Errorf("InstallBashrc error = %v, want it to wrap ErrBashrcNotWritable", err)
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read ~/.bashrc after refused install: %v", readErr)
	}
	if string(got) != seededBashrc {
		t.Fatalf("read-only ~/.bashrc was modified:\ngot:  %q\nwant: %q", got, seededBashrc)
	}
}

// TestInstallBashWritesThroughRegularOwnedSymlinkTarget covers the original
// description's "write through the symlink target only if it is a regular
// file the user owns" clause: ~/.bashrc is a symlink to a real file
// elsewhere; InstallBashrc updates the target's contents and leaves the
// symlink itself in place.
func TestInstallBashWritesThroughRegularOwnedSymlinkTarget(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(t.TempDir(), "real-bashrc")
	if err := os.WriteFile(real, []byte(seededBashrc), 0o644); err != nil {
		t.Fatalf("seed real bashrc target: %v", err)
	}
	path := install.DefaultBashrcPath(home)
	if err := os.Symlink(real, path); err != nil {
		t.Fatalf("symlink ~/.bashrc -> real: %v", err)
	}

	if err := install.InstallBashrc(path); err != nil {
		t.Fatalf("InstallBashrc through symlink: %v", err)
	}

	linkInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat ~/.bashrc after install: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.bashrc is no longer a symlink after install")
	}
	resolved, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink ~/.bashrc after install: %v", err)
	}
	if resolved != real {
		t.Fatalf("~/.bashrc symlink now points at %q, want unchanged %q", resolved, real)
	}

	gotReal, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read symlink target after install: %v", err)
	}
	want := seededBashrc + install.BashrcBlock
	if string(gotReal) != want {
		t.Fatalf("symlink target after install =\n%q\nwant\n%q", gotReal, want)
	}
}
