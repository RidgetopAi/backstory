package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// agentEnvMarker is one environment variable an agent harness sets on the
// shell its model drives (its Bash / exec / terminal tool). A non-empty value
// means "this process descends from an agent's tool call".
type agentEnvMarker struct {
	Var     string // the variable name
	Harness string // which harness sets it
	Source  string // where that was established, so a change upstream can be re-checked
}

// agentSessionEnv is the named table eraseConfirmation reads to decide that
// `backstory purge` / `delete` / `restore` is running inside an agent session
// (task 4803bf95). Order is only the order the refusal names the first hit.
//
// It is a speed bump against an agent erasing memory through its own shell,
// not a security boundary: an agent can unset any of these. It exists so an
// agent cannot do it by accident, or by obeying a prompt-injected "clean up
// with backstory purge --yes", without the human seeing a prompt.
var agentSessionEnv = []agentEnvMarker{
	{"CLAUDECODE", "claude", "measured 2026-11-25: CLAUDECODE=1 in a Claude Code Bash tool's env"},
	{"CLAUDE_CODE_ENTRYPOINT", "claude", "measured 2026-11-25: CLAUDE_CODE_ENTRYPOINT=cli in the same env"},
	{"CODEX_SANDBOX_NETWORK_DISABLED", "codex", "measured 2026-11-25: set by `codex sandbox -- env` (codex-cli 0.151.0) on a sandboxed command"},
	{"CODEX_SANDBOX", "codex", "Codex sets it (=seatbelt) on a macOS-sandboxed command; std-env 2.6.1 agent table"},
	{"CODEX_THREAD_ID", "codex", "string in the codex-cli 0.151.0 binary; std-env 2.6.1 agent table lists it for codex"},
	{"HERMES_SESSION_ID", "hermes", "hermes-agent 29112bef agent/agent_init.py sets it process-wide; measured 2026-11-25 that tools/environments/local.py _sanitize_subprocess_env keeps it for the terminal tool"},
	{"PI_CODING_AGENT", "pi", "pi 1.6.0 dist/cli/setup.js setupCli sets PI_CODING_AGENT=true; its bash tool's getShellEnv spreads process.env"},
	{"AI_AGENT", "claude, pi (a cross-harness convention)", "measured 2026-11-25: AI_AGENT=claude-code_<version>_agent in a Claude Code Bash tool's env; pi 1.6.0 setupCli sets AI_AGENT=pi; std-env 2.6.1 reads it first for any agent"},
	{"OPENCODE", "opencode", "std-env 2.6.1 agent table; not measured on an installed opencode"},
}

// agentSession reports the first agentSessionEnv marker set (non-empty) in
// the environment getenv reads.
func agentSession(getenv func(string) string) (agentEnvMarker, bool) {
	for _, m := range agentSessionEnv {
		if getenv(m.Var) != "" {
			return m, true
		}
	}
	return agentEnvMarker{}, false
}

// controllingTTYPath is the controlling terminal the in-agent confirmation
// is read from.
const controllingTTYPath = "/dev/tty"

// eraseConfirmation decides whether purge/delete/restore (verb) may go ahead and,
// when a question must be asked, where it is asked:
//
//   - outside an agent session: --yes goes ahead with no question (the
//     panel's Forget/Delete path); otherwise the question is asked on stdin
//     when stdin is a terminal, and refused when it is not.
//   - inside one (agentSession): --yes is refused, and the question is
//     asked on the controlling terminal (/dev/tty) — never stdin, so
//     `yes | backstory purge …` does not answer it. No controlling terminal
//     (an agent's tool shell has none) refuses.
//
// It returns ask == nil with ok to go ahead unasked, a non-nil ask to put
// the question through, or ok == false after printing why it refused.
// closeFn is always safe to call.
func eraseConfirmation(verb string, yes bool, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) (ask *confirmer, ok bool, closeFn func()) {
	noop := func() {}
	m, inAgent := agentSession(getenv)
	if !inAgent {
		if yes {
			return nil, true, noop
		}
		if !isTerminal(stdin) {
			_, _ = fmt.Fprintf(stderr, "backstory %s: refusing to %s without --yes on a non-interactive session\n", verb, verb)
			return nil, false, noop
		}
		return &confirmer{in: stdin, out: stdout}, true, noop
	}
	if yes {
		_, _ = fmt.Fprintf(stderr, "backstory %s: --yes is refused inside an agent session (%s is set, %s); you must confirm in a terminal\n", verb, m.Var, m.Harness)
	}
	tty, err := os.OpenFile(controllingTTYPath, os.O_RDWR, 0)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "backstory %s: %s must be confirmed by you in a terminal; run it yourself outside the agent (%s is set, %s; no controlling terminal: %v)\n", verb, verb, m.Var, m.Harness, err)
		return nil, false, noop
	}
	return &confirmer{in: tty, out: tty}, true, func() { _ = tty.Close() }
}

// confirmer is where one y/N question is put and answered.
type confirmer struct {
	in  io.Reader
	out io.Writer
}

// answer prints question and reads one line, surrounding whitespace
// trimmed; EOF reads as the empty answer.
func (c *confirmer) answer(question string) string {
	_, _ = fmt.Fprint(c.out, question)
	scanner := bufio.NewScanner(c.in)
	if !scanner.Scan() {
		return ""
	}
	return strings.TrimSpace(scanner.Text())
}

// yesNo is answer where only a bare "y" or "Y" counts as consent; EOF, an
// empty line, or anything else refuses.
func (c *confirmer) yesNo(question string) bool {
	a := c.answer(question)
	return a == "y" || a == "Y"
}
