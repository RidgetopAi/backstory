package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/recall"
	"github.com/RidgetopAi/backstory/internal/store"
)

// recallDefaultAltitude is `backstory recall`'s altitude when --altitude is
// not given (PLAN.md §Phase 4 CLI: "default altitude summary").
const recallDefaultAltitude = recall.AltitudeSummary

// runRecall is `backstory recall [query|id] [--project KEY|--here]
// [--altitude headline|summary|full] [--budget N] [--json]`: a read-only
// human-path command that opens the store directly and never dials the
// daemon socket (PLAN.md §Phase 4 CLI, decision d9d456e7's "commands
// reading the store directly").
func runRecall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project key (default: the git repo of the current directory)")
	_ = fs.Bool("here", false, "use the git repo of the current directory (the default; accepted for explicitness)")
	altitudeFlag := fs.String("altitude", string(recallDefaultAltitude), "headline|summary|full")
	budget := fs.Int("budget", block.DefaultBudgetTokens, "token budget for the rendered result")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON instead of rendered text")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	altitude := recall.Altitude(*altitudeFlag)
	switch altitude {
	case recall.AltitudeHeadline, recall.AltitudeSummary, recall.AltitudeFull:
	default:
		_, _ = fmt.Fprintf(stderr, "backstory recall: invalid --altitude %q (want headline, summary, or full)\n", *altitudeFlag)
		return 2
	}

	projectKey, err := resolveHumanProjectKey(*projectFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory recall:", err)
		return 1
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory recall:", err)
		return 1
	}
	st, err := store.Open(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory recall:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	anchor, err := recallAnchor(st, projectKey, fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory recall:", err)
		return 1
	}

	result, err := recall.Build(st, anchor, altitude, *budget)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory recall:", err)
		return 1
	}

	if *jsonOut {
		return printRecallJSON(stdout, projectKey, altitude, result)
	}
	return printRecallText(stdout, projectKey, result)
}

// recallAnchor builds query's Anchor: an id, or a prefix unique to exactly
// one record, anchors on that record's own edge-walked neighbourhood
// (recall.RecordAnchor); anything else, including "", is a project anchor
// (no query) or a free-text anchor scoped to projectKey. A record whose id
// happens to also read like search text still wins the id reading —
// FindRecordByIDPrefix is checked first — so a real match is never
// shadowed by treating it as a query string.
func recallAnchor(st *store.Store, projectKey, query string) (recall.Anchor, error) {
	if query == "" {
		return recall.ProjectAnchor(projectKey), nil
	}
	if _, ok, err := st.FindRecordByIDPrefix(query); err != nil {
		return recall.Anchor{}, fmt.Errorf("resolve %q: %w", query, err)
	} else if ok {
		return recall.RecordAnchor(query), nil
	}
	return recall.TextAnchor(projectKey, query), nil
}

// recallItemJSON is one item in `backstory recall --json`'s items array:
// enough to identify a record (id, kind), weigh how much to trust it
// (tier), and know its provenance (status) — the fields the DONE WHEN
// clause 1 requires, plus the body and status-specific detail recall.Item
// itself carries.
type recallItemJSON struct {
	ID                    string  `json:"id"`
	Kind                  string  `json:"kind"`
	Tier                  string  `json:"tier"`
	Status                string  `json:"status"`
	Text                  string  `json:"text,omitempty"`
	SupersededByID        string  `json:"superseded_by,omitempty"`
	ContradictionEvidence []int64 `json:"contradiction_evidence,omitempty"`
}

// recallOutputJSON is `backstory recall --json`'s top-level shape:
// ProjectKey is always set, even when Items is empty, so an empty result
// still names the project it is honestly empty for (DONE WHEN clause 3).
type recallOutputJSON struct {
	ProjectKey string           `json:"project_key"`
	Altitude   string           `json:"altitude"`
	Items      []recallItemJSON `json:"items"`
}

func printRecallJSON(stdout io.Writer, projectKey string, altitude recall.Altitude, result recall.Result) int {
	out := recallOutputJSON{ProjectKey: projectKey, Altitude: string(altitude), Items: []recallItemJSON{}}
	for _, item := range result.Items {
		out.Items = append(out.Items, recallItemJSON{
			ID:                    item.ID,
			Kind:                  string(item.Kind),
			Tier:                  string(item.Tier),
			Status:                string(item.Status),
			Text:                  item.Text,
			SupersededByID:        item.SupersededByID,
			ContradictionEvidence: item.ContradictionEvidence,
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		_, _ = fmt.Fprintln(stdout, err) // unreachable in practice: recallOutputJSON has no un-marshalable field
		return 1
	}
	_, _ = fmt.Fprintln(stdout, string(b))
	return 0
}

// printRecallText prints result.Rendered, or, when it turned up nothing,
// an honest empty state naming projectKey (DONE WHEN clause 3) — never a
// bare blank line.
func printRecallText(stdout io.Writer, projectKey string, result recall.Result) int {
	if len(result.Items) == 0 {
		_, _ = fmt.Fprintf(stdout, "no records for project %s\n", projectKey)
		return 0
	}
	_, _ = fmt.Fprintln(stdout, result.Rendered)
	return 0
}
