package project_test

import (
	"testing"

	"github.com/RidgetopAi/backstory/internal/project"
)

// TestWorkspaceHomeCoversAnyDepth is decision f3fa04c7's HOME containment
// check: the workspace dir itself, a direct child, and a deeper descendant
// all resolve to the SAME workspace key, unlike workspaceOf's (Key's own,
// unexported) direct-child-only rule.
func TestWorkspaceHomeCoversAnyDepth(t *testing.T) {
	workspaces := []string{"/home/b/projects"}

	cases := []struct {
		cwd  string
		want string
		ok   bool
	}{
		{"/home/b/projects", "workspace:/home/b/projects", true},
		{"/home/b/projects/omarcade", "workspace:/home/b/projects", true},
		{"/home/b/projects/omarcade/sub/deeper", "workspace:/home/b/projects", true},
		{"/home/b/other", "", false},
		{"/home/b/projects-nope", "", false}, // must not match on a mere string prefix
	}
	for _, c := range cases {
		got, ok := project.WorkspaceHome(c.cwd, workspaces)
		if ok != c.ok || got != c.want {
			t.Errorf("WorkspaceHome(%q, %v) = (%q, %v), want (%q, %v)", c.cwd, workspaces, got, ok, c.want, c.ok)
		}
	}
}

// TestWorkspaceRelativeName is decision f3fa04c7's LABELS display rule:
// workspace-relative inside a workspace, plain basename outside one — a
// strict superset of the old filepath.Base(toplevel) display name.
func TestWorkspaceRelativeName(t *testing.T) {
	workspaces := []string{"/home/b/projects"}

	cases := []struct {
		dir  string
		want string
	}{
		{"/home/b/projects/omarcade", "projects/omarcade"},
		{"/home/b/projects", "projects"},
		{"/home/b/other-repo", "other-repo"},
		{"/home/b/projects-nope", "projects-nope"}, // no false-positive on a string prefix
	}
	for _, c := range cases {
		if got := project.WorkspaceRelativeName(c.dir, workspaces); got != c.want {
			t.Errorf("WorkspaceRelativeName(%q, %v) = %q, want %q", c.dir, workspaces, got, c.want)
		}
	}
}

// labelFakeGit is a canned dir -> Repo lookup for Label/LabelForPath tests.
type labelFakeGit map[string]project.Repo

func (f labelFakeGit) Repo(dir string) (project.Repo, bool) { r, ok := f[dir]; return r, ok }
func (f labelFakeGit) State(string) (project.State, bool)   { return project.State{}, false }

// TestLabelResolvesToContainingRepoToplevel is decision f3fa04c7's LABELS
// rule: a path inside a git repo labels by the REPO's toplevel, made
// workspace-relative, not by the path's own directory.
func TestLabelResolvesToContainingRepoToplevel(t *testing.T) {
	workspaces := []string{"/home/b/projects"}
	git := labelFakeGit{
		"/home/b/projects/omarcade/sub": {Toplevel: "/home/b/projects/omarcade"},
	}
	got := project.LabelForPath("/home/b/projects/omarcade/sub/main.go", git, workspaces)
	if want := "projects/omarcade"; got != want {
		t.Errorf("LabelForPath(.../sub/main.go) = %q, want %q", got, want)
	}
}

// TestLabelFallsBackToDirOutsideAnyRepo is LABELS' non-git fallback: a path
// whose directory is not inside a git working tree labels by the directory
// itself, workspace-relative.
func TestLabelFallsBackToDirOutsideAnyRepo(t *testing.T) {
	workspaces := []string{"/home/b/projects"}
	git := labelFakeGit{} // every dir reports "not a git repo"
	got := project.LabelForPath("/home/b/projects/notes/todo.txt", git, workspaces)
	if want := "projects/notes"; got != want {
		t.Errorf("LabelForPath(notes/todo.txt) = %q, want %q (the folder containing it, workspace-relative)", got, want)
	}
}
