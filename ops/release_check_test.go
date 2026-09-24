// TestMakeReleaseCheck is the punch's acceptance clause 1 (task
// 6b4b5367): `make release-check VERSION=...` must exit 0 for a version with
// a matching CHANGELOG.md section and PKGBUILD pkgver, and non-zero for a
// non-semver version, a missing CHANGELOG section, or a pkgver mismatch.
package ops

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runReleaseCheck copies the real Makefile and ops/release-check.sh into a
// scratch directory alongside the given CHANGELOG.md / PKGBUILD content,
// then runs `make release-check VERSION=<version>` from that directory so
// the test exercises the actual make target wiring, not just the script.
func runReleaseCheck(t *testing.T, version, changelog, pkgbuild string) (exitErr error, output string) {
	t.Helper()

	makefile, err := os.ReadFile(filepath.Join("..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	script, err := os.ReadFile("release-check.sh")
	if err != nil {
		t.Fatalf("read release-check.sh: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), makefile, 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ops", "aur"), 0o755); err != nil {
		t.Fatalf("mkdir ops/aur: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ops", "release-check.sh"), script, 0o755); err != nil {
		t.Fatalf("write release-check.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ops", "aur", "PKGBUILD"), []byte(pkgbuild), 0o644); err != nil {
		t.Fatalf("write PKGBUILD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(changelog), 0o644); err != nil {
		t.Fatalf("write CHANGELOG.md: %v", err)
	}

	cmd := exec.Command("make", "release-check", "VERSION="+version)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return err, string(out)
}

const validChangelog = "## [0.9.0] - 2026-09-23\n\n### Added\n\n- Something.\n"
const validPKGBUILD = "pkgname=backstory\npkgver=0.9.0\npkgrel=1\n"

func TestMakeReleaseCheckValidVersionPasses(t *testing.T) {
	err, out := runReleaseCheck(t, "v0.9.0", validChangelog, validPKGBUILD)
	if err != nil {
		t.Fatalf("release-check on a matching version should exit 0, got %v\noutput:\n%s", err, out)
	}
}

func TestMakeReleaseCheckNonSemverVersionFails(t *testing.T) {
	err, out := runReleaseCheck(t, "not-semver", validChangelog, validPKGBUILD)
	if err == nil {
		t.Fatalf("release-check on a non-semver VERSION should fail, output:\n%s", out)
	}
}

func TestMakeReleaseCheckMissingChangelogSectionFails(t *testing.T) {
	changelog := "## [0.8.0] - 2026-01-01\n\n### Added\n\n- Older stuff.\n"
	err, out := runReleaseCheck(t, "v0.9.0", changelog, validPKGBUILD)
	if err == nil {
		t.Fatalf("release-check with no CHANGELOG section for VERSION should fail, output:\n%s", out)
	}
}

func TestMakeReleaseCheckPkgverMismatchFails(t *testing.T) {
	pkgbuild := "pkgname=backstory\npkgver=0.8.0\npkgrel=1\n"
	err, out := runReleaseCheck(t, "v0.9.0", validChangelog, pkgbuild)
	if err == nil {
		t.Fatalf("release-check with a mismatched PKGBUILD pkgver should fail, output:\n%s", out)
	}
}
