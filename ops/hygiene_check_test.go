// TestHygieneCheck is task 74c05c78's acceptance clause 2: the hygiene check
// run by `make check` must exit non-zero naming any tracked critic/mutation
// evidence path, and exit 0 on a clean tree.
package ops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runHygiene builds a temp git repo tracking the given files and runs the
// real ops/hygiene-check.sh in it.
func runHygiene(t *testing.T, files ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs("hygiene-check.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec // fixed test literals
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil { //nolint:gosec // t.TempDir()
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil { //nolint:gosec // t.TempDir()
			t.Fatal(err)
		}
	}
	git("add", "-A", "-f")
	cmd := exec.Command(script) //nolint:gosec // repo script path
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestHygieneCheckFailsOnCriticEvidence(t *testing.T) {
	for _, path := range []string{
		"ops/critic-deadbeef/mut-1-x.txt",
		"ops/critic-deadbeef/notes.md",
		"docs/mut-3-foo.txt",
		"ops/make-check.txt",
	} {
		out, err := runHygiene(t, "README.md", path)
		if err == nil {
			t.Fatalf("%s: hygiene check should exit non-zero\n%s", path, out)
		}
		if !strings.Contains(out, path) {
			t.Fatalf("%s: output should name the path, got:\n%s", path, out)
		}
	}
}

func TestHygieneCheckPassesOnCleanTree(t *testing.T) {
	out, err := runHygiene(t, "README.md", "ops/install.sh", "internal/x/testdata/make-check.txt", "internal/x/testdata/mut-1-a.txt", "ops/critic-notes/a.txt")
	if err != nil {
		t.Fatalf("clean tree should exit 0, got %v\n%s", err, out)
	}
}
