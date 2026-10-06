package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/RidgetopAi/backstory/internal/backfill/claude"
	"github.com/RidgetopAi/backstory/internal/backfill/codex"
	"github.com/RidgetopAi/backstory/internal/backfill/hermes"
	"github.com/RidgetopAi/backstory/internal/backfill/pi"
)

// runBackfill dispatches `backstory backfill <target>`. claude, codex,
// hermes and pi are the targets today (PLAN.md §Phase 3, decision 3e14db82);
// opencode/Copilot importers are out of scope for this punch.
func runBackfill(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, `backstory backfill: missing target, e.g. "backstory backfill claude"`)
		return 2
	}
	switch args[0] {
	case "claude":
		return runBackfillClaude(args[1:], stdout, stderr)
	case "codex":
		return runBackfillCodex(args[1:], stdout, stderr)
	case "hermes":
		return runBackfillHermes(args[1:], stdout, stderr)
	case "pi":
		return runBackfillPi(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "backstory backfill: unknown target %q\n", args[0])
		return 2
	}
}

// runBackfillClaude implements `backstory backfill claude [--root DIR]`: it
// imports every new line from the transcript root into the daemon's own
// store and prints Result's one-line summary.
func runBackfillClaude(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backfill claude", flag.ContinueOnError)
	root := fs.String("root", "", "transcript root (default: $BACKSTORY_CLAUDE_ROOT, else ~/.claude/projects)")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill claude:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill claude:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	res, err := claude.Import(st, claude.Options{Root: *root, CaptureOff: captureOff})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill claude:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, res.String())
	return 0
}

// runBackfillCodex implements `backstory backfill codex [--root DIR]`: it
// imports every new line from the rollout root into the daemon's own store
// and prints Result's one-line summary.
func runBackfillCodex(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backfill codex", flag.ContinueOnError)
	root := fs.String("root", "", "rollout root (default: $BACKSTORY_CODEX_ROOT, else ~/.codex/sessions)")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill codex:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill codex:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	res, err := codex.Import(st, codex.Options{Root: *root, CaptureOff: captureOff})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill codex:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, res.String())
	return 0
}

// runBackfillHermes implements `backstory backfill hermes [--path FILE]`: it
// imports every new session/event from Hermes Agent's own state.db into the
// daemon's own store and prints Result's one-line summary.
func runBackfillHermes(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backfill hermes", flag.ContinueOnError)
	path := fs.String("path", "", "state.db path (default: $HERMES_HOME/state.db, else ~/.hermes/state.db)")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill hermes:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill hermes:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	res, err := hermes.Import(st, hermes.Options{Path: *path, CaptureOff: captureOff})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill hermes:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, res.String())
	return 0
}

// runBackfillPi implements `backstory backfill pi [--root DIR]`: it imports
// every transcript under the Pi sessions root into the daemon's own store
// and prints Result's one-line summary.
func runBackfillPi(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backfill pi", flag.ContinueOnError)
	root := fs.String("root", "", "transcript root (default: $BACKSTORY_PI_ROOT, else ~/.pi/agent/sessions)")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill pi:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill pi:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	res, err := pi.Import(st, pi.Options{Root: *root, CaptureOff: captureOff})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill pi:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, res.String())
	return 0
}
