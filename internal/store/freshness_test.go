package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/google/uuid"
)

const freshnessProjectKey = "proj-a"

// mustInsertHandoff inserts a kind=handoff record, optionally with about[]
// paths, and returns the stored row read back via GetRecord (so EventCursor
// and every other derived column are populated the way callers see them).
func mustInsertHandoff(t *testing.T, s *Store, sessionID string, about []string) Record {
	t.Helper()
	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "handoff text",
		About: about, SessionID: sessionID, ProjectKey: freshnessProjectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord handoff: %v", err)
	}
	rec, err := s.GetRecord(id)
	if err != nil {
		t.Fatalf("GetRecord handoff: %v", err)
	}
	return rec
}

// mustInsertHandoffAtTS inserts a kind=handoff row directly with a
// caller-chosen ts, bypassing InsertRecord (which always stamps ts =
// time.Now()) — the same technique ts_test.go's insertRecordFixture uses,
// needed here to build a handoff old enough to test the "age alone never
// flags it" invariant.
func mustInsertHandoffAtTS(t *testing.T, s *Store, sessionID string, ts time.Time, about []string) string {
	t.Helper()
	id := uuid.NewString()
	aboutJSON, err := marshalStrings(about)
	if err != nil {
		t.Fatalf("marshal about: %v", err)
	}
	_, err = s.db.Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '[]', NULL, NULL, NULL)`,
		id, tsToNanos(ts), string(KindHandoff), string(TierAgentDeclared), "an old handoff", aboutJSON,
		sessionID, freshnessProjectKey)
	if err != nil {
		t.Fatalf("insert handoff fixture: %v", err)
	}
	return id
}

func mustInsertNoteAbout(t *testing.T, s *Store, sessionID string, about []string) string {
	t.Helper()
	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: "a later note",
		About: about, SessionID: sessionID, ProjectKey: freshnessProjectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord note: %v", err)
	}
	return id
}

func mustAppendToolUseEvent(t *testing.T, s *Store, sessionID, name, path string) int64 {
	t.Helper()
	b, err := json.Marshal(payload.ToolUse{Name: name, Path: path})
	if err != nil {
		t.Fatalf("marshal tool.use payload: %v", err)
	}
	id, err := s.AppendEvent(Event{TS: time.Now(), Kind: payload.KindToolUse, SessionID: sessionID, Source: "shell", Payload: string(b)})
	if err != nil {
		t.Fatalf("AppendEvent tool.use: %v", err)
	}
	return id
}

// mustContradict runs confirm's contradict action against targetID, citing
// a freshly appended timeline event as its required positive evidence, and
// returns the new confirm record's id.
func mustContradict(t *testing.T, s *Store, sessionID, targetID string) string {
	t.Helper()
	evidenceID := mustAppendToolUseEvent(t, s, sessionID, "Bash", "")
	id, err := s.Confirm(ConfirmParams{
		Identity: Identity{Kind: IdentityAgent}, Action: ConfirmContradict, RecordID: targetID,
		Evidence: []int64{evidenceID}, SessionID: sessionID, ProjectKey: freshnessProjectKey,
	})
	if err != nil {
		t.Fatalf("Confirm contradict: %v", err)
	}
	return id
}

func mustAffirm(t *testing.T, s *Store, sessionID, targetID string) string {
	t.Helper()
	id, err := s.Confirm(ConfirmParams{
		Identity: Identity{Kind: IdentityAgent}, Action: ConfirmAffirm, RecordID: targetID,
		SessionID: sessionID, ProjectKey: freshnessProjectKey,
	})
	if err != nil {
		t.Fatalf("Confirm affirm: %v", err)
	}
	return id
}

func newFreshnessFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, freshnessProjectKey)
	sessionID := mustStartSessionInProject(t, s, freshnessProjectKey)
	return s, sessionID
}

// TestHandoffFreshnessReasonA_Contradicted is DONE WHEN clause 1, reason
// (a): a `contradicts` edge into the handoff, on its own, flags it with
// FreshnessContradicted and the contradicting record's id as evidence.
func TestHandoffFreshnessReasonA_Contradicted(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sessionID, []string{"a.go"})
	contradiction := mustContradict(t, s, sessionID, h.ID)

	reasons, err := s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness: %v", err)
	}
	if len(reasons) != 1 {
		t.Fatalf("HandoffFreshness = %+v, want exactly 1 reason", reasons)
	}
	if reasons[0].Kind != FreshnessContradicted {
		t.Errorf("reason kind = %q, want %q", reasons[0].Kind, FreshnessContradicted)
	}
	if fmt.Sprint(reasons[0].RecordIDs) != fmt.Sprint([]string{contradiction}) {
		t.Errorf("reason record ids = %v, want [%s]", reasons[0].RecordIDs, contradiction)
	}
}

// TestHandoffFreshnessReasonB_LaterRecordSharingAbout is DONE WHEN clause 1,
// reason (b): a later decision/note/outcome record in the same project
// sharing an about[] path, on its own, flags the handoff.
func TestHandoffFreshnessReasonB_LaterRecordSharingAbout(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sessionID, []string{"a.go", "b.go"})
	later := mustInsertNoteAbout(t, s, sessionID, []string{"b.go", "c.go"})

	reasons, err := s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness: %v", err)
	}
	if len(reasons) != 1 {
		t.Fatalf("HandoffFreshness = %+v, want exactly 1 reason", reasons)
	}
	if reasons[0].Kind != FreshnessLaterRecord {
		t.Errorf("reason kind = %q, want %q", reasons[0].Kind, FreshnessLaterRecord)
	}
	if fmt.Sprint(reasons[0].RecordIDs) != fmt.Sprint([]string{later}) {
		t.Errorf("reason record ids = %v, want [%s]", reasons[0].RecordIDs, later)
	}
}

// TestHandoffFreshnessReasonB_EarlierRecordDoesNotFlag proves "later" is
// insertion sequence: a decision/note/outcome record sharing an about[]
// path but inserted BEFORE the handoff must never count as evidence.
func TestHandoffFreshnessReasonB_EarlierRecordDoesNotFlag(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)
	_ = mustInsertNoteAbout(t, s, sessionID, []string{"a.go"})
	h := mustInsertHandoff(t, s, sessionID, []string{"a.go"})

	reasons, err := s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness: %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("HandoffFreshness = %+v, want none (the sharing record predates the handoff)", reasons)
	}
}

// TestHandoffFreshnessReasonC_LaterEventTouchingAbout is DONE WHEN clause 1,
// reason (c): a later timeline event touching a named path, on its own,
// flags the handoff — but only a mutating tool event (payload.
// IsMutatingFileTool), the same "changed a file" rule the delta slot uses;
// a Read of the same path must not flag it.
func TestHandoffFreshnessReasonC_LaterEventTouchingAbout(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sessionID, []string{"a.go"})
	_ = mustAppendToolUseEvent(t, s, sessionID, "Read", "a.go") // observed, not changed: must not flag
	editID := mustAppendToolUseEvent(t, s, sessionID, "Edit", "a.go")
	_ = mustAppendToolUseEvent(t, s, sessionID, "Edit", "unrelated.go") // different path: must not flag

	reasons, err := s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness: %v", err)
	}
	if len(reasons) != 1 {
		t.Fatalf("HandoffFreshness = %+v, want exactly 1 reason", reasons)
	}
	if reasons[0].Kind != FreshnessLaterActivity {
		t.Errorf("reason kind = %q, want %q", reasons[0].Kind, FreshnessLaterActivity)
	}
	if fmt.Sprint(reasons[0].EventIDs) != fmt.Sprint([]int64{editID}) {
		t.Errorf("reason event ids = %v, want [%d]", reasons[0].EventIDs, editID)
	}
}

// TestHandoffFreshnessClearedByAffirmThenReflaggedByNewEvidence is DONE WHEN
// clause 1's affirm half: a later `confirm affirm` informing the handoff
// clears an existing flag, and evidence arriving after the affirm re-flags
// it with only the new evidence (the affirmed-away evidence stays cleared).
func TestHandoffFreshnessClearedByAffirmThenReflaggedByNewEvidence(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sessionID, []string{"a.go"})
	firstContradiction := mustContradict(t, s, sessionID, h.ID)

	reasons, err := s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness (before affirm): %v", err)
	}
	if len(reasons) != 1 || reasons[0].Kind != FreshnessContradicted {
		t.Fatalf("HandoffFreshness (before affirm) = %+v, want 1 contradicted reason", reasons)
	}

	mustAffirm(t, s, sessionID, h.ID)

	reasons, err = s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness (after affirm): %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("HandoffFreshness (after affirm) = %+v, want none: the affirm must clear the earlier contradiction %s", reasons, firstContradiction)
	}

	secondContradiction := mustContradict(t, s, sessionID, h.ID)

	reasons, err = s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness (after re-contradiction): %v", err)
	}
	if len(reasons) != 1 || reasons[0].Kind != FreshnessContradicted {
		t.Fatalf("HandoffFreshness (after re-contradiction) = %+v, want 1 contradicted reason", reasons)
	}
	if fmt.Sprint(reasons[0].RecordIDs) != fmt.Sprint([]string{secondContradiction}) {
		t.Errorf("reason record ids = %v, want only the post-affirm contradiction [%s] (not the cleared %s)",
			reasons[0].RecordIDs, secondContradiction, firstContradiction)
	}
}

// TestHandoffFreshnessPromoteDoesNotClearLikeAffirmDoes proves confirm's
// promote action — the OTHER action that mints an `informs` edge
// (store/confirm.go's confirmEdgeType) — is not mistaken for an affirm: a
// promote targeting a still-flagged handoff must not clear the flag. Since
// promote requires the target to be tier=inferred (ErrPromoteNotADraft),
// this exercises HandoffFreshness against an inferred-tier handoff.
func TestHandoffFreshnessPromoteDoesNotClearLikeAffirmDoes(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, freshnessProjectKey)
	inferringSession := mustStartSessionInProject(t, s, freshnessProjectKey)
	promotingSession := mustStartSessionInProject(t, s, freshnessProjectKey)

	handoffID, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityInference}, Kind: KindHandoff, Text: "an inferred handoff",
		About: []string{"a.go"}, SessionID: inferringSession, ProjectKey: freshnessProjectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord inferred handoff: %v", err)
	}
	h, err := s.GetRecord(handoffID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}

	contradiction := mustContradict(t, s, promotingSession, h.ID)

	if _, err := s.Confirm(ConfirmParams{
		Identity: Identity{Kind: IdentityAgent}, Action: ConfirmPromote, RecordID: h.ID,
		SessionID: promotingSession, ProjectKey: freshnessProjectKey,
	}); err != nil {
		t.Fatalf("Confirm promote: %v", err)
	}

	reasons, err := s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness: %v", err)
	}
	if len(reasons) != 1 || reasons[0].Kind != FreshnessContradicted {
		t.Fatalf("HandoffFreshness = %+v, want the contradiction (%s) still flagged: promote must not clear it like affirm does", reasons, contradiction)
	}
}

// TestHandoffFreshnessNoLaterActivityNotFlagged is DONE WHEN clause 2's
// first half: a handoff with about[] set but no later record, event or
// contradiction at all is not flagged.
func TestHandoffFreshnessNoLaterActivityNotFlagged(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sessionID, []string{"a.go"})

	reasons, err := s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness: %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("HandoffFreshness = %+v, want none: no later activity exists at all", reasons)
	}
}

// TestHandoffFreshnessOldWithNoLaterActivityNotFlagged is DONE WHEN clause
// 2's second half: a handoff older than 30 days, with no later activity, is
// not flagged — HandoffFreshness must never derive a reason from elapsed
// time.
func TestHandoffFreshnessOldWithNoLaterActivityNotFlagged(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)

	old := time.Now().Add(-31 * 24 * time.Hour)
	handoffID := mustInsertHandoffAtTS(t, s, sessionID, old, []string{"a.go"})
	h, err := s.GetRecord(handoffID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if !h.TS.Before(time.Now().Add(-30 * 24 * time.Hour)) {
		t.Fatalf("test fixture bug: handoff ts %v is not older than 30 days", h.TS)
	}

	reasons, err := s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness: %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("HandoffFreshness = %+v, want none: age alone must never flag a handoff", reasons)
	}
}

// TestHandoffFreshnessNoAboutOnlyFlaggableByContradiction is the "handoff
// with no about[]" invariant: reasons (b) and (c) require an about[] path
// in common, impossible against an empty set, so only a contradiction can
// flag an about-less handoff — never a later record or event that would
// have matched had about[] been set.
func TestHandoffFreshnessNoAboutOnlyFlaggableByContradiction(t *testing.T) {
	s, sessionID := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sessionID, nil)

	_ = mustInsertNoteAbout(t, s, sessionID, []string{"a.go"})
	_ = mustAppendToolUseEvent(t, s, sessionID, "Edit", "a.go")

	reasons, err := s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness (no about, later activity elsewhere): %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("HandoffFreshness = %+v, want none: an about-less handoff cannot be flagged by (b) or (c)", reasons)
	}

	contradiction := mustContradict(t, s, sessionID, h.ID)
	reasons, err = s.HandoffFreshness(h)
	if err != nil {
		t.Fatalf("HandoffFreshness (no about, contradicted): %v", err)
	}
	if len(reasons) != 1 || reasons[0].Kind != FreshnessContradicted {
		t.Fatalf("HandoffFreshness = %+v, want exactly the contradiction (%s): (a) still applies with no about[]", reasons, contradiction)
	}
}

// --- Critic mutation probes ---
//
// Mutation probe 1 — flag by age: in HandoffFreshness (freshness.go), add a
// reason whenever time.Since(h.TS) exceeds some threshold -> RED
// (TestHandoffFreshnessOldWithNoLaterActivityNotFlagged: reasons is
// non-empty though no positive evidence exists); remove it -> GREEN.
//
// Mutation probe 2 — ignore the affirm boundary: in HandoffFreshness, use
// h.ID as boundaryID unconditionally instead of the latest affirm's id ->
// RED (TestHandoffFreshnessClearedByAffirmThenReflaggedByNewEvidence: the
// affirmed-away first contradiction stays flagged, and the re-flag after
// affirm carries both contradictions instead of only the new one); restore
// the affirm lookup -> GREEN.
//
// Mutation probe 3 — drop the Resume slot marker: see internal/block's own
// mutation probe for staleMarker.
