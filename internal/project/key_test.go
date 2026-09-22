package project_test

import (
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

	main := project.Key("/home/b/main", git)
	worktree := project.Key("/home/b/main/.worktrees/fix", git)

	if main != worktree {
		t.Fatalf("main key %q != worktree key %q, want equal", main, worktree)
	}
}

func TestKeySubdirectoryYieldsRepoKey(t *testing.T) {
	git := fakeGit{
		"/repo":        {CommonDir: "/repo/.git", Toplevel: "/repo"},
		"/repo/pkg/ui": {CommonDir: "/repo/.git", Toplevel: "/repo"},
	}

	root := project.Key("/repo", git)
	sub := project.Key("/repo/pkg/ui", git)

	if root != sub {
		t.Fatalf("repo key %q != subdirectory key %q, want equal", root, sub)
	}
}

func TestKeyDifferentRemotesYieldDifferentKeys(t *testing.T) {
	git := fakeGit{
		"/a": {CommonDir: "/a/.git", RemoteURL: "git@example.com:one.git", Toplevel: "/a"},
		"/b": {CommonDir: "/b/.git", RemoteURL: "git@example.com:two.git", Toplevel: "/b"},
	}

	ka := project.Key("/a", git)
	kb := project.Key("/b", git)

	if ka == kb {
		t.Fatalf("keys for different remotes both = %q, want different", ka)
	}
}

func TestKeyRepoWithNoRemoteUsesToplevel(t *testing.T) {
	git := fakeGit{
		"/repo": {CommonDir: "/repo/.git", RemoteURL: "", Toplevel: "/repo"},
	}

	got := project.Key("/repo", git)
	if got != "/repo" {
		t.Fatalf("Key = %q, want toplevel /repo", got)
	}
}

func TestKeyNonGitDirIsItsOwnPath(t *testing.T) {
	git := fakeGit{} // empty: no cwd is a git working tree

	got := project.Key("/tmp/scratch", git)
	if got != "/tmp/scratch" {
		t.Fatalf("Key = %q, want /tmp/scratch", got)
	}
}
