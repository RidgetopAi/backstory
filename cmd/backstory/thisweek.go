package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/RidgetopAi/backstory/internal/install"
	"github.com/RidgetopAi/backstory/internal/project"
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
// them, it is never scoped to one project (--here only names the project you are in): This Week
// is a summary across every project with activity in the window
// (decision 9be5c1d5), the Quickshell panel's single data source.
func runThisWeek(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("this-week", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print machine-readable JSON instead of rendered text")
	hereFlag := fs.String("here", "", "with --json: name the project of DIR, or of the focused terminal when `auto`")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory this-week:", err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory this-week:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	// Resolved once here, at the process entry, never inside internal/week
	// (task 482b2320, decision f3fa04c7's clause 7).
	workspaceDirs := resolveWorkspaceDirs()

	result, err := week.Build(week.Params{Store: st, Git: project.RealGit{}, WorkspaceDirs: workspaceDirs, Locations: thisWeekLocations(), Now: thisWeekNow(), ExtraAttention: codexApprovalAttention()})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory this-week:", err)
		return 1
	}

	if *jsonOut {
		var here *hereJSON
		var windows map[string]windowRef
		if *hereFlag != "" {
			r := hereResolver{git: project.RealGit{}, workspaces: workspaceDirs, result: result,
				displayName: func(key, dir string) string {
					if proj, ok, err := st.GetProject(key); err == nil && ok {
						if name, err := week.DisplayName(st, key, workspaceDirs); err == nil && proj.Toplevel != "" {
							return name
						}
					}
					return project.Label(dir, project.RealGit{}, workspaceDirs)
				}}
			here, err = r.resolveHere(*hereFlag)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "backstory this-week:", err)
				return 1
			}
			if *hereFlag == hereAuto {
				windows = r.windowsByProject()
				if windows == nil {
					windows = map[string]windowRef{}
				}
			}
		}
		return printThisWeekJSON(stdout, result, here, windows)
	}
	return printThisWeekText(stdout, result)
}

// attentionItemJSON is one item in `backstory this-week --json`'s
// attention array (PANEL-CONTRACT.md §Attention).
type attentionItemJSON struct {
	Kind       string `json:"kind"`
	ProjectKey string `json:"project_key"`
	Reason     string `json:"reason"`
	// HandoffID is set only on a possibly-stale-handoff item: the id
	// `backstory affirm` takes to dismiss it.
	HandoffID   string   `json:"handoff_id,omitempty"`
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
	HandoffNext      string `json:"handoff_next"`
	LastAgent        string `json:"last_agent"`
	// Agents is every known harness active in the window, newest first;
	// LastAgent is Agents[0].Agent. Never null.
	Agents []agentSummaryJSON `json:"agents"`
	// HandoffAgent is the agent of the session that wrote the handoff.
	HandoffAgent string `json:"handoff_agent"`
	// Window is set (possibly "") only under --here auto.
	Window *string `json:"window,omitempty"`
	// Tmux is set (possibly "") exactly when Window is.
	Tmux *string `json:"tmux,omitempty"`
}

// agentSummaryJSON is one agent's footprint within a row's window.
type agentSummaryJSON struct {
	Agent        string `json:"agent"`
	LastActivity string `json:"last_activity"`
	SessionCount int    `json:"session_count"`
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
	Here         *hereJSON             `json:"here,omitempty"`
}

// windows is nil unless --here auto was given (then every summary carries
// a window, "" when none).
func printThisWeekJSON(stdout io.Writer, result week.Result, here *hereJSON, windows map[string]windowRef) int {
	out := thisWeekOutputJSON{
		Attention:    []attentionItemJSON{},
		WhereLeftOff: []whereLeftOffRowJSON{},
		Week:         []dayProjectStatsJSON{},
		Here:         here,
	}
	summary := func(p week.ProjectSummary) projectSummaryJSON {
		s := projectSummary(p)
		if windows != nil {
			w := windows[p.ProjectKey]
			s.Window = &w.Address
			s.Tmux = &w.Tmux
		}
		return s
	}
	for _, it := range result.Attention {
		out.Attention = append(out.Attention, attentionItemJSON{
			Kind:        string(it.Kind),
			ProjectKey:  it.ProjectKey,
			Reason:      it.Reason,
			HandoffID:   it.HandoffID,
			EvidenceIDs: it.EvidenceIDs,
		})
	}
	for _, row := range result.WhereLeftOff {
		if row.Group == "" {
			p := summary(row.Project)
			out.WhereLeftOff = append(out.WhereLeftOff, whereLeftOffRowJSON{Project: &p})
			continue
		}
		children := make([]projectSummaryJSON, len(row.Children))
		for i, c := range row.Children {
			children[i] = summary(c)
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
	agents := make([]agentSummaryJSON, len(p.Agents))
	for i, a := range p.Agents {
		agents[i] = agentSummaryJSON{Agent: a.Agent, LastActivity: formatTSOrEmpty(a.LastActivity), SessionCount: a.SessionCount}
	}
	return projectSummaryJSON{
		Agents:           agents,
		HandoffAgent:     p.HandoffAgent,
		ProjectKey:       p.ProjectKey,
		DisplayName:      p.DisplayName,
		CWD:              p.CWD,
		LastActivity:     formatTSOrEmpty(p.LastActivity),
		HandoffID:        p.HandoffID,
		HandoffFirstLine: p.HandoffFirstLine,
		HandoffStale:     p.HandoffStale,
		HandoffNext:      p.HandoffNext,
		LastAgent:        p.LastAgent,
	}
}

func formatTSOrEmpty(t time.Time) string {
	if !store.ValidTS(t) {
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

// thisWeekLocations resolves week.LocationRules from the process
// environment at the entry point ($HOME, $TMPDIR, $XDG_RUNTIME_DIR, the OS
// temp dir), so internal/week itself never reads it.
func thisWeekLocations() week.LocationRules {
	home, _ := os.UserHomeDir()
	roots := []string{os.TempDir(), os.Getenv("TMPDIR"), os.Getenv("XDG_RUNTIME_DIR")}
	if home != "" {
		home = filepath.Clean(home)
	}
	return week.DefaultLocationRules(home, roots...)
}

// codexApprovalReason is the Attention line for installed-but-unapproved
// Codex hooks. Backstory never writes Codex's trust entry itself.
const codexApprovalReason = "Codex hooks are not approved, so Codex gets no warm block and nothing is captured: open codex, trust the directory, and choose \"Trust all and continue\""

// codexApprovalAttention is the Attention item for Backstory hooks installed
// in Codex but not yet approved there; nil when approved or not installed.
func codexApprovalAttention() []week.AttentionItem {
	home, err := os.UserHomeDir()
	if err != nil || !install.CodexHooksUnapproved(home, install.Options{}) {
		return nil
	}
	return []week.AttentionItem{{Kind: week.AttentionCodexHooksNotApproved, ProjectKey: "codex", Reason: codexApprovalReason, EvidenceIDs: []string{}}}
}
