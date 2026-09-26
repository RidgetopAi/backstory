package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// launchersJSPath is js/launchers.js's own doc comment's claim made
// checkable: the one file in this plugin that names an external binary.
const launchersJSPath = "js/launchers.js"

// terminalLaunchScriptLiteral is the exact `sh -c` script
// js/launchers.js's terminalLaunchCommand ships today (round 2 defect A's
// measured fix). TestTerminalLaunchCommandCatchesCwdInterpolation asserts
// this literal is still present before mutating a copy of it, so the
// mutation test fails loudly instead of silently passing if the real
// script ever changes shape.
const terminalLaunchScriptLiteral = `'cd "$1" && exec "${SHELL:-sh}"'`

// checkTerminalLaunchCommand is DONE WHEN clause (3) itself, factored out
// so both the real (unmutated) test and the critic-mutation test below run
// the identical assertion — the same pattern panel_contract_test.go's
// TestThisWeekFieldContractCatchesRename already uses for clause (1).
func checkTerminalLaunchCommand(command []string, cwd string) error {
	want := []string{"xdg-terminal-exec", "--", "sh", "-c"}
	if len(command) != 7 {
		return fmt.Errorf("want 7 args, got %d: %v", len(command), command)
	}
	for i, w := range want {
		if command[i] != w {
			return fmt.Errorf("arg %d = %q, want %q", i, command[i], w)
		}
	}
	if command[5] != "sh" {
		return fmt.Errorf("arg 5 = %q, want %q", command[5], "sh")
	}
	if command[6] != cwd {
		return fmt.Errorf("arg 6 = %q, want cwd %q (cwd must be the final argument)", command[6], cwd)
	}
	script := command[4]
	if strings.Contains(script, cwd) {
		return fmt.Errorf("script (arg 4) contains a copy of cwd: %q", script)
	}
	return nil
}

// TestTerminalLaunchCommandShape is DONE WHEN clause (3): a cwd containing
// a single quote and a space (the class of path that breaks naive shell
// interpolation) must still come through as exactly one argument, never
// copied into the script string.
func TestTerminalLaunchCommandShape(t *testing.T) {
	cwd := `/home/ridgetop/my 'project` + " dir"
	raw := evalJS(t, "terminalLaunchCommand("+jsStringLiteral(cwd)+")", launchersJSPath)

	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode terminalLaunchCommand(%q) result %s: %v", cwd, raw, err)
	}
	if err := checkTerminalLaunchCommand(got, cwd); err != nil {
		t.Fatalf("%v; got %v", err, got)
	}
}

// TestTerminalLaunchCommandCatchesCwdInterpolation proves
// checkTerminalLaunchCommand is not vacuous: interpolating cwd into the
// `sh -c` script string (exactly the mutation DONE WHEN clause (6)'s
// critic applies) must make it fail. This mutates an in-memory copy of
// js/launchers.js's source, never the file on disk.
func TestTerminalLaunchCommandCatchesCwdInterpolation(t *testing.T) {
	raw, err := os.ReadFile(launchersJSPath)
	if err != nil {
		t.Fatalf("read %s: %v", launchersJSPath, err)
	}
	source := string(raw)
	if !strings.Contains(source, terminalLaunchScriptLiteral) {
		t.Fatalf("fixture assumption broke: %s no longer contains %s", launchersJSPath, terminalLaunchScriptLiteral)
	}

	mutatedScript := `'cd "' + cwd + '" && exec "${SHELL:-sh}"'`
	mutated := strings.Replace(source, terminalLaunchScriptLiteral, mutatedScript, 1)
	if mutated == source {
		t.Fatalf("mutation was a no-op")
	}

	cwd := "/tmp/some project"
	exprCwd := jsStringLiteral(cwd)

	realRaw := evalJSSource(t, "terminalLaunchCommand("+exprCwd+")", source)
	var real []string
	if err := json.Unmarshal(realRaw, &real); err != nil {
		t.Fatalf("decode unmutated result %s: %v", realRaw, err)
	}
	if err := checkTerminalLaunchCommand(real, cwd); err != nil {
		t.Fatalf("unmutated %s failed its own shape check: %v", launchersJSPath, err)
	}

	mutatedRaw := evalJSSource(t, "terminalLaunchCommand("+exprCwd+")", mutated)
	var bad []string
	if err := json.Unmarshal(mutatedRaw, &bad); err != nil {
		t.Fatalf("decode mutated result %s: %v", mutatedRaw, err)
	}
	if err := checkTerminalLaunchCommand(bad, cwd); err == nil {
		t.Fatalf("expected the cwd-interpolation mutation to fail checkTerminalLaunchCommand, but it passed: %v", bad)
	}
}
