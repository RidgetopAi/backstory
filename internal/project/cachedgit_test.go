package project_test

import (
	"testing"

	"github.com/RidgetopAi/backstory/internal/project"
)

// countingGit answers Repo from a fixed map and counts every call it gets.
type countingGit struct {
	repos      map[string]project.Repo
	repoCalls  map[string]int
	stateCalls int
}

func (g *countingGit) Repo(cwd string) (project.Repo, bool) {
	g.repoCalls[cwd]++
	r, ok := g.repos[cwd]
	return r, ok
}

func (g *countingGit) State(string) (project.State, bool) {
	g.stateCalls++
	return project.State{Branch: "main"}, true
}

func TestCachedGitAsksForEachFolderOnce(t *testing.T) {
	inner := &countingGit{
		repos:     map[string]project.Repo{"/r": {CommonDir: "/r/.git", Toplevel: "/r"}},
		repoCalls: map[string]int{},
	}
	g := project.NewCachedGit(inner)

	for i := 0; i < 3; i++ {
		if r, ok := g.Repo("/r"); !ok || r.CommonDir != "/r/.git" || r.Toplevel != "/r" {
			t.Fatalf("Repo(/r) = %+v, %v; want the inner answer", r, ok)
		}
		if _, ok := g.Repo("/not-a-repo"); ok {
			t.Fatalf("Repo(/not-a-repo) ok = true; want false")
		}
	}
	if inner.repoCalls["/r"] != 1 || inner.repoCalls["/not-a-repo"] != 1 {
		t.Errorf("inner Repo calls = %v; want each folder asked once, including one that is not a repo", inner.repoCalls)
	}
}

func TestCachedGitStateIsNeverCached(t *testing.T) {
	inner := &countingGit{repoCalls: map[string]int{}}
	g := project.NewCachedGit(inner)

	g.State("/r")
	g.State("/r")
	if inner.stateCalls != 2 {
		t.Errorf("inner State calls = %d; want 2 (uncommitted counts can change at any time)", inner.stateCalls)
	}
}
