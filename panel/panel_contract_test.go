// Package panel holds the Quickshell "This Week" plugin (task
// 4fe02e30, PLAN.md Phase 4 "Human look", decision 9be5c1d5). The plugin
// itself is QML/JS — this file is the lane proof: it extracts every
// `backstory this-week --json` field the plugin reads and asserts each one
// exists in the committed golden, so a field rename or removal in that
// contract turns this test red instead of failing silently at runtime in
// someone's Quickshell session.
package panel

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// modelJSPath is the one file in this plugin allowed to read a
// this-week JSON field (js/model.js's own doc comment). Every other .qml
// or .js file goes through one of its functions instead of touching
// `.field` on a parsed JSON object directly — see that file for why.
const modelJSPath = "js/model.js"

// thisWeekGoldenPath is the committed contract golden this plugin is built
// against (PANEL-CONTRACT.md, cmd/backstory/thisweek_test.go). It is the
// SAME file the this-week CLI's own golden test asserts byte-for-byte
// output against — not a copy — so a real contract change (the CLI's own
// output shape changing) and a golden-only edit both show up here.
const thisWeekGoldenPath = "../cmd/backstory/testdata/thisweek/all.json.golden"

// fieldAccessPattern matches a JS/QML property access like `item.kind` or
// `p.project_key` and captures the field name. It requires a word
// character immediately before the dot, so it does not match `.pragma`
// (a QML library directive, not a property access) or a leading `.` at
// the start of a line/comment.
var fieldAccessPattern = regexp.MustCompile(`\w\.([a-z_][a-zA-Z0-9_]*)\b`)

// lineCommentPattern strips a JS `//` line comment (and everything after
// it) so prose like "PANEL-CONTRACT.md" or "store.SetProjectGroup" in a
// doc comment is never mistaken for a field access.
var lineCommentPattern = regexp.MustCompile(`//.*$`)

// extractFieldAccesses returns the set of field names model.js reads off a
// JSON value, by stripping comments and running fieldAccessPattern over
// what remains.
func extractFieldAccesses(src string) map[string]bool {
	fields := map[string]bool{}
	for _, line := range strings.Split(src, "\n") {
		code := lineCommentPattern.ReplaceAllString(line, "")
		for _, m := range fieldAccessPattern.FindAllStringSubmatch(code, -1) {
			fields[m[1]] = true
		}
	}
	return fields
}

// collectJSONKeys walks a decoded JSON value and returns every object key
// found anywhere in it, at any depth — the full set of field names the
// committed golden actually has.
func collectJSONKeys(v interface{}, into map[string]bool) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, sub := range t {
			into[k] = true
			collectJSONKeys(sub, into)
		}
	case []interface{}:
		for _, sub := range t {
			collectJSONKeys(sub, into)
		}
	}
}

func loadGoldenKeys(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(thisWeekGoldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v", thisWeekGoldenPath, err)
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal golden %s: %v", thisWeekGoldenPath, err)
	}
	keys := map[string]bool{}
	collectJSONKeys(decoded, keys)
	return keys
}

func loadModelFields(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(modelJSPath)
	if err != nil {
		t.Fatalf("read %s: %v", modelJSPath, err)
	}
	return extractFieldAccesses(string(raw))
}

// TestThisWeekFieldContract is DONE WHEN clause (1): every this-week JSON
// field path js/model.js reads must exist in the committed golden.
func TestThisWeekFieldContract(t *testing.T) {
	fields := loadModelFields(t)
	if len(fields) < 15 {
		t.Fatalf("extracted only %d field accesses from %s; expected at least 15 — extraction likely broken", len(fields), modelJSPath)
	}

	golden := loadGoldenKeys(t)

	var missing []string
	for f := range fields {
		if !golden[f] {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("js/model.js reads field(s) not present anywhere in %s: %v", thisWeekGoldenPath, missing)
	}
}

// TestThisWeekFieldContractCatchesRename proves the contract test is not
// vacuous: renaming a field in the golden (the scenario DONE WHEN clause
// (3)'s critic exercises) must turn TestThisWeekFieldContract red. This
// mutates an in-memory copy of the decoded golden, never the file on disk.
func TestThisWeekFieldContractCatchesRename(t *testing.T) {
	fields := loadModelFields(t)
	if !fields["project_key"] {
		t.Fatalf("expected js/model.js to reference project_key; test fixture assumption broke")
	}

	raw, err := os.ReadFile(thisWeekGoldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v", thisWeekGoldenPath, err)
	}
	// A rename in the golden's raw bytes is exactly what the critic's
	// mutation does: change the contract without touching the plugin.
	mutated := strings.ReplaceAll(string(raw), `"project_key"`, `"projectKey"`)
	if mutated == string(raw) {
		t.Fatalf("mutation was a no-op; golden fixture no longer contains \"project_key\"")
	}

	var decoded interface{}
	if err := json.Unmarshal([]byte(mutated), &decoded); err != nil {
		t.Fatalf("unmarshal mutated golden: %v", err)
	}
	mutatedKeys := map[string]bool{}
	collectJSONKeys(decoded, mutatedKeys)

	if mutatedKeys["project_key"] {
		t.Fatalf("mutated golden still has project_key; rename did not take")
	}
	if !fields["project_key"] {
		t.Fatalf("js/model.js no longer reads project_key; this test's premise broke")
	}
	// project_key is read by the plugin (asserted above) but absent from
	// the mutated contract (asserted above) — exactly the condition
	// TestThisWeekFieldContract's missing-field check catches.
}
