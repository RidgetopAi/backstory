package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// manualSystemCommands are run by the service and the agents' hooks, never
// by a person, so the User Manual names them once instead of documenting them.
var manualSystemCommands = map[string]bool{"daemon": true, "mcp": true, "hook": true, "shell": true, "sync-serve": true}

// TestManualCoversEveryCommand keeps docs/USER-MANUAL.md in step with the CLI:
// every user-facing command in main.go's usage has its own "### <command>"
// entry, and every `backstory <command>` the manual shows is one main.go
// dispatches.
func TestManualCoversEveryCommand(t *testing.T) {
	b, err := os.ReadFile("../../docs/USER-MANUAL.md")
	if err != nil {
		t.Fatal(err)
	}
	manual := string(b)

	listed := regexp.MustCompile(`(?m)^  ([a-z][a-z-]*) {2,}`).FindAllStringSubmatch(usage, -1)
	if len(listed) == 0 {
		t.Fatal("parsed no commands from main.go's usage")
	}
	for _, m := range listed {
		name := m[1]
		if manualSystemCommands[name] {
			if !strings.Contains(manual, "`"+name+"`") {
				t.Errorf("manual does not mention system command %q", name)
			}
			continue
		}
		if !strings.Contains(manual, "\n### "+name+"\n") {
			t.Errorf("manual has no \"### %s\" entry in its command reference", name)
		}
	}

	for _, m := range regexp.MustCompile("(?m)(?:^|`)backstory ([a-z][a-z-]*)").FindAllStringSubmatch(manual, -1) {
		var out, errb strings.Builder
		run([]string{m[1], "--help"}, strings.NewReader(""), &out, &errb)
		if strings.Contains(errb.String(), "unknown command") {
			t.Errorf("manual names `backstory %s`, which main.go does not dispatch", m[1])
		}
	}
}

// TestManualTurningSyncOff pins the "Turning sync off" section to the
// behavior TestPairRemoveClearsSyncWarnings asserts, and keeps the old gear
// icon wording out of the docs and the panel README.
func TestManualTurningSyncOff(t *testing.T) {
	b, err := os.ReadFile("../../docs/USER-MANUAL.md")
	if err != nil {
		t.Fatal(err)
	}
	manual := string(b)
	i := strings.Index(manual, "\n#### Turning sync off\n")
	if i < 0 {
		t.Fatal("manual has no \"Turning sync off\" section")
	}
	section := manual[i+1:]
	if j := strings.Index(section[5:], "\n#"); j >= 0 {
		section = section[:j+5]
	}
	for _, want := range []string{
		"backstory pair --remove <computer>",
		"backstory install sync --check",
		"backstory install sync --remove",
		"backstory pair <computer>",
		"SYNC FAILING",
		"Everything already synced stays",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Turning sync off section does not mention %q", want)
		}
	}
	if strings.Count(manual, "(#turning-sync-off)") < 2 {
		t.Error("troubleshooting does not link to Turning sync off")
	}
	for _, want := range []string{
		"The bar popup has no\nclose button. The pop-out window has one, after the pin at the right end of every view's header.",
		"| Close | close button (pop-out window only) | `Esc` |",
	} {
		if !strings.Contains(manual, want) {
			t.Errorf("manual lost the close-button wording %q", want)
		}
	}
	for _, f := range []string{"../../docs/USER-MANUAL.md", "../../panel/README.md"} {
		d, err := os.ReadFile(f) //nolint:gosec // fixed doc paths from the list above
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(d)), "gear") {
			t.Errorf("%s still mentions a gear icon", f)
		}
	}
}
