package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// runBackstoryThisWeek runs `backstory this-week --json` as a subprocess of
// bin with env, mirroring runBackstoryGroup's shape — used here instead of
// this file's own package's in-process runThisWeekCLI helper, which pins
// BACKSTORY_WORKSPACE_DIRS to a fixed fixture dir this test must not share.
func runBackstoryThisWeek(t *testing.T, bin string, env []string, dir string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, "this-week", "--json") //nolint:gosec // bin is a binary this test just built
	cmd.Env = env
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run backstory this-week --json: %v (stderr: %s)", err, errOut.String())
	}
	return out.String(), errOut.String(), exitCode
}

// buildBackstoryFromRef builds the backstory binary from git ref ref, in a
// throwaway worktree, and returns its path — DONE WHEN clause 8's
// cross-binary proof needs an actual pre-punch (main) build, not just the
// current source tree in-process.
func buildBackstoryFromRef(t *testing.T, ref string) string {
	t.Helper()
	worktree := filepath.Join(t.TempDir(), "worktree")
	if out, err := exec.Command("git", "worktree", "add", "--detach", worktree, ref).CombinedOutput(); err != nil { //nolint:gosec // ref is this test's own literal, worktree is a t.TempDir() path
		t.Fatalf("git worktree add %s %s: %v\n%s", worktree, ref, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("git", "worktree", "remove", "--force", worktree).Run() })

	bin := filepath.Join(t.TempDir(), "backstory-"+ref)
	build := exec.Command("go", "build", "-o", bin, "./cmd/backstory") //nolint:gosec // fixed literal args, bin is this test's own t.TempDir() path
	build.Dir = worktree
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build (ref %s): %v\n%s", ref, out, err)
	}
	return bin
}

// seedLegacyWorkspaceSession opens dbPath with no workspace dirs configured
// (so writes land under whatever literal key they're given, exactly what a
// pre-79f7b20e build would have produced) and seeds BOTH workspaceDir's
// plain-key projects row (with a session, so This Week's activity window
// has something to (mis)classify if the leak this punch fixes ever
// reappeared) and its canonical workspace:-prefixed projects row: main's OWN
// build already canonicalizes a `group set` write (task 4fe02e30), so its
// project_groups insert foreign-keys into the canonical row, not the plain
// one — a store project_groups.project_key can land on either row before
// this punch's sweep runs.
func seedLegacyWorkspaceSession(t *testing.T, dbPath, workspaceDir string) {
	t.Helper()
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	if err := st.UpsertProject(store.Project{Key: workspaceDir, Toplevel: workspaceDir, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", workspaceDir, err)
	}
	if _, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: workspaceDir, ProjectKey: workspaceDir,
		StartedAt: time.Now(), Origin: store.OriginLive,
	}); err != nil {
		t.Fatalf("StartSession(%s): %v", workspaceDir, err)
	}
	if err := st.UpsertProject(store.Project{Key: "workspace:" + workspaceDir, Toplevel: workspaceDir, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject(workspace:%s): %v", workspaceDir, err)
	}
}

// TestMainBuiltGroupSetOnLegacyKeyIsCanonicalUnderBranchTools is DONE WHEN
// clause 8: `backstory group set` run with main's OWN build (before this
// punch's sweep/canonicalizer existed) against a plain, un-prefixed
// workspace path still lands under the canonical "workspace:" key once the
// BRANCH's tools open the same store — group list shows it once, keyed
// canonically, with the plain key nowhere in the output; group clear
// reverses it; this-week never surfaces the plain key either.
func TestMainBuiltGroupSetOnLegacyKeyIsCanonicalUnderBranchTools(t *testing.T) {
	mainBin := buildBackstoryFromRef(t, "main")
	branchBin := buildBackstory(t)

	dataDir := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "projects")
	env := testXDGEnv("XDG_DATA_HOME="+dataDir, "BACKSTORY_WORKSPACE_DIRS="+workspaceDir)
	wsKey := "workspace:" + workspaceDir

	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	seedLegacyWorkspaceSession(t, dbPath, workspaceDir)

	if _, stderr, code := runBackstoryGroup(t, mainBin, env, t.TempDir(), "set", "old", workspaceDir); code != 0 {
		t.Fatalf("main-built group set old %s: exit %d (stderr: %s)", workspaceDir, code, stderr)
	}

	listOut, stderr, code := runBackstoryGroup(t, branchBin, env, t.TempDir(), "list", "--json")
	if code != 0 {
		t.Fatalf("branch group list --json: exit %d (stderr: %s)", code, stderr)
	}
	var list groupListOutput
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("decode group list --json: %v\n%s", err, listOut)
	}
	var oldProjects []string
	for _, g := range list.Groups {
		if g.Group == "old" {
			oldProjects = g.Projects
		}
	}
	if len(oldProjects) != 1 || oldProjects[0] != wsKey {
		t.Fatalf(`group "old" projects = %v, want exactly [%q]`, oldProjects, wsKey)
	}
	for _, g := range list.Groups {
		for _, p := range g.Projects {
			if p == workspaceDir {
				t.Fatalf("group list --json shows the plain legacy key %q as a project, want only %q", workspaceDir, wsKey)
			}
		}
	}
	for _, p := range list.Ungrouped {
		if p == workspaceDir {
			t.Fatalf("group list --json Ungrouped still lists the plain legacy key %q", workspaceDir)
		}
	}

	if _, stderr, code := runBackstoryGroup(t, branchBin, env, t.TempDir(), "clear", wsKey); code != 0 {
		t.Fatalf("branch group clear %s: exit %d (stderr: %s)", wsKey, code, stderr)
	}
	listOut, stderr, code = runBackstoryGroup(t, branchBin, env, t.TempDir(), "list", "--json")
	if code != 0 {
		t.Fatalf("branch group list --json (after clear): exit %d (stderr: %s)", code, stderr)
	}
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("decode group list --json (after clear): %v\n%s", err, listOut)
	}
	if len(list.Groups) != 0 {
		t.Fatalf("group list --json Groups after clear = %+v, want empty", list.Groups)
	}

	thisWeekOut, thisWeekErr, code := runBackstoryThisWeek(t, branchBin, env, t.TempDir())
	if code != 0 {
		t.Fatalf("branch this-week --json: exit %d (stderr: %s)", code, thisWeekErr)
	}
	var week thisWeekOutputJSON
	if err := json.Unmarshal([]byte(thisWeekOut), &week); err != nil {
		t.Fatalf("decode this-week --json: %v\n%s", err, thisWeekOut)
	}
	for _, row := range week.WhereLeftOff {
		if row.Project != nil && row.Project.ProjectKey == workspaceDir {
			t.Fatalf("this-week --json where_left_off has a row keyed on the plain legacy key %q", workspaceDir)
		}
		for _, c := range row.Children {
			if c.ProjectKey == workspaceDir {
				t.Fatalf("this-week --json where_left_off group %q has a child keyed on the plain legacy key %q", row.Group, workspaceDir)
			}
		}
	}
	for _, d := range week.Week {
		if d.ProjectKey == workspaceDir {
			t.Fatalf("this-week --json week has a row keyed on the plain legacy key %q", workspaceDir)
		}
	}
}
