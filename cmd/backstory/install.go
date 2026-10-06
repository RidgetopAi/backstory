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
or more harnesses under $HOME. With no harness named, acts on every harness
detected under $HOME (filesystem only): claude = ~/.claude or ~/.claude.json,
codex = ~/.codex, hermes = $HERMES_HOME else ~/.hermes, pi = ~/.pi/agent.
Each harness skipped is reported with the path not found. Naming harnesses
explicitly skips detection; agents (the generic AGENTS.md fallback) is only
ever installed by name. pi installs an extension under ~/.pi/agent; hermes
installs a memory-provider plugin and the backstory skill under $HERMES_HOME.

Valid harness names: claude, codex, hermes, pi, agents

  --check      report each item's status (present/absent/outdated/foreign-conflict)
               and whether the daemon socket answers; exits non-zero unless
               every item is present; writes nothing
  --remove     reverse a prior install, leaving foreign entries untouched
  --no-verify  skip running the installed hook to confirm it fires
  --binary P   register P (an absolute path) as the backstory binary hook
               commands and MCP entries invoke; default is this executable

"install bash" is not a harness: it adds (or, with --remove, removes) the
marked block in ~/.bashrc that wires bash's preexec/precmd shell command
capture (task 7fe84ffb) into every interactive shell.

usage: backstory install bash [--check] [--remove]

  --check      report whether the block is present; exit non-zero if absent
  --remove     remove exactly the marked block, leaving the rest of
               ~/.bashrc untouched
`

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
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			// --binary takes a value in the next argument, which is not a harness.
			if (a == "--binary" || a == "-binary") && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		} else {
			harnessArgs = append(harnessArgs, a)
		}
	}

	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	check := fs.Bool("check", false, "report install status per item")
	remove := fs.Bool("remove", false, "remove everything backstory install added")
	noVerify := fs.Bool("no-verify", false, "skip verifying the installed hook actually fires")
	binary := fs.String("binary", "", "path of the backstory binary hooks and MCP entries invoke (default: this executable)")
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, installUsage) }
	if err := fs.Parse(flagArgs); err != nil {
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

	var adapters []install.Adapter
	for _, h := range harnessArgs {
		a, ok := install.AdapterByName(h)
		if !ok {
			_, _ = fmt.Fprintf(stderr, "backstory install: unknown harness %q; valid harnesses: %s\n", h, strings.Join(install.HarnessNames(), ", "))
			return 2
		}
		adapters = append(adapters, a)
	}
	detected := len(harnessArgs) == 0
	if detected {
		var skipped []install.Detection
		for _, d := range install.DetectHarnesses(home) {
			if d.Detected {
				adapters = append(adapters, d.Adapter)
			} else {
				skipped = append(skipped, d)
			}
		}
		for _, d := range skipped {
			_, _ = fmt.Fprintf(stdout, "%s: skipped — %s not found\n", d.Adapter.Name(), d.NotFound())
		}
		if len(adapters) == 0 {
			_, _ = fmt.Fprintf(stderr, "backstory install: no harness detected under %s; nothing written. Name one explicitly; valid harnesses: %s\n", home, strings.Join(install.HarnessNames(), ", "))
			return 1
		}
	}
	opts := install.Options{Verify: !*noVerify, BinaryPath: *binary}
	if sock, err := socketPath(); err == nil {
		opts.SocketPath = sock
	}

	var rc int
	switch {
	case *check:
		rc = runInstallCheck(adapters, home, opts, stdout, stderr)
	case *remove:
		rc = runInstallRemove(adapters, home, opts, stdout, stderr)
	default:
		rc = runInstallInstall(adapters, home, opts, detected, stdout, stderr)
	}
	if detected && rc == 0 && !*check && !*remove {
		_, _ = fmt.Fprintln(stdout, "shell command capture is separate: run `backstory install bash` to add it")
	}
	return rc
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
	// The files can all be present while nothing is listening: dial the
	// daemon too. Informational — the exit code still reflects the items.
	sock, answering := install.DaemonAnswering(opts)
	if answering {
		_, _ = fmt.Fprintf(stdout, "daemon: answering (%s)\n", sock)
	} else {
		_, _ = fmt.Fprintf(stdout, "daemon: not answering (%s)\n", sock)
	}
	if !allPresent {
		return 1
	}
	return 0
}

func runInstallRemove(adapters []install.Adapter, home string, opts install.Options, stdout, stderr io.Writer) int {
	for _, a := range adapters {
		if err := a.Remove(home, opts); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory install --remove:", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "%s: removed\n", a.Name())
	}
	return 0
}

// runInstallInstall installs every adapter. A foreign-conflict in one harness
// leaves that harness exactly as it was (the adapters check before writing)
// and is reported as skipped; every other harness is still installed. Under
// detection (no harness named) a skip is not a failure while something was
// installed; a harness named explicitly that conflicts still exits 1, as does
// any other error.
func runInstallInstall(adapters []install.Adapter, home string, opts install.Options, detected bool, stdout, stderr io.Writer) int {
	var installed, skipped []string
	failed := false
	for _, o := range install.InstallAll(adapters, home, opts) {
		name := o.Adapter.Name()
		switch {
		case o.Err == nil:
			installed = append(installed, name)
			_, _ = fmt.Fprintf(stdout, "%s: installed\n", name)
			if n, ok := o.Adapter.(interface{ InstallNotice() string }); ok {
				_, _ = fmt.Fprintln(stdout, n.InstallNotice())
			}
		case o.Conflict:
			skipped = append(skipped, name)
			if detected {
				_, _ = fmt.Fprintf(stdout, "%s: skipped — %s; left exactly as it was\n", name, o.Err)
			} else {
				_, _ = fmt.Fprintf(stderr, "backstory install: %v\n", o.Err)
				failed = true
			}
		default:
			_, _ = fmt.Fprintf(stderr, "backstory install: %s: %v\n", name, o.Err)
			failed = true
		}
	}
	if len(skipped) > 0 {
		_, _ = fmt.Fprintf(stdout, "installed: %s; skipped: %s\n", listOrNone(installed), strings.Join(skipped, ", "))
	}
	if failed || len(installed) == 0 {
		if !failed {
			_, _ = fmt.Fprintln(stderr, "backstory install: nothing was installed: every harness conflicted with an existing entry")
		}
		return 1
	}
	return 0
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
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
		_, _ = fmt.Fprintf(stdout, "bashrc: shell command capture block added to %s\n", path)
		return 0
	}
}
