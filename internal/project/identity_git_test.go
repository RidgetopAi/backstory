package project_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/project"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestKeyWorktreeSharesKeyAndSecondCloneDoesNot is DONE WHEN clause 4 (task
// 029485ae), against real git: a worktree of foo keys the same as foo, and a
// second clone of the same remote at another path keys differently — with or
// without a remote configured, before or after it changes.
func TestKeyWorktreeSharesKeyAndSecondCloneDoesNot(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	for _, v := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v)
	}
	// An explicit workspace list, so the ambient BACKSTORY_WORKSPACE_DIRS
	// can never change the answer.
	ws := []string{filepath.Join(home, "projects")}
	git := project.RealGit{}

	foo := filepath.Join(home, "projects", "foo")
	if err := os.MkdirAll(foo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, foo, "init")
	gitIn(t, foo, "commit", "--allow-empty", "-m", "init")
	noRemote := project.Key(foo, git, ws)

	gitIn(t, foo, "remote", "add", "origin", "https://example.invalid/foo.git")
	withRemote := project.Key(foo, git, ws)
	gitIn(t, foo, "remote", "set-url", "origin", "https://example.invalid/foo2.git")
	changed := project.Key(foo, git, ws)
	if noRemote != withRemote || withRemote != changed {
		t.Fatalf("key moved with the remote: none=%q added=%q changed=%q", noRemote, withRemote, changed)
	}

	wt := filepath.Join(home, "wt")
	gitIn(t, foo, "worktree", "add", "-b", "side", wt)
	if got := project.Key(wt, git, ws); got != withRemote {
		t.Fatalf("worktree key = %q, want foo's %q", got, withRemote)
	}
	sub := filepath.Join(foo, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := project.Key(sub, git, ws); got != withRemote {
		t.Fatalf("subdirectory key = %q, want %q", got, withRemote)
	}

	copyDir := filepath.Join(home, "projects", "foo-copy")
	gitIn(t, home, "clone", "-q", foo, copyDir)
	gitIn(t, copyDir, "remote", "set-url", "origin", "https://example.invalid/foo2.git") // same remote URL as foo
	if got := project.Key(copyDir, git, ws); got == withRemote {
		t.Fatalf("second clone of the same remote keys the same as foo: %q", got)
	}
}
