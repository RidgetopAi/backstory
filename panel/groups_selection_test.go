package panel

import (
	"encoding/json"
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

// TestProjectDisplayName covers the basename-derivation the redesigned
// GroupProjectRow.qml shows in place of a raw project key, against the
// exact ungrouped keys observed on Brian's desk (round 2 defect C):
// `backstory group list --json` returning
// ["/home/ridgetop","/home/ridgetop/.local/bin","/home/ridgetop/projects","workspace:/home/ridgetop/projects"].
func TestProjectDisplayName(t *testing.T) {
	cases := []struct {
		projectKey string
		want       string
	}{
		{"/home/ridgetop", "ridgetop"},
		{"/home/ridgetop/.local/bin", "bin"},
		{"/home/ridgetop/projects", "projects"},
		{"workspace:/home/ridgetop/projects", "projects"},
		{"/", "/"},
	}
	for _, c := range cases {
		t.Run(c.projectKey, func(t *testing.T) {
			expr := "projectDisplayName(" + jsStringLiteral(c.projectKey) + ")"
			got := evalJSValue(t, expr)
			if got != c.want {
				t.Fatalf("%s = %v, want %q", expr, got, c.want)
			}
		})
	}
}
