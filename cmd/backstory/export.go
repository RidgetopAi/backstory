package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/RidgetopAi/backstory/internal/export"
	"github.com/RidgetopAi/backstory/internal/project"
)

// runExport is `backstory export [--project KEY|--here] [--altitude
// headline|summary] [--out <path>|-]`: a read-only human-path command, like
// recall and timeline, that opens the store directly and never dials the
// daemon socket (PLAN.md §Phase 4 CLI, decision d9d456e7). It writes a
// per-project markdown mirror for another tool's memory file to read
// (Claude auto-memory, Hermes MEMORY.md, OpenClaw imports) — decision
// 262cf929: with no --out (or --out -) the mirror goes to stdout only,
// nothing touches disk; --out <path> writes that path atomically. It never
// guesses another tool's own memory path itself.
func runExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project key (default: the git repo of the current directory)")
	_ = fs.Bool("here", false, "use the git repo of the current directory (the default; accepted for explicitness)")
	altitudeFlag := fs.String("altitude", string(export.DefaultAltitude), "headline|summary")
	outFlag := fs.String("out", "-", "write the mirror to this path atomically; \"-\" (the default) prints it to stdout, touching no file")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	altitude := export.Altitude(*altitudeFlag)
	switch altitude {
	case export.AltitudeHeadline, export.AltitudeSummary:
	default:
		_, _ = fmt.Fprintf(stderr, "backstory export: invalid --altitude %q (want headline or summary)\n", *altitudeFlag)
		return 2
	}

	projectKey, err := resolveHumanProjectKey(*projectFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory export:", err)
		return 1
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory export:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory export:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	// Resolved once here, at the process entry, never inside internal/export
	// or internal/recall (task 482b2320, decision f3fa04c7's clause 7).
	workspaces, _ := project.DefaultWorkspaceDirs()

	rendered, err := export.Build(export.Params{Store: st, ProjectKey: projectKey, Altitude: altitude, WorkspaceDirs: workspaces})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory export:", err)
		return 1
	}

	if *outFlag == "" || *outFlag == "-" {
		_, _ = fmt.Fprint(stdout, rendered)
		return 0
	}
	if err := export.WriteAtomic(*outFlag, []byte(rendered)); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory export:", err)
		return 1
	}
	return 0
}
