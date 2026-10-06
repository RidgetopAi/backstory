package block

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// testRunners is the one config list the Tests line matches against: a
// command is a test run when its head (commandHead) starts with one of these
// at a word boundary. Longer specific forms need no ordering — any match wins.
var testRunners = []string{
	"pytest",
	"python -m pytest",
	"python3 -m pytest",
	"python -m unittest",
	"python3 -m unittest",
	"go test",
	"cargo test",
	"npm test",
	"npm run test",
	"make test",
	"make check",
}

// commandSeparators end the part of a command line that names what runs: a
// pipe, `;`, `&&` or `||` hands the rest to something else.
var commandSeparators = regexp.MustCompile(`\|\||&&|\||;`)

// redirectToken matches shell redirection words such as `2>&1` or `>out`.
var redirectToken = regexp.MustCompile(`^\d*[<>]`)

// commandHead is cmd's text before the first separator, whitespace collapsed.
func commandHead(cmd string) string {
	if loc := commandSeparators.FindStringIndex(cmd); loc != nil {
		cmd = cmd[:loc[0]]
	}
	return strings.Join(strings.Fields(cmd), " ")
}

// commandKey is the normalised identity a later result clears a failure by:
// commandHead with flag tokens (starting with `-`) and redirections removed,
// so `python -m unittest` and `python -m unittest -v 2>&1 | tail` share a key.
// A command with nothing left keys on its raw text.
func commandKey(cmd string) string {
	var kept []string
	for _, tok := range strings.Fields(commandHead(cmd)) {
		if strings.HasPrefix(tok, "-") || redirectToken.MatchString(tok) {
			continue
		}
		kept = append(kept, tok)
	}
	if len(kept) == 0 {
		return cmd
	}
	return strings.Join(kept, " ")
}

// isTestCommand reports whether cmd runs one of testRunners.
func isTestCommand(cmd string) bool {
	head := commandHead(cmd)
	for _, r := range testRunners {
		if head == r || strings.HasPrefix(head, r+" ") {
			return true
		}
	}
	return false
}

// testRun is the newest observed test-runner command.
type testRun struct {
	found   bool
	cmd     string
	exit    *int
	isError bool
	ts      time.Time
}

// testsLine renders `Tests: <command> (last exit N, <age>)` for the newest
// observed test-runner command in events (ordered by id), or "" when none
// was observed. A result with no recorded exit is never rendered as exit 0:
// it reads `last failed` when is_error, else `last ok`.
func testsLine(events []store.TimelineEvent, now time.Time) string {
	toolCmd := map[string]string{}
	var last testRun
	for _, e := range events {
		switch e.Kind {
		case payload.KindToolUse:
			var tu payload.ToolUse
			if json.Unmarshal([]byte(e.Payload), &tu) == nil && tu.ToolUseID != "" && tu.Command != "" {
				toolCmd[tu.ToolUseID] = tu.Command
			}
		case payload.KindToolResult:
			var tr payload.ToolResult
			if json.Unmarshal([]byte(e.Payload), &tr) != nil {
				continue
			}
			if cmd, ok := toolCmd[tr.ToolUseID]; ok && isTestCommand(cmd) {
				last = testRun{found: true, cmd: cmd, exit: tr.Exit, isError: tr.IsError, ts: e.TS}
			}
		case payload.KindShellCommand:
			var sc payload.ShellCommand
			if json.Unmarshal([]byte(e.Payload), &sc) == nil && isTestCommand(sc.Cmd) {
				exit := sc.Exit
				last = testRun{found: true, cmd: sc.Cmd, exit: &exit, ts: e.TS}
			}
		}
	}
	if !last.found {
		return ""
	}
	cmd := strings.Join(strings.Fields(last.cmd), " ")
	if r := []rune(cmd); len(r) > maxFailureCmdRunes {
		cmd = string(r[:maxFailureCmdRunes]) + "…"
	}
	outcome := "last ok"
	switch {
	case last.exit != nil:
		outcome = fmt.Sprintf("last exit %d", *last.exit)
	case last.isError:
		outcome = "last failed"
	}
	return fmt.Sprintf("Tests: %s (%s, %s)", cmd, outcome, formatAge(now.Sub(last.ts)))
}
