package week

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// fixtureNow is every test's reference instant, so nothing here depends on
// time.Now(): the window is exactly [fixtureNow - 6d, fixtureNow] UTC.
var fixtureNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
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
	appendEvent(t, st, store.Event{TS: fixtureNow.Add(-1 * 24 * time.Hour), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{"name":"Read","path":"main.go"}`})
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
	appendEvent(t, st, store.Event{TS: fixtureNow.Add(-1 * 24 * time.Hour), Kind: "tool.use", SessionID: sidA, Source: "shell", Payload: `{}`})
	appendEvent(t, st, store.Event{TS: fixtureNow.Add(-2 * 24 * time.Hour), Kind: "tool.use", SessionID: sidB, Source: "shell", Payload: `{}`})

	human := store.Identity{Kind: store.IdentityHuman, Actor: "human"}
	if err := st.SetProjectGroup(human, "widget-suite", "proj-group-a"); err != nil {
		t.Fatalf("SetProjectGroup(a): %v", err)
	}
	if err := st.SetProjectGroup(human, "widget-suite", "proj-group-b"); err != nil {
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

// TestBuildWeekGridExcludesTombstonedRecords: two records written today, one
// tombstoned by the human, count as ONE record written.
func TestBuildWeekGridExcludesTombstonedRecords(t *testing.T) {
	st := openTestStore(t)
	const proj = "proj-week-tomb"
	upsertProject(t, st, proj, "/home/brian/weektomb")
	day := time.Now().UTC().Truncate(24 * time.Hour)
	sid := startSession(t, st, "sess-week-tomb", proj, "/home/brian/weektomb", day)
	var ids []string
	for _, text := range []string{"kept", "deleted"} {
		id, err := st.InsertRecord(store.InsertRecordParams{
			Identity: agentIdentity, Kind: store.KindNote, Text: text, SessionID: sid, ProjectKey: proj,
		})
		if err != nil {
			t.Fatalf("insert note: %v", err)
		}
		ids = append(ids, id)
	}
	if err := st.TombstoneRecord(ids[1], store.Identity{Kind: store.IdentityHuman, Actor: "human"}); err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	res, err := Build(Params{Store: st, Now: time.Now()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var got, seen int
	for _, d := range res.Week {
		if d.ProjectKey == proj {
			got += d.RecordsWritten
			seen++
		}
	}
	if seen == 0 || got != 1 {
		t.Fatalf("RecordsWritten = %d over %d day rows, want 1", got, seen)
	}
}

// --- task ade3a4c3: This Week shows only places where real agent work happened ---

// repoGit is a fake project.Git: root is a git repo (no remote, so its key
// is its toplevel); every other dir is not.
type repoGit struct{ root string }

func (g repoGit) Repo(dir string) (project.Repo, bool) {
	if within(dir, g.root) {
		return project.Repo{CommonDir: filepath.Join(g.root, ".git"), Toplevel: g.root}, true
	}
	return project.Repo{}, false
}

func (g repoGit) State(string) (project.State, bool) { return project.State{}, false }

// desk is the synthetic mirror of the desk store; every path hangs off home.
type desk struct {
	st                                       *store.Store
	home, scratch, outside, workspace        string
	repo, livestream, work, shellOnly, notes string
	tombstoned                               string
	git                                      repoGit
	excludedKeys                             []string
	rules                                    LocationRules
}

func (d *desk) session(t *testing.T, id, agent string, origin store.SessionOrigin, cwd, key string) string {
	t.Helper()
	upsertProject(t, d.st, key, cwd)
	sid, err := d.st.StartSession(store.StartSessionParams{
		ID: id, Agent: agent, CWD: cwd, ProjectKey: key, StartedAt: fixtureNow.Add(-time.Hour), Origin: origin,
	})
	if err != nil {
		t.Fatalf("StartSession(%s): %v", id, err)
	}
	return sid
}

func (d *desk) event(t *testing.T, sid, kind, payload string) {
	t.Helper()
	appendEvent(t, d.st, store.Event{TS: fixtureNow.Add(-30 * time.Minute), Kind: kind, SessionID: sid, Source: "shell", Payload: payload})
}

func (d *desk) record(t *testing.T, sid, key string, kind store.RecordKind, text string) string {
	t.Helper()
	upsertProject(t, d.st, key, strings.TrimPrefix(key, "workspace:"))
	id, err := d.st.InsertRecord(store.InsertRecordParams{
		Identity: agentIdentity, Kind: kind, Text: text, About: []string{"x.go"}, SessionID: sid, ProjectKey: key,
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	return id
}

func newDesk(t *testing.T) *desk {
	t.Helper()
	d := &desk{st: openTestStore(t)}
	d.home = t.TempDir()
	d.scratch = t.TempDir() // a sibling temp dir: outside home, the scratchpad
	d.outside = t.TempDir() // outside home, like /root
	d.workspace = filepath.Join(d.home, "projects")
	d.repo = filepath.Join(d.workspace, "wobble-party")
	d.livestream = filepath.Join(d.home, "livestream")
	d.work = filepath.Join(d.home, "Work")
	d.shellOnly = filepath.Join(d.home, "Shelly")
	d.notes = filepath.Join(d.home, "Notes")
	d.tombstoned = filepath.Join(d.home, "Gone")
	d.git = repoGit{root: d.repo}
	d.rules = DefaultLocationRules(d.home, d.scratch)
	localBin := filepath.Join(d.home, ".local", "bin")

	// Shell-only sessions (agent unknown, command events) in HOME/livestream.
	for i, id := range []string{"live-1", "live-2"} {
		sid := d.session(t, id, "unknown", store.OriginLive, d.livestream, d.livestream)
		d.event(t, sid, "command", `{"command":"ls"}`)
		d.event(t, sid, "session.git_state", `{"could_not_observe":true}`)
		_ = i
	}
	// Unknown git_state-only sessions outside HOME and in HOME/.local/bin.
	for _, dir := range []string{d.outside, localBin} {
		sid := d.session(t, "cli-"+filepath.Base(dir), "unknown", store.OriginLive, dir, dir)
		d.event(t, sid, "session.git_state", `{"could_not_observe":true}`)
	}
	// Unknown shell + backfilled pi start/end-only sessions in HOME itself.
	sid := d.session(t, "home-shell", "unknown", store.OriginLive, d.home, d.home)
	d.event(t, sid, "command", `{"command":"cd livestream"}`)
	sid = d.session(t, "home-pi", "pi", store.OriginBackfilled, d.home, d.home)
	d.event(t, sid, "session.start", `{}`)
	d.event(t, sid, "session.end", `{}`)
	// Shell-only in a non-repo dir under HOME.
	sid = d.session(t, "shelly", "unknown", store.OriginLive, d.shellOnly, d.shellOnly)
	d.event(t, sid, "command", `{"command":"ls"}`)

	// Real agent tool activity in excluded locations: excluded by location
	// rule alone (outside HOME, dot-directory, HOME itself).
	for _, dir := range []string{d.outside, localBin, d.home} {
		sid := d.session(t, "claude-excluded-"+filepath.Base(dir), "claude", store.OriginLive, dir, dir)
		d.event(t, sid, "tool.use", `{"name":"Read","path":"a.txt"}`)
	}
	// An unknown-agent session with a tool event: not a known harness.
	sid = d.session(t, "unknown-tools", "unknown", store.OriginLive, d.livestream, d.livestream)
	d.event(t, sid, "tool.use", `{"name":"Read","path":"a.txt"}`)

	// A Claude session in the fixture repo writing the repo, its own memory
	// and a temp-dir scratchpad; a handoff filed at the workspace makes the
	// workspace an active home (the desk's label path).
	memory := filepath.Join(d.home, ".claude", "projects", "X", "memory", "m.md")
	pad := filepath.Join(d.scratch, "claude-1000", "scratchpad", "s.md")
	sid = d.session(t, "claude-repo", "claude", store.OriginLive, d.repo, d.repo)
	for _, p := range []string{filepath.Join(d.repo, "main.go"), memory, pad} {
		d.event(t, sid, "tool.use", fmt.Sprintf(`{"name":"Write","path":%q}`, p))
	}
	d.record(t, sid, d.repo, store.KindNote, "repo note")
	d.record(t, sid, "workspace:"+d.workspace, store.KindHandoff, "resume wobble-party")

	// (2) A non-repo dir with a Claude tool.use session; a dir with only a
	// non-tombstoned record; a dir whose only record is tombstoned.
	sid = d.session(t, "claude-work", "claude", store.OriginLive, d.work, d.work)
	d.event(t, sid, "tool.use", `{"name":"Read","path":"a.txt"}`)
	d.record(t, "", d.notes, store.KindNote, "kept")
	goneID := d.record(t, "", d.tombstoned, store.KindNote, "removed")
	if err := d.st.TombstoneRecord(goneID, store.Identity{Kind: store.IdentityHuman, Actor: "human"}); err != nil {
		t.Fatalf("TombstoneRecord: %v", err)
	}

	d.excludedKeys = []string{localBin, d.livestream, d.outside, localBin, d.home, d.shellOnly, d.tombstoned}
	return d
}

func (d *desk) build(t *testing.T, rules LocationRules) Result {
	t.Helper()
	res, err := Build(Params{Store: d.st, Git: d.git, WorkspaceDirs: []string{d.workspace}, Locations: rules, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return res
}

func whereKeys(res Result) map[string]bool {
	got := map[string]bool{}
	for _, row := range res.WhereLeftOff {
		if row.Group == "" {
			got[row.Project.ProjectKey] = true
			continue
		}
		for _, c := range row.Children {
			got[c.ProjectKey] = true
		}
	}
	return got
}

// Clause 1+2: only real-work places get a row; nothing excluded leaks into
// where_left_off, week or attention.
func TestBuildDeskShowsOnlyRealWorkPlaces(t *testing.T) {
	d := newDesk(t)
	res := d.build(t, d.rules)

	got := whereKeys(res)
	for _, want := range []string{d.repo, d.work, d.notes} {
		if !got[want] {
			t.Errorf("where_left_off missing %s; got %v", want, got)
		}
	}
	if len(res.WhereLeftOff) != 3 {
		t.Errorf("where_left_off rows = %d, want 3 (repo, Work, Notes): %+v", len(res.WhereLeftOff), res.WhereLeftOff)
	}
	for _, row := range res.WhereLeftOff {
		name := row.Project.DisplayName
		for _, bad := range []string{"memory", "scratchpad", "livestream", "Shelly", "Gone", "bin", filepath.Base(d.home)} {
			if name == bad {
				t.Errorf("excluded location %q has a where_left_off row", name)
			}
		}
	}
	// week and attention name only surviving locations.
	allowed := map[string]bool{d.repo: true, d.work: true, d.notes: true}
	for _, w := range res.Week {
		if !allowed[w.ProjectKey] && !strings.HasPrefix(w.ProjectKey, "workspace:") && w.DisplayName != "projects/wobble-party" {
			t.Errorf("week entry for excluded location: %+v", w)
		}
		for _, bad := range d.excludedKeys {
			if w.ProjectKey == bad {
				t.Errorf("week entry for excluded key %s", bad)
			}
		}
	}
	for _, a := range res.Attention {
		for _, bad := range d.excludedKeys {
			if a.ProjectKey == bad {
				t.Errorf("attention item for excluded key %s: %+v", bad, a)
			}
		}
	}
}

// Clause 2 in isolation: same directory, shell-only vs tool.use vs record.
func TestBuildRealWorkRule(t *testing.T) {
	d := newDesk(t)
	got := whereKeys(d.build(t, d.rules))
	if !got[d.work] {
		t.Errorf("Claude tool.use session in %s must earn a row", d.work)
	}
	if got[d.shellOnly] {
		t.Errorf("shell-only dir %s must not earn a row", d.shellOnly)
	}
	if !got[d.notes] {
		t.Errorf("non-tombstoned record dir %s must earn a row", d.notes)
	}
	if got[d.tombstoned] {
		t.Errorf("tombstoned-only dir %s must not earn a row", d.tombstoned)
	}
}

// Clause 3: the config is the single source of the exclusion decision.
func TestLocationRulesExcluded(t *testing.T) {
	home, tmp := "/home/fx", "/tmp/fx-scratch"
	r := DefaultLocationRules(home, tmp)
	for _, dir := range []string{home, "/root", "/", "/srv/x", tmp, tmp + "/claude/scratchpad", home + "/.claude/x", home + "/.local/bin", home + "/.claude/projects/-a/memory"} {
		if !r.Excluded(dir) {
			t.Errorf("Excluded(%q) = false, want true", dir)
		}
	}
	for _, dir := range []string{home + "/projects/x", home + "/Work", home + "/livestream", home + "/projects/.hidden-in-repo/x"} {
		if r.Excluded(dir) {
			t.Errorf("Excluded(%q) = true, want false", dir)
		}
	}
	if (LocationRules{}).Excluded("/root") {
		t.Errorf("zero LocationRules must exclude nothing")
	}
}

// Clause 4: the filter is read-side only — the store still holds and
// returns every excluded location's sessions, events and records.
func TestExcludedLocationsRemainReadableInStore(t *testing.T) {
	d := newDesk(t)
	before, err := d.st.ActiveProjectKeys(windowStart(fixtureNow))
	if err != nil {
		t.Fatal(err)
	}
	d.build(t, d.rules)
	after, err := d.st.ActiveProjectKeys(windowStart(fixtureNow))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("Build changed active keys: %d -> %d", len(before), len(after))
	}
	have := map[string]bool{}
	for _, k := range after {
		have[k] = true
	}
	for _, k := range d.excludedKeys {
		if !have[k] && k != d.tombstoned {
			t.Errorf("store lost activity for excluded key %s", k)
		}
	}
	if evs, err := d.st.EventsForTimeline(d.livestream, windowStart(fixtureNow), "", 0); err != nil || len(evs) != 5 {
		t.Errorf("timeline events for livestream = %d (err %v), want 5", len(evs), err)
	}
	if recs, err := d.st.RecordsForProjectAll(d.tombstoned, 10); err != nil || len(recs) != 1 {
		t.Errorf("records for tombstoned dir = %d (err %v), want 1", len(recs), err)
	}
}

// --- task 3a5f9a02: per-folder agents[] ---

func startAgentSession(t *testing.T, st *store.Store, id, agent, key, cwd string, startedAt time.Time) string {
	t.Helper()
	upsertProject(t, st, key, cwd)
	sid, err := st.StartSession(store.StartSessionParams{
		ID: id, Agent: agent, CWD: cwd, ProjectKey: key, StartedAt: startedAt, Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession(%s): %v", id, err)
	}
	return sid
}

func rowByKey(t *testing.T, res Result, key string) ProjectSummary {
	t.Helper()
	for _, row := range res.WhereLeftOff {
		if row.Group == "" && row.Project.ProjectKey == key {
			return row.Project
		}
		for _, c := range row.Children {
			if c.ProjectKey == key {
				return c
			}
		}
	}
	t.Fatalf("no where_left_off row for %s: %+v", key, res.WhereLeftOff)
	return ProjectSummary{}
}

// A folder with claude (oldest), pi and hermes (newest) sessions lists them
// newest first with per-agent last_activity and session_count; zero-length
// 'unknown' sessions newer than all of them appear nowhere and never become
// last_agent.
func TestBuildAgentsNewestFirstKnownHarnessesOnly(t *testing.T) {
	st := openTestStore(t)
	const key, cwd = "proj-agents", "/home/brian/proj-agents"
	type sess struct {
		id, agent string
		at        time.Duration
	}
	for _, s := range []sess{
		{"claude-old", "claude", -5 * time.Hour},
		{"claude-mid", "claude", -4 * time.Hour},
		{"pi-1", "pi", -3 * time.Hour},
		{"hermes-1", "hermes", -2 * time.Hour},
	} {
		sid := startAgentSession(t, st, s.id, s.agent, key, cwd, fixtureNow.Add(s.at))
		appendEvent(t, st, store.Event{TS: fixtureNow.Add(s.at + time.Minute), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{"name":"Read","path":"a.txt"}`})
	}
	for i := 0; i < 3; i++ { // zero-length, newer than everything
		startAgentSession(t, st, fmt.Sprintf("unknown-%d", i), "unknown", key, cwd, fixtureNow.Add(-time.Duration(i+1)*time.Minute))
	}

	res, err := Build(Params{Store: st, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	row := rowByKey(t, res, key)
	want := []AgentSummary{
		{Agent: "hermes", LastActivity: fixtureNow.Add(-2*time.Hour + time.Minute), SessionCount: 1},
		{Agent: "pi", LastActivity: fixtureNow.Add(-3*time.Hour + time.Minute), SessionCount: 1},
		{Agent: "claude", LastActivity: fixtureNow.Add(-4*time.Hour + time.Minute), SessionCount: 2},
	}
	if len(row.Agents) != len(want) {
		t.Fatalf("agents = %+v, want %+v", row.Agents, want)
	}
	for i, w := range want {
		g := row.Agents[i]
		if g.Agent != w.Agent || !g.LastActivity.Equal(w.LastActivity) || g.SessionCount != w.SessionCount {
			t.Errorf("agents[%d] = %+v, want %+v", i, g, w)
		}
	}
	if row.LastAgent != "hermes" {
		t.Errorf("LastAgent = %q, want hermes", row.LastAgent)
	}
}

// Tie-break regression: the workspace row's newest session is hermes with a
// pi session 7 minutes older; last_agent follows last_activity.
func TestBuildWorkspaceRowLastAgentMatchesLastActivity(t *testing.T) {
	d := newDesk(t)
	wsKey := "workspace:" + d.workspace
	main := filepath.Join(d.workspace, "vidflow", "main.go")
	newest := fixtureNow.Add(-20 * time.Minute)
	for _, s := range []struct {
		id, agent string
		at        time.Time
	}{
		{"pi-ws", "pi", newest.Add(-7 * time.Minute)},
		{"hermes-ws", "hermes", newest},
	} {
		sid := startAgentSession(t, d.st, s.id, s.agent, wsKey, d.workspace, s.at)
		appendEvent(t, d.st, store.Event{TS: s.at, Kind: "tool.use", SessionID: sid, Source: "shell", Payload: fmt.Sprintf(`{"name":"Write","path":%q}`, main)})
	}
	res := d.build(t, d.rules)
	var row *ProjectSummary
	for _, r := range res.WhereLeftOff {
		if r.Group == "" && strings.HasPrefix(r.Project.ProjectKey, "workspace:") {
			p := r.Project
			row = &p
		}
	}
	if row == nil {
		t.Fatalf("no workspace row: %+v", res.WhereLeftOff)
	}
	if row.LastAgent != "hermes" {
		t.Errorf("LastAgent = %q, want hermes (agents %+v)", row.LastAgent, row.Agents)
	}
	if len(row.Agents) == 0 || !row.Agents[0].LastActivity.Equal(row.LastActivity) {
		t.Errorf("agents[0] = %+v, row last_activity = %v", row.Agents, row.LastActivity)
	}
}

// --- task b104bf31: workspace agents[] counts every known-harness session ---

func wsRows(res Result) []ProjectSummary {
	var out []ProjectSummary
	for _, r := range res.WhereLeftOff {
		if r.Group == "" && strings.HasPrefix(r.Project.ProjectKey, "workspace:") {
			out = append(out, r.Project)
		}
	}
	return out
}

// A pi session with tool work and a newer hermes session with only
// session.start/end, both keyed to the workspace: the row lists both agents
// and last_agent is hermes.
func TestBuildWorkspaceAgentsIncludeNoToolSessions(t *testing.T) {
	d := newDesk(t)
	wsKey := "workspace:" + d.workspace
	at := fixtureNow.Add(-3 * time.Hour)
	pi := startAgentSession(t, d.st, "pi-ws-work", "pi", wsKey, d.workspace, at)
	appendEvent(t, d.st, store.Event{TS: at, Kind: "tool.use", SessionID: pi, Source: "shell", Payload: fmt.Sprintf(`{"name":"Write","path":%q}`, filepath.Join(d.workspace, "notes.md"))})
	hAt := fixtureNow.Add(-10 * time.Minute)
	h := startAgentSession(t, d.st, "hermes-ws-chat", "hermes", wsKey, d.workspace, hAt)
	appendEvent(t, d.st, store.Event{TS: hAt, Kind: "session.start", SessionID: h, Source: "shell", Payload: `{}`})
	appendEvent(t, d.st, store.Event{TS: hAt.Add(time.Minute), Kind: "session.end", SessionID: h, Source: "shell", Payload: `{}`})

	rows := wsRows(d.build(t, d.rules))
	if len(rows) != 1 {
		t.Fatalf("workspace rows = %+v, want 1", rows)
	}
	row := rows[0]
	if len(row.Agents) != 2 || row.Agents[0].Agent != "hermes" || row.Agents[1].Agent != "pi" {
		t.Errorf("agents = %+v, want [hermes pi]", row.Agents)
	}
	if row.LastAgent != "hermes" {
		t.Errorf("LastAgent = %q, want hermes", row.LastAgent)
	}
}

// One rule for both row types: a no-tool hermes session counts once on a
// repo row and once on a workspace row.
func TestBuildHermesNoToolSessionCountSameForRepoAndWorkspaceRows(t *testing.T) {
	d := newDesk(t)
	wsKey := "workspace:" + d.workspace
	pi := startAgentSession(t, d.st, "pi-ws-work", "pi", wsKey, d.workspace, fixtureNow.Add(-3*time.Hour))
	appendEvent(t, d.st, store.Event{TS: fixtureNow.Add(-3 * time.Hour), Kind: "tool.use", SessionID: pi, Source: "shell", Payload: fmt.Sprintf(`{"name":"Write","path":%q}`, filepath.Join(d.workspace, "notes.md"))})
	startAgentSession(t, d.st, "hermes-ws-chat", "hermes", wsKey, d.workspace, fixtureNow.Add(-20*time.Minute))
	startAgentSession(t, d.st, "hermes-repo-chat", "hermes", d.repo, d.repo, fixtureNow.Add(-10*time.Minute))

	res := d.build(t, d.rules)
	count := func(agents []AgentSummary) int {
		for _, a := range agents {
			if a.Agent == "hermes" {
				return a.SessionCount
			}
		}
		return 0
	}
	rows := wsRows(res)
	if len(rows) != 1 {
		t.Fatalf("workspace rows = %+v, want 1", rows)
	}
	repoRow := rowByKey(t, res, d.repo)
	if w, r := count(rows[0].Agents), count(repoRow.Agents); w != 1 || r != 1 {
		t.Errorf("hermes session_count workspace=%d repo=%d, want 1 and 1", w, r)
	}
}

// Row inclusion is unchanged: a label whose only sessions did no real work
// produces no row.
func TestBuildWorkspaceLabelWithOnlyNoWorkSessionsHasNoRow(t *testing.T) {
	d := newDesk(t)
	wsKey := "workspace:" + d.workspace
	cwd := filepath.Join(d.workspace, "idle-folder")
	h := startAgentSession(t, d.st, "hermes-idle", "hermes", wsKey, cwd, fixtureNow.Add(-10*time.Minute))
	appendEvent(t, d.st, store.Event{TS: fixtureNow.Add(-9 * time.Minute), Kind: "session.end", SessionID: h, Source: "shell", Payload: `{}`})

	res := d.build(t, d.rules)
	for _, r := range res.WhereLeftOff {
		if r.Project.DisplayName == "idle-folder" || strings.Contains(r.Project.CWD, "idle-folder") {
			t.Errorf("unexpected row for no-work label: %+v", r.Project)
		}
	}
}

// --- Attention kind (b): only the latest observable git_state counts ---

func uncommittedItems(t *testing.T, st *store.Store) []AttentionItem {
	t.Helper()
	res, err := Build(Params{Store: st, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var out []AttentionItem
	for _, it := range res.Attention {
		if it.Kind == AttentionUncommittedAtSessionEnd {
			out = append(out, it)
		}
	}
	return out
}

func appendGitState(t *testing.T, st *store.Store, sid, payload string) int64 {
	t.Helper()
	appendEvent(t, st, store.Event{TS: fixtureNow.Add(-24 * time.Hour), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{"name":"Read","path":"main.go"}`})
	return appendEvent(t, st, store.Event{TS: fixtureNow.Add(-24 * time.Hour), Kind: "session.git_state", SessionID: sid, Source: "daemon", Payload: payload})
}

func TestUncommittedDedupsToLatestEventPerProject(t *testing.T) {
	st := openTestStore(t)
	upsertProject(t, st, "proj-dedup", "/home/brian/dedup")
	var last int64
	for i, n := range []int{1, 2, 3} {
		sid := startSession(t, st, fmt.Sprintf("sess-dedup-%d", i), "proj-dedup", "/home/brian/dedup", fixtureNow.Add(-2*time.Hour))
		last = appendGitState(t, st, sid, fmt.Sprintf(`{"branch":"main","uncommitted_count":%d}`, n))
	}
	items := uncommittedItems(t, st)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want exactly 1", items)
	}
	if want := strconv.FormatInt(last, 10); len(items[0].EvidenceIDs) != 1 || items[0].EvidenceIDs[0] != want {
		t.Fatalf("EvidenceIDs = %v, want [%s]", items[0].EvidenceIDs, want)
	}
	if !strings.Contains(items[0].Reason, "3 uncommitted") {
		t.Fatalf("Reason = %q, want latest count 3", items[0].Reason)
	}
}

func TestUncommittedClearedByLaterCleanEvent(t *testing.T) {
	st := openTestStore(t)
	upsertProject(t, st, "proj-clean", "/home/brian/clean")
	sid := startSession(t, st, "sess-clean", "proj-clean", "/home/brian/clean", fixtureNow.Add(-2*time.Hour))
	appendGitState(t, st, sid, `{"branch":"main","uncommitted_count":4}`)
	appendGitState(t, st, sid, `{"branch":"main","uncommitted_count":0}`)
	if items := uncommittedItems(t, st); len(items) != 0 {
		t.Fatalf("items = %+v, want none after clean session", items)
	}
}

func TestUncommittedSurvivesLaterCouldNotObserveEvent(t *testing.T) {
	st := openTestStore(t)
	upsertProject(t, st, "proj-cno-later", "/home/brian/cno-later")
	sid := startSession(t, st, "sess-cno-later", "proj-cno-later", "/home/brian/cno-later", fixtureNow.Add(-2*time.Hour))
	dirty := appendGitState(t, st, sid, `{"branch":"main","uncommitted_count":2}`)
	appendGitState(t, st, sid, `{"could_not_observe":true}`)
	items := uncommittedItems(t, st)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want 1", items)
	}
	if want := strconv.FormatInt(dirty, 10); items[0].EvidenceIDs[0] != want {
		t.Fatalf("EvidenceIDs = %v, want [%s]", items[0].EvidenceIDs, want)
	}
}

// TestBuildWeekGridLeavesShellSessionsOut: a project with one agent session
// and three shell sessions (the per-command ones older daemons wrote, with
// their git_state) reports 1 session on its row and no uncommitted flag.
func TestBuildWeekGridLeavesShellSessionsOut(t *testing.T) {
	st := openTestStore(t)
	const proj = "proj-shell-week"
	upsertProject(t, st, proj, "/home/brian/shellweek")
	day := time.Now().UTC().Truncate(24 * time.Hour)
	agent := startSession(t, st, "sess-agent", proj, "/home/brian/shellweek", day)
	appendEvent(t, st, store.Event{TS: day, Kind: "tool.use", SessionID: agent, Source: "posttooluse", Payload: `{"name":"Edit","path":"main.go"}`})
	for i := 0; i < 3; i++ {
		sid, err := st.StartSession(store.StartSessionParams{
			ID: fmt.Sprintf("sess-shell-%d", i), Agent: "shell", CWD: "/home/brian/shellweek", ProjectKey: proj,
			StartedAt: day, Origin: store.OriginLive,
		})
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		appendEvent(t, st, store.Event{TS: day.Add(time.Minute), Kind: "shell.command", SessionID: sid, Source: "shell", Payload: `{"cmd":"ls"}`})
		appendEvent(t, st, store.Event{TS: day.Add(2 * time.Minute), Kind: "session.git_state", SessionID: sid, Source: "daemon", Payload: `{"branch":"main","uncommitted_count":4}`})
	}

	res, err := Build(Params{Store: st, Now: time.Now()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var found *DayProjectStats
	for i := range res.Week {
		if res.Week[i].ProjectKey == proj {
			found = &res.Week[i]
		}
	}
	if found == nil {
		t.Fatalf("no week entry for %s in %+v", proj, res.Week)
	}
	if found.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1 (shell sessions are not counted)", found.Sessions)
	}
	for _, a := range res.Attention {
		if a.ProjectKey == proj && a.Kind == AttentionUncommittedAtSessionEnd {
			t.Errorf("shell session git_state raised an uncommitted flag: %+v", a)
		}
	}
}
