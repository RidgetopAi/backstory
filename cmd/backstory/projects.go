package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/RidgetopAi/backstory/internal/week"
)

// projectsNeverActive is what the last-activity column shows for a project
// with no sessions.
const projectsNeverActive = "never"

// projectsActivityLayout formats the last-activity column.
const projectsActivityLayout = "2006-01-02 15:04"

// projectRow is one project in `backstory projects --json`.
type projectRow struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	LastActivity *string `json:"last_activity"`
	Sessions     int     `json:"sessions"`
	Records      int     `json:"records"`
}

// runProjects is `backstory projects [--json]`: every known project, newest
// activity first, with the exact KEY that purge --project, records, recall
// and timeline take. Read-only.
func runProjects(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print the projects as a JSON array")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "backstory projects: usage: backstory projects [--json]")
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory projects:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory projects:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	summaries, err := st.ProjectSummaries()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory projects:", err)
		return 1
	}
	workspaceDirs := resolveWorkspaceDirs()
	rows := make([]projectRow, 0, len(summaries))
	for _, ps := range summaries {
		label, err := week.DisplayName(st, ps.Key, workspaceDirs)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory projects:", err)
			return 1
		}
		row := projectRow{Key: ps.Key, Label: label, Sessions: ps.Sessions, Records: ps.Records}
		if !ps.LastActivity.IsZero() {
			s := ps.LastActivity.UTC().Format(time.RFC3339)
			row.LastActivity = &s
		}
		rows = append(rows, row)
	}

	if *jsonOut {
		b, err := json.Marshal(rows)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory projects:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(b))
		return 0
	}

	if len(rows) == 0 {
		_, _ = fmt.Fprintln(stdout, "no projects")
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "LABEL\tKEY\tLAST ACTIVITY\tSESSIONS\tRECORDS")
	for i, r := range rows {
		last := projectsNeverActive
		if r.LastActivity != nil {
			last = summaries[i].LastActivity.UTC().Format(projectsActivityLayout)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\n", r.Label, r.Key, last, r.Sessions, r.Records)
	}
	_ = tw.Flush()
	return 0
}
