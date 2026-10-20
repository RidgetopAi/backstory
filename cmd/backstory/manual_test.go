package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// manualSystemCommands are run by the service and the agents' hooks, never
// by a person, so the User Manual names them once instead of documenting them.
var manualSystemCommands = map[string]bool{"daemon": true, "mcp": true, "hook": true, "shell": true}

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
