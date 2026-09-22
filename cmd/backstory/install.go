package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/RidgetopAi/backstory/internal/install"
)

const installUsage = `usage: backstory install claude [--check] [--remove] [--no-verify]

Registers (or, with --remove, unregisters) Backstory's Claude Code
integration under $HOME: the mcpServers.backstory entry, the SessionStart
hook, the skill file, and the CLAUDE.md stub.

  --check      report each item's status (present/absent/foreign-conflict)
               and exit non-zero unless every item is present; writes nothing
  --remove     reverse a prior install, leaving foreign entries untouched
  --no-verify  skip running the installed hook to confirm it fires
`

// runInstall is the `backstory install` subcommand. Only the "claude"
// harness is in scope (PLAN.md §Phase 2's week-one slice); Codex, Copilot
// and the rest are out of scope for this punch.
func runInstall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "claude" {
		_, _ = fmt.Fprint(stderr, installUsage)
		return 2
	}
	args = args[1:]

	fs := flag.NewFlagSet("install claude", flag.ContinueOnError)
	check := fs.Bool("check", false, "report install status per item")
	remove := fs.Bool("remove", false, "remove everything backstory install added")
	noVerify := fs.Bool("no-verify", false, "skip verifying the installed hook actually fires")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *check && *remove {
		_, _ = fmt.Fprintln(stderr, "backstory install: --check and --remove are mutually exclusive")
		return 2
	}

	home, err := os.UserHomeDir()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install:", err)
		return 1
	}
	paths := install.DefaultPaths(home)
	opts := install.Options{Verify: !*noVerify}

	switch {
	case *check:
		return runInstallCheck(paths, opts, stdout, stderr)
	case *remove:
		return runInstallRemove(paths, opts, stderr)
	default:
		return runInstallInstall(paths, opts, stderr)
	}
}

func runInstallCheck(paths install.Paths, opts install.Options, stdout, stderr io.Writer) int {
	items, err := install.Check(paths, opts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install --check:", err)
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

func runInstallRemove(paths install.Paths, opts install.Options, stderr io.Writer) int {
	if err := install.Remove(paths, opts); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install --remove:", err)
		return 1
	}
	return 0
}

func runInstallInstall(paths install.Paths, opts install.Options, stderr io.Writer) int {
	if err := install.Install(paths, opts); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install:", err)
		return 1
	}
	return 0
}
