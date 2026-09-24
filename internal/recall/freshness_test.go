package recall

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

const freshnessProjectKey = "proj-fresh"

func newFreshnessFixture(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := newTestStore(t)
	mustUpsertProject(t, st, freshnessProjectKey)
	sessionID := mustStartSession(t, st, freshnessProjectKey)
	return st, sessionID
}

func mustInsertHandoffAbout(t *testing.T, st *store.Store, sessionID string, about []string) store.Record {
	t.Helper()
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: "handoff text",
		About: about, SessionID: sessionID, ProjectKey: freshnessProjectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord handoff: %v", err)
	}
	rec, err := st.GetRecord(id)
	if err != nil {
		t.Fatalf("GetRecord handoff: %v", err)
	}
	return rec
}

func mustInsertNoteAbout(t *testing.T, st *store.Store, sessionID string, about []string) string {
	t.Helper()
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindNote, Text: "a later note",
		About: about, SessionID: sessionID, ProjectKey: freshnessProjectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord note: %v", err)
	}
	return id
}

func mustAppendToolUseEvent(t *testing.T, st *store.Store, sessionID, name, path string) int64 {
	t.Helper()
	b, err := json.Marshal(payload.ToolUse{Name: name, Path: path})
	if err != nil {
		t.Fatalf("marshal tool.use payload: %v", err)
	}
	id, err := st.AppendEvent(store.Event{TS: time.Now(), Kind: payload.KindToolUse, SessionID: sessionID, Source: "shell", Payload: string(b)})
	if err != nil {
		t.Fatalf("AppendEvent tool.use: %v", err)
	}
	return id
}

func mustContradict(t *testing.T, st *store.Store, sessionID, targetID string) string {
	t.Helper()
	evidenceID := mustAppendToolUseEvent(t, st, sessionID, "Bash", "")
	id, err := st.Confirm(store.ConfirmParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Action: store.ConfirmContradict, RecordID: targetID,
		Evidence: []int64{evidenceID}, SessionID: sessionID, ProjectKey: freshnessProjectKey,
	})
	if err != nil {
		t.Fatalf("Confirm contradict: %v", err)
	}
	return id
}

func mustAffirm(t *testing.T, st *store.Store, sessionID, targetID string) {
	t.Helper()
	if _, err := st.Confirm(store.ConfirmParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Action: store.ConfirmAffirm, RecordID: targetID,
		SessionID: sessionID, ProjectKey: freshnessProjectKey,
	}); err != nil {
		t.Fatalf("Confirm affirm: %v", err)
	}
}

// buildHandoffItem runs Build over freshnessProjectKey's whole ledger and
// returns the Item for handoffID.
func buildHandoffItem(t *testing.T, st *store.Store, handoffID string) Item {
	t.Helper()
	result, err := Build(st, ProjectAnchor(freshnessProjectKey), AltitudeFull, testBudget)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, item := range result.Items {
		if item.ID == handoffID {
			return item
		}
	}
	t.Fatalf("Build result missing handoff %s: %+v", handoffID, result.Items)
	return Item{}
}

// --- DONE WHEN clause 1: each reason on its own, affirm clears, re-flag. ---

func TestBuildFlagsHandoffPossiblyStaleReasonContradicted(t *testing.T) {
	st, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoffAbout(t, st, sessionID, []string{"a.go"})
	contradiction := mustContradict(t, st, sessionID, h.ID)

	item := buildHandoffItem(t, st, h.ID)
	if item.Status != StatusPossiblyStale {
		t.Fatalf("Status = %q, want %q", item.Status, StatusPossiblyStale)
	}
	if len(item.StaleReasons) != 1 || item.StaleReasons[0].Kind != store.FreshnessContradicted {
		t.Fatalf("StaleReasons = %+v, want exactly one FreshnessContradicted reason", item.StaleReasons)
	}
	if fmt.Sprint(item.StaleReasons[0].RecordIDs) != fmt.Sprint([]string{contradiction}) {
		t.Errorf("reason evidence ids = %v, want [%s]", item.StaleReasons[0].RecordIDs, contradiction)
	}
}

func TestBuildFlagsHandoffPossiblyStaleReasonLaterRecord(t *testing.T) {
	st, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoffAbout(t, st, sessionID, []string{"a.go"})
	later := mustInsertNoteAbout(t, st, sessionID, []string{"a.go"})

	item := buildHandoffItem(t, st, h.ID)
	if item.Status != StatusPossiblyStale {
		t.Fatalf("Status = %q, want %q", item.Status, StatusPossiblyStale)
	}
	if len(item.StaleReasons) != 1 || item.StaleReasons[0].Kind != store.FreshnessLaterRecord {
		t.Fatalf("StaleReasons = %+v, want exactly one FreshnessLaterRecord reason", item.StaleReasons)
	}
	if fmt.Sprint(item.StaleReasons[0].RecordIDs) != fmt.Sprint([]string{later}) {
		t.Errorf("reason evidence ids = %v, want [%s]", item.StaleReasons[0].RecordIDs, later)
	}
}

func TestBuildFlagsHandoffPossiblyStaleReasonLaterActivity(t *testing.T) {
	st, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoffAbout(t, st, sessionID, []string{"a.go"})
	editID := mustAppendToolUseEvent(t, st, sessionID, "Edit", "a.go")

	item := buildHandoffItem(t, st, h.ID)
	if item.Status != StatusPossiblyStale {
		t.Fatalf("Status = %q, want %q", item.Status, StatusPossiblyStale)
	}
	if len(item.StaleReasons) != 1 || item.StaleReasons[0].Kind != store.FreshnessLaterActivity {
		t.Fatalf("StaleReasons = %+v, want exactly one FreshnessLaterActivity reason", item.StaleReasons)
	}
	if fmt.Sprint(item.StaleReasons[0].EventIDs) != fmt.Sprint([]int64{editID}) {
		t.Errorf("reason evidence ids = %v, want [%d]", item.StaleReasons[0].EventIDs, editID)
	}
}

// TestBuildHandoffFlagClearedByAffirmThenReflaggedByNewEvidence is DONE
// WHEN clause 1's affirm half, proved through Build/Item rather than
// store.HandoffFreshness directly (internal/store/freshness_test.go covers
// the engine itself).
func TestBuildHandoffFlagClearedByAffirmThenReflaggedByNewEvidence(t *testing.T) {
	st, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoffAbout(t, st, sessionID, []string{"a.go"})
	mustContradict(t, st, sessionID, h.ID)

	flagged := buildHandoffItem(t, st, h.ID)
	if flagged.Status != StatusPossiblyStale {
		t.Fatalf("Status (before affirm) = %q, want %q", flagged.Status, StatusPossiblyStale)
	}

	mustAffirm(t, st, sessionID, h.ID)

	cleared := buildHandoffItem(t, st, h.ID)
	if cleared.Status != StatusCurrent {
		t.Fatalf("Status (after affirm) = %q, want %q (the affirm must clear the flag)", cleared.Status, StatusCurrent)
	}

	secondContradiction := mustContradict(t, st, sessionID, h.ID)

	reflagged := buildHandoffItem(t, st, h.ID)
	if reflagged.Status != StatusPossiblyStale {
		t.Fatalf("Status (after re-contradiction) = %q, want %q", reflagged.Status, StatusPossiblyStale)
	}
	if len(reflagged.StaleReasons) != 1 || fmt.Sprint(reflagged.StaleReasons[0].RecordIDs) != fmt.Sprint([]string{secondContradiction}) {
		t.Errorf("StaleReasons after re-contradiction = %+v, want only the post-affirm contradiction [%s]",
			reflagged.StaleReasons, secondContradiction)
	}
}

// --- DONE WHEN clause 2: no later activity at all, and 30+ days old with
// no later activity, are NOT flagged. ---

func TestBuildHandoffNotFlaggedWithNoLaterActivity(t *testing.T) {
	st, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoffAbout(t, st, sessionID, []string{"a.go"})

	item := buildHandoffItem(t, st, h.ID)
	if item.Status != StatusCurrent {
		t.Fatalf("Status = %q, want %q: no later activity exists at all", item.Status, StatusCurrent)
	}
	if len(item.StaleReasons) != 0 {
		t.Errorf("StaleReasons = %+v, want none", item.StaleReasons)
	}
}

func TestBuildHandoffOlderThan30DaysWithNoLaterActivityNotFlagged(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, freshnessProjectKey)
	sessionID := mustStartSession(t, st, freshnessProjectKey)

	old := time.Now().Add(-31 * 24 * time.Hour)
	oldHandoffID := mustInsertHandoffAtTS(t, st, sessionID, freshnessProjectKey, old, []string{"a.go"})

	item := buildHandoffItem(t, st, oldHandoffID)
	if item.Status != StatusCurrent {
		t.Fatalf("Status = %q, want %q: age alone must never flag a handoff", item.Status, StatusCurrent)
	}
}

// mustInsertHandoffAtTS inserts a kind=handoff row directly with a
// caller-chosen ts, bypassing InsertRecord (which always stamps ts =
// time.Now()) — the same technique mustInsertRecordAtTS (recall_test.go)
// uses for kind=note, needed here to build a handoff old enough to test the
// "age alone never flags it" invariant.
func mustInsertHandoffAtTS(t *testing.T, st *store.Store, sessionID, projectKey string, ts time.Time, about []string) string {
	t.Helper()
	aboutJSON, err := json.Marshal(about)
	if err != nil {
		t.Fatalf("marshal about: %v", err)
	}
	var id string
	err = st.DB().QueryRow(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at)
		VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, '[]', NULL, NULL, NULL)
		RETURNING id`,
		ts.UTC().UnixNano(), string(store.KindHandoff), string(store.TierAgentDeclared), "an old handoff", string(aboutJSON), sessionID, projectKey).Scan(&id)
	if err != nil {
		t.Fatalf("insert handoff at ts fixture: %v", err)
	}
	return id
}
