package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
	"github.com/RidgetopAi/backstory/internal/week"
)

// runPurge is `backstory purge (--session ID | --project KEY [--since T]
// [--until T] [--location DIR]) [--dry-run] [--yes]` (decision 02c511b3 D4): the human erases
// captured activity by whole session. Like `delete` it opens the store
// directly — PurgeSessions is a human-only power the socket API never
// reaches (AGENT-CONTRACT.md §User-only powers).
func runPurge(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("purge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sessionFlag := fs.String("session", "", "purge this one session id")
	projectFlag := fs.String("project", "", "purge this project's sessions (optionally within --since/--until by session start)")
	sinceFlag := fs.String("since", "", "with --project: sessions started at or after this bound (a duration ago, or RFC3339)")
	untilFlag := fs.String("until", "", "with --project: sessions started before this bound (a duration ago, or RFC3339)")
	locationFlag := fs.String("location", "", "with --project: act only on the sessions and records This Week attributes to this directory's row (a row's cwd)")
	dryRun := fs.Bool("dry-run", false, "print what would be purged and change nothing")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "backstory purge: unexpected argument", fs.Arg(0))
		return 2
	}
	if (*sessionFlag == "") == (*projectFlag == "") {
		_, _ = fmt.Fprintln(stderr, "backstory purge: give exactly one of --session ID or --project KEY")
		return 2
	}
	if *locationFlag != "" && *projectFlag == "" {
		_, _ = fmt.Fprintln(stderr, "backstory purge: --location only applies with --project")
		return 2
	}
	if *sessionFlag != "" && (*sinceFlag != "" || *untilFlag != "") {
		_, _ = fmt.Fprintln(stderr, "backstory purge: --since/--until only apply with --project")
		return 2
	}
	scope := store.PurgeScope{SessionID: *sessionFlag, ProjectKey: *projectFlag}
	now := time.Now()
	for _, b := range []struct {
		name, raw string
		dst       *time.Time
	}{{"since", *sinceFlag, &scope.Since}, {"until", *untilFlag, &scope.Until}} {
		if b.raw == "" {
			continue
		}
		t, err := parseSince(b.raw, now)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "backstory purge: invalid --%s %q: %v\n", b.name, b.raw, err)
			return 2
		}
		*b.dst = t
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory purge:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory purge:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	if *locationFlag != "" {
		ls, err := week.LocationScope(st, project.RealGit{}, resolveWorkspaceDirs(), *locationFlag)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory purge:", err)
			return 1
		}
		scope.Location = &ls
	}

	preview, err := st.PurgePreview(scope)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory purge:", err)
		return 1
	}
	if *dryRun {
		_, _ = fmt.Fprintf(stdout, "would purge %d sessions, %d events, %d records\n", preview.Sessions, preview.Events, preview.Records)
		return 0
	}

	if !*yes {
		if !isTerminal(stdin) {
			_, _ = fmt.Fprintln(stderr, "backstory purge: refusing to purge without --yes on a non-interactive session")
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "purge %d sessions, %d events, %d records? This cannot be undone. [y/N] ", preview.Sessions, preview.Events, preview.Records)
		scanner := bufio.NewScanner(stdin)
		if !scanner.Scan() {
			_, _ = fmt.Fprintln(stdout, "aborted")
			return 1
		}
		if a := strings.TrimSpace(scanner.Text()); a != "y" && a != "Y" {
			_, _ = fmt.Fprintln(stdout, "aborted")
			return 1
		}
	}

	res, err := st.PurgeSessions(scope, humanIdentity)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory purge:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "purged %d sessions, %d events, %d records\n", res.Sessions, res.Events, res.Records)
	return 0
}
