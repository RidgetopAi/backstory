package recall

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

const testBudget = 100000 // generous enough that no test below hits it by accident, unless it means to.

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustUpsertProject(t *testing.T, st *store.Store, key string) {
	t.Helper()
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: key, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}

func mustStartSession(t *testing.T, st *store.Store, projectKey string) string {
	t.Helper()
	pid := 1
	id, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: "/home/fixture/" + projectKey, ProjectKey: projectKey,
		PID: &pid, StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return id
}

// mustInsertRecord inserts a record through the public API (so ts = time.
// Now(), i.e. insertion order and ts agree) and returns its id.
func mustInsertRecord(t *testing.T, st *store.Store, sessionID, projectKey string, kind store.RecordKind, text string) string {
	t.Helper()
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: kind, Text: text,
		SessionID: sessionID, ProjectKey: projectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	return id
}

// mustInsertRecordWithEvidence inserts a record carrying evidence event
// ids — used to build the record on the FROM side of a `contradicts` edge,
// since evidence lives on the record, not the edge.
func mustInsertRecordWithEvidence(t *testing.T, st *store.Store, sessionID, projectKey string, kind store.RecordKind, text string, evidence []int64) string {
	t.Helper()
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: kind, Text: text,
		SessionID: sessionID, ProjectKey: projectKey, Evidence: evidence,
	})
	if err != nil {
		t.Fatalf("InsertRecord with evidence: %v", err)
	}
	return id
}

// mustInsertRecordAtTS inserts a records row directly with a caller-chosen
// ts, bypassing InsertRecord (which always stamps ts = time.Now()) — the
// same technique internal/store/ts_test.go uses, reached here through the
// exported Store.DB() handle since internal/recall is a separate package.
func mustInsertRecordAtTS(t *testing.T, st *store.Store, sessionID, projectKey string, ts time.Time, text string) string {
	t.Helper()
	var id string
	err := st.DB().QueryRow(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at)
		VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, '[]', ?, ?, '[]', NULL, NULL, NULL)
		RETURNING id`,
		ts.UTC().UnixNano(), string(store.KindNote), string(store.TierAgentDeclared), text, sessionID, projectKey).Scan(&id)
	if err != nil {
		t.Fatalf("insert record at ts fixture: %v", err)
	}
	return id
}

func mustLinkEdge(t *testing.T, st *store.Store, fromID, toID string, typ store.EdgeType, declaredBy string) {
	t.Helper()
	if err := st.LinkEdge(fromID, toID, typ, declaredBy); err != nil {
		t.Fatalf("LinkEdge %s->%s (%s): %v", fromID, toID, typ, err)
	}
}

// itemIDs collects Item.ID in order, for compact order assertions.
func itemIDs(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

// --- DONE WHEN clause 1: all three anchor kinds, ids and order. ---

// TestBuildProjectAnchorReturnsWholeLedgerNewestFirst covers the project
// anchor: every record in the project (any kind, not just one), newest
// first by sequence.
func TestBuildProjectAnchorReturnsWholeLedgerNewestFirst(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, "proj-a")
	mustUpsertProject(t, st, "proj-b")
	sessionA := mustStartSession(t, st, "proj-a")
	sessionB := mustStartSession(t, st, "proj-b")

	first := mustInsertRecord(t, st, sessionA, "proj-a", store.KindNote, "first")
	second := mustInsertRecord(t, st, sessionA, "proj-a", store.KindDecision, "second")
	third := mustInsertRecord(t, st, sessionA, "proj-a", store.KindHandoff, "third")
	_ = mustInsertRecord(t, st, sessionB, "proj-b", store.KindNote, "other project, must not appear")

	result, err := Build(st, ProjectAnchor("proj-a"), AltitudeFull, testBudget, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := itemIDs(result.Items)
	want := []string{third, second, first}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("project anchor item ids = %v, want %v (newest first by sequence)", got, want)
	}
}

// TestBuildRecordAnchorWalksEdgesWithinTwoHops covers the record anchor,
// both full id and a unique short prefix: the anchor record plus every
// record reachable within edgeWalkHops (2) of any edge type, either
// direction — records reachable only at hop 3 must not appear.
func TestBuildRecordAnchorWalksEdgesWithinTwoHops(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, "proj-a")
	sessionID := mustStartSession(t, st, "proj-a")

	unrelated := mustInsertRecord(t, st, sessionID, "proj-a", store.KindNote, "unrelated, no edges at all")
	root := mustInsertRecord(t, st, sessionID, "proj-a", store.KindDecision, "the anchor record")
	hop1In := mustInsertRecord(t, st, sessionID, "proj-a", store.KindHandoff, "supersedes the anchor (1 hop, incoming)")
	hop1Out := mustInsertRecord(t, st, sessionID, "proj-a", store.KindNote, "informed by the anchor (1 hop, outgoing)")
	hop2 := mustInsertRecord(t, st, sessionID, "proj-a", store.KindOutcome, "caused by hop1In (2 hops)")
	hop3 := mustInsertRecord(t, st, sessionID, "proj-a", store.KindNote, "3 hops away, must not appear")

	mustLinkEdge(t, st, hop1In, root, store.EdgeSupersedes, sessionID)
	mustLinkEdge(t, st, root, hop1Out, store.EdgeInforms, sessionID)
	mustLinkEdge(t, st, hop1In, hop2, store.EdgeCaused, sessionID)
	mustLinkEdge(t, st, hop2, hop3, store.EdgeInforms, sessionID)

	wantIDs := []string{hop2, hop1Out, hop1In, root} // newest-first among the visited set, by insertion sequence

	t.Run("full id", func(t *testing.T) {
		result, err := Build(st, RecordAnchor(root), AltitudeFull, testBudget, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		got := itemIDs(result.Items)
		if fmt.Sprint(got) != fmt.Sprint(wantIDs) {
			t.Fatalf("record anchor item ids = %v, want %v", got, wantIDs)
		}
		for _, id := range got {
			if id == unrelated || id == hop3 {
				t.Errorf("record anchor result unexpectedly includes %s", id)
			}
		}
	})

	t.Run("unique short prefix", func(t *testing.T) {
		result, err := Build(st, RecordAnchor(root[:8]), AltitudeFull, testBudget, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		got := itemIDs(result.Items)
		if fmt.Sprint(got) != fmt.Sprint(wantIDs) {
			t.Fatalf("record anchor (short prefix) item ids = %v, want %v", got, wantIDs)
		}
	})
}

// TestBuildTextAnchorMatchesScopedToProjectInSequenceOrder covers the
// free-text anchor: an FTS match scoped to the anchor's project (a
// same-text match in a different project must not appear), re-ordered into
// sequence order rather than left in FTS relevance order.
func TestBuildTextAnchorMatchesScopedToProjectInSequenceOrder(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, "proj-a")
	mustUpsertProject(t, st, "proj-b")
	sessionA := mustStartSession(t, st, "proj-a")
	sessionB := mustStartSession(t, st, "proj-b")

	older := mustInsertRecord(t, st, sessionA, "proj-a", store.KindNote, "widget throughput improved")
	newer := mustInsertRecord(t, st, sessionA, "proj-a", store.KindDecision, "widget throughput regression fixed")
	_ = mustInsertRecord(t, st, sessionA, "proj-a", store.KindNote, "unrelated: gadget latency")
	_ = mustInsertRecord(t, st, sessionB, "proj-b", store.KindNote, "widget throughput in proj-b, must not appear")

	result, err := Build(st, TextAnchor("proj-a", "widget"), AltitudeFull, testBudget, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := itemIDs(result.Items)
	want := []string{newer, older}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("text anchor item ids = %v, want %v (newest first by sequence)", got, want)
	}
}

// --- DONE WHEN clause 2: altitude/budget on a 200+ record fixture. ---

// TestBuildAltitudesFitBudgetOnLargeFixture seeds 200 records and checks,
// for each altitude, that Rendered's own token estimate never exceeds the
// budget passed in, and that headline renders exactly one line per item.
func TestBuildAltitudesFitBudgetOnLargeFixture(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, "proj-a")
	sessionID := mustStartSession(t, st, "proj-a")

	// 200 records comfortably exceeds MaxRecordsPerSessionPerMinute, so this
	// fixture bypasses InsertRecord's rate cap the same way
	// mustInsertRecordAtTS does elsewhere in this file — the cap is a write
	// concern, orthogonal to what this test is proving about recall's own
	// altitude/budget rendering.
	const n = 200
	base := time.Now()
	for i := 0; i < n; i++ {
		mustInsertRecordAtTS(t, st, sessionID, "proj-a", base.Add(time.Duration(i)*time.Second),
			fmt.Sprintf("record body %d: some reasonably long descriptive text about what happened, for altitude rendering purposes", i))
	}

	cases := []struct {
		altitude Altitude
		budget   int
	}{
		{AltitudeHeadline, 500},
		{AltitudeSummary, 2000},
		{AltitudeFull, 4000},
	}
	for _, tc := range cases {
		t.Run(string(tc.altitude), func(t *testing.T) {
			result, err := Build(st, ProjectAnchor("proj-a"), tc.altitude, tc.budget, nil)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if len(result.Items) == 0 {
				t.Fatal("Build returned zero items on a 200-record fixture; strengthen the test or the budget")
			}
			if got := block.EstimateTokens(result.Rendered); got > tc.budget {
				t.Errorf("EstimateTokens(Rendered) = %d, want <= %d", got, tc.budget)
			}
			if tc.altitude == AltitudeHeadline {
				lines := strings.Split(result.Rendered, "\n")
				if len(lines) != len(result.Items) {
					t.Errorf("headline rendered %d lines for %d items, want exactly one line per item:\n%s",
						len(lines), len(result.Items), result.Rendered)
				}
			}
		})
	}
}

// --- DONE WHEN clause 3: superseded / tombstoned / contradicted. ---

func TestBuildMarksSupersededStatusWithSupersedingID(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, "proj-a")
	sessionID := mustStartSession(t, st, "proj-a")

	old := mustInsertRecord(t, st, sessionID, "proj-a", store.KindHandoff, "the old handoff")
	newRec := mustInsertRecord(t, st, sessionID, "proj-a", store.KindHandoff, "the new handoff")
	mustLinkEdge(t, st, newRec, old, store.EdgeSupersedes, sessionID)

	result, err := Build(st, ProjectAnchor("proj-a"), AltitudeFull, testBudget, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var found bool
	for _, item := range result.Items {
		if item.ID != old {
			continue
		}
		found = true
		if item.Status != StatusSuperseded {
			t.Errorf("superseded record's Status = %q, want %q", item.Status, StatusSuperseded)
		}
		if item.SupersededByID != newRec {
			t.Errorf("SupersededByID = %q, want %q", item.SupersededByID, newRec)
		}
	}
	if !found {
		t.Fatalf("Build result missing the superseded record %s: %+v", old, result.Items)
	}
}

func TestBuildMarksTombstonedStatusOmitsText(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, "proj-a")
	sessionID := mustStartSession(t, st, "proj-a")

	id := mustInsertRecord(t, st, sessionID, "proj-a", store.KindNote, "secret-ish text that must be omitted once tombstoned")
	if err := st.TombstoneRecord(id, store.Identity{Kind: store.IdentityHuman}); err != nil {
		t.Fatalf("TombstoneRecord: %v", err)
	}

	result, err := Build(st, ProjectAnchor("proj-a"), AltitudeFull, testBudget, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var found bool
	for _, item := range result.Items {
		if item.ID != id {
			continue
		}
		found = true
		if item.Status != StatusTombstoned {
			t.Errorf("tombstoned record's Status = %q, want %q", item.Status, StatusTombstoned)
		}
		if item.Text != "" {
			t.Errorf("tombstoned record's Text = %q, want empty", item.Text)
		}
	}
	if !found {
		t.Fatalf("Build result missing the tombstoned record %s (invariant 1: it must still surface, just with no text): %+v", id, result.Items)
	}
}

// TestBuildMarksTombstonedStatusKeepsEdges covers the other half of
// SCHEMA.md invariant 1: a tombstoned record omits its text (covered by
// TestBuildMarksTombstonedStatusOmitsText above) but keeps its edges.
func TestBuildMarksTombstonedStatusKeepsEdges(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, "proj-a")
	sessionID := mustStartSession(t, st, "proj-a")

	other := mustInsertRecord(t, st, sessionID, "proj-a", store.KindNote, "record linked to the one that will be tombstoned")
	id := mustInsertRecord(t, st, sessionID, "proj-a", store.KindNote, "text that will be tombstoned")
	mustLinkEdge(t, st, id, other, store.EdgeInforms, sessionID)

	if err := st.TombstoneRecord(id, store.Identity{Kind: store.IdentityHuman}); err != nil {
		t.Fatalf("TombstoneRecord: %v", err)
	}

	result, err := Build(st, ProjectAnchor("proj-a"), AltitudeFull, testBudget, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var found bool
	for _, item := range result.Items {
		if item.ID != id {
			continue
		}
		found = true
		if item.Status != StatusTombstoned {
			t.Fatalf("tombstoned record's Status = %q, want %q", item.Status, StatusTombstoned)
		}
		if len(item.Edges) != 1 {
			t.Fatalf("tombstoned record's Edges = %+v, want exactly one edge (the informs edge to %s); SCHEMA.md invariant 1: tombstone omits text, keeps edges", item.Edges, other)
		}
		e := item.Edges[0]
		if e.Type != store.EdgeInforms || e.FromID != id || e.ToID != other {
			t.Errorf("tombstoned record's edge = %+v, want informs %s -> %s", e, id, other)
		}
	}
	if !found {
		t.Fatalf("Build result missing the tombstoned record %s: %+v", id, result.Items)
	}
}

func TestBuildMarksContradictedStatusWithEvidenceIDs(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, "proj-a")
	sessionID := mustStartSession(t, st, "proj-a")

	claim := mustInsertRecord(t, st, sessionID, "proj-a", store.KindNote, "claim: the migration is done")
	evidence := []int64{101, 102}
	contradiction := mustInsertRecordWithEvidence(t, st, sessionID, "proj-a", store.KindNote,
		"actually the migration failed", evidence)
	mustLinkEdge(t, st, contradiction, claim, store.EdgeContradicts, sessionID)

	result, err := Build(st, ProjectAnchor("proj-a"), AltitudeFull, testBudget, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var found bool
	for _, item := range result.Items {
		if item.ID != claim {
			continue
		}
		found = true
		if item.Status != StatusContradicted {
			t.Errorf("contradicted record's Status = %q, want %q", item.Status, StatusContradicted)
		}
		if fmt.Sprint(item.ContradictionEvidence) != fmt.Sprint(evidence) {
			t.Errorf("ContradictionEvidence = %v, want %v", item.ContradictionEvidence, evidence)
		}
	}
	if !found {
		t.Fatalf("Build result missing the contradicted record %s: %+v", claim, result.Items)
	}
}

// --- DONE WHEN clause 4: sequence order survives disagreeing ts. ---

// TestBuildOrdersBySequenceDespiteDisagreeingTS inserts the record with the
// LATER ts first (so insertion sequence and ts order are opposites) and
// checks Build's project anchor returns items in sequence order (newest
// inserted first), not ts order.
func TestBuildOrdersBySequenceDespiteDisagreeingTS(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, "proj-a")
	sessionID := mustStartSession(t, st, "proj-a")

	later := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// insertedFirst carries the LATER ts; insertedSecond carries the
	// EARLIER ts. Sequence order (rowid) says insertedSecond is newest;
	// ts order would say insertedFirst is newest. They disagree on
	// purpose.
	insertedFirst := mustInsertRecordAtTS(t, st, sessionID, "proj-a", later, "inserted first, but carries a later ts")
	insertedSecond := mustInsertRecordAtTS(t, st, sessionID, "proj-a", earlier, "inserted second, but carries an earlier ts")

	result, err := Build(st, ProjectAnchor("proj-a"), AltitudeFull, testBudget, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := itemIDs(result.Items)
	want := []string{insertedSecond, insertedFirst} // sequence order: most recently inserted first
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("item order = %v, want %v (sequence order, despite ts disagreeing with it)", got, want)
	}
}

// --- Critic mutation probes (DONE WHEN clause 5) ---
//
// Mutation probe 1 — drop the superseded annotation: in annotate()
// (recall.go), remove the `for _, e := range edges { if e.Type ==
// store.EdgeSupersedes ... }` block (or just its `item.Status =
// StatusSuperseded` line) -> RED
// (TestBuildMarksSupersededStatusWithSupersedingID: "superseded record's
// Status = \"current\", want \"superseded\""); restore it -> GREEN.
//
// Mutation probe 2 — order items by ts instead of sequence: in
// RecordsForProjectAll (internal/store/records.go), change `ORDER BY rowid
// DESC` to `ORDER BY ts DESC` -> RED
// (TestBuildOrdersBySequenceDespiteDisagreeingTS: item order flips to the
// ts-based order the fixture was built to disagree with); restore `ORDER
// BY rowid DESC` -> GREEN.
//
// Mutation probe 3 — ignore the budget: in fit() (recall.go), delete the
// `if block.EstimateTokens(candidate) > budgetTokens { break }` guard (or
// hardcode it to `false`) -> RED
// (TestBuildAltitudesFitBudgetOnLargeFixture: EstimateTokens(Rendered)
// exceeds the 500/2000/4000-token budgets on the 200-record fixture);
// restore the guard -> GREEN.
//
// Mutation probe 4 — drop a tombstoned record's edges: in annotate()
// (recall.go), add `item.Edges = nil` (or similar) inside the `if
// rec.TombstonedAt != nil` branch -> RED
// (TestBuildMarksTombstonedStatusKeepsEdges: "tombstoned record's Edges =
// [], want exactly one edge"); remove it -> GREEN.
