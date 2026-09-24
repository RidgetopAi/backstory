package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
	"github.com/RidgetopAi/backstory/internal/week"
)

// thisWeekNow returns the reference instant `backstory this-week` builds
// its window against. A package variable, not a straight time.Now() call,
// so a test can pin it to a fixed instant and get byte-for-byte
// reproducible golden output (recall_test.go and timeline_test.go's own
// goldens sidestep this by never depending on "now" at all; this command
// cannot, since This Week's whole window is relative to it).
var thisWeekNow = time.Now

// runThisWeek is `backstory this-week [--json]`: a read-only, store-wide
// human-path command, like recall and timeline (PLAN.md §Phase 4 CLI,
// decision d9d456e7's "commands reading the store directly") — but unlike
// them, it is never scoped to one project (no --project/--here): This Week
// is a summary across every project with activity in the window
// (decision 9be5c1d5), the Quickshell panel's single data source.
func runThisWeek(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("this-week", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print machine-readable JSON instead of rendered text")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory this-week:", err)
		return 1
	}
	st, err := store.Open(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory this-week:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	result, err := week.Build(week.Params{Store: st, Now: thisWeekNow()})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory this-week:", err)
		return 1
	}

	if *jsonOut {
		return printThisWeekJSON(stdout, result)
	}
	return printThisWeekText(stdout, result)
}

// attentionItemJSON is one item in `backstory this-week --json`'s
// attention array (PANEL-CONTRACT.md §Attention).
type attentionItemJSON struct {
	Kind        string   `json:"kind"`
	ProjectKey  string   `json:"project_key"`
	Reason      string   `json:"reason"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// projectSummaryJSON is one project's Where-you-left-off entry, standalone
// or as a group's child (PANEL-CONTRACT.md §Where you left off).
type projectSummaryJSON struct {
	ProjectKey       string `json:"project_key"`
	DisplayName      string `json:"display_name"`
	CWD              string `json:"cwd"`
	LastActivity     string `json:"last_activity"`
	HandoffID        string `json:"handoff_id,omitempty"`
	HandoffFirstLine string `json:"handoff_first_line,omitempty"`
	HandoffStale     bool   `json:"handoff_stale,omitempty"`
}

// whereLeftOffRowJSON is one row: Project is set for a standalone project
// row (Group == ""); Children is set for a group row (PANEL-CONTRACT.md
// §Where you left off).
type whereLeftOffRowJSON struct {
	Group    string               `json:"group,omitempty"`
	Project  *projectSummaryJSON  `json:"project,omitempty"`
	Children []projectSummaryJSON `json:"children,omitempty"`
}

// dayProjectStatsJSON is one project's activity for one calendar day in
// the window (PANEL-CONTRACT.md §The week).
type dayProjectStatsJSON struct {
	Day            string `json:"day"`
	ProjectKey     string `json:"project_key"`
	DisplayName    string `json:"display_name"`
	Sessions       int    `json:"sessions"`
	FilesTouched   int    `json:"files_touched"`
	RecordsWritten int    `json:"records_written"`
}

// thisWeekOutputJSON is `backstory this-week --json`'s top-level shape —
// the Quickshell panel's single data source, documented field-for-field in
// PANEL-CONTRACT.md. Every array is always present, even when empty
// (never omitted, never null): an empty Attention array is itself the
// signal "nothing to flag" (decision 9be5c1d5).
type thisWeekOutputJSON struct {
	Attention    []attentionItemJSON   `json:"attention"`
	WhereLeftOff []whereLeftOffRowJSON `json:"where_left_off"`
	Week         []dayProjectStatsJSON `json:"week"`
}

func printThisWeekJSON(stdout io.Writer, result week.Result) int {
	out := thisWeekOutputJSON{
		Attention:    []attentionItemJSON{},
		WhereLeftOff: []whereLeftOffRowJSON{},
		Week:         []dayProjectStatsJSON{},
	}
	for _, it := range result.Attention {
		out.Attention = append(out.Attention, attentionItemJSON{
			Kind:        string(it.Kind),
			ProjectKey:  it.ProjectKey,
			Reason:      it.Reason,
			EvidenceIDs: it.EvidenceIDs,
		})
	}
	for _, row := range result.WhereLeftOff {
		if row.Group == "" {
			p := projectSummary(row.Project)
			out.WhereLeftOff = append(out.WhereLeftOff, whereLeftOffRowJSON{Project: &p})
			continue
		}
		children := make([]projectSummaryJSON, len(row.Children))
		for i, c := range row.Children {
			children[i] = projectSummary(c)
		}
		out.WhereLeftOff = append(out.WhereLeftOff, whereLeftOffRowJSON{Group: row.Group, Children: children})
	}
	for _, d := range result.Week {
		out.Week = append(out.Week, dayProjectStatsJSON{
			Day:            d.Day.Format("2006-01-02"),
			ProjectKey:     d.ProjectKey,
			DisplayName:    d.DisplayName,
			Sessions:       d.Sessions,
			FilesTouched:   d.FilesTouched,
			RecordsWritten: d.RecordsWritten,
		})
	}

	b, err := json.Marshal(out)
	if err != nil {
		_, _ = fmt.Fprintln(stdout, err) // unreachable in practice: thisWeekOutputJSON has no un-marshalable field
		return 1
	}
	_, _ = fmt.Fprintln(stdout, string(b))
	return 0
}

func projectSummary(p week.ProjectSummary) projectSummaryJSON {
	return projectSummaryJSON{
		ProjectKey:       p.ProjectKey,
		DisplayName:      p.DisplayName,
		CWD:              p.CWD,
		LastActivity:     formatTSOrEmpty(p.LastActivity),
		HandoffID:        p.HandoffID,
		HandoffFirstLine: p.HandoffFirstLine,
		HandoffStale:     p.HandoffStale,
	}
}

func formatTSOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// printThisWeekText prints result.Rendered, or, when every section came up
// empty, an honest empty state — never a bare blank line.
func printThisWeekText(stdout io.Writer, result week.Result) int {
	if result.Rendered == "" {
		_, _ = fmt.Fprintln(stdout, "backstory: nothing to show for this week.")
		return 0
	}
	_, _ = fmt.Fprintln(stdout, result.Rendered)
	return 0
}
