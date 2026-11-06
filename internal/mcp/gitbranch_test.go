package mcp

import (
	"testing"

	"github.com/RidgetopAi/backstory/internal/project"
)

// The note path stamps the writing session's branch next to git_head, and a
// detached HEAD or a non-git folder stamps nothing (task 64cd2b5e).
func TestNoteStampsGitBranch(t *testing.T) {
	applyGitStampEnv(t, gitStampEnvModes[0])
	repo := newTempRepo(t)
	st := mustOpenStore(t)
	shim := dialShim(t, testDaemonWithGit(t, st, "claude", repo, "git-proj", fakeGit{repo: project.State{Branch: "feature-a"}}))

	id := noteText(t, shim, "handoff", "on feature-a")
	rec, err := st.GetRecord(id)
	if err != nil || rec.GitBranch != "feature-a" {
		t.Fatalf("git_branch = %q, %v; want feature-a", rec.GitBranch, err)
	}

	// Detached HEAD: git reports the branch as "HEAD", which names no branch.
	detached := dialShim(t, testDaemonWithGit(t, st, "claude", repo, "git-proj", fakeGit{repo: project.State{Branch: "HEAD"}}))
	id = noteText(t, detached, "handoff", "detached")
	if rec, _ := st.GetRecord(id); rec.GitBranch != "" {
		t.Errorf("detached HEAD stamped %q, want none", rec.GitBranch)
	}

	plain := t.TempDir()
	st2 := mustOpenStore(t)
	shim2 := dialShim(t, testDaemonWithGit(t, st2, "claude", plain, "plain-proj", fakeGit{}))
	id = noteText(t, shim2, "handoff", "no git")
	if rec, _ := st2.GetRecord(id); rec.GitBranch != "" || rec.GitHead != "" {
		t.Errorf("non-git folder stamped branch %q head %q", rec.GitBranch, rec.GitHead)
	}
}
