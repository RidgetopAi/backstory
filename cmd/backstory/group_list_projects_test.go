package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// groupListFixtureOmarcadeKey is a git project_key in the exact shape
// internal/project.Key produces for a repo with a remote: <git common
// dir>|<remote URL> — the shape the CAUSE section measured on Brian's desk
// (task 4fe02e30 round 5): naively taking the text after the key's last
// "/" renders this as "omarcade.git" (the remote URL's own basename), not
// the project's real display name.
const groupListFixtureOmarcadeKey = fixtureWorkspaceDir + "/omarcade/.git|git@github.com:example/omarcade.git"

// groupListFixtureLegacyKey / groupListFixtureWorkspaceKey are the same
// workspace folder under its pre- and post-79f7b20e keys (task 50249f56):
// a store that has BOTH rows (an old build wrote the plain key; a new
// build re-keyed to workspace:) must show them as ONE project, not two.
const groupListFixtureLegacyKey = fixtureWorkspaceDir
const groupListFixtureWorkspaceKey = "workspace:" + fixtureWorkspaceDir

// groupListFixtureOutsideKey is a folder outside any configured workspace
// dir — internal/project.Key's third case (neither a git repo nor a
// workspace dir/child): its own key is just the directory itself.
const groupListFixtureOutsideKey = "/home/brian/.local/bin"

// buildGroupListFixtureStore seeds exactly the store shape DONE WHEN
// clause 1 describes: "a git-repo key under a workspace, a legacy plain
// key /…/projects plus its workspace:/…/projects key, and a folder outside
// any workspace". No sessions are started for any of them, so
// ProjectsSeenSince (the `ungrouped` field's own source) reports nothing —
// keeping the golden output independent of wall-clock time entirely.
func buildGroupListFixtureStore(t *testing.T, dataDir string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.UpsertProject(store.Project{
		Key: groupListFixtureOmarcadeKey, Toplevel: fixtureWorkspaceDir + "/omarcade", FirstSeen: date(3, 9, 0),
	}); err != nil {
		t.Fatalf("UpsertProject(omarcade): %v", err)
	}
	if err := st.UpsertProject(store.Project{
		Key: groupListFixtureLegacyKey, Toplevel: fixtureWorkspaceDir, FirstSeen: date(2, 9, 0),
	}); err != nil {
		t.Fatalf("UpsertProject(legacy): %v", err)
	}
	if err := st.UpsertProject(store.Project{
		Key: groupListFixtureWorkspaceKey, Toplevel: fixtureWorkspaceDir, FirstSeen: date(4, 9, 0),
	}); err != nil {
		t.Fatalf("UpsertProject(workspace): %v", err)
	}
	if err := st.UpsertProject(store.Project{
		Key: groupListFixtureOutsideKey, Toplevel: groupListFixtureOutsideKey, FirstSeen: date(5, 9, 0),
	}); err != nil {
		t.Fatalf("UpsertProject(outside): %v", err)
	}

	human := store.Identity{Kind: store.IdentityHuman, Actor: "human"}
	if err := st.SetProjectGroup(human, "media", groupListFixtureOmarcadeKey); err != nil {
		t.Fatalf("SetProjectGroup(omarcade): %v", err)
	}
}

// runGroupListCLI runs `backstory group <args...>` in-process against
// dataDir's store, with BACKSTORY_WORKSPACE_DIRS pinned to
// fixtureWorkspaceDir (thisweek_test.go's own fixed workspace dir) so
// group list's display_name computation (week.DisplayName) resolves the
// same workspace-relative names this-week's own golden tests do,
// independent of the invoking environment's own $HOME.
func runGroupListCLI(t *testing.T, dataDir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", fixtureWorkspaceDir)

	var outBuf, errBuf bytes.Buffer
	code = run(append([]string{"group"}, args...), bytes.NewReader(nil), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

func groupListGoldenPath(name string) string {
	return filepath.Join("testdata", "group", name)
}

// compareGroupListGolden mirrors thisweek_test.go's compareThisWeekGolden
// (same RA_UPDATE_GOLDEN=1 regeneration mechanism), against
// testdata/group instead of testdata/thisweek.
func compareGroupListGolden(t *testing.T, name, got string) {
	t.Helper()
	path := groupListGoldenPath(name)
	if os.Getenv("RA_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // test fixture output, not sensitive
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // path is this test's own fixed testdata path
	if err != nil {
		t.Fatalf("read golden %s: %v (run with RA_UPDATE_GOLDEN=1 to create it)", path, err)
	}
	if got != string(want) {
		t.Fatalf("output does not match golden %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// TestGroupListJSONProjectsArrayDisplayNamesAndDedup is DONE WHEN clause 1:
// `backstory group list --json` against a store holding a git-repo key
// under a workspace, a legacy plain key plus its workspace: key, and a
// folder outside any workspace returns a `projects` array of {key,
// display_name, group}; the git repo's display_name is "projects/omarcade"
// (never "omarcade.git"); the legacy and workspace keys appear as ONE
// entry keyed by the workspace key; groups/ungrouped are unchanged.
func TestGroupListJSONProjectsArrayDisplayNamesAndDedup(t *testing.T) {
	dataDir := t.TempDir()
	buildGroupListFixtureStore(t, dataDir)

	stdout, stderr, code := runGroupListCLI(t, dataDir, "list", "--json")
	if code != 0 {
		t.Fatalf("group list --json: exit %d (stderr: %s)", code, stderr)
	}
	compareGroupListGolden(t, "list.json.golden", stdout)

	var got groupListOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode group list --json output %q: %v", stdout, err)
	}

	if len(got.Projects) != 3 {
		t.Fatalf("Projects = %+v, want exactly 3 entries (git repo, ONE merged legacy/workspace entry, outside-workspace folder)", got.Projects)
	}
	byKey := map[string]groupListProject{}
	for _, p := range got.Projects {
		byKey[p.Key] = p
	}

	omarcade, ok := byKey[groupListFixtureOmarcadeKey]
	if !ok {
		t.Fatalf("Projects has no entry for the git repo key %q; got %+v", groupListFixtureOmarcadeKey, got.Projects)
	}
	if omarcade.DisplayName != "projects/omarcade" {
		t.Errorf("git repo display_name = %q, want %q (never a %q suffix)", omarcade.DisplayName, "projects/omarcade", ".git")
	}
	if omarcade.Group != "media" {
		t.Errorf("git repo group = %q, want %q", omarcade.Group, "media")
	}

	if _, stillLegacy := byKey[groupListFixtureLegacyKey]; stillLegacy {
		t.Errorf("Projects still has the legacy key %q as its own entry; want it merged into the workspace key %q", groupListFixtureLegacyKey, groupListFixtureWorkspaceKey)
	}
	merged, ok := byKey[groupListFixtureWorkspaceKey]
	if !ok {
		t.Fatalf("Projects has no entry for the workspace key %q (legacy/workspace merge dropped both)", groupListFixtureWorkspaceKey)
	}
	if merged.DisplayName != "projects" {
		t.Errorf("merged legacy/workspace display_name = %q, want %q", merged.DisplayName, "projects")
	}
	if merged.Group != "" {
		t.Errorf("merged legacy/workspace group = %q, want \"\" (ungrouped)", merged.Group)
	}

	outside, ok := byKey[groupListFixtureOutsideKey]
	if !ok {
		t.Fatalf("Projects has no entry for the outside-workspace folder %q", groupListFixtureOutsideKey)
	}
	if outside.DisplayName != "bin" {
		t.Errorf("outside-workspace display_name = %q, want %q", outside.DisplayName, "bin")
	}

	if len(got.Groups) != 1 || got.Groups[0].Group != "media" || len(got.Groups[0].Projects) != 1 || got.Groups[0].Projects[0] != groupListFixtureOmarcadeKey {
		t.Errorf("Groups = %+v, want exactly one group %q with %q (existing field unchanged)", got.Groups, "media", groupListFixtureOmarcadeKey)
	}
	if len(got.Ungrouped) != 0 {
		t.Errorf("Ungrouped = %v, want empty (existing field unchanged; no sessions seeded)", got.Ungrouped)
	}
}

// TestGroupListDisplayNameMatchesThisWeekForActiveProject is DONE WHEN
// clause 2: for a project active in the window, `group list --json`'s
// projects[].display_name and `this-week --json`'s own where_left_off row
// agree byte-for-byte — proof that both read display_name off the SAME
// function (week.DisplayName), not two independently-derived names that
// could drift the way the CAUSE section's bug did.
func TestGroupListDisplayNameMatchesThisWeekForActiveProject(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.UpsertProject(store.Project{
		Key: groupListFixtureOmarcadeKey, Toplevel: fixtureWorkspaceDir + "/omarcade", FirstSeen: date(3, 9, 0),
	}); err != nil {
		t.Fatalf("UpsertProject(omarcade): %v", err)
	}
	sess, err := st.StartSession(store.StartSessionParams{
		ID: "sess-omarcade-active-1", Agent: "claude", CWD: fixtureWorkspaceDir + "/omarcade", ProjectKey: groupListFixtureOmarcadeKey,
		StartedAt: date(3, 9, 0), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(omarcade): %v", err)
	}
	if _, err := st.AppendEvent(store.Event{TS: date(3, 9, 0), Kind: "session.start", SessionID: sess, Source: "shell", Payload: `{}`}); err != nil {
		t.Fatalf("AppendEvent(session.start): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	orig := thisWeekNow
	thisWeekNow = func() time.Time { return thisWeekFixtureNow }
	t.Cleanup(func() { thisWeekNow = orig })

	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", fixtureWorkspaceDir)

	var outBuf, errBuf bytes.Buffer
	if c := run([]string{"this-week", "--json"}, bytes.NewReader(nil), &outBuf, &errBuf); c != 0 {
		t.Fatalf("this-week --json: exit %d (stderr: %s)", c, errBuf.String())
	}
	var thisWeek thisWeekOutputJSON
	if err := json.Unmarshal(outBuf.Bytes(), &thisWeek); err != nil {
		t.Fatalf("decode this-week --json output %q: %v", outBuf.String(), err)
	}
	var thisWeekName string
	for _, row := range thisWeek.WhereLeftOff {
		if row.Project != nil && row.Project.ProjectKey == groupListFixtureOmarcadeKey {
			thisWeekName = row.Project.DisplayName
		}
	}
	if thisWeekName == "" {
		t.Fatalf("this-week --json has no where_left_off row for %s; got %+v", groupListFixtureOmarcadeKey, thisWeek.WhereLeftOff)
	}

	outBuf.Reset()
	errBuf.Reset()
	if c := run([]string{"group", "list", "--json"}, bytes.NewReader(nil), &outBuf, &errBuf); c != 0 {
		t.Fatalf("group list --json: exit %d (stderr: %s)", c, errBuf.String())
	}
	var groupList groupListOutput
	if err := json.Unmarshal(outBuf.Bytes(), &groupList); err != nil {
		t.Fatalf("decode group list --json output %q: %v", outBuf.String(), err)
	}
	var groupListName string
	for _, p := range groupList.Projects {
		if p.Key == groupListFixtureOmarcadeKey {
			groupListName = p.DisplayName
		}
	}
	if groupListName == "" {
		t.Fatalf("group list --json Projects has no entry for %s; got %+v", groupListFixtureOmarcadeKey, groupList.Projects)
	}

	if groupListName != thisWeekName {
		t.Fatalf("group list display_name %q != this-week display_name %q for the SAME project %s", groupListName, thisWeekName, groupListFixtureOmarcadeKey)
	}
	if groupListName != "projects/omarcade" {
		t.Fatalf("display_name = %q, want %q", groupListName, "projects/omarcade")
	}
}
