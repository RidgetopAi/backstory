package ops

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMakeUninstallLeavesNoHarnessReferencingBackstory: after make install
// and make uninstall against a fixture HOME holding claude, codex, pi, hermes
// and a bashrc (the service manager is the recording fake), nothing under
// ~/.claude, ~/.claude.json, ~/.codex, ~/.pi, ~/.hermes or ~/.bashrc mentions
// backstory — before this, uninstall removed the binary and left every
// harness's hooks calling it.
func TestMakeUninstallLeavesNoHarnessReferencingBackstory(t *testing.T) {
	f := newInstallFixture(t)
	for _, d := range []string{".claude", ".codex", ".pi/agent", ".hermes"} {
		if err := os.MkdirAll(f.path(d), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(f.path(".bashrc"), []byte("# my bashrc\nalias ll='ls -l'\n"), 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}

	f.mustMake("install")
	installed := f.path(".local", "bin", "backstory")
	cmd := exec.Command(installed, "install", "bash") //nolint:gosec // the binary this test just built and installed
	cmd.Env = append(os.Environ(), "HOME="+f.home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install bash: %v\n%s", err, out)
	}
	// Precondition: the integrations exist, or the assertion below is vacuous.
	if refs := backstoryReferences(t, f.home); len(refs) < 4 {
		t.Fatalf("fixture install wrote too few integrations to prove anything: %v", refs)
	}

	f.mustMake("uninstall")

	mustNotExist(t, installed)
	if refs := backstoryReferences(t, f.home); len(refs) > 0 {
		t.Errorf("harness files still reference backstory after make uninstall: %v", refs)
	}
	if b, err := os.ReadFile(f.path(".bashrc")); err != nil || string(b) != "# my bashrc\nalias ll='ls -l'\n" { //nolint:gosec // fixture path under t.TempDir
		t.Errorf("bashrc not restored: %q (%v)", b, err)
	}
}

// backstoryReferences lists the files under the harness locations of home
// whose content mentions backstory.
func backstoryReferences(t *testing.T, home string) []string {
	t.Helper()
	var refs []string
	for _, root := range []string{".claude", ".claude.json", ".codex", ".pi", ".hermes", ".bashrc"} {
		err := filepath.WalkDir(filepath.Join(home, root), func(p string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			b, err := os.ReadFile(p) //nolint:gosec // fixture path under t.TempDir
			if err != nil {
				return err
			}
			if strings.Contains(strings.ToLower(string(b)), "backstory") {
				refs = append(refs, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return refs
}
