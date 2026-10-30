// TestMakeReleaseCheck is the punch's acceptance clause 1 (task
// 6b4b5367): `make release-check VERSION=...` must exit 0 for a version with
// a matching CHANGELOG.md section and PKGBUILD pkgver, and non-zero for a
// non-semver version, a missing CHANGELOG section, or a pkgver mismatch.
package ops

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const realSHA = "1e5516887975bbc2a7a3bb403adb0d53cbcb5795a95a469380ffc9389a844ecf"

// runReleaseCheck copies the real Makefile and ops/release-check.sh into a
// scratch directory alongside the given CHANGELOG.md / PKGBUILD content,
// then runs `make release-check VERSION=<version>` from that directory so
// the test exercises the actual make target wiring, not just the script.
func runReleaseCheck(t *testing.T, version, changelog, pkgbuild string) (output string, runErr error) {
	t.Helper()
	return runReleaseCheckSrcinfo(t, version, changelog, pkgbuild, srcinfoFor(pkgbuild))
}

// srcinfoFor derives a .SRCINFO that agrees with the given PKGBUILD text.
func srcinfoFor(pkgbuild string) string {
	ver := regexp.MustCompile(`(?m)^pkgver=(.*)$`).FindStringSubmatch(pkgbuild)
	sha := regexp.MustCompile(`(?m)^sha256sums=\('([^']*)'\)$`).FindStringSubmatch(pkgbuild)
	out := "pkgbase = backstory\n"
	if ver != nil {
		out += "\tpkgver = " + ver[1] + "\n"
	}
	if sha != nil {
		out += "\tsha256sums = " + sha[1] + "\n"
	}
	return out + "\npkgname = backstory\n"
}

func runReleaseCheckSrcinfo(t *testing.T, version, changelog, pkgbuild, srcinfo string) (output string, runErr error) {
	t.Helper()

	makefile, err := os.ReadFile(filepath.Join("..", "Makefile")) //nolint:gosec // fixed repo-relative path, not external input
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	script, err := os.ReadFile("release-check.sh") //nolint:gosec // fixed repo-relative path, not external input
	if err != nil {
		t.Fatalf("read release-check.sh: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), makefile, 0o600); err != nil { //nolint:gosec // dir is this test's own t.TempDir(), not external input
		t.Fatalf("write Makefile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ops", "aur"), 0o750); err != nil { //nolint:gosec // dir is this test's own t.TempDir(), not external input
		t.Fatalf("mkdir ops/aur: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ops", "release-check.sh"), script, 0o700); err != nil { //nolint:gosec // dir is this test's own t.TempDir(); needs +x
		t.Fatalf("write release-check.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ops", "aur", "PKGBUILD"), []byte(pkgbuild), 0o600); err != nil { //nolint:gosec // dir is this test's own t.TempDir(), not external input
		t.Fatalf("write PKGBUILD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ops", "aur", ".SRCINFO"), []byte(srcinfo), 0o600); err != nil { //nolint:gosec // dir is this test's own t.TempDir(), not external input
		t.Fatalf("write .SRCINFO: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(changelog), 0o600); err != nil { //nolint:gosec // dir is this test's own t.TempDir(), not external input
		t.Fatalf("write CHANGELOG.md: %v", err)
	}

	cmd := exec.Command("make", "release-check", "VERSION="+version) //nolint:gosec // version is a fixed test literal, not external input
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

const validChangelog = "## [0.9.0] - 2026-09-23\n\n### Added\n\n- Something.\n"
const validPKGBUILD = "pkgname=backstory\npkgver=0.9.0\npkgrel=1\nsha256sums=('" + realSHA + "')\n"

func TestMakeReleaseCheckValidVersionPasses(t *testing.T) {
	out, err := runReleaseCheck(t, "v0.9.0", validChangelog, validPKGBUILD)
	if err != nil {
		t.Fatalf("release-check on a matching version should exit 0, got %v\noutput:\n%s", err, out)
	}
}

func TestMakeReleaseCheckNonSemverVersionFails(t *testing.T) {
	out, err := runReleaseCheck(t, "not-semver", validChangelog, validPKGBUILD)
	if err == nil {
		t.Fatalf("release-check on a non-semver VERSION should fail, output:\n%s", out)
	}
}

func TestMakeReleaseCheckMissingChangelogSectionFails(t *testing.T) {
	changelog := "## [0.8.0] - 2026-01-01\n\n### Added\n\n- Older stuff.\n"
	out, err := runReleaseCheck(t, "v0.9.0", changelog, validPKGBUILD)
	if err == nil {
		t.Fatalf("release-check with no CHANGELOG section for VERSION should fail, output:\n%s", out)
	}
}

func TestMakeReleaseCheckPkgverMismatchFails(t *testing.T) {
	pkgbuild := "pkgname=backstory\npkgver=0.8.0\npkgrel=1\nsha256sums=('" + realSHA + "')\n"
	out, err := runReleaseCheck(t, "v0.9.0", validChangelog, pkgbuild)
	if err == nil {
		t.Fatalf("release-check with a mismatched PKGBUILD pkgver should fail, output:\n%s", out)
	}
}

func pkgbuildWithSHA(sha string) string {
	return "pkgname=backstory\npkgver=0.9.0\npkgrel=1\nsha256sums=('" + sha + "')\n"
}

func TestMakeReleaseCheckRefusesSKIPSha256sums(t *testing.T) {
	out, err := runReleaseCheck(t, "v0.9.0", validChangelog, pkgbuildWithSHA("SKIP"))
	if err == nil {
		t.Fatalf("release-check with sha256sums=('SKIP') should fail, output:\n%s", out)
	}
	if !strings.Contains(out, "sha256sums") {
		t.Fatalf("failure message should name sha256sums, got:\n%s", out)
	}
}

func TestMakeReleaseCheckRefusesMalformedSha256sums(t *testing.T) {
	for _, bad := range []string{"abc123", strings.ToUpper(realSHA), realSHA + "0", "g" + realSHA[1:]} {
		out, err := runReleaseCheck(t, "v0.9.0", validChangelog, pkgbuildWithSHA(bad))
		if err == nil {
			t.Fatalf("release-check with sha %q should fail, output:\n%s", bad, out)
		}
		if !strings.Contains(out, "sha256sums") {
			t.Fatalf("failure message for %q should name sha256sums, got:\n%s", bad, out)
		}
	}
}

func TestMakeReleaseCheckSrcinfoDisagreementFails(t *testing.T) {
	otherSHA := strings.Repeat("a", 64)
	cases := map[string]string{
		"sha": "pkgbase = backstory\n\tpkgver = 0.9.0\n\tsha256sums = " + otherSHA + "\n",
		"ver": "pkgbase = backstory\n\tpkgver = 0.8.0\n\tsha256sums = " + realSHA + "\n",
	}
	for name, srcinfo := range cases {
		out, err := runReleaseCheckSrcinfo(t, "v0.9.0", validChangelog, validPKGBUILD, srcinfo)
		if err == nil {
			t.Fatalf("%s: release-check with a disagreeing .SRCINFO should fail, output:\n%s", name, out)
		}
	}
}

// TestRealAURFiles pins the committed ops/aur files: release-check passes on
// them, no SKIP survives, and .SRCINFO agrees with the PKGBUILD on pkgver,
// source and sha256sums.
func TestRealAURFiles(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("aur", name)) //nolint:gosec // fixed repo-relative path, not external input
		if err != nil {
			t.Fatalf("read aur/%s: %v", name, err)
		}
		return string(b)
	}
	pkgbuild, srcinfo := read("PKGBUILD"), read(".SRCINFO")
	for _, name := range []string{"PKGBUILD", ".SRCINFO", "backstory.install"} {
		if strings.Contains(read(name), "SKIP") {
			t.Errorf("aur/%s contains SKIP", name)
		}
	}
	field := func(text, re string) string {
		m := regexp.MustCompile(re).FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("no match for %s", re)
		}
		return m[1]
	}
	pkgver := field(pkgbuild, `(?m)^pkgver=(.*)$`)
	if got := field(srcinfo, `(?m)^\s*pkgver = (.*)$`); got != pkgver {
		t.Errorf(".SRCINFO pkgver %q != PKGBUILD %q", got, pkgver)
	}
	pkgname := field(pkgbuild, `(?m)^pkgname=(.*)$`)
	src := field(pkgbuild, `(?m)^source=\("([^"]*)"\)$`)
	src = strings.NewReplacer("$pkgname", pkgname, "$pkgver", pkgver).Replace(src)
	if got := field(srcinfo, `(?m)^\s*source = (.*)$`); got != src {
		t.Errorf(".SRCINFO source %q != PKGBUILD %q", got, src)
	}
	sha := field(pkgbuild, `(?m)^sha256sums=\('([^']*)'\)$`)
	if got := field(srcinfo, `(?m)^\s*sha256sums = (.*)$`); got != sha {
		t.Errorf(".SRCINFO sha256sums %q != PKGBUILD %q", got, sha)
	}
	if sha != realSHA {
		t.Errorf("PKGBUILD sha256sums %q != measured v1.1.0 tarball sha", sha)
	}

	cmd := exec.Command("make", "release-check", "VERSION=v"+pkgver) //nolint:gosec // pkgver read from the repo's own PKGBUILD
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release-check on the real files should exit 0: %v\n%s", err, out)
	}
}
