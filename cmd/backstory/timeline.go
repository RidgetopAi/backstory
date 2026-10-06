package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// runTimeline is `backstory timeline [--project KEY|--here] [--since
// <duration|RFC3339>] [--kind K] [--limit N] [--json]`: a read-only
// human-path command, like recall, that opens the store directly and never
// dials the daemon socket (PLAN.md §Phase 4 CLI, decision d9d456e7). Events
// are always ordered by timeline_events.id — sequence, never ts (SCHEMA.md
// invariant 10) — even when --since's bound is itself a ts comparison and a
// backfilled session's clock disagrees with sequence order.
func runTimeline(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("timeline", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project key (default: the git repo of the current directory)")
	_ = fs.Bool("here", false, "use the git repo of the current directory (the default; accepted for explicitness)")
	sinceFlag := fs.String("since", "", "only events at or after this bound: a duration (e.g. 2h30m) ago, or an RFC3339 timestamp")
	kindFlag := fs.String("kind", "", "only events of this kind")
	limit := fs.Int("limit", 0, "keep only the most recent N events, oldest to newest (0 = no limit)")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON instead of rendered text")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var since time.Time
	if *sinceFlag != "" {
		t, err := parseSince(*sinceFlag, time.Now())
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "backstory timeline: invalid --since %q: %v\n", *sinceFlag, err)
			return 2
		}
		since = t
	}

	projectKey, err := resolveHumanProjectKey(*projectFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory timeline:", err)
		return 1
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory timeline:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory timeline:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	events, err := st.EventsForTimeline(projectKey, since, *kindFlag, *limit)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory timeline:", err)
		return 1
	}

	if *jsonOut {
		return printTimelineJSON(stdout, projectKey, events)
	}
	return printTimelineText(stdout, projectKey, events)
}

// parseSince parses --since's bound: a value time.ParseDuration accepts
// (e.g. "2h30m") means "now minus that duration"; anything else is parsed
// as RFC3339.
func parseSince(raw string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		return now.Add(-d), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("want a duration (e.g. 2h30m) or an RFC3339 timestamp")
	}
	return t, nil
}

// timelineEventJSON is one event in `backstory timeline --json`'s events
// array, in sequence order.
type timelineEventJSON struct {
	ID        int64           `json:"id"`
	TS        string          `json:"ts"`
	Kind      string          `json:"kind"`
	SessionID string          `json:"session_id,omitempty"`
	Source    string          `json:"source"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Workspace string          `json:"workspace,omitempty"`
	Window    string          `json:"window,omitempty"`
}

// timelineOutputJSON is `backstory timeline --json`'s top-level shape:
// ProjectKey is always set, even when Events is empty, so an empty result
// still names the project it is honestly empty for (DONE WHEN clause 3).
type timelineOutputJSON struct {
	ProjectKey string              `json:"project_key"`
	Events     []timelineEventJSON `json:"events"`
}

func printTimelineJSON(stdout io.Writer, projectKey string, events []store.TimelineEvent) int {
	out := timelineOutputJSON{ProjectKey: projectKey, Events: []timelineEventJSON{}}
	for _, e := range events {
		out.Events = append(out.Events, timelineEventJSON{
			ID:        e.ID,
			TS:        store.FormatTS(e.TS, time.RFC3339Nano),
			Kind:      e.Kind,
			SessionID: e.SessionID,
			Source:    e.Source,
			Payload:   json.RawMessage(e.Payload),
			Workspace: e.Workspace,
			Window:    e.Window,
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		_, _ = fmt.Fprintln(stdout, err) // unreachable in practice: a stored event's payload is always valid JSON
		return 1
	}
	_, _ = fmt.Fprintln(stdout, string(b))
	return 0
}

// printTimelineText prints one line per event, in sequence order, or, when
// there are none, an honest empty state naming projectKey (DONE WHEN
// clause 3) — never a bare blank line.
func printTimelineText(stdout io.Writer, projectKey string, events []store.TimelineEvent) int {
	if len(events) == 0 {
		_, _ = fmt.Fprintf(stdout, "no timeline events for project %s\n", projectKey)
		return 0
	}
	for _, e := range events {
		session := e.SessionID
		if session == "" {
			session = "-"
		}
		line := fmt.Sprintf("%d · %s · %s · %s", e.ID, store.FormatTS(e.TS, time.RFC3339Nano), e.Kind, session)
		if e.Kind == payload.KindToolUse {
			var tu payload.ToolUse
			if json.Unmarshal([]byte(e.Payload), &tu) == nil && tu.RecordID != "" {
				line += " · record " + tu.RecordID
			}
		}
		_, _ = fmt.Fprintln(stdout, line)
	}
	return 0
}
