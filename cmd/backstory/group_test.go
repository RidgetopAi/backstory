package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// seedGroupableProject opens dbPath (creating it, same as storePath()/
// store.Open would from a `backstory group` invocation) and upserts
// projectKey — project_groups.project_key is a real foreign key into
// projects (SCHEMA.md), so an explicit <project-key> argument needs the
// project to already exist, exactly like a real project the daemon has
// observed at least once.
func seedGroupableProject(t *testing.T, dbPath, projectKey string) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	if err := st.UpsertProject(store.Project{Key: projectKey, Toplevel: "/tmp/" + projectKey, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", projectKey, err)
	}
}

// seedSessionForProject records a session for projectKey started at
// startedAt, the signal `backstory group list`'s "ungrouped ... seen this
// week" reads (store.Store.ProjectsSeenSince).
func seedSessionForProject(t *testing.T, dbPath, projectKey string, startedAt time.Time) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	if err := st.UpsertProject(store.Project{Key: projectKey, Toplevel: "/tmp/" + projectKey, FirstSeen: startedAt}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", projectKey, err)
	}
	if _, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: "/tmp/" + projectKey, ProjectKey: projectKey,
		StartedAt: startedAt, Origin: store.OriginLive,
	}); err != nil {
		t.Fatalf("StartSession(%s): %v", projectKey, err)
	}
}

// groupOfOrFatal opens dbPath and returns projectKey's current group,
// failing the test on a store error (a project that was never grouped is a
// valid ("", false), not a failure).
func groupOfOrFatal(t *testing.T, dbPath, projectKey string) (string, bool) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	name, ok, err := st.GroupOf(projectKey)
	if err != nil {
		t.Fatalf("GroupOf(%s): %v", projectKey, err)
	}
	return name, ok
}

// listGroupsOrFatal opens dbPath and returns every project_groups row.
func listGroupsOrFatal(t *testing.T, dbPath string) []store.GroupMembership {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	groups, err := st.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	return groups
}

// runBackstoryGroup runs `backstory group <args...>` as a subprocess with
// env and working directory dir, returning its stdout, stderr and exit
// code.
func runBackstoryGroup(t *testing.T, bin string, env []string, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	fullArgs := append([]string{"group"}, args...)
	cmd := exec.Command(bin, fullArgs...) //nolint:gosec // bin is the binary this test just built, args are this test's own literals
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
		t.Fatalf("run backstory group %v: %v (stderr: %s)", args, err, errOut.String())
	}
	return out.String(), errOut.String(), exitCode
}

// initGitRepo runs `git init` in dir so internal/project.RealGit resolves
// it as a real project identity, unlike a bare workspace directory.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q", dir) //nolint:gosec // fixed literal git subcommand, dir is this test's own t.TempDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", dir, err, out)
	}
}

// TestGroupSetMoveClearRoundTripAgainstTempStore is the punch's DONE WHEN
// clause 1's set/move/clear half: `backstory group set/clear` against a
// temp store, over an explicit <project-key>, round-trips through the
// store exactly like the direct store.SetProjectGroup/ClearProjectGroup
// calls groups_test.go already covers — this proves the CLI wiring, not
// just the store API underneath it.
func TestGroupSetMoveClearRoundTripAgainstTempStore(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	seedGroupableProject(t, dbPath, "proj-a")

	// Set.
	stdout, stderr, code := runBackstoryGroup(t, bin, env, cwd, "set", "work", "proj-a")
	if code != 0 {
		t.Fatalf("group set proj-a -> work: exit %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	if name, ok := groupOfOrFatal(t, dbPath, "proj-a"); !ok || name != "work" {
		t.Fatalf("GroupOf(proj-a) after set = (%q, %v), want (\"work\", true)", name, ok)
	}

	// Move: re-set to a different group overwrites, not adds.
	stdout, stderr, code = runBackstoryGroup(t, bin, env, cwd, "set", "personal", "proj-a")
	if code != 0 {
		t.Fatalf("group set proj-a -> personal: exit %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	if name, ok := groupOfOrFatal(t, dbPath, "proj-a"); !ok || name != "personal" {
		t.Fatalf("GroupOf(proj-a) after move = (%q, %v), want (\"personal\", true)", name, ok)
	}
	if groups := listGroupsOrFatal(t, dbPath); len(groups) != 1 {
		t.Fatalf("ListGroups after move = %+v, want exactly 1 row (move, not add)", groups)
	}

	// Clear.
	stdout, stderr, code = runBackstoryGroup(t, bin, env, cwd, "clear", "proj-a")
	if code != 0 {
		t.Fatalf("group clear proj-a: exit %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	if name, ok := groupOfOrFatal(t, dbPath, "proj-a"); ok {
		t.Fatalf("GroupOf(proj-a) after clear = (%q, true), want not-ok", name)
	}
	if groups := listGroupsOrFatal(t, dbPath); len(groups) != 0 {
		t.Fatalf("ListGroups after clear = %+v, want empty", groups)
	}
}

// TestGroupListTextAndJSONShowGroupsAndUngroupedSeenThisWeek is the punch's
// DONE WHEN clause 1's list half: `backstory group list` (text and --json)
// shows groups with their projects, plus ungrouped projects seen this week
// — and excludes an ungrouped project whose only session is older than a
// week.
func TestGroupListTextAndJSONShowGroupsAndUngroupedSeenThisWeek(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	seedGroupableProject(t, dbPath, "proj-work-a")
	seedGroupableProject(t, dbPath, "proj-work-b")
	seedSessionForProject(t, dbPath, "proj-recent-ungrouped", time.Now().Add(-2*time.Hour))
	seedSessionForProject(t, dbPath, "proj-stale-ungrouped", time.Now().Add(-30*24*time.Hour))

	if _, stderr, code := runBackstoryGroup(t, bin, env, cwd, "set", "work", "proj-work-a"); code != 0 {
		t.Fatalf("group set proj-work-a: exit %d (stderr: %s)", code, stderr)
	}
	if _, stderr, code := runBackstoryGroup(t, bin, env, cwd, "set", "work", "proj-work-b"); code != 0 {
		t.Fatalf("group set proj-work-b: exit %d (stderr: %s)", code, stderr)
	}

	// Text.
	stdout, stderr, code := runBackstoryGroup(t, bin, env, cwd, "list")
	if code != 0 {
		t.Fatalf("group list: exit %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	for _, want := range []string{"work:", "proj-work-a", "proj-work-b", "ungrouped (seen this week):", "proj-recent-ungrouped"} {
		if !bytes.Contains([]byte(stdout), []byte(want)) {
			t.Errorf("group list text output missing %q; got:\n%s", want, stdout)
		}
	}
	if bytes.Contains([]byte(stdout), []byte("proj-stale-ungrouped")) {
		t.Errorf("group list text output includes proj-stale-ungrouped (session > a week old); got:\n%s", stdout)
	}

	// JSON.
	stdout, stderr, code = runBackstoryGroup(t, bin, env, cwd, "list", "--json")
	if code != 0 {
		t.Fatalf("group list --json: exit %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	var got groupListOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode group list --json output %q: %v", stdout, err)
	}
	if len(got.Groups) != 1 || got.Groups[0].Group != "work" {
		t.Fatalf("group list --json Groups = %+v, want one group named work", got.Groups)
	}
	wantProjects := []string{"proj-work-a", "proj-work-b"}
	if len(got.Groups[0].Projects) != 2 || got.Groups[0].Projects[0] != wantProjects[0] || got.Groups[0].Projects[1] != wantProjects[1] {
		t.Fatalf("group list --json work group projects = %v, want %v", got.Groups[0].Projects, wantProjects)
	}
	foundRecent, foundStale := false, false
	for _, p := range got.Ungrouped {
		if p == "proj-recent-ungrouped" {
			foundRecent = true
		}
		if p == "proj-stale-ungrouped" {
			foundStale = true
		}
	}
	if !foundRecent {
		t.Errorf("group list --json Ungrouped = %v, want proj-recent-ungrouped present", got.Ungrouped)
	}
	if foundStale {
		t.Errorf("group list --json Ungrouped = %v, want proj-stale-ungrouped absent (session > a week old)", got.Ungrouped)
	}
}

// TestGroupSetHereResolvesGitProjectKey is the punch's DONE WHEN clause 2's
// success half: `group set --here` from inside a real git working tree
// resolves and groups the same key internal/project.Key computes for it,
// not the raw directory path.
func TestGroupSetHereResolvesGitProjectKey(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")

	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	wantKey := project.Key(repoDir, project.RealGit{})

	stdout, stderr, code := runBackstoryGroup(t, bin, env, repoDir, "set", "work", "--here")
	if code != 0 {
		t.Fatalf("group set work --here: exit %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	if name, ok := groupOfOrFatal(t, dbPath, wantKey); !ok || name != "work" {
		t.Fatalf("GroupOf(%s) after set --here = (%q, %v), want (\"work\", true)", wantKey, name, ok)
	}
}

// TestGroupSetHereInWorkspaceDirRefusesAndWritesNothing is the punch's DONE
// WHEN clause 2's refusal half: `group set --here` from a directory that is
// not inside any git working tree (a workspace, not a project,
// AGENT-CONTRACT.md §Project = git repository identity) is refused with a
// message and leaves project_groups empty.
func TestGroupSetHereInWorkspaceDirRefusesAndWritesNothing(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")

	workspaceDir := t.TempDir() // deliberately never `git init`-ed

	stdout, stderr, code := runBackstoryGroup(t, bin, env, workspaceDir, "set", "work", "--here")
	if code == 0 {
		t.Fatalf("group set work --here in a non-git workspace exited 0, want non-zero (stdout: %s)", stdout)
	}
	if stderr == "" {
		t.Error("group set work --here in a non-git workspace: stderr empty, want a refusal message")
	}
	if groups := listGroupsOrFatal(t, dbPath); len(groups) != 0 {
		t.Fatalf("ListGroups after refused --here set = %+v, want empty (nothing written)", groups)
	}

	// clear --here is refused the same way.
	stdout, stderr, code = runBackstoryGroup(t, bin, env, workspaceDir, "clear", "--here")
	if code == 0 {
		t.Fatalf("group clear --here in a non-git workspace exited 0, want non-zero (stdout: %s)", stdout)
	}
	if stderr == "" {
		t.Error("group clear --here in a non-git workspace: stderr empty, want a refusal message")
	}
}

// TestGroupCommandsWorkWithoutXDGRuntimeDirSet proves clause 3 for the case
// this package's other tests already exercise implicitly (testXDGEnv never
// sets XDG_RUNTIME_DIR): `backstory group` needs no daemon socket at all,
// so it must work whether or not XDG_RUNTIME_DIR is present in the
// invoking environment. This test additionally strips HOME so the env
// genuinely carries no XDG_RUNTIME_DIR and no HOME at all — only the
// XDG_DATA_HOME storePath() actually needs.
func TestGroupCommandsWorkWithoutXDGRuntimeDirSet(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := append(envWithout(os.Environ(), "XDG_RUNTIME_DIR", "XDG_DATA_HOME", "XDG_STATE_HOME", "HOME"),
		"XDG_DATA_HOME="+dataDir, "PATH="+os.Getenv("PATH"))
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	seedGroupableProject(t, dbPath, "proj-no-runtime-dir")

	if _, stderr, code := runBackstoryGroup(t, bin, env, cwd, "set", "work", "proj-no-runtime-dir"); code != 0 {
		t.Fatalf("group set without XDG_RUNTIME_DIR/HOME: exit %d (stderr: %s)", code, stderr)
	}
	if name, ok := groupOfOrFatal(t, dbPath, "proj-no-runtime-dir"); !ok || name != "work" {
		t.Fatalf("GroupOf(proj-no-runtime-dir) = (%q, %v), want (\"work\", true)", name, ok)
	}
	if stdout, stderr, code := runBackstoryGroup(t, bin, env, cwd, "list"); code != 0 {
		t.Fatalf("group list without XDG_RUNTIME_DIR/HOME: exit %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
}

// TestGroupCommandsWorkWithOnlyHomeSet proves clause 3's other half:
// `backstory group` falls back to $HOME/.local/share/backstory/backstory.db
// (daemon.go's storePath) and works correctly when neither XDG_DATA_HOME
// nor XDG_RUNTIME_DIR is set at all, only HOME.
func TestGroupCommandsWorkWithOnlyHomeSet(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	dbPath := filepath.Join(home, ".local", "share", "backstory", "backstory.db")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	seedGroupableProject(t, dbPath, "proj-home-fallback")

	if _, stderr, code := runBackstoryGroup(t, bin, env, cwd, "set", "work", "proj-home-fallback"); code != 0 {
		t.Fatalf("group set with only HOME set: exit %d (stderr: %s)", code, stderr)
	}
	if name, ok := groupOfOrFatal(t, dbPath, "proj-home-fallback"); !ok || name != "work" {
		t.Fatalf("GroupOf(proj-home-fallback) = (%q, %v), want (\"work\", true)", name, ok)
	}
	stdout, stderr, code := runBackstoryGroup(t, bin, env, cwd, "list", "--json")
	if code != 0 {
		t.Fatalf("group list --json with only HOME set: exit %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	var got groupListOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode group list --json output %q: %v", stdout, err)
	}
	if len(got.Groups) != 1 || got.Groups[0].Group != "work" {
		t.Fatalf("group list --json Groups = %+v, want one group named work", got.Groups)
	}
}
