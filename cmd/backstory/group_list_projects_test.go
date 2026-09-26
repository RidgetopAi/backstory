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
// any workspace" — each with a session started THIS WEEK (task 4fe02e30
// round 6 guard fix), at the fixed reference date(3, 9, 0), so
// ProjectsSeenSince (the `ungrouped` field's own source) reports all four
// and the golden output stays independent of wall-clock time.
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

	// Sessions THIS WEEK under all four keys (task 4fe02e30 round 6 guard
	// fix): the round-5 fixture seeded none at all, so ProjectsSeenSince/
	// `ungrouped` was always empty and hid round 6's defect B entirely — a
	// folder with history under both its legacy and workspace keys showing
	// up TWICE in `ungrouped`, the raw legacy one never regrouped by a
	// `group set` on the workspace key. Started at date(3, 9, 0), inside
	// thisWeekFixtureNow's own fixed window, so this file's output stays
	// independent of wall-clock time.
	for _, sess := range []struct {
		id, key, cwd string
	}{
		{"sess-seen-omarcade", groupListFixtureOmarcadeKey, fixtureWorkspaceDir + "/omarcade"},
		{"sess-seen-legacy", groupListFixtureLegacyKey, fixtureWorkspaceDir},
		{"sess-seen-workspace", groupListFixtureWorkspaceKey, fixtureWorkspaceDir},
		{"sess-seen-outside", groupListFixtureOutsideKey, groupListFixtureOutsideKey},
	} {
		if _, err := st.StartSession(store.StartSessionParams{
			ID: sess.id, Agent: "claude", CWD: sess.cwd, ProjectKey: sess.key,
			StartedAt: date(3, 9, 0), Origin: store.OriginLive,
		}); err != nil {
			t.Fatalf("StartSession(%s): %v", sess.key, err)
		}
	}

	human := store.Identity{Kind: store.IdentityHuman, Actor: "human"}
	if err := st.SetProjectGroup(human, "media", groupListFixtureOmarcadeKey, nil); err != nil {
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
	origGroupListNow := groupListNow
	groupListNow = func() time.Time { return thisWeekFixtureNow }
	t.Cleanup(func() { groupListNow = origGroupListNow })

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
	// Ungrouped (task 4fe02e30 round 6 defect B): the legacy key and the
	// workspace key both have a session this week, but they are the SAME
	// folder — Ungrouped must show it ONCE, under its canonical workspace
	// key, never the raw legacy key. The git repo key is grouped (media),
	// so it never appears here at all.
	wantUngrouped := map[string]bool{groupListFixtureWorkspaceKey: true, groupListFixtureOutsideKey: true}
	if len(got.Ungrouped) != len(wantUngrouped) {
		t.Fatalf("Ungrouped = %v, want exactly %v", got.Ungrouped, wantUngrouped)
	}
	for _, key := range got.Ungrouped {
		if !wantUngrouped[key] {
			t.Errorf("Ungrouped contains unexpected key %q; got %v, want %v", key, got.Ungrouped, wantUngrouped)
		}
	}
	for _, key := range got.Ungrouped {
		if key == groupListFixtureLegacyKey {
			t.Errorf("Ungrouped still has the legacy key %q as its own entry; want it merged into the workspace key %q", groupListFixtureLegacyKey, groupListFixtureWorkspaceKey)
		}
	}
}

// TestGroupListDisplayNameMatchesThisWeekForActiveProject is DONE WHEN
// clauses 2 and 4: for EVERY project in the seeded fixture (task 4fe02e30
// round 6 guard fix — round 5 only checked the git repo), `group list
// --json`'s projects[].display_name and `this-week --json`'s own
// where_left_off row agree byte-for-byte — proof that both read
// display_name off the SAME function (week.DisplayName), not two
// independently-derived names that could drift the way the CAUSE section's
// bug did. The legacy key is compared against group list's WORKSPACE key
// entry (never its own): group list merges the legacy/workspace pair into
// one entry keyed by the workspace form (clause 1), but this-week still
// carries the legacy key as its own repoKey row (its own DONE WHEN clause
// 2 fix only changes which GROUP that row lands in, not its identity) — so
// the correct byte-for-byte comparison for that folder is this-week's
// legacy-key row against group list's merged workspace-key entry, both
// "projects".
func TestGroupListDisplayNameMatchesThisWeekForActiveProject(t *testing.T) {
	dataDir := t.TempDir()
	buildGroupListFixtureStore(t, dataDir)

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
	thisWeekNames := map[string]string{}
	for _, row := range thisWeek.WhereLeftOff {
		if row.Project != nil {
			thisWeekNames[row.Project.ProjectKey] = row.Project.DisplayName
		}
		for _, child := range row.Children {
			thisWeekNames[child.ProjectKey] = child.DisplayName
		}
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
	groupListNames := map[string]string{}
	for _, p := range groupList.Projects {
		groupListNames[p.Key] = p.DisplayName
	}

	cases := []struct {
		label           string
		thisWeekKey     string
		groupListKey    string
		wantDisplayName string
	}{
		{"git repo", groupListFixtureOmarcadeKey, groupListFixtureOmarcadeKey, "projects/omarcade"},
		{"legacy key vs merged workspace entry", groupListFixtureLegacyKey, groupListFixtureWorkspaceKey, "projects"},
		{"outside-workspace folder", groupListFixtureOutsideKey, groupListFixtureOutsideKey, "bin"},
	}
	for _, c := range cases {
		thisWeekName, ok := thisWeekNames[c.thisWeekKey]
		if !ok {
			t.Fatalf("%s: this-week --json has no where_left_off row for %s; got %+v", c.label, c.thisWeekKey, thisWeek.WhereLeftOff)
		}
		groupListName, ok := groupListNames[c.groupListKey]
		if !ok {
			t.Fatalf("%s: group list --json Projects has no entry for %s; got %+v", c.label, c.groupListKey, groupList.Projects)
		}
		if groupListName != thisWeekName {
			t.Errorf("%s: group list display_name %q != this-week display_name %q", c.label, groupListName, thisWeekName)
		}
		if groupListName != c.wantDisplayName {
			t.Errorf("%s: display_name = %q, want %q", c.label, groupListName, c.wantDisplayName)
		}
	}
}

// runThisWeekJSONForGroupListTest runs `this-week --json` against dataDir's
// store the same way TestGroupListDisplayNameMatchesThisWeekForActiveProject
// does (thisWeekNow pinned, workspace dirs pinned), decoded.
func runThisWeekJSONForGroupListTest(t *testing.T, dataDir string) thisWeekOutputJSON {
	t.Helper()
	orig := thisWeekNow
	thisWeekNow = func() time.Time { return thisWeekFixtureNow }
	t.Cleanup(func() { thisWeekNow = orig })
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", fixtureWorkspaceDir)

	var outBuf, errBuf bytes.Buffer
	if c := run([]string{"this-week", "--json"}, bytes.NewReader(nil), &outBuf, &errBuf); c != 0 {
		t.Fatalf("this-week --json: exit %d (stderr: %s)", c, errBuf.String())
	}
	var out thisWeekOutputJSON
	if err := json.Unmarshal(outBuf.Bytes(), &out); err != nil {
		t.Fatalf("decode this-week --json output %q: %v", outBuf.String(), err)
	}
	return out
}

// whereLeftOffGroupChildren returns the ProjectKeys under groupName's own
// where_left_off row, nil if that group has no row at all.
func whereLeftOffGroupChildren(out thisWeekOutputJSON, groupName string) []string {
	for _, row := range out.WhereLeftOff {
		if row.Group == groupName {
			keys := make([]string, len(row.Children))
			for i, c := range row.Children {
				keys[i] = c.ProjectKey
			}
			return keys
		}
	}
	return nil
}

// whereLeftOffHasUngroupedProject reports whether where_left_off has an
// own-row (Group == "") entry for projectKey.
func whereLeftOffHasUngroupedProject(out thisWeekOutputJSON, projectKey string) bool {
	for _, row := range out.WhereLeftOff {
		if row.Project != nil && row.Project.ProjectKey == projectKey {
			return true
		}
	}
	return false
}

// containsString reports whether ss contains s.
func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestGroupSetOnWorkspaceKeyRegroupsLegacyHistoryAndClearReverses is DONE
// WHEN clause 2 (task 4fe02e30 round 6 defect B fix): `group set g
// workspace:/…/projects` on a store with sessions under BOTH that folder's
// legacy plain key and its workspace-prefixed key regroups the LEGACY
// key's history too — group list shows the folder once, in group g
// (never a separate ungrouped legacy row), and this-week --json's
// where_left_off shows the legacy key's own row as group g's child (never
// its own ungrouped row) — because GroupOf/SetProjectGroup/ClearProjectGroup
// all canonicalize a legacy plain key to its workspace key before any group
// table read or write (store.CanonicalProjectKey), so a group set on
// either form of the same folder's key agrees. `group clear` on the
// workspace key reverses both: the folder goes back to ungrouped in group
// list (shown once, under its canonical workspace key) and the legacy
// key's this-week row goes back to being its own ungrouped row.
func TestGroupSetOnWorkspaceKeyRegroupsLegacyHistoryAndClearReverses(t *testing.T) {
	dataDir := t.TempDir()
	buildGroupListFixtureStore(t, dataDir)

	// Before: the folder is ungrouped (legacy+workspace merged into one
	// ungrouped entry — DONE WHEN clause 1), and this-week shows the legacy
	// key as its own ungrouped row.
	stdout, stderr, code := runGroupListCLI(t, dataDir, "list", "--json")
	if code != 0 {
		t.Fatalf("group list --json (before): exit %d (stderr: %s)", code, stderr)
	}
	var before groupListOutput
	if err := json.Unmarshal([]byte(stdout), &before); err != nil {
		t.Fatalf("decode group list --json (before) %q: %v", stdout, err)
	}
	if !containsString(before.Ungrouped, groupListFixtureWorkspaceKey) {
		t.Fatalf("group list --json (before) Ungrouped = %v, want %q present", before.Ungrouped, groupListFixtureWorkspaceKey)
	}
	thisWeekBefore := runThisWeekJSONForGroupListTest(t, dataDir)
	if !whereLeftOffHasUngroupedProject(thisWeekBefore, groupListFixtureLegacyKey) {
		t.Fatalf("this-week --json (before) has no ungrouped where_left_off row for the legacy key %q; got %+v", groupListFixtureLegacyKey, thisWeekBefore.WhereLeftOff)
	}

	// Set: group the WORKSPACE key.
	if _, stderr, code := runGroupListCLI(t, dataDir, "set", "regrouped", groupListFixtureWorkspaceKey); code != 0 {
		t.Fatalf("group set regrouped %s: exit %d (stderr: %s)", groupListFixtureWorkspaceKey, code, stderr)
	}

	stdout, stderr, code = runGroupListCLI(t, dataDir, "list", "--json")
	if code != 0 {
		t.Fatalf("group list --json (after set): exit %d (stderr: %s)", code, stderr)
	}
	var afterSet groupListOutput
	if err := json.Unmarshal([]byte(stdout), &afterSet); err != nil {
		t.Fatalf("decode group list --json (after set) %q: %v", stdout, err)
	}
	if containsString(afterSet.Ungrouped, groupListFixtureWorkspaceKey) || containsString(afterSet.Ungrouped, groupListFixtureLegacyKey) {
		t.Errorf("group list --json (after set) Ungrouped = %v, want neither the workspace nor legacy key present", afterSet.Ungrouped)
	}
	var regroupedMembers []string
	for _, g := range afterSet.Groups {
		if g.Group == "regrouped" {
			regroupedMembers = g.Projects
		}
	}
	if len(regroupedMembers) != 1 || regroupedMembers[0] != groupListFixtureWorkspaceKey {
		t.Fatalf("group list --json (after set) group \"regrouped\" projects = %v, want exactly [%q] (the canonical key, never the legacy one)", regroupedMembers, groupListFixtureWorkspaceKey)
	}

	thisWeekAfterSet := runThisWeekJSONForGroupListTest(t, dataDir)
	regroupedChildren := whereLeftOffGroupChildren(thisWeekAfterSet, "regrouped")
	if !containsString(regroupedChildren, groupListFixtureLegacyKey) {
		t.Fatalf("this-week --json (after set) where_left_off group \"regrouped\" children = %v, want the legacy key %q present", regroupedChildren, groupListFixtureLegacyKey)
	}
	if whereLeftOffHasUngroupedProject(thisWeekAfterSet, groupListFixtureLegacyKey) {
		t.Errorf("this-week --json (after set) still has a separate ungrouped where_left_off row for the legacy key %q", groupListFixtureLegacyKey)
	}

	// Clear: reverses both.
	if _, stderr, code := runGroupListCLI(t, dataDir, "clear", groupListFixtureWorkspaceKey); code != 0 {
		t.Fatalf("group clear %s: exit %d (stderr: %s)", groupListFixtureWorkspaceKey, code, stderr)
	}

	stdout, stderr, code = runGroupListCLI(t, dataDir, "list", "--json")
	if code != 0 {
		t.Fatalf("group list --json (after clear): exit %d (stderr: %s)", code, stderr)
	}
	var afterClear groupListOutput
	if err := json.Unmarshal([]byte(stdout), &afterClear); err != nil {
		t.Fatalf("decode group list --json (after clear) %q: %v", stdout, err)
	}
	if !containsString(afterClear.Ungrouped, groupListFixtureWorkspaceKey) {
		t.Errorf("group list --json (after clear) Ungrouped = %v, want %q present again", afterClear.Ungrouped, groupListFixtureWorkspaceKey)
	}
	for _, g := range afterClear.Groups {
		if g.Group == "regrouped" {
			t.Errorf("group list --json (after clear) still has group \"regrouped\": %+v", g)
		}
	}

	thisWeekAfterClear := runThisWeekJSONForGroupListTest(t, dataDir)
	if !whereLeftOffHasUngroupedProject(thisWeekAfterClear, groupListFixtureLegacyKey) {
		t.Errorf("this-week --json (after clear) has no ungrouped where_left_off row for the legacy key %q again; got %+v", groupListFixtureLegacyKey, thisWeekAfterClear.WhereLeftOff)
	}
	if containsString(whereLeftOffGroupChildren(thisWeekAfterClear, "regrouped"), groupListFixtureLegacyKey) {
		t.Errorf("this-week --json (after clear) still has the legacy key under group \"regrouped\"")
	}
}
