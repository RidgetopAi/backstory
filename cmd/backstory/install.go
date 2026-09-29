package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/RidgetopAi/backstory/internal/install"
)

const installUsage = `usage: backstory install [harness...] [--check] [--remove] [--no-verify]

Registers (or, with --remove, unregisters) Backstory's integration for one
or more harnesses under $HOME. With no harness named, installs Claude Code
(the default adapter; decision 3e14db82's others — codex and pi — are
available; hermes is a recognized name but not yet available; agents is
the generic fallback; pi installs an extension under ~/.pi/agent).

Valid harness names: claude, codex, hermes, pi, agents

  --check      report each item's status (present/absent/foreign-conflict)
               and exit non-zero unless every item is present; writes nothing
  --remove     reverse a prior install, leaving foreign entries untouched
  --no-verify  skip running the installed hook to confirm it fires

"install bash" is not a harness: it adds (or, with --remove, removes) the
marked block in ~/.bashrc that wires bash's preexec/precmd shell command
capture (task 7fe84ffb) into every interactive shell.

usage: backstory install bash [--check] [--remove]

  --check      report whether the block is present; exit non-zero if absent
  --remove     remove exactly the marked block, leaving the rest of
               ~/.bashrc untouched
`

// defaultHarnesses is what `backstory install` with no harness names
// installs. It is today's pre-adapter-seam behaviour (Claude only),
// preserved until the rest of decision 3e14db82's adapters land, at which
// point the punch's CLI spec calls for this becoming "every harness
// detected on the machine" instead.
var defaultHarnesses = []string{install.HarnessClaude}

// runInstall is the `backstory install` subcommand. Harness names are the
// leading non-flag arguments (in any position relative to the flags, since
// every flag here is boolean); an unrecognized name exits non-zero listing
// every valid one. "bash" is not a harness but the shell-capture
// target (task fc7d4ee8), dispatched before the adapter path with its own
// flag set.
func runInstall(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "bash" {
		return runInstallBash(args[1:], stdout, stderr)
	}

	var harnessArgs, flagArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
		} else {
			harnessArgs = append(harnessArgs, a)
		}
	}

	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	check := fs.Bool("check", false, "report install status per item")
	remove := fs.Bool("remove", false, "remove everything backstory install added")
	noVerify := fs.Bool("no-verify", false, "skip verifying the installed hook actually fires")
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, installUsage) }
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if *check && *remove {
		_, _ = fmt.Fprintln(stderr, "backstory install: --check and --remove are mutually exclusive")
		return 2
	}

	harnesses := harnessArgs
	if len(harnesses) == 0 {
		harnesses = defaultHarnesses
	}

	adapters := make([]install.Adapter, 0, len(harnesses))
	for _, h := range harnesses {
		a, ok := install.AdapterByName(h)
		if !ok {
			_, _ = fmt.Fprintf(stderr, "backstory install: unknown harness %q; valid harnesses: %s\n", h, strings.Join(install.HarnessNames(), ", "))
			return 2
		}
		adapters = append(adapters, a)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory install:", err)
		return 1
	}
	opts := install.Options{Verify: !*noVerify}

	switch {
	case *check:
		return runInstallCheck(adapters, home, opts, stdout, stderr)
	case *remove:
		return runInstallRemove(adapters, home, opts, stderr)
	default:
		return runInstallInstall(adapters, home, opts, stderr)
	}
}

func runInstallCheck(adapters []install.Adapter, home string, opts install.Options, stdout, stderr io.Writer) int {
	allPresent := true
	for _, a := range adapters {
		items, err := a.Check(home, opts)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory install --check:", err)
			return 1
		}
		for _, it := range items {
			_, _ = fmt.Fprintf(stdout, "%s: %s: %s\n", a.Name(), it.Name, it.Status)
			if it.Status != install.StatusPresent {
				allPresent = false
			}
		}
	}
	if !allPresent {
		return 1
	}
	return 0
}

func runInstallRemove(adapters []install.Adapter, home string, opts install.Options, stderr io.Writer) int {
	for _, a := range adapters {
		if err := a.Remove(home, opts); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory install --remove:", err)
			return 1
		}
	}
	return 0
}

func runInstallInstall(adapters []install.Adapter, home string, opts install.Options, stderr io.Writer) int {
	for _, a := range adapters {
		if err := a.Install(home, opts); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory install:", err)
			return 1
		}
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
