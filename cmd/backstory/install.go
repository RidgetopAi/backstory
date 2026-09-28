package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/RidgetopAi/backstory/internal/install"
)

const installUsage = `usage: backstory install claude [--check] [--remove] [--no-verify]
       backstory install bash [--check] [--remove]

"install claude" registers (or, with --remove, unregisters) Backstory's
Claude Code integration under $HOME: the mcpServers.backstory entry, the
SessionStart hook, the skill file, and the CLAUDE.md stub.

  --check      report each item's status (present/absent/foreign-conflict)
               and exit non-zero unless every item is present; writes nothing
  --remove     reverse a prior install, leaving foreign entries untouched
  --no-verify  skip running the installed hook to confirm it fires

"install bash" adds (or, with --remove, removes) the marked block in
~/.bashrc that wires bash's preexec/precmd shell command capture (task
7fe84ffb) into every interactive shell.

  --check      report whether the block is present; exit non-zero if absent
  --remove     remove exactly the marked block, leaving the rest of
               ~/.bashrc untouched
`

// runInstall is the `backstory install` subcommand: "claude" registers the
// Claude Code integration (PLAN.md §Phase 2's week-one slice; Codex,
// Copilot and the rest are out of scope for that punch); "bash" wires shell
// command capture into ~/.bashrc (task fc7d4ee8, part 2 of task 7fe84ffb).
func runInstall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, installUsage)
		return 2
	}
	switch args[0] {
	case "claude":
		return runInstallClaude(args[1:], stdout, stderr)
	case "bash":
		return runInstallBash(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprint(stderr, installUsage)
		return 2
	}
}

func runInstallClaude(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install claude", flag.ContinueOnError)
	check := fs.Bool("check", false, "report install status per item")
	remove := fs.Bool("remove", false, "remove everything backstory install added")
	noVerify := fs.Bool("no-verify", false, "skip verifying the installed hook actually fires")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *check && *remove {
		_, _ = fmt.Fprintln(stderr, "backstory install claude: --check and --remove are mutually exclusive")
		return 2
	}

	home, err := os.UserHomeDir()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install claude:", err)
		return 1
	}
	paths := install.DefaultPaths(home)
	opts := install.Options{Verify: !*noVerify}

	switch {
	case *check:
		return runInstallClaudeCheck(paths, opts, stdout, stderr)
	case *remove:
		return runInstallClaudeRemove(paths, opts, stderr)
	default:
		return runInstallClaudeInstall(paths, opts, stderr)
	}
}

func runInstallClaudeCheck(paths install.Paths, opts install.Options, stdout, stderr io.Writer) int {
	items, err := install.Check(paths, opts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install claude --check:", err)
		return 1
	}
	allPresent := true
	for _, it := range items {
		_, _ = fmt.Fprintf(stdout, "%s: %s\n", it.Name, it.Status)
		if it.Status != install.StatusPresent {
			allPresent = false
		}
	}
	if !allPresent {
		return 1
	}
	return 0
}

func runInstallClaudeRemove(paths install.Paths, opts install.Options, stderr io.Writer) int {
	if err := install.Remove(paths, opts); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install claude --remove:", err)
		return 1
	}
	return 0
}

func runInstallClaudeInstall(paths install.Paths, opts install.Options, stderr io.Writer) int {
	if err := install.Install(paths, opts); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install claude:", err)
		return 1
	}
	return 0
}

func runInstallBash(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install bash", flag.ContinueOnError)
	check := fs.Bool("check", false, "report whether the bashrc block is present")
	remove := fs.Bool("remove", false, "remove the bashrc block backstory install added")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *check && *remove {
		_, _ = fmt.Fprintln(stderr, "backstory install bash: --check and --remove are mutually exclusive")
		return 2
	}

	home, err := os.UserHomeDir()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install bash:", err)
		return 1
	}
	path := install.DefaultBashrcPath(home)

	switch {
	case *check:
		status := install.BashrcStatus(path)
		_, _ = fmt.Fprintf(stdout, "bashrc: %s\n", status)
		if status != install.StatusPresent {
			return 1
		}
		return 0
	case *remove:
		if err := install.RemoveBashrc(path); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory install bash --remove:", err)
			return 1
		}
		return 0
	default:
		if err := install.InstallBashrc(path); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory install bash:", err)
			return 1
		}
		return 0
	}
}
