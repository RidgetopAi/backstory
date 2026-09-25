package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// thisWeekFixtureNow is the fixed reference instant every this-week golden
// test builds against (via the thisWeekNow package var override below):
// the window is exactly [2026-01-02T00:00:00Z, 2026-01-08T18:00:00Z], so
// nothing in this file depends on time.Now() — including records.ts, which
// store.InsertRecord always stamps with the real wall clock and has no
// override for, so every record here is inserted with raw SQL instead
// (mustInsertThisWeekRecord), the same technique recall_test.go's
// mustInsertFixtureRecord uses for its own golden determinism.
var thisWeekFixtureNow = time.Date(2026, 1, 8, 18, 0, 0, 0, time.UTC)

// fixtureWorkspaceDir is the fixed (never t.TempDir()-random) workspace dir
// every this-week golden test configures via BACKSTORY_WORKSPACE_DIRS
// (runThisWeekCLI): a literal path, like every other fixture path in this
// file, so the workspace-relative display labels (task 482b2320, decision
// f3fa04c7) it produces are byte-for-byte reproducible in the golden.
const fixtureWorkspaceDir = "/home/brian/projects"

// date builds a fixture timestamp in January 2026 — every call site in
// this file falls within thisWeekFixtureNow's 7-day window, 2026-01-02
// through 2026-01-08.
func date(d, hh, mm int) time.Time {
	return time.Date(2026, time.January, d, hh, mm, 0, 0, time.UTC)
}

// fixtureRecord is the input to mustInsertThisWeekRecord: every records
// column a this-week golden fixture needs to control directly.
type fixtureRecord struct {
	ID, ProjectKey, SessionID string
	TS                        time.Time
	Kind                      store.RecordKind
	Tier                      store.Tier
	Text                      string
	About                     []string
	Evidence                  []int64
	Outcome                   *store.Outcome
	ExpiresAt                 *time.Time
}

func mustInsertThisWeekRecord(t *testing.T, st *store.Store, r fixtureRecord) {
	t.Helper()
	about := r.About
	if about == nil {
		about = []string{}
	}
	aboutJSON, err := json.Marshal(about)
	if err != nil {
		t.Fatalf("marshal about for %s: %v", r.ID, err)
	}
	evidence := r.Evidence
	if evidence == nil {
		evidence = []int64{}
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal evidence for %s: %v", r.ID, err)
	}
	var outcome any
	if r.Outcome != nil {
		outcome = string(*r.Outcome)
	}
	var expiresAt any
	if r.ExpiresAt != nil {
		expiresAt = r.ExpiresAt.UTC().UnixNano()
	}
	_, err = st.DB().Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at, tombstoned_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, NULL)`,
		r.ID, r.TS.UTC().UnixNano(), string(r.Kind), string(r.Tier), r.Text, string(aboutJSON),
		r.SessionID, r.ProjectKey, string(evidenceJSON), outcome, expiresAt)
	if err != nil {
		t.Fatalf("insert fixture record %s: %v", r.ID, err)
	}
}

// buildThisWeekFixtureStore seeds five projects across the fixture window:
//   - acme-week-main: a handoff a later same-project decision shares an
//     about[] path with (possibly-stale-handoff), and a session-end
//     git_state event with uncommitted_count > 0 (uncommitted-at-session-
//     end) — plus the week grid's one project with a real file touched.
//   - acme-week-contradiction: an outcome record a later confirm record
//     contradicts, with evidence (contradiction).
//   - acme-week-claim: a claim whose expires_at is before thisWeekFixtureNow
//     and carries no produced_outcome edge (expired-claim).
//   - acme-week-group-a / acme-week-group-b: both set into project group
//     "widget-suite" (store.SetProjectGroup), no attention of their own —
//     DONE WHEN clause 3's group collapse, exercised end to end through the
//     CLI.
//   - acme-week-omarcade / acme-week-vidflow: two repos under the fixed
//     workspace fixtureWorkspaceDir, each with its own handoff filed under
//     the WORKSPACE's home key (exactly what HOME files a real one under) —
//     task 482b2320's DONE WHEN clause 5: separate where_left_off rows with
//     display_name projects/omarcade and projects/vidflow, each resolving
//     its OWN handoff out of the shared home.
func buildThisWeekFixtureStore(t *testing.T, dataDir string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	// --- acme-week-main: possibly-stale handoff + uncommitted session end.
	const mainProject = "acme-week-main"
	if err := st.UpsertProject(store.Project{Key: mainProject, Toplevel: "/home/brian/acme-week-main", FirstSeen: date(3, 9, 0)}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", mainProject, err)
	}
	sessMain, err := st.StartSession(store.StartSessionParams{
		ID: "sess-main-1", Agent: "claude", CWD: "/home/brian/acme-week-main", ProjectKey: mainProject,
		StartedAt: date(3, 9, 0), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(main): %v", err)
	}
	mustAppendEvent(t, st, store.Event{TS: date(3, 9, 0), Kind: "session.start", SessionID: sessMain, Source: "shell", Payload: `{}`})
	mustAppendEvent(t, st, store.Event{TS: date(3, 9, 5), Kind: "tool.use", SessionID: sessMain, Source: "shell", Payload: `{"name":"Edit","path":"main.go"}`})
	mustAppendEvent(t, st, store.Event{TS: date(3, 9, 6), Kind: "tool.use", SessionID: sessMain, Source: "shell", Payload: `{"name":"Read","path":"README.md"}`})
	mustAppendEvent(t, st, store.Event{TS: date(3, 10, 0), Kind: "session.git_state", SessionID: sessMain, Source: "daemon", Payload: `{"branch":"main","uncommitted_count":2}`})

	mustInsertThisWeekRecord(t, st, fixtureRecord{
		ID: "handoff-0001", ProjectKey: mainProject, SessionID: sessMain, TS: date(3, 9, 10),
		Kind: store.KindHandoff, Tier: store.TierAgentDeclared, Text: "Resume: ship the CLI\nMODE: build",
		About: []string{"main.go"},
	})
	mustInsertThisWeekRecord(t, st, fixtureRecord{
		ID: "decision-0001", ProjectKey: mainProject, SessionID: sessMain, TS: date(3, 9, 20),
		Kind: store.KindDecision, Tier: store.TierAgentDeclared, Text: "Switched to a different approach",
		About: []string{"main.go"},
	})

	// --- acme-week-contradiction: a contradicted outcome.
	const contradictionProject = "acme-week-contradiction"
	if err := st.UpsertProject(store.Project{Key: contradictionProject, Toplevel: "/home/brian/acme-week-contradiction", FirstSeen: date(4, 9, 0)}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", contradictionProject, err)
	}
	sessContradiction, err := st.StartSession(store.StartSessionParams{
		ID: "sess-contradiction-1", Agent: "claude", CWD: "/home/brian/acme-week-contradiction", ProjectKey: contradictionProject,
		StartedAt: date(4, 9, 0), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(contradiction): %v", err)
	}
	toolResultID := mustAppendEvent(t, st, store.Event{TS: date(4, 9, 5), Kind: "tool.result", SessionID: sessContradiction, Source: "shell", Payload: `{"exit":1}`})

	trueOutcome := store.OutcomeTrue
	mustInsertThisWeekRecord(t, st, fixtureRecord{
		ID: "outcome1", ProjectKey: contradictionProject, SessionID: sessContradiction, TS: date(4, 9, 0),
		Kind: store.KindOutcome, Tier: store.TierAgentDeclared, Text: "tests pass", Outcome: &trueOutcome,
	})
	mustInsertThisWeekRecord(t, st, fixtureRecord{
		ID: "confirm1", ProjectKey: contradictionProject, SessionID: sessContradiction, TS: date(4, 9, 10),
		Kind: store.KindConfirm, Tier: store.TierAgentDeclared, Text: "actually the tests failed",
		Evidence: []int64{toolResultID},
	})
	if err := st.LinkEdge("confirm1", "outcome1", store.EdgeContradicts, sessContradiction); err != nil {
		t.Fatalf("LinkEdge contradicts: %v", err)
	}

	// --- acme-week-claim: an expired claim with no linked outcome.
	const claimProject = "acme-week-claim"
	if err := st.UpsertProject(store.Project{Key: claimProject, Toplevel: "/home/brian/acme-week-claim", FirstSeen: date(5, 9, 0)}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", claimProject, err)
	}
	sessClaim, err := st.StartSession(store.StartSessionParams{
		ID: "sess-claim-1", Agent: "claude", CWD: "/home/brian/acme-week-claim", ProjectKey: claimProject,
		StartedAt: date(5, 9, 0), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(claim): %v", err)
	}
	mustAppendEvent(t, st, store.Event{TS: date(5, 9, 0), Kind: "session.start", SessionID: sessClaim, Source: "shell", Payload: `{}`})
	expiresAt := date(6, 0, 0)
	mustInsertThisWeekRecord(t, st, fixtureRecord{
		ID: "claim001", ProjectKey: claimProject, SessionID: sessClaim, TS: date(5, 9, 5),
		Kind: store.KindClaim, Tier: store.TierAgentDeclared, Text: "claiming the migration",
		About: []string{"thing.go"}, ExpiresAt: &expiresAt,
	})

	// --- acme-week-omarcade / acme-week-vidflow: two repos sharing one
	// workspace HOME (task 482b2320, decision f3fa04c7). Both handoffs are
	// filed under the workspace's own key, exactly as internal/mcp's HOME
	// rule would file them for a session started inside either repo.
	const omarcadeProject = "acme-week-omarcade"
	const vidflowProject = "acme-week-vidflow"
	const workspaceHome = "workspace:" + fixtureWorkspaceDir
	if err := st.UpsertProject(store.Project{Key: workspaceHome, Toplevel: fixtureWorkspaceDir, FirstSeen: date(3, 8, 0)}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", workspaceHome, err)
	}
	if err := st.UpsertProject(store.Project{Key: omarcadeProject, Toplevel: fixtureWorkspaceDir + "/omarcade", FirstSeen: date(3, 8, 0)}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", omarcadeProject, err)
	}
	if err := st.UpsertProject(store.Project{Key: vidflowProject, Toplevel: fixtureWorkspaceDir + "/vidflow", FirstSeen: date(3, 8, 0)}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", vidflowProject, err)
	}
	sessOmarcade, err := st.StartSession(store.StartSessionParams{
		ID: "sess-omarcade-1", Agent: "claude", CWD: fixtureWorkspaceDir + "/omarcade", ProjectKey: omarcadeProject,
		StartedAt: date(3, 8, 0), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(omarcade): %v", err)
	}
	mustAppendEvent(t, st, store.Event{TS: date(3, 8, 0), Kind: "session.start", SessionID: sessOmarcade, Source: "shell", Payload: `{}`})
	mustInsertThisWeekRecord(t, st, fixtureRecord{
		ID: "handoff-omarcade", ProjectKey: workspaceHome, SessionID: sessOmarcade, TS: date(3, 8, 5),
		Kind: store.KindHandoff, Tier: store.TierAgentDeclared, Text: "Resume: ship omarcade's feature",
	})

	sessVidflow, err := st.StartSession(store.StartSessionParams{
		ID: "sess-vidflow-1", Agent: "claude", CWD: fixtureWorkspaceDir + "/vidflow", ProjectKey: vidflowProject,
		StartedAt: date(3, 9, 0), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(vidflow): %v", err)
	}
	mustAppendEvent(t, st, store.Event{TS: date(3, 9, 0), Kind: "session.start", SessionID: sessVidflow, Source: "shell", Payload: `{}`})
	mustInsertThisWeekRecord(t, st, fixtureRecord{
		ID: "handoff-vidflow", ProjectKey: workspaceHome, SessionID: sessVidflow, TS: date(3, 9, 5),
		Kind: store.KindHandoff, Tier: store.TierAgentDeclared, Text: "Resume: ship vidflow's feature",
	})

	// --- acme-week-group-a / acme-week-group-b: grouped, nothing to flag.
	const groupAProject = "acme-week-group-a"
	const groupBProject = "acme-week-group-b"
	for i, p := range []struct {
		key       string
		toplevel  string
		sessionID string
		startedAt time.Time
	}{
		{groupAProject, "/home/brian/acme-week-group-a", "sess-group-a-1", date(6, 9, 0)},
		{groupBProject, "/home/brian/acme-week-group-b", "sess-group-b-1", date(7, 9, 0)},
	} {
		if err := st.UpsertProject(store.Project{Key: p.key, Toplevel: p.toplevel, FirstSeen: p.startedAt}); err != nil {
			t.Fatalf("UpsertProject(%s): %v", p.key, err)
		}
		sid, err := st.StartSession(store.StartSessionParams{
			ID: p.sessionID, Agent: "claude", CWD: p.toplevel, ProjectKey: p.key,
			StartedAt: p.startedAt, Origin: store.OriginLive,
		})
		if err != nil {
			t.Fatalf("StartSession(%s): %v", p.key, err)
		}
		mustAppendEvent(t, st, store.Event{TS: p.startedAt, Kind: "session.start", SessionID: sid, Source: "shell", Payload: `{}`})
		_ = i
	}
	human := store.Identity{Kind: store.IdentityHuman, Actor: "human"}
	if err := st.SetProjectGroup(human, "widget-suite", groupAProject); err != nil {
		t.Fatalf("SetProjectGroup(a): %v", err)
	}
	if err := st.SetProjectGroup(human, "widget-suite", groupBProject); err != nil {
		t.Fatalf("SetProjectGroup(b): %v", err)
	}
}

func mustAppendEvent(t *testing.T, st *store.Store, e store.Event) int64 {
	t.Helper()
	id, err := st.AppendEvent(e)
	if err != nil {
		t.Fatalf("AppendEvent(kind=%s): %v", e.Kind, err)
	}
	return id
}

// runThisWeekCLI runs `backstory this-week` in-process against dataDir's
// store, with thisWeekNow pinned to thisWeekFixtureNow for the duration of
// the call, and BACKSTORY_WORKSPACE_DIRS pinned to fixtureWorkspaceDir
// (task 482b2320) so every golden output's workspace-relative display
// labels are independent of the invoking environment's own $HOME.
func runThisWeekCLI(t *testing.T, dataDir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", fixtureWorkspaceDir)
	orig := thisWeekNow
	thisWeekNow = func() time.Time { return thisWeekFixtureNow }
	t.Cleanup(func() { thisWeekNow = orig })

	var outBuf, errBuf bytes.Buffer
	code = run(append([]string{"this-week"}, args...), bytes.NewReader(nil), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

func thisWeekGoldenPath(name string) string {
	return filepath.Join("testdata", "thisweek", name)
}

// compareThisWeekGolden mirrors recall_test.go's compareGolden (same
// RA_UPDATE_GOLDEN=1 regeneration mechanism), against testdata/thisweek
// instead of testdata/recall.
func compareThisWeekGolden(t *testing.T, name, got string) {
	t.Helper()
	path := thisWeekGoldenPath(name)
	if os.Getenv("RA_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // test fixture output, not sensitive
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // path is this test's own fixed testdata path
	if err != nil {
		t.Fatalf("read golden %s: %v (run with RA_UPDATE_GOLDEN=1 to create it)", path, err)
	}
	if got != string(want) {
		t.Fatalf("output does not match golden %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// TestThisWeekGoldenText is DONE WHEN clause 4's text half: `backstory
// this-week` against the fixture store matches the committed golden.
func TestThisWeekGoldenText(t *testing.T) {
	dataDir := t.TempDir()
	buildThisWeekFixtureStore(t, dataDir)
	stdout, stderr, code := runThisWeekCLI(t, dataDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	compareThisWeekGolden(t, "all.golden", stdout)
}

// TestThisWeekGoldenJSON is DONE WHEN clause 4: `backstory this-week
// --json` against the fixture store matches the committed golden, and
// every field PANEL-CONTRACT.md documents is present.
func TestThisWeekGoldenJSON(t *testing.T) {
	dataDir := t.TempDir()
	buildThisWeekFixtureStore(t, dataDir)
	stdout, stderr, code := runThisWeekCLI(t, dataDir, "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	compareThisWeekGolden(t, "all.json.golden", stdout)

	var parsed thisWeekOutputJSON
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("decode --json output: %v", err)
	}
	if len(parsed.Attention) != 4 {
		t.Fatalf("attention items = %d, want 4 (got %+v)", len(parsed.Attention), parsed.Attention)
	}
	if len(parsed.WhereLeftOff) != 6 {
		t.Fatalf("where_left_off rows = %d, want 6 (3 standalone + 1 group + omarcade + vidflow; got %+v)", len(parsed.WhereLeftOff), parsed.WhereLeftOff)
	}
	if len(parsed.Week) != 7 {
		t.Fatalf("week entries = %d, want 7 (got %+v)", len(parsed.Week), parsed.Week)
	}

	// task 482b2320, decision f3fa04c7's DONE WHEN clause 5: omarcade and
	// vidflow each get their OWN where_left_off row, each resolving its OWN
	// handoff out of the shared workspace home, with a workspace-relative
	// display_name.
	names := map[string]projectSummaryJSON{}
	for _, row := range parsed.WhereLeftOff {
		if row.Project != nil {
			names[row.Project.DisplayName] = *row.Project
		}
	}
	omarcade, ok := names["projects/omarcade"]
	if !ok {
		t.Fatalf("no where_left_off row with display_name projects/omarcade; got %+v", parsed.WhereLeftOff)
	}
	if omarcade.HandoffFirstLine != "Resume: ship omarcade's feature" {
		t.Errorf("omarcade row handoff_first_line = %q, want its OWN handoff, not vidflow's", omarcade.HandoffFirstLine)
	}
	vidflow, ok := names["projects/vidflow"]
	if !ok {
		t.Fatalf("no where_left_off row with display_name projects/vidflow; got %+v", parsed.WhereLeftOff)
	}
	if vidflow.HandoffFirstLine != "Resume: ship vidflow's feature" {
		t.Errorf("vidflow row handoff_first_line = %q, want its OWN handoff, not omarcade's", vidflow.HandoffFirstLine)
	}
}

// TestThisWeekEmptyStoreText is DONE WHEN clause 2's CLI half: an empty
// store's text render names no section at all.
func TestThisWeekEmptyStoreText(t *testing.T) {
	dataDir := t.TempDir()
	stdout, stderr, code := runThisWeekCLI(t, dataDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.Contains(stdout, "Attention") {
		t.Fatalf("empty-store text render contains an Attention heading or filler line:\n%s", stdout)
	}
}

// TestThisWeekEmptyStoreJSON is DONE WHEN clause 2's --json half: every
// array is empty, never null or omitted.
func TestThisWeekEmptyStoreJSON(t *testing.T) {
	dataDir := t.TempDir()
	stdout, stderr, code := runThisWeekCLI(t, dataDir, "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	want := `{"attention":[],"where_left_off":[],"week":[]}` + "\n"
	if stdout != want {
		t.Fatalf("empty-store --json output = %q, want %q", stdout, want)
	}
}
