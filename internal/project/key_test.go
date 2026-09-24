package project_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/RidgetopAi/backstory/internal/project"
)

// fakeGit is a canned cwd -> Repo lookup; no test in this file shells out to
// a real git binary.
type fakeGit map[string]project.Repo

func (f fakeGit) Repo(cwd string) (project.Repo, bool) {
	r, ok := f[cwd]
	return r, ok
}

func TestKeyWorktreeAndMainCheckoutShareKey(t *testing.T) {
	git := fakeGit{
		"/home/b/main": {
			CommonDir: "/home/b/main/.git",
			RemoteURL: "git@example.com:ridgetopai/backstory.git",
			Toplevel:  "/home/b/main",
		},
		"/home/b/main/.worktrees/fix": {
			CommonDir: "/home/b/main/.git", // worktree-safe: shares the main checkout's common dir
			RemoteURL: "git@example.com:ridgetopai/backstory.git",
			Toplevel:  "/home/b/main/.worktrees/fix",
		},
	}

	main := project.Key("/home/b/main", git, nil)
	worktree := project.Key("/home/b/main/.worktrees/fix", git, nil)

	if main != worktree {
		t.Fatalf("main key %q != worktree key %q, want equal", main, worktree)
	}
}

func TestKeySubdirectoryYieldsRepoKey(t *testing.T) {
	git := fakeGit{
		"/repo":        {CommonDir: "/repo/.git", Toplevel: "/repo"},
		"/repo/pkg/ui": {CommonDir: "/repo/.git", Toplevel: "/repo"},
	}

	root := project.Key("/repo", git, nil)
	sub := project.Key("/repo/pkg/ui", git, nil)

	if root != sub {
		t.Fatalf("repo key %q != subdirectory key %q, want equal", root, sub)
	}
}

func TestKeyDifferentRemotesYieldDifferentKeys(t *testing.T) {
	git := fakeGit{
		"/a": {CommonDir: "/a/.git", RemoteURL: "git@example.com:one.git", Toplevel: "/a"},
		"/b": {CommonDir: "/b/.git", RemoteURL: "git@example.com:two.git", Toplevel: "/b"},
	}

	ka := project.Key("/a", git, nil)
	kb := project.Key("/b", git, nil)

	if ka == kb {
		t.Fatalf("keys for different remotes both = %q, want different", ka)
	}
}

func TestKeyRepoWithNoRemoteUsesToplevel(t *testing.T) {
	git := fakeGit{
		"/repo": {CommonDir: "/repo/.git", RemoteURL: "", Toplevel: "/repo"},
	}

	got := project.Key("/repo", git, nil)
	if got != "/repo" {
		t.Fatalf("Key = %q, want toplevel /repo", got)
	}
}

func TestKeyNonGitDirIsItsOwnPath(t *testing.T) {
	git := fakeGit{} // empty: no cwd is a git working tree

	got := project.Key("/tmp/scratch", git, nil)
	if got != "/tmp/scratch" {
		t.Fatalf("Key = %q, want /tmp/scratch", got)
	}
}

// TestKeyWorkspaceDirIsWorkspaceIdentity is proof (1) for task 79f7b20e
// (decision bcc9fa54): a session started in a folder that holds many repos
// (e.g. ~/projects) must resolve to a distinct workspace identity, never an
// ordinary project key equal to the dir itself.
func TestKeyWorkspaceDirIsWorkspaceIdentity(t *testing.T) {
	git := fakeGit{} // empty: no cwd is a git working tree
	workspaces := []string{"/home/b/projects"}

	got := project.Key("/home/b/projects", git, workspaces)
	want := "workspace:/home/b/projects"
	if got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
}

// TestKeyNonGitChildOfWorkspaceIsWorkspaceIdentity covers the original
// description's second clause: a non-git dir directly under a workspace
// (e.g. a scratch folder dropped in ~/projects) is also a workspace, not a
// project of its own.
func TestKeyNonGitChildOfWorkspaceIsWorkspaceIdentity(t *testing.T) {
	git := fakeGit{}
	workspaces := []string{"/home/b/projects"}

	got := project.Key("/home/b/projects/scratch-notes", git, workspaces)
	want := "workspace:/home/b/projects"
	if got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
}

// TestKeyRepoUnderWorkspaceYieldsRepoKeyUnchanged is proof (1)'s second
// clause: a real repo living under a workspace dir must still resolve to
// its own repo key, exactly as it did before the workspace rule existed.
func TestKeyRepoUnderWorkspaceYieldsRepoKeyUnchanged(t *testing.T) {
	git := fakeGit{
		"/home/b/projects/backstory": {
			CommonDir: "/home/b/projects/backstory/.git",
			RemoteURL: "git@example.com:ridgetopai/backstory.git",
			Toplevel:  "/home/b/projects/backstory",
		},
	}
	workspaces := []string{"/home/b/projects"}

	got := project.Key("/home/b/projects/backstory", git, workspaces)
	want := "/home/b/projects/backstory/.git|git@example.com:ridgetopai/backstory.git"
	if got != want {
		t.Fatalf("Key = %q, want %q (repo key unchanged despite living under a workspace dir)", got, want)
	}
}

// TestKeyNonWorkspaceNonGitDirBehavesAsToday is proof (1)'s third clause: a
// dir that is neither a git repo nor in the configured workspace list is
// unaffected by the workspace rule.
func TestKeyNonWorkspaceNonGitDirBehavesAsToday(t *testing.T) {
	git := fakeGit{}
	workspaces := []string{"/home/b/projects"} // configured, but unrelated to /tmp/scratch

	got := project.Key("/tmp/scratch", git, workspaces)
	if got != "/tmp/scratch" {
		t.Fatalf("Key = %q, want /tmp/scratch (unaffected by an unrelated workspace list)", got)
	}
}

// TestKeyWorkspaceListComesFromConfigNotHardcoded is proof (2): the same
// cwd is a workspace only when it appears in the list Key is given, never
// because some path is hard-coded into the resolver.
func TestKeyWorkspaceListComesFromConfigNotHardcoded(t *testing.T) {
	git := fakeGit{}

	notConfigured := project.Key("/srv/repos", git, []string{"/home/b/projects"})
	if notConfigured != "/srv/repos" {
		t.Fatalf("Key = %q, want /srv/repos when /srv/repos is not in the configured list", notConfigured)
	}

	configured := project.Key("/srv/repos", git, []string{"/srv/repos"})
	if configured != "workspace:/srv/repos" {
		t.Fatalf("Key = %q, want workspace:/srv/repos when /srv/repos is the configured list", configured)
	}
}

// TestDefaultWorkspaceDirsDefaultsToHomeProjects is proof (3): HOME is
// injected via t.Setenv rather than read from the invoking environment, so
// this test's result does not depend on whether that environment happens
// to have a real home directory set.
func TestDefaultWorkspaceDirsDefaultsToHomeProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", "")

	dirs, err := project.DefaultWorkspaceDirs()
	if err != nil {
		t.Fatalf("DefaultWorkspaceDirs: %v", err)
	}
	want := []string{filepath.Join(home, "projects")}
	if !reflect.DeepEqual(dirs, want) {
		t.Fatalf("DefaultWorkspaceDirs = %v, want %v", dirs, want)
	}
}

// TestDefaultWorkspaceDirsHonorsEnvOverride proves the list is genuinely
// configuration: BACKSTORY_WORKSPACE_DIRS wins even though HOME is also
// set to a real (injected) home directory in this process.
func TestDefaultWorkspaceDirsHonorsEnvOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", "/a/one"+string(os.PathListSeparator)+"/b/two")

	dirs, err := project.DefaultWorkspaceDirs()
	if err != nil {
		t.Fatalf("DefaultWorkspaceDirs: %v", err)
	}
	want := []string{"/a/one", "/b/two"}
	if !reflect.DeepEqual(dirs, want) {
		t.Fatalf("DefaultWorkspaceDirs = %v, want %v", dirs, want)
	}
}

// TestDefaultWorkspaceDirsWithoutHomeReturnsError is proof (3)'s "without
// HOME set" half: with no override and no home directory available, the
// resolver fails cleanly instead of silently resolving to "" or panicking.
func TestDefaultWorkspaceDirsWithoutHomeReturnsError(t *testing.T) {
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", "")
	t.Setenv("HOME", "")

	if _, err := project.DefaultWorkspaceDirs(); err == nil {
		t.Fatal("DefaultWorkspaceDirs: want error when HOME is unset, got nil")
	}
}

// TestKeyContainsNoByteBelowSpace is proof (3) for task e7951178: the
// separator joining CommonDir and RemoteURL must be printable, or `status`
// returns it over JSON as a \u0000 (or similar) escape that every agent
// renders literally instead of a real separator (critic T1 on 14704ebe).
func TestKeyContainsNoByteBelowSpace(t *testing.T) {
	git := fakeGit{
		"/repo": {CommonDir: "/repo/.git", RemoteURL: "git@example.com:ridgetopai/backstory.git", Toplevel: "/repo"},
	}

	got := project.Key("/repo", git, nil)
	for _, b := range []byte(got) {
		if b < 0x20 {
			t.Fatalf("Key = %q, contains byte 0x%02x below 0x20", got, b)
		}
	}
}
