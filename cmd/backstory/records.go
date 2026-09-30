package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/recall"
	"github.com/RidgetopAi/backstory/internal/store"
	"github.com/RidgetopAi/backstory/internal/week"
)

// recordsScanLimit bounds how many of a project's newest records `backstory
// records` reads: the human path's window onto the ledger, named rather
// than inlined.
const recordsScanLimit = 10000

// statusDeleted is the human-facing name of recall's tombstoned status.
const statusDeleted = "deleted"

// runRecords is `backstory records [--project KEY|--here] [--location DIR] [--kind K]
// [--history] [--json]`: a read-only human-path command that lists what
// Backstory saved for a project, newest first, opening the store directly
// (decision d9d456e7). Each record's status comes from recall.Annotate, the
// recall engine's own status path — there is no second implementation.
func runRecords(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("records", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project key (default: the git repo of the current directory)")
	_ = fs.Bool("here", false, "use the git repo of the current directory (the default; accepted for explicitness)")
	locationFlag := fs.String("location", "", "only the records This Week attributes to this directory's row (a row's cwd)")
	kindFlag := fs.String("kind", "", "only records of this kind")
	history := fs.Bool("history", false, "also list superseded and deleted records")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON instead of rendered text")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if locationGiven(fs) && *locationFlag == "" {
		_, _ = fmt.Fprintln(stderr, "backstory records: --location needs a directory")
		return 2
	}

	var projectKey string
	if *locationFlag != "" && *projectFlag == "" {
		projectKey = project.Key(*locationFlag, project.RealGit{}, resolveWorkspaceDirs())
	} else {
		var err error
		if projectKey, err = resolveHumanProjectKey(*projectFlag); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory records:", err)
			return 1
		}
	}
	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory records:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory records:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	if _, ok, err := st.GetProject(projectKey); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory records:", err)
		return 1
	} else if !ok && *locationFlag == "" {
		_, _ = fmt.Fprintf(stderr, "backstory records: unknown project %q\n", projectKey)
		return 1
	}

	var recs []store.Record
	if *locationFlag != "" {
		var ls store.LocationScope
		if ls, err = week.LocationScope(st, project.RealGit{}, resolveWorkspaceDirs(), *locationFlag); err == nil {
			recs, err = st.RecordsForLocation(ls, recordsScanLimit)
		}
	} else {
		recs, err = st.RecordsForProjectAll(projectKey, recordsScanLimit)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory records:", err)
		return 1
	}
	workspaces, _ := project.DefaultWorkspaceDirs()

	rows := []recordRowJSON{}
	for _, rec := range recs {
		if *kindFlag != "" && string(rec.Kind) != *kindFlag {
			continue
		}
		item, err := recall.Annotate(st, rec, workspaces)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory records:", err)
			return 1
		}
		status := string(item.Status)
		if item.Status == recall.StatusTombstoned {
			status = statusDeleted
		}
		if !*history && (item.Status == recall.StatusSuperseded || item.Status == recall.StatusTombstoned) {
			continue
		}
		rows = append(rows, recordRowJSON{
			ID:           rec.ID,
			TS:           rec.TS.UTC().Format(time.RFC3339),
			Kind:         string(rec.Kind),
			Tier:         string(rec.Tier),
			Status:       status,
			SupersededBy: item.SupersededByID,
			Text:         item.Text,
			ts:           rec.TS,
		})
	}

	if *jsonOut {
		b, err := json.Marshal(recordsOutputJSON{ProjectKey: projectKey, Records: rows})
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory records:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintf(stdout, "no records for project %s\n", projectKey)
		return 0
	}
	now := time.Now()
	for _, r := range rows {
		_, _ = fmt.Fprintf(stdout, "%s  %s  %s  %s  %s  %s\n",
			r.ID[:min(8, len(r.ID))], recordAge(now.Sub(r.ts)), r.Kind, r.Tier, r.Status, firstLineOf(r.Text))
	}
	return 0
}

// recordRowJSON is one record in `backstory records --json`'s records array.
type recordRowJSON struct {
	ID           string `json:"id"`
	TS           string `json:"ts"`
	Kind         string `json:"kind"`
	Tier         string `json:"tier"`
	Status       string `json:"status"`
	SupersededBy string `json:"superseded_by,omitempty"`
	Text         string `json:"text"`

	ts time.Time
}

// recordsOutputJSON is `backstory records --json`'s top-level shape:
// ProjectKey is always set, even when Records is empty.
type recordsOutputJSON struct {
	ProjectKey string          `json:"project_key"`
	Records    []recordRowJSON `json:"records"`
}

// recordAge renders d as a compact age: "45s", "12m", "3h", "5d".
func recordAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func firstLineOf(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}
