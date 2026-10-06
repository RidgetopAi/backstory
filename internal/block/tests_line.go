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

// commandSeparators split a command into segments: a newline, `;`, `&&`,
// `||` or a pipe hands the rest to something else.
var commandSeparators = regexp.MustCompile(`\|\||&&|\||;|\n`)

// redirectToken matches shell redirection words such as `2>&1` or `>out`.
var redirectToken = regexp.MustCompile(`^\d*[<>]`)

// pipefailSetting matches a `set` segment that turns pipefail on, e.g.
// `set -o pipefail` or `set -euo pipefail`.
var pipefailSetting = regexp.MustCompile(`^set\s(.*\s)?-[A-Za-z]*o\s+pipefail\b`)

// segment is one separator-delimited piece of a command line and the
// separator that followed it ("" for the last).
type segment struct{ text, sep string }

// splitSegments cuts cmd at every commandSeparators match, whitespace
// collapsed, dropping empty segments.
func splitSegments(cmd string) []segment {
	var segs []segment
	prev := 0
	add := func(text, sep string) {
		if text = strings.Join(strings.Fields(text), " "); text != "" {
			segs = append(segs, segment{text, sep})
		}
	}
	for _, loc := range commandSeparators.FindAllStringIndex(cmd, -1) {
		add(cmd[prev:loc[0]], cmd[loc[0]:loc[1]])
		prev = loc[1]
	}
	add(cmd[prev:], "")
	return segs
}

// runnerSegment reports whether a segment's text starts with a test runner.
func runnerSegment(text string) bool {
	for _, r := range testRunners {
		if text == r || strings.HasPrefix(text, r+" ") {
			return true
		}
	}
	return false
}

// cmdInfo is what the block knows about one command line.
type cmdInfo struct {
	head   string // the test-runner segment, else the first segment
	isTest bool   // some segment runs one of testRunners
	// piped: the runner segment feeds a pipe and pipefail is not on, so the
	// command's exit status is the last pipeline stage's, not the runner's.
	piped bool
}

// analyseCommand segments cmd and finds its first test-runner segment.
func analyseCommand(cmd string) cmdInfo {
	segs := splitSegments(cmd)
	info := cmdInfo{}
	if len(segs) > 0 {
		info.head = segs[0].text
	}
	pipefail := false
	for _, sg := range segs {
		if pipefailSetting.MatchString(sg.text) {
			pipefail = true
		}
		if runnerSegment(sg.text) {
			info.head, info.isTest = sg.text, true
			info.piped = sg.sep == "|" && !pipefail
			break
		}
	}
	return info
}

// commandHead is the segment of cmd that names what runs: its test-runner
// segment, else its first segment.
func commandHead(cmd string) string { return analyseCommand(cmd).head }

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

// isTestCommand reports whether any segment of cmd runs one of testRunners.
func isTestCommand(cmd string) bool { return analyseCommand(cmd).isTest }

// exitUnobserved reports whether cmd's exit status says nothing about its
// test runner (the runner is piped without pipefail): such an exit never
// clears a failure and never renders as a pass. A non-zero one is still
// positive evidence of failure.
func exitUnobserved(cmd string) bool { return analyseCommand(cmd).piped }

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
	case last.exit != nil && *last.exit != 0:
		outcome = fmt.Sprintf("last exit %d", *last.exit)
	case last.isError:
		outcome = "last failed"
	case exitUnobserved(last.cmd):
		outcome = "exit not observed (piped)"
	case last.exit != nil:
		outcome = fmt.Sprintf("last exit %d", *last.exit)
	}
	return fmt.Sprintf("Tests: %s (%s, %s)", cmd, outcome, formatAge(now.Sub(last.ts)))
}
