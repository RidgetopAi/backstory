package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// records --json carries a handoff's branch (read from a real git repo on
// feature-a) and omits it for a record with an unknown branch; the text
// output shows "on feature-a" (task a3bf318f).
func TestRecordsShowHandoffBranch(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "feature-a", repo}, {"-C", repo, "commit", "-q", "--allow-empty", "-m", "x", "--author=t <t@t>"}} {
		c := exec.Command("git", args...) //nolint:gosec // literal args, test-owned temp dir
		c.Env = append(c.Environ(), "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	branch := project.CurrentBranch(project.RealGit{}, repo)
	if branch != "feature-a" {
		t.Fatalf("real git branch = %q", branch)
	}

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProject(store.Project{Key: recallFixtureProject, Toplevel: recallFixtureProject, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ins := func(text, br string) string {
		id, err := st.InsertRecord(store.InsertRecordParams{
			Identity: store.Identity{Kind: store.IdentityAgent, Actor: "seed"}, Kind: store.KindHandoff,
			Text: text, ProjectKey: recallFixtureProject, GitBranch: br,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	withID := ins("branchy handoff", branch)
	withoutID := ins("unknown branch handoff", "")
	_ = st.Close()

	out, _, code := runRecordsCLI(t, dir, "--project", recallFixtureProject, "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, r := range decodeRecords(t, out) {
		switch r.ID {
		case withID:
			if r.Branch != "feature-a" {
				t.Errorf("branch = %q, want feature-a", r.Branch)
			}
		case withoutID:
			if r.Branch != "" {
				t.Errorf("unknown-branch record carries %q", r.Branch)
			}
		}
	}
	if strings.Count(out, `"branch"`) != 1 {
		t.Errorf("want exactly one branch key (omitted when unknown): %s", out)
	}
	text, _, _ := runRecordsCLI(t, dir, "--project", recallFixtureProject)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if has := strings.Contains(line, "on feature-a"); has != strings.HasPrefix(line, withID[:8]) {
			t.Errorf("text line %q: on-feature-a=%v", line, has)
		}
	}
}
