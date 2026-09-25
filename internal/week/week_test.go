package week

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// fixtureNow is every test's reference instant, so nothing here depends on
// time.Now(): the window is exactly [fixtureNow - 6d, fixtureNow] UTC.
var fixtureNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func upsertProject(t *testing.T, st *store.Store, key, toplevel string) {
	t.Helper()
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: toplevel, FirstSeen: fixtureNow}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", key, err)
	}
}

func startSession(t *testing.T, st *store.Store, id, projectKey, cwd string, startedAt time.Time) string {
	t.Helper()
	sid, err := st.StartSession(store.StartSessionParams{
		ID: id, Agent: "claude", CWD: cwd, ProjectKey: projectKey, StartedAt: startedAt, Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(%s): %v", id, err)
	}
	return sid
}

func appendEvent(t *testing.T, st *store.Store, e store.Event) int64 {
	t.Helper()
	id, err := st.AppendEvent(e)
	if err != nil {
		t.Fatalf("AppendEvent(kind=%s): %v", e.Kind, err)
	}
	return id
}

var agentIdentity = store.Identity{Kind: store.IdentityAgent, Actor: "sess-agent"}

// --- Attention kind (a): possibly-stale handoff ---

const staleHandoffProject = "proj-stale-handoff"

func seedStaleHandoffProject(t *testing.T, st *store.Store) {
	t.Helper()
	upsertProject(t, st, staleHandoffProject, "/home/brian/stale-handoff")
	sid := startSession(t, st, "sess-stale", staleHandoffProject, "/home/brian/stale-handoff", fixtureNow.Add(-2*24*time.Hour))
	appendEvent(t, st, store.Event{TS: fixtureNow.Add(-2 * 24 * time.Hour), Kind: "session.start", SessionID: sid, Source: "shell", Payload: `{}`})

	handoffID, err := st.InsertRecord(store.InsertRecordParams{
		Identity: agentIdentity, Kind: store.KindHandoff, Text: "resume here\nMODE: build",
		About: []string{"main.go"}, SessionID: sid, ProjectKey: staleHandoffProject,
	})
	if err != nil {
		t.Fatalf("insert handoff: %v", err)
	}
	// A later decision sharing the handoff's about[] path is
	// store.FreshnessLaterRecord positive evidence (internal/store/freshness.go).
	if _, err := st.InsertRecord(store.InsertRecordParams{
		Identity: agentIdentity, Kind: store.KindDecision, Text: "changed approach",
		About: []string{"main.go"}, SessionID: sid, ProjectKey: staleHandoffProject,
	}); err != nil {
		t.Fatalf("insert later decision: %v", err)
	}
	_ = handoffID
}

// --- Attention kind (b): uncommitted changes at session end ---

const uncommittedProject = "proj-uncommitted"
const couldNotObserveProject = "proj-could-not-observe"

func seedUncommittedProject(t *testing.T, st *store.Store) {
	t.Helper()
	upsertProject(t, st, uncommittedProject, "/home/brian/uncommitted")
	sid := startSession(t, st, "sess-uncommitted", uncommittedProject, "/home/brian/uncommitted", fixtureNow.Add(-1*24*time.Hour))
	count := 3
	appendEvent(t, st, store.Event{
		TS: fixtureNow.Add(-1 * 24 * time.Hour), Kind: "session.git_state", SessionID: sid, Source: "daemon",
		Payload: `{"branch":"main","uncommitted_count":3}`,
	})
	_ = count
}

// seedCouldNotObserveProject records a session-end git-state event that
// COULD NOT observe the repo — DONE WHEN clause 1's second half: this must
// produce NO Attention item at all, never a folded-in "0 uncommitted".
func seedCouldNotObserveProject(t *testing.T, st *store.Store) {
	t.Helper()
	upsertProject(t, st, couldNotObserveProject, "/home/brian/could-not-observe")
	sid := startSession(t, st, "sess-cno", couldNotObserveProject, "/home/brian/could-not-observe", fixtureNow.Add(-1*24*time.Hour))
	appendEvent(t, st, store.Event{
		TS: fixtureNow.Add(-1 * 24 * time.Hour), Kind: "session.git_state", SessionID: sid, Source: "daemon",
		Payload: `{"could_not_observe":true}`,
	})
}

// --- Attention kind (c): flagged contradiction ---

const contradictionProject = "proj-contradiction"

func seedContradictionProject(t *testing.T, st *store.Store) (targetID, sourceID string) {
	t.Helper()
	upsertProject(t, st, contradictionProject, "/home/brian/contradiction")
	sid := startSession(t, st, "sess-contradiction", contradictionProject, "/home/brian/contradiction", fixtureNow.Add(-3*24*time.Hour))
	evID := appendEvent(t, st, store.Event{
		TS: fixtureNow.Add(-3 * 24 * time.Hour), Kind: "tool.result", SessionID: sid, Source: "shell",
		Payload: `{"exit":1}`,
	})

	target, err := st.InsertRecord(store.InsertRecordParams{
		Identity: agentIdentity, Kind: store.KindOutcome, Text: "tests pass",
		SessionID: sid, ProjectKey: contradictionProject,
		Outcome: outcomePtr(store.OutcomeTrue),
	})
	if err != nil {
		t.Fatalf("insert target outcome: %v", err)
	}

	source, err := st.InsertRecordWithEdges(store.InsertRecordParams{
		Identity: agentIdentity, Kind: store.KindConfirm, Text: "actually the tests failed",
		SessionID: sid, ProjectKey: contradictionProject, Evidence: []int64{evID},
	}, []store.EdgeSpec{{OtherID: target, Type: store.EdgeContradicts, DeclaredBy: sid, Field: "record_id"}})
	if err != nil {
		t.Fatalf("insert contradicting record: %v", err)
	}
	return target, source
}

func outcomePtr(o store.Outcome) *store.Outcome { return &o }

// --- Attention kind (d): expired claim, no linked outcome ---

const expiredClaimProject = "proj-expired-claim"

func seedExpiredClaimProject(t *testing.T, st *store.Store) (claimID string) {
	t.Helper()
	upsertProject(t, st, expiredClaimProject, "/home/brian/expired-claim")
	sid := startSession(t, st, "sess-expired-claim", expiredClaimProject, "/home/brian/expired-claim", fixtureNow.Add(-4*24*time.Hour))
	expiresAt := fixtureNow.Add(-1 * time.Hour)
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity: agentIdentity, Kind: store.KindClaim, Text: "claiming path/to/thing",
		About: []string{"thing.go"}, SessionID: sid, ProjectKey: expiredClaimProject, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	return id
}

// --- a workspace identity, which must never appear anywhere in Build's result ---

func seedWorkspaceIdentity(t *testing.T, st *store.Store) {
	t.Helper()
	const wsKey = "workspace:/home/brian/projects"
	upsertProject(t, st, wsKey, "/home/brian/projects")
	startSession(t, st, "sess-workspace", wsKey, "/home/brian/projects", fixtureNow.Add(-1*time.Hour))
}

// buildFullFixtureStore seeds every Attention kind, a workspace identity,
// and returns the store plus the record ids the assertions need.
func buildFullFixtureStore(t *testing.T) (st *store.Store, contradictionTarget, contradictionSource, claimID string) {
	t.Helper()
	st = openTestStore(t)
	seedStaleHandoffProject(t, st)
	seedUncommittedProject(t, st)
	seedCouldNotObserveProject(t, st)
	target, source := seedContradictionProject(t, st)
	claim := seedExpiredClaimProject(t, st)
	seedWorkspaceIdentity(t, st)
	return st, target, source, claim
}

func TestBuildAttentionAllFourKindsWithEvidenceIDs(t *testing.T) {
	st, target, source, claim := buildFullFixtureStore(t)

	res, err := Build(Params{Store: st, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	byKind := map[AttentionKind][]AttentionItem{}
	for _, it := range res.Attention {
		byKind[it.Kind] = append(byKind[it.Kind], it)
	}

	// (a) possibly-stale handoff.
	stale := byKind[AttentionPossiblyStaleHandoff]
	if len(stale) != 1 {
		t.Fatalf("possibly-stale-handoff items = %d, want 1 (got %+v)", len(stale), stale)
	}
	if stale[0].ProjectKey != staleHandoffProject {
		t.Errorf("possibly-stale-handoff project = %q, want %q", stale[0].ProjectKey, staleHandoffProject)
	}
	if len(stale[0].EvidenceIDs) == 0 {
		t.Errorf("possibly-stale-handoff evidence ids empty, want at least one")
	}

	// (b) uncommitted at session end — exactly one, from uncommittedProject,
	// never from couldNotObserveProject.
	uncommitted := byKind[AttentionUncommittedAtSessionEnd]
	if len(uncommitted) != 1 {
		t.Fatalf("uncommitted-at-session-end items = %d, want 1 (got %+v)", len(uncommitted), uncommitted)
	}
	if uncommitted[0].ProjectKey != uncommittedProject {
		t.Errorf("uncommitted-at-session-end project = %q, want %q", uncommitted[0].ProjectKey, uncommittedProject)
	}
	if len(uncommitted[0].EvidenceIDs) != 1 {
		t.Errorf("uncommitted-at-session-end evidence ids = %v, want exactly 1 (the event id)", uncommitted[0].EvidenceIDs)
	}
	for _, it := range uncommitted {
		if it.ProjectKey == couldNotObserveProject {
			t.Fatalf("could-not-observe project produced an uncommitted-at-session-end item: %+v", it)
		}
	}

	// (c) contradiction.
	contradictions := byKind[AttentionContradiction]
	if len(contradictions) != 1 {
		t.Fatalf("contradiction items = %d, want 1 (got %+v)", len(contradictions), contradictions)
	}
	if contradictions[0].ProjectKey != contradictionProject {
		t.Errorf("contradiction project = %q, want %q", contradictions[0].ProjectKey, contradictionProject)
	}
	if len(contradictions[0].EvidenceIDs) == 0 {
		t.Errorf("contradiction evidence ids empty, want at least one")
	}
	if !strings.Contains(contradictions[0].Reason, shortID(target)) || !strings.Contains(contradictions[0].Reason, shortID(source)) {
		t.Errorf("contradiction reason = %q, want it to name both %s and %s", contradictions[0].Reason, target, source)
	}

	// (d) expired claim, no linked outcome.
	expired := byKind[AttentionExpiredClaim]
	if len(expired) != 1 {
		t.Fatalf("expired-claim items = %d, want 1 (got %+v)", len(expired), expired)
	}
	if expired[0].ProjectKey != expiredClaimProject {
		t.Errorf("expired-claim project = %q, want %q", expired[0].ProjectKey, expiredClaimProject)
	}
	if len(expired[0].EvidenceIDs) != 1 || expired[0].EvidenceIDs[0] != claim {
		t.Errorf("expired-claim evidence ids = %v, want [%s]", expired[0].EvidenceIDs, claim)
	}
}

// TestBuildCouldNotObserveProducesNoAttentionItem is DONE WHEN clause 1's
// own second half, isolated: a store with ONLY a could-not-observe
// session-end event produces an entirely empty Attention slice.
func TestBuildCouldNotObserveProducesNoAttentionItem(t *testing.T) {
	st := openTestStore(t)
	seedCouldNotObserveProject(t, st)

	res, err := Build(Params{Store: st, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Attention) != 0 {
		t.Fatalf("Attention = %+v, want empty", res.Attention)
	}
}

// TestBuildWorkspaceIdentityNeverAppears asserts the workspace identity
// seeded into buildFullFixtureStore shows up nowhere: not in Attention, not
// in Where-you-left-off (standalone or as a group child), not in The week.
func TestBuildWorkspaceIdentityNeverAppears(t *testing.T) {
	st, _, _, _ := buildFullFixtureStore(t)

	res, err := Build(Params{Store: st, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	const wsKey = "workspace:/home/brian/projects"
	for _, it := range res.Attention {
		if it.ProjectKey == wsKey {
			t.Fatalf("workspace identity appeared in Attention: %+v", it)
		}
	}
	for _, row := range res.WhereLeftOff {
		if row.Project.ProjectKey == wsKey {
			t.Fatalf("workspace identity appeared as a Where-you-left-off row: %+v", row)
		}
		for _, c := range row.Children {
			if c.ProjectKey == wsKey {
				t.Fatalf("workspace identity appeared as a group child: %+v", c)
			}
		}
	}
	for _, d := range res.Week {
		if d.ProjectKey == wsKey {
			t.Fatalf("workspace identity appeared in The week: %+v", d)
		}
	}
}

// --- DONE WHEN clause 2: a fixture with nothing to flag ---

const quietProject = "proj-quiet"

func seedQuietFixtureStore(t *testing.T) *store.Store {
	t.Helper()
	st := openTestStore(t)
	upsertProject(t, st, quietProject, "/home/brian/quiet")
	sid := startSession(t, st, "sess-quiet", quietProject, "/home/brian/quiet", fixtureNow.Add(-1*24*time.Hour))
	appendEvent(t, st, store.Event{TS: fixtureNow.Add(-1 * 24 * time.Hour), Kind: "session.start", SessionID: sid, Source: "shell", Payload: `{}`})
	if _, err := st.InsertRecord(store.InsertRecordParams{
		Identity: agentIdentity, Kind: store.KindHandoff, Text: "all quiet",
		SessionID: sid, ProjectKey: quietProject,
	}); err != nil {
		t.Fatalf("insert handoff: %v", err)
	}
	return st
}

func TestBuildEmptyAttentionHasNoHeadingOrFillerLine(t *testing.T) {
	st := seedQuietFixtureStore(t)

	res, err := Build(Params{Store: st, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Attention) != 0 {
		t.Fatalf("Attention = %+v, want empty", res.Attention)
	}
	if strings.Contains(res.Rendered, "Attention") {
		t.Fatalf("Rendered text contains an Attention heading or filler line with nothing to flag:\n%s", res.Rendered)
	}
	// Nothing at all may come before the first real section: a bare filler
	// line ("all good") with no heading must fail this too.
	if !strings.HasPrefix(res.Rendered, "Where you left off:") {
		t.Fatalf("Rendered text does not start with the Where you left off heading (filler before it?):\n%s", res.Rendered)
	}
	if b, err := json.Marshal(res.Attention); err != nil || string(b) != "[]" {
		t.Fatalf("Attention marshals as %s (err %v), want []", b, err)
	}
}

// TestFreshnessEvidenceIDsNeverNull: a stale reason with no ids must still
// serialise evidence_ids as [], never null (PANEL-CONTRACT.md).
func TestFreshnessEvidenceIDsNeverNull(t *testing.T) {
	b, err := json.Marshal(freshnessEvidenceIDs(nil))
	if err != nil || string(b) != "[]" {
		t.Fatalf("freshnessEvidenceIDs(nil) marshals as %s (err %v), want []", b, err)
	}
}

// --- DONE WHEN clause 3: two projects in one user group collapse into one row ---

func TestBuildGroupedProjectsCollapseIntoOneRowWithBothAsChildren(t *testing.T) {
	st := openTestStore(t)
	upsertProject(t, st, "proj-group-a", "/home/brian/group-a")
	upsertProject(t, st, "proj-group-b", "/home/brian/group-b")
	sidA := startSession(t, st, "sess-group-a", "proj-group-a", "/home/brian/group-a", fixtureNow.Add(-1*24*time.Hour))
	sidB := startSession(t, st, "sess-group-b", "proj-group-b", "/home/brian/group-b", fixtureNow.Add(-2*24*time.Hour))
	appendEvent(t, st, store.Event{TS: fixtureNow.Add(-1 * 24 * time.Hour), Kind: "session.start", SessionID: sidA, Source: "shell", Payload: `{}`})
	appendEvent(t, st, store.Event{TS: fixtureNow.Add(-2 * 24 * time.Hour), Kind: "session.start", SessionID: sidB, Source: "shell", Payload: `{}`})

	human := store.Identity{Kind: store.IdentityHuman, Actor: "human"}
	if err := st.SetProjectGroup(human, "widget-suite", "proj-group-a", nil); err != nil {
		t.Fatalf("SetProjectGroup(a): %v", err)
	}
	if err := st.SetProjectGroup(human, "widget-suite", "proj-group-b", nil); err != nil {
		t.Fatalf("SetProjectGroup(b): %v", err)
	}

	res, err := Build(Params{Store: st, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var groupRows []WhereLeftOffRow
	for _, row := range res.WhereLeftOff {
		if row.Group != "" {
			groupRows = append(groupRows, row)
		}
	}
	if len(groupRows) != 1 {
		t.Fatalf("group rows = %d, want 1 (got %+v)", len(groupRows), res.WhereLeftOff)
	}
	row := groupRows[0]
	if row.Group != "widget-suite" {
		t.Errorf("group name = %q, want widget-suite", row.Group)
	}
	if len(row.Children) != 2 {
		t.Fatalf("group children = %d, want 2 (got %+v)", len(row.Children), row.Children)
	}
	gotKeys := map[string]bool{row.Children[0].ProjectKey: true, row.Children[1].ProjectKey: true}
	if !gotKeys["proj-group-a"] || !gotKeys["proj-group-b"] {
		t.Errorf("group children = %+v, want proj-group-a and proj-group-b", row.Children)
	}

	// Neither grouped project also appears as its own standalone row.
	for _, r := range res.WhereLeftOff {
		if r.Group == "" && (r.Project.ProjectKey == "proj-group-a" || r.Project.ProjectKey == "proj-group-b") {
			t.Fatalf("grouped project also rendered as a standalone row: %+v", r)
		}
	}
}

// --- The week: per-day, per-project stats ---

func TestBuildWeekGridCountsSessionsFilesAndRecords(t *testing.T) {
	st := openTestStore(t)
	const proj = "proj-week"
	upsertProject(t, st, proj, "/home/brian/week")
	// records.ts is always the real insert-time clock (store.InsertRecordWithEdges
	// has no ts override, unlike Event.TS), so this test anchors its session and
	// events to "today" (real wall-clock) instead of a fixed historical fixtureNow,
	// and captures Now AFTER the record insert so dayStats' now-bound on the
	// window's last day never excludes it.
	day := time.Now().UTC().Truncate(24 * time.Hour)
	sid := startSession(t, st, "sess-week", proj, "/home/brian/week", day)
	appendEvent(t, st, store.Event{TS: day, Kind: "session.start", SessionID: sid, Source: "shell", Payload: `{}`})
	appendEvent(t, st, store.Event{TS: day.Add(time.Minute), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{"name":"Edit","path":"main.go"}`})
	appendEvent(t, st, store.Event{TS: day.Add(2 * time.Minute), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{"name":"Read","path":"README.md"}`})
	if _, err := st.InsertRecord(store.InsertRecordParams{
		Identity: agentIdentity, Kind: store.KindNote, Text: "made progress", SessionID: sid, ProjectKey: proj,
	}); err != nil {
		t.Fatalf("insert note: %v", err)
	}
	now := time.Now()

	res, err := Build(Params{Store: st, Now: now})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var found *DayProjectStats
	for i := range res.Week {
		if res.Week[i].ProjectKey == proj {
			found = &res.Week[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no week entry for %s in %+v", proj, res.Week)
	}
	if found.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", found.Sessions)
	}
	if found.FilesTouched != 1 {
		t.Errorf("FilesTouched = %d, want 1 (Read must not count — payload.IsMutatingFileTool)", found.FilesTouched)
	}
	if found.RecordsWritten != 1 {
		t.Errorf("RecordsWritten = %d, want 1", found.RecordsWritten)
	}
}
