package panel

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// commandSitePattern matches the two ways a .qml file in this plugin
// names a command to run: a Process's `command:`/`command =` property, and
// a direct `execDetached(...)` call. DONE WHEN clause 5: "Every command
// the QML runs comes from js/launchers.js" — js/launchers.js's own doc
// comment makes this claim; this test is that claim made checkable by
// asserting every such site's right-hand side goes through `Launchers.`,
// never a literal argv array written inline in a .qml file.
var commandSitePattern = regexp.MustCompile(`(?:\bcommand\s*[:=]|execDetached\()`)

// TestEveryCommandSiteUsesLaunchers walks every panel/*.qml file (never
// js/launchers.js itself, which is exactly where a literal argv belongs)
// and asserts every `command:`/`command =`/`execDetached(...)` site's
// source line names `Launchers.` — the one file allowed to name an
// external binary (js/launchers.js's own doc comment).
func TestEveryCommandSiteUsesLaunchers(t *testing.T) {
	matches, err := filepath.Glob("*.qml")
	if err != nil {
		t.Fatalf("glob *.qml: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no *.qml files found in panel/ — glob pattern broke")
	}

	found := 0
	for _, path := range matches {
		raw, err := os.ReadFile(path) //nolint:gosec // path is this test's own filepath.Glob("*.qml") result, never external input
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			code := lineCommentPattern.ReplaceAllString(line, "")
			if !commandSitePattern.MatchString(code) {
				continue
			}
			found++
			// A multi-line execDetached({ ... }) call's own `command:`
			// key can be a few lines below the call itself (Panel.qml's
			// openTerminal) — look at a small window, not just this one
			// line, before flagging it.
			window := strings.Join(lines[i:min(i+4, len(lines))], "\n")
			if !strings.Contains(window, "Launchers.") {
				t.Errorf("%s:%d: command site does not reference Launchers.* within 4 lines: %q", path, i+1, strings.TrimSpace(line))
			}
		}
	}
	if found < 3 {
		t.Fatalf("found only %d command site(s) across panel/*.qml; expected at least 3 (this-week fetch, terminal launch, bar toggle) — pattern likely broken", found)
	}
}

// TestEveryCommandSiteUsesLaunchersCatchesInlineArgv proves the test above
// is not vacuous: a command site with a literal argv array instead of a
// Launchers.* call (exactly the kind of drift DONE WHEN clause 5 guards
// against) must be caught by commandSitePattern + the Launchers. check.
func TestEveryCommandSiteUsesLaunchersCatchesInlineArgv(t *testing.T) {
	line := `    command: ["backstory", "this-week", "--json"]`
	code := lineCommentPattern.ReplaceAllString(line, "")
	if !commandSitePattern.MatchString(code) {
		t.Fatalf("commandSitePattern did not match a literal command: assignment: %q", line)
	}
	if strings.Contains(code, "Launchers.") {
		t.Fatalf("fixture line unexpectedly contains Launchers.: %q", line)
	}
}
