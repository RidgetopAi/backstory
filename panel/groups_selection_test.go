package panel

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// groupsJSPath is the pure-JS module GroupEditor.qml uses for project
// selection and add-control enablement (round 2 defect C's redesign);
// DONE WHEN clause (5) requires it be covered by a test.
const groupsJSPath = "js/groups.js"

func evalJSValue(t *testing.T, expr string) interface{} {
	t.Helper()
	raw := evalJS(t, expr, groupsJSPath)
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s result %s: %v", expr, raw, err)
	}
	return v
}

// fixtureProject is one entry in the `projects` array `backstory group
// list --json` returns (task 4fe02e30 round 5 fix), the shape
// displayNameForKey reads.
type fixtureProject struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Group       string `json:"group"`
}

// jsonLiteral renders v as JSON, which is also valid JS literal syntax, so
// a Go value can be substituted into an `expr` passed to evalJS/evalJSValue.
func jsonLiteral(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(b)
}

// TestCanSubmitGroup is the "+" control's enablement rule: DONE WHEN
// clause (5) requires it stay disabled until a project (never typed, only
// ever a projectKey the editor already has from a `group list --json`
// row) and a group name are both chosen.
func TestCanSubmitGroup(t *testing.T) {
	cases := []struct {
		name       string
		projectKey string
		groupName  string
		want       bool
	}{
		{"neither set", "", "", false},
		{"no project", "", "work", false},
		{"no group name", "/home/ridgetop", "", false},
		{"whitespace-only group name", "/home/ridgetop", "   ", false},
		{"both set", "/home/ridgetop", "work", true},
		{"group name needs trimming", "/home/ridgetop", "  work  ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr := "canSubmitGroup(" + jsStringLiteral(c.projectKey) + ", " + jsStringLiteral(c.groupName) + ")"
			got := evalJSValue(t, expr)
			if got != c.want {
				t.Fatalf("%s = %v, want %v", expr, got, c.want)
			}
		})
	}
}

// groupListFixtureProjects is a `group list --json` `projects` array
// exercising the exact case round 5's desk defect measured: a git
// project_key (its own display_name computed store-side, workspace-
// relative, "projects/omarcade" — never the remote URL's basename
// "omarcade.git"), a workspace key, and a folder outside any workspace.
var groupListFixtureProjects = []fixtureProject{
	{Key: "/home/brian/projects/omarcade/.git|git@github.com:example/omarcade.git", DisplayName: "projects/omarcade", Group: "media"},
	{Key: "workspace:/home/brian/projects", DisplayName: "projects", Group: ""},
	{Key: "/home/brian/.local/bin", DisplayName: "bin", Group: ""},
}

// TestDisplayNameForKey is DONE WHEN clause (3): the group editor's
// display name comes from `group list --json`'s own `projects` array
// (looked up by key), never derived from the key's own shape.
func TestDisplayNameForKey(t *testing.T) {
	projectsLiteral := jsonLiteral(t, groupListFixtureProjects)
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"git repo key resolves to its display_name, never a .git-suffixed basename", groupListFixtureProjects[0].Key, "projects/omarcade"},
		{"workspace key resolves to its display_name", groupListFixtureProjects[1].Key, "projects"},
		{"outside-workspace key resolves to its display_name", groupListFixtureProjects[2].Key, "bin"},
		{"unknown key falls back to itself", "/no/such/project", "/no/such/project"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr := "displayNameForKey(" + projectsLiteral + ", " + jsStringLiteral(c.key) + ")"
			got := evalJSValue(t, expr)
			if got != c.want {
				t.Fatalf("%s = %v, want %q", expr, got, c.want)
			}
		})
	}
}

// TestProjectsInGroup is task 4fe02e30 round 6 DONE WHEN clause (3): the
// group editor's per-group member rows come from projectsInGroup(projects,
// groupName), filtering `projects` by its own `group` field — never from
// `group list --json`'s separate `groups[].projects` key list, which is
// not deduped against a legacy/workspace key pair for the same folder the
// way `projects` itself already is (round 6 desk defect A).
func TestProjectsInGroup(t *testing.T) {
	projects := []fixtureProject{
		{Key: "workspace:/home/brian/projects", DisplayName: "projects", Group: "media"},
		{Key: "/home/brian/.local/bin", DisplayName: "bin", Group: ""},
		{Key: "/home/brian/projects/vidflow", DisplayName: "projects/vidflow", Group: "media"},
	}
	projectsLiteral := jsonLiteral(t, projects)

	expr := "projectsInGroup(" + projectsLiteral + ", " + jsStringLiteral("media") + ")"
	raw := evalJS(t, expr, groupsJSPath)
	var got []fixtureProject
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode %s result %s: %v", expr, raw, err)
	}
	wantKeys := []string{projects[0].Key, projects[2].Key}
	if len(got) != len(wantKeys) {
		t.Fatalf("projectsInGroup(media) = %+v, want %d entries", got, len(wantKeys))
	}
	for i, k := range wantKeys {
		if got[i].Key != k {
			t.Errorf("projectsInGroup(media)[%d].Key = %q, want %q", i, got[i].Key, k)
		}
	}

	emptyExpr := "projectsInGroup(" + projectsLiteral + ", " + jsStringLiteral("no-such-group") + ")"
	emptyRaw := evalJS(t, emptyExpr, groupsJSPath)
	var empty []fixtureProject
	if err := json.Unmarshal(emptyRaw, &empty); err != nil {
		t.Fatalf("decode %s result %s: %v", emptyExpr, emptyRaw, err)
	}
	if len(empty) != 0 {
		t.Errorf("projectsInGroup(no-such-group) = %+v, want empty", empty)
	}
}

// TestUngroupedProjects is DONE WHEN clause (3)'s ungrouped-section half:
// ungroupedProjects(projects) is exactly projects filtered to group === "".
func TestUngroupedProjects(t *testing.T) {
	projects := []fixtureProject{
		{Key: "workspace:/home/brian/projects", DisplayName: "projects", Group: "media"},
		{Key: "/home/brian/.local/bin", DisplayName: "bin", Group: ""},
	}
	projectsLiteral := jsonLiteral(t, projects)

	expr := "ungroupedProjects(" + projectsLiteral + ")"
	raw := evalJS(t, expr, groupsJSPath)
	var got []fixtureProject
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode %s result %s: %v", expr, raw, err)
	}
	if len(got) != 1 || got[0].Key != projects[1].Key {
		t.Fatalf("ungroupedProjects(...) = %+v, want exactly [%+v]", got, projects[1])
	}
}

// displayNameForKeyFuncLiteral is the exact body js/groups.js's
// displayNameForKey ships today.
// TestDisplayNameForKeyCatchesKeyDerivedRegression asserts this literal is
// still present before mutating a copy of it, so the mutation test fails
// loudly instead of silently passing if the real function ever changes
// shape.
const displayNameForKeyFuncLiteral = `function displayNameForKey(projects, projectKey) {
  for (var i = 0; i < projects.length; i++) {
    if (projects[i].key === projectKey) return projects[i].display_name
  }
  return projectKey
}`

// oldKeyDerivedDisplayNameFuncLiteral is round 2 defect C's original
// projectDisplayName body (the text after the key's own last "/") given
// displayNameForKey's new name and signature — exactly the regression DONE
// WHEN clause (5)'s critic applies ("make the group editor derive its
// label from the key again").
const oldKeyDerivedDisplayNameFuncLiteral = `function displayNameForKey(projects, projectKey) {
  var key = projectKey.indexOf("workspace:") === 0 ? projectKey.slice("workspace:".length) : projectKey
  key = key.replace(/\/+$/, "")
  var slash = key.lastIndexOf("/")
  var base = slash >= 0 ? key.slice(slash + 1) : key
  return base.length > 0 ? base : projectKey
}`

// TestDisplayNameForKeyCatchesKeyDerivedRegression proves
// TestDisplayNameForKey is not vacuous: reverting displayNameForKey to
// derive a name from the key's own shape (the mutation DONE WHEN clause
// (5)'s critic applies) must produce the WRONG name for a git project_key
// ("omarcade.git", the remote URL's basename, instead of the store-computed
// "projects/omarcade"). This mutates an in-memory copy of js/groups.js's
// source, never the file on disk.
func TestDisplayNameForKeyCatchesKeyDerivedRegression(t *testing.T) {
	raw, err := os.ReadFile(groupsJSPath)
	if err != nil {
		t.Fatalf("read %s: %v", groupsJSPath, err)
	}
	source := string(raw)
	if !strings.Contains(source, displayNameForKeyFuncLiteral) {
		t.Fatalf("fixture assumption broke: %s no longer contains the expected displayNameForKey body", groupsJSPath)
	}

	mutated := strings.Replace(source, displayNameForKeyFuncLiteral, oldKeyDerivedDisplayNameFuncLiteral, 1)
	if mutated == source {
		t.Fatalf("mutation was a no-op")
	}

	gitKey := groupListFixtureProjects[0].Key
	wantDisplayName := groupListFixtureProjects[0].DisplayName
	projectsLiteral := jsonLiteral(t, groupListFixtureProjects)
	expr := "displayNameForKey(" + projectsLiteral + ", " + jsStringLiteral(gitKey) + ")"

	realRaw := evalJSSource(t, expr, source)
	var real string
	if err := json.Unmarshal(realRaw, &real); err != nil {
		t.Fatalf("decode unmutated result %s: %v", realRaw, err)
	}
	if real != wantDisplayName {
		t.Fatalf("unmutated displayNameForKey(%s) = %q, want %q", gitKey, real, wantDisplayName)
	}

	mutatedRaw := evalJSSource(t, expr, mutated)
	var bad string
	if err := json.Unmarshal(mutatedRaw, &bad); err != nil {
		t.Fatalf("decode mutated result %s: %v", mutatedRaw, err)
	}
	if bad == wantDisplayName {
		t.Fatalf("expected the key-derivation regression to produce a name other than %q, but it still matched (got %q)", wantDisplayName, bad)
	}
}
