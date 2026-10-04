package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// TestWindowAddressIsValidatedBeforeAnyHyprctlDispatch is task fc1f340d's
// DONE WHEN clause (2) for the address that reaches a hyprctl dispatch
// string: only 0x<hex> passes, anything that could break out of the
// dispatch argument is refused.
func TestWindowAddressIsValidatedBeforeAnyHyprctlDispatch(t *testing.T) {
	cases := map[string]bool{
		"0x55aa":          true,
		"0xDEADbeef":      true,
		"":                false,
		"0x":              false,
		"55aa":            false,
		"0x1; rm":         false,
		"0x1\" }) --":     false,
		"0x55aa\n":        false,
		" 0x55aa":         false,
		"address:0x55aa":  false,
		"0x55aa$(reboot)": false,
		"0xZZ":            false,
	}
	for addr, want := range cases {
		raw := evalJS(t, "isWindowAddress("+jsStringLiteral(addr)+")", launchersJSPath)
		if got := string(raw) == "true"; got != want {
			t.Errorf("isWindowAddress(%q) = %v, want %v", addr, got, want)
		}
	}
}

// TestFocusAndAgentCommandShapes pins the argv of every command task
// fc1f340d added to js/launchers.js.
func TestFocusAndAgentCommandShapes(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{`focusWindowCommand("0x55aa")`, []string{"hyprctl", "dispatch", `hl.dsp.focus({ window = "address:0x55aa" })`}},
		{`focusWindowFallbackCommand("0x55aa")`, []string{"hyprctl", "dispatch", "focuswindow", "address:0x55aa"}},
		{`agentPickCommand()`, []string{"omarchy", "agent", "--pick"}},
		{`defaultAgentCommand()`, []string{"omarchy-default-agent"}},
		{`thisWeekCommand()`, []string{"backstory", "this-week", "--json", "--here", "auto"}},
		{`agentPromptCommand("go")`, []string{"omarchy", "agent", "prompt", "go"}},
	}
	for _, c := range cases {
		var got []string
		if err := json.Unmarshal(evalJS(t, c.expr, launchersJSPath), &got); err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestNewFieldsAreReadOnlyThroughModelJS: the fields the redesign consumes
// are accessors in js/model.js, and no other panel file reads them off a
// parsed object directly.
func TestNewFieldsAreReadOnlyThroughModelJS(t *testing.T) {
	fields := loadModelFields(t)
	for _, f := range []string{"here", "window", "handoff_next", "last_agent", "tmux", "display_name", "cwd", "source"} {
		if !fields[f] {
			t.Errorf("js/model.js has no accessor reading %q", f)
		}
	}
	direct := regexp.MustCompile(`\.(handoff_next|last_agent|window|tmux|here)\b`)
	for _, pattern := range []string{"*.qml", "js/*.js"} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if path == modelJSPath {
				continue
			}
			raw, err := os.ReadFile(path) //nolint:gosec // glob of this package's own files
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(raw), "\n") {
				if m := direct.FindString(lineCommentPattern.ReplaceAllString(line, "")); m != "" {
					t.Errorf("%s:%d reads %s directly; use js/model.js", path, i+1, m)
				}
			}
		}
	}
}

// TestContinueFocusPlan: Continue on a summary with a window and a tmux
// target builds the hyprctl focus argv and the tmux select argvs, and never a
// new-terminal argv; a malformed target is rejected and builds no tmux argv.
func TestContinueFocusPlan(t *testing.T) {
	type plan struct {
		Focus         []string   `json:"focus"`
		FocusFallback []string   `json:"focusFallback"`
		Tmux          [][]string `json:"tmuxCommands"`
	}
	eval := func(expr string) *plan {
		raw := evalJS(t, expr, launchersJSPath)
		if string(raw) == "null" {
			return nil
		}
		var p plan
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("%s: %v (%s)", expr, err, raw)
		}
		return &p
	}
	argvs := func(p *plan) [][]string { return append([][]string{p.Focus, p.FocusFallback}, p.Tmux...) }

	p := eval(`continueFocusPlan("0x55aa", "Work:1.2")`)
	if p == nil {
		t.Fatal("no plan for a valid window")
	}
	if strings.Join(p.Focus, "\x00") != strings.Join(focusArgv, "\x00") {
		t.Errorf("focus = %q", p.Focus)
	}
	want := [][]string{{"tmux", "select-window", "-t", "Work:1"}, {"tmux", "select-pane", "-t", "Work:1.2"}}
	if len(p.Tmux) != 2 || strings.Join(p.Tmux[0], " ") != strings.Join(want[0], " ") || strings.Join(p.Tmux[1], " ") != strings.Join(want[1], " ") {
		t.Errorf("tmux argvs = %q, want %q", p.Tmux, want)
	}
	for _, a := range argvs(p) {
		if a[0] == "xdg-terminal-exec" {
			t.Errorf("plan builds a new-terminal argv: %q", a)
		}
	}

	for _, target := range []string{"Work:1.0;kill-server", "Work 1:1.0", "Work:1.0 ", "-t:1.0", "Work:1", "Work:x.0", "", "Work:1.0\n"} {
		p := eval(`continueFocusPlan("0x55aa", ` + jsStringLiteral(target) + `)`)
		if p == nil || len(p.Tmux) != 0 {
			t.Errorf("target %q: plan %+v, want focus only and no tmux argv", target, p)
		}
	}
	if p := eval(`continueFocusPlan("", "Work:1.0")`); p != nil {
		t.Errorf("no window must give no plan (agent launch), got %+v", p)
	}
}

var focusArgv = []string{"hyprctl", "dispatch", `hl.dsp.focus({ window = "address:0x55aa" })`}

// launchTableBinaries is the contract for js/launchers.js's LAUNCH_TABLE
// (task 4f4fd91c, decision 7026e48e): exactly these harness keys, each
// launching that harness's own binary as argv[0].
var launchTableBinaries = map[string]string{
	"claude": "claude",
	"codex":  "codex",
	"hermes": "hermes",
	"pi":     "pi",
}

// checkLaunchTable is the assertion itself, shared with the critic-mutation
// test below.
func checkLaunchTable(table map[string]struct {
	Argv   []string `json:"argv"`
	Prompt string   `json:"prompt"`
}) error {
	if len(table) != len(launchTableBinaries) {
		return fmt.Errorf("launch table has %d keys, want exactly %d (claude, codex, hermes, pi)", len(table), len(launchTableBinaries))
	}
	for key, bin := range launchTableBinaries {
		entry, ok := table[key]
		if !ok {
			return fmt.Errorf("launch table has no %q entry", key)
		}
		if len(entry.Argv) == 0 || entry.Argv[0] != bin {
			return fmt.Errorf("launch table %q argv[0] = %v, want %q", key, entry.Argv, bin)
		}
	}
	return nil
}

func loadLaunchTable(t *testing.T) map[string]struct {
	Argv   []string `json:"argv"`
	Prompt string   `json:"prompt"`
} {
	t.Helper()
	raw := evalJS(t, "LAUNCH_TABLE", launchersJSPath)
	var table map[string]struct {
		Argv   []string `json:"argv"`
		Prompt string   `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("decode LAUNCH_TABLE %s: %v", raw, err)
	}
	return table
}

func TestLaunchTableKeysAndBinaries(t *testing.T) {
	if err := checkLaunchTable(loadLaunchTable(t)); err != nil {
		t.Fatal(err)
	}
}

// TestLaunchTableFlagsMirrorOmarchyAgent pins the flags the task spec says
// mirror omarchy-agent's own launch lines, and that the hermes prompt rides
// --query=.
func TestLaunchTableFlagsMirrorOmarchyAgent(t *testing.T) {
	table := loadLaunchTable(t)
	want := map[string]string{
		"claude": "claude --permission-mode auto",
		"codex":  "codex --approve-for-me",
		"hermes": "hermes chat --yolo --tui",
		"pi":     "pi",
	}
	for key, argv := range want {
		if got := strings.Join(table[key].Argv, " "); got != argv {
			t.Errorf("%s argv = %q, want %q", key, got, argv)
		}
	}
	if table["hermes"].Prompt != "--query=" {
		t.Errorf("hermes prompt = %q, want --query=", table["hermes"].Prompt)
	}
}

// TestLaunchTableCatchesWrongBinary proves the contract is not vacuous.
func TestLaunchTableCatchesWrongBinary(t *testing.T) {
	table := loadLaunchTable(t)
	entry := table["pi"]
	entry.Argv = []string{"omarchy", "agent", "prompt"}
	table["pi"] = entry
	if checkLaunchTable(table) == nil {
		t.Fatal("checkLaunchTable accepted pi launching omarchy")
	}
	delete(table, "codex")
	if checkLaunchTable(table) == nil {
		t.Fatal("checkLaunchTable accepted a table missing codex")
	}
}
