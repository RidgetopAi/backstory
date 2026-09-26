package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/RidgetopAi/backstory/internal/backfill/claude"
)

// runBackfill dispatches `backstory backfill <target>`. claude is the only
// target today (PLAN.md §Phase 3); Codex/opencode/Hermes/Copilot importers
// are out of scope for this punch.
func runBackfill(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, `backstory backfill: missing target, e.g. "backstory backfill claude"`)
		return 2
	}
	switch args[0] {
	case "claude":
		return runBackfillClaude(args[1:], stdout, stderr)
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

	res, err := claude.Import(st, claude.Options{Root: *root})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backfill claude:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, res.String())
	return 0
}
