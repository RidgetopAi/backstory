package block_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/payload"
)

type outcome int

const (
	passes     outcome = iota // observed, non-error: clears the failure, Tests line shows a pass
	fails                     // the runner (or chain) exited non-zero: shown as a failure
	unobserved                // the result says nothing about the runner: the failure survives
	notTestCmd                // not a test command: no Tests line change
)

// attributionRows is the table of command → result → expectation.
var attributionRows = []struct {
	name     string
	cmd      string
	exit     int // 0 = a non-error result
	want     outcome
	testHead string // pass/fail/unobserved: the Tests line must start "Tests: <testHead>"
}{
	{"bare runner", "pytest", 0, passes, "pytest"},
	{"cd and runner", "cd x && pytest -q", 0, passes, "pytest -q"},
	{"runner fails", "pytest -q", 1, fails, "pytest -q"},
	{"chain exit shown", "git diff --check && pytest", 2, fails, "pytest"},
	{"runner then commit fails", "pytest && git commit -m x", 1, unobserved, "pytest"},
	{"semicolon after", "pytest; git status --short", 0, unobserved, "pytest"},
	{"or true", "pytest || true", 0, unobserved, "pytest"},
	{"echo status", "pytest; echo $?", 0, unobserved, "pytest"},
	{"piped without pipefail", "pytest 2>&1 | tail -3", 0, unobserved, "pytest 2>&1"},
	{"pipefail pipe", "set -o pipefail; pytest 2>&1 | tee log", 0, passes, "pytest 2>&1"},
	{"background", "pytest > log 2>&1 &", 0, unobserved, ""},
	{"separator in quotes", `git commit -m "fix; pytest passes"`, 0, notTestCmd, ""},
	{"heredoc body", "cat > README.md <<'EOF'\npytest\nEOF", 0, notTestCmd, ""},
	{"heredoc body unterminated", "cat > README.md <<'EOF'\npytest", 0, notTestCmd, ""},
	{"bash -lc wrapper", `/usr/bin/bash -lc 'git diff --check && python3 -m unittest -v'`, 0, passes, "python3 -m unittest"},
	{"bash -c with true", `bash -c 'pytest; true'`, 0, unobserved, "pytest"},
	{"subshell", "( cd x && pytest )", 0, notTestCmd, ""},
	{"timeout", "timeout 60 pytest", 0, notTestCmd, ""},
	{"negation", "! pytest", 0, notTestCmd, ""},
	{"if", "if pytest; then :; fi", 0, notTestCmd, ""},
	{"cd newline runner", "cd x\npytest", 0, passes, "pytest"},
	{"runner newline status", "pytest\ngit status", 0, unobserved, "pytest"},
	{"brace group", "{ cd x; pytest; }", 0, notTestCmd, ""},
	{"trailing semicolon", "pytest;", 0, passes, "pytest"},
}

func TestCommandAttributionTable(t *testing.T) {
	for i, row := range attributionRows {
		t.Run(row.name, func(t *testing.T) {
			out := renderAfterHandoff(t, func(s func(string, any)) {
				exit1 := 1
				// A failure for pytest is open, and an older passing run exists.
				opens := "pytest" // the runner the row's command must (not) clear
				if strings.HasPrefix(row.testHead, "python3 -m unittest") {
					opens = "python3 -m unittest"
				}
				s(payload.KindToolUse, payload.ToolUse{ToolUseID: "f", Name: "Bash", Command: opens})
				s(payload.KindToolResult, payload.ToolResult{ToolUseID: "f", IsError: true, Exit: &exit1})
				s(payload.KindToolUse, payload.ToolUse{ToolUseID: "g", Name: "Bash", Command: "go test ./..."})
				s(payload.KindToolResult, payload.ToolResult{ToolUseID: "g"})

				id := fmt.Sprintf("row%d", i)
				s(payload.KindToolUse, payload.ToolUse{ToolUseID: id, Name: "Bash", Command: row.cmd})
				tr := payload.ToolResult{ToolUseID: id}
				if row.exit != 0 {
					e := row.exit
					tr.IsError, tr.Exit = true, &e
				}
				s(payload.KindToolResult, tr)
			})
			hasFailure := strings.Contains(out, "Last failure:")
			switch row.want {
			case passes:
				if hasFailure {
					t.Errorf("a passing run left the failure open:\n%s", out)
				}
				wantTests(t, out, "Tests: "+row.testHead, "(last ok")
			case fails:
				if !hasFailure {
					t.Errorf("a failing run shows no Last failure:\n%s", out)
				}
				wantTests(t, out, "Tests: "+row.testHead, fmt.Sprintf("(last exit %d", row.exit))
			case unobserved:
				if !hasFailure {
					t.Errorf("an unobserved run cleared the failure:\n%s", out)
				}
				if row.exit != 0 {
					wantTests(t, out, "Tests: "+row.testHead, fmt.Sprintf("(last exit %d", row.exit))
				} else if row.testHead != "" {
					wantTests(t, out, "Tests: "+row.testHead, "exit not observed")
				}
				if row.testHead == "" || row.exit == 0 {
					if strings.Contains(out, "last ok") && row.testHead != "" {
						t.Errorf("unobserved run rendered as a pass:\n%s", out)
					}
				}
			case notTestCmd:
				if !hasFailure {
					t.Errorf("a non-test command cleared the failure:\n%s", out)
				}
				if !strings.Contains(out, "Tests: go test ./... (last ok") {
					t.Errorf("a non-test command changed the Tests line:\n%s", out)
				}
			}
			if row.want == unobserved && row.testHead == "" && !strings.Contains(out, "Tests: go test ./... (last ok") {
				t.Errorf("a backgrounded run changed the Tests line:\n%s", out)
			}
		})
	}
}

// wantTests asserts the Tests line starts with prefix and carries outcome.
func wantTests(t *testing.T, out, prefix, outcome string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Tests: ") {
			if !strings.HasPrefix(line, prefix+" ") || !strings.Contains(line, outcome) {
				t.Errorf("Tests line %q: want prefix %q and %q", line, prefix, outcome)
			}
			return
		}
	}
	t.Errorf("no Tests line; want %q %q:\n%s", prefix, outcome, out)
}

// A heredoc that writes a file, then a real test run on later lines: the
// Tests line names the runner, not the `cat`.
func TestTestsLineSkipsHeredocHead(t *testing.T) {
	cmd := "cat > a.py <<'EOF'\nimport unittest\nEOF\npython3 -m unittest -v"
	out := renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindToolUse, payload.ToolUse{ToolUseID: "u", Name: "Bash", Command: cmd})
		s(payload.KindToolResult, payload.ToolResult{ToolUseID: "u"})
	})
	wantTests(t, out, "Tests: python3 -m unittest", "(last ok")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Tests: ") && (strings.Contains(line, "cat") || strings.Contains(line, "EOF")) {
			t.Errorf("Tests line carries the heredoc: %q", line)
		}
	}
}
