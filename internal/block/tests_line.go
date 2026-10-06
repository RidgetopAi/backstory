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

// redirectToken matches shell redirection words such as `2>&1` or `>out`.
var redirectToken = regexp.MustCompile(`^\d*[<>]`)

// pipefailSetting matches a `set` segment that turns pipefail on, e.g.
// `set -o pipefail` or `set -euo pipefail`.
var pipefailSetting = regexp.MustCompile(`^set\s(.*\s)?-[A-Za-z]*o\s+pipefail\b`)

// shellWrapper matches `bash -c`, `sh -lc`, `/usr/bin/bash -lc`: a shell
// invoked with one quoted script (Codex wraps every command this way). The
// script is the rest of the command.
var shellWrapper = regexp.MustCompile(`^\s*(?:\S*/)?(?:ba|z)?sh\s+-[A-Za-z]*c[A-Za-z]*\s+(['"])`)

// segment is one separator-delimited piece of a command line and the
// separator that followed it ("" for the last).
type segment struct {
	text, sep string
	// nested: the segment starts inside a `( … )` or `{ …; }` group, so a
	// runner word at its start is not the command the line's exit reports.
	nested bool
}

// peelWrapper unwraps at most one `bash -c '<script>'` style wrapper whose
// quoted script runs to the end of cmd; any other command is returned as is.
func peelWrapper(cmd string) string {
	m := shellWrapper.FindStringSubmatchIndex(cmd)
	if m == nil {
		return cmd
	}
	quote := cmd[m[2]]
	body := strings.TrimRight(cmd[m[3]:], " \t\r\n")
	if len(body) == 0 || body[len(body)-1] != quote {
		return cmd
	}
	body = body[:len(body)-1]
	if quote == '\'' {
		if strings.Contains(body, "'") {
			return cmd
		}
		return body
	}
	var sb strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '"':
			return cmd // an unescaped quote: the script does not run to the end
		case c == '\\' && i+1 < len(body) && strings.IndexByte("\"\\$`", body[i+1]) >= 0:
			i++
			sb.WriteByte(body[i])
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// heredoc is a `<<DELIM` whose body starts on the line after the current one.
type heredoc struct {
	delim string
	strip bool // `<<-`: leading tabs are ignored on the terminator line
}

// splitSegments cuts cmd at every unquoted newline, `;`, `&&`, `||`, `|` or
// `&`, whitespace collapsed, dropping empty segments. Separators inside
// quotes do not split, comments are skipped, and heredoc bodies are dropped:
// a heredoc's body lines are data, never commands.
func splitSegments(cmd string) []segment {
	rs := []rune(cmd)
	var (
		segs      []segment
		buf       strings.Builder
		depth     int
		startDeep bool
		pending   []heredoc
	)
	flush := func(sep string) {
		if text := strings.Join(strings.Fields(buf.String()), " "); text != "" {
			segs = append(segs, segment{text: text, sep: sep, nested: startDeep})
		}
		buf.Reset()
		startDeep = depth > 0
	}
	atWordStart := func() bool {
		s := buf.String()
		return s == "" || strings.HasSuffix(s, " ") || strings.HasSuffix(s, "\t")
	}
	at := func(i int) rune {
		if i >= 0 && i < len(rs) {
			return rs[i]
		}
		return 0
	}
	isSpace := func(r rune) bool { return r == 0 || r == ' ' || r == '\t' || r == '\n' }
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\':
			if at(i+1) == '\n' {
				buf.WriteByte(' ')
			} else {
				buf.WriteRune(r)
				buf.WriteRune(at(i + 1))
			}
			i++
		case r == '\'' || r == '"':
			buf.WriteRune(r)
			for i++; i < len(rs) && rs[i] != r; i++ {
				buf.WriteRune(rs[i])
				if r == '"' && rs[i] == '\\' && i+1 < len(rs) {
					i++
					buf.WriteRune(rs[i])
				}
			}
			if i < len(rs) {
				buf.WriteRune(r)
			}
		case r == '#' && atWordStart():
			for i+1 < len(rs) && rs[i+1] != '\n' {
				i++
			}
		case r == '(':
			depth++
			buf.WriteRune(r)
		case r == ')':
			if depth > 0 {
				depth--
			}
			buf.WriteRune(r)
		case r == '{' && atWordStart() && isSpace(at(i+1)):
			depth++
			buf.WriteRune(r)
		case r == '}' && atWordStart() && (isSpace(at(i+1)) || strings.ContainsRune(";&|)", at(i+1))):
			if depth > 0 {
				depth--
			}
			buf.WriteRune(r)
		case r == '<' && at(i+1) == '<' && at(i+2) == '<':
			buf.WriteString("<<<")
			i += 2
		case r == '<' && at(i+1) == '<':
			j := i + 2
			h := heredoc{}
			if at(j) == '-' {
				h.strip = true
				j++
			}
			for at(j) == ' ' || at(j) == '\t' {
				j++
			}
			for ; j < len(rs) && !isSpace(rs[j]) && !strings.ContainsRune(";&|()<>", rs[j]); j++ {
				if rs[j] == '\'' || rs[j] == '"' {
					q := rs[j]
					for j++; j < len(rs) && rs[j] != q; j++ {
						h.delim += string(rs[j])
					}
				} else if rs[j] != '\\' {
					h.delim += string(rs[j])
				}
			}
			pending = append(pending, h)
			buf.WriteString(string(rs[i:j]))
			i = j - 1
		case r == '\n':
			flush("\n")
			for _, h := range pending {
				for i+1 < len(rs) {
					end := i + 1
					for end < len(rs) && rs[end] != '\n' {
						end++
					}
					line := string(rs[i+1 : end])
					i = end
					if h.strip {
						line = strings.TrimLeft(line, "\t")
					}
					if line == h.delim {
						break
					}
				}
			}
			pending = nil
		case r == ';':
			flush(";")
		case r == '&' && at(i+1) == '&':
			flush("&&")
			i++
		case r == '&' && (at(i+1) == '>' || strings.HasSuffix(buf.String(), ">") || strings.HasSuffix(buf.String(), "<")):
			buf.WriteRune(r) // a redirection: `2>&1`, `&>log`
		case r == '&':
			flush("&")
		case r == '|':
			sep := "|"
			if at(i+1) == '|' {
				sep = "||"
				i++
			} else if at(i+1) == '&' {
				i++
			}
			flush(sep)
		default:
			buf.WriteRune(r)
		}
	}
	flush("")
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
	isTest bool   // a segment runs one of testRunners
	// observed: the command's exit status is the runner's (it is the last
	// segment, or only `&&` segments — or pipe stages under pipefail — follow).
	// Only meaningful when isTest.
	observed bool
	// piped: the exit is unobserved because the runner feeds a pipe and
	// pipefail is not on, so the status is the last pipeline stage's.
	piped bool
}

// followersKeepExit reports whether segs[i:] leave the exit status of the
// segment at i attributable to it: every separator after it is `&&`, or a
// pipe under pipefail; a trailing `;` or newline (nothing after it) is fine.
func followersKeepExit(segs []segment, i int, pipefail bool) (ok, piped bool) {
	for j := i; j < len(segs); j++ {
		switch sep := segs[j].sep; {
		case sep == "" || sep == "&&":
		case sep == "|" && pipefail:
		case (sep == ";" || sep == "\n") && j == len(segs)-1:
		default:
			return false, sep == "|"
		}
	}
	return true, false
}

// analyseCommand segments cmd (peeling one shell -c wrapper) and finds its
// first test-runner segment. A runner segment must start with the runner
// word outside any quote, group, heredoc body or background job.
func analyseCommand(cmd string) cmdInfo {
	segs := splitSegments(peelWrapper(cmd))
	info := cmdInfo{}
	if len(segs) > 0 {
		info.head = segs[0].text
	}
	pipefail := false
	for i, sg := range segs {
		if pipefailSetting.MatchString(sg.text) {
			pipefail = true
		}
		if runnerSegment(sg.text) && !sg.nested && sg.sep != "&" {
			info.head, info.isTest = sg.text, true
			info.observed, info.piped = followersKeepExit(segs, i, pipefail)
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
// test runner (a `;`, `||`, newline, pipe without pipefail or `&` follows
// it): such a non-error result never clears a failure and never renders as
// a pass. A non-zero one is still positive evidence of failure.
func exitUnobserved(cmd string) bool {
	info := analyseCommand(cmd)
	return info.isTest && !info.observed
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
	cmd := commandHead(last.cmd)
	if r := []rune(cmd); len(r) > maxFailureCmdRunes {
		cmd = string(r[:maxFailureCmdRunes]) + "…"
	}
	outcome := "last ok"
	switch {
	case last.exit != nil && *last.exit != 0:
		outcome = fmt.Sprintf("last exit %d", *last.exit)
	case last.isError:
		outcome = "last failed"
	case analyseCommand(last.cmd).piped:
		outcome = "exit not observed (piped)"
	case exitUnobserved(last.cmd):
		outcome = "exit not observed"
	case last.exit != nil:
		outcome = fmt.Sprintf("last exit %d", *last.exit)
	}
	return fmt.Sprintf("Tests: %s (%s, %s)", cmd, outcome, formatAge(now.Sub(last.ts)))
}
