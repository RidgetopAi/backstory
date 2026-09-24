package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// mustInsertHandoffAbout is mustInsertHandoff plus about[] paths, needed for
// the freshness marker's reasons (b) and (c), which require an about[] path
// in common to ever fire.
func mustInsertHandoffAbout(t *testing.T, s *store.Store, sessionID, text string, about []string) store.Record {
	t.Helper()
	if _, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: text,
		About: about, SessionID: sessionID, ProjectKey: testProjectKey,
	}); err != nil {
		t.Fatalf("InsertRecord handoff: %v", err)
	}
	rec, ok, err := s.LatestRecord(testProjectKey, store.KindHandoff)
	if err != nil || !ok {
		t.Fatalf("LatestRecord after insert handoff: ok=%v err=%v", ok, err)
	}
	return rec
}

func mustContradictRecord(t *testing.T, s *store.Store, sessionID, targetID string) string {
	t.Helper()
	evidenceID := mustAppendEvent(t, s, sessionID, "shell.exec", time.Now(), map[string]any{})
	id, err := s.Confirm(store.ConfirmParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Action: store.ConfirmContradict, RecordID: targetID,
		Evidence: []int64{evidenceID}, SessionID: sessionID, ProjectKey: testProjectKey,
	})
	if err != nil {
		t.Fatalf("Confirm contradict: %v", err)
	}
	return id
}

func mustAffirmRecord(t *testing.T, s *store.Store, sessionID, targetID string) {
	t.Helper()
	if _, err := s.Confirm(store.ConfirmParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Action: store.ConfirmAffirm, RecordID: targetID,
		SessionID: sessionID, ProjectKey: testProjectKey,
	}); err != nil {
		t.Fatalf("Confirm affirm: %v", err)
	}
}

// TestRenderResumeSlotShowsPossiblyStaleMarkerAndAttentionCountsIt is DONE
// WHEN clause 3's flagged half: a handoff with a later mutating edit to a
// path it names gets the "⚠ possibly stale" marker right in the Resume
// slot, naming the reason and its evidence id, and the Attention slot
// counts it.
func TestRenderResumeSlotShowsPossiblyStaleMarkerAndAttentionCountsIt(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	handoff := mustInsertHandoffAbout(t, s, self, "resume at main.go", []string{"main.go"})
	mustAppendToolUseNamed(t, s, self, handoff.TS.Add(time.Minute), "Edit", "main.go")

	out := mustRender(t, s, fakeProcFS{}, self)

	idxResume := strings.Index(out, "Resume:")
	idxMarker := strings.Index(out, "⚠ possibly stale:")
	idxAttention := strings.Index(out, "Attention:")
	if idxResume < 0 || idxMarker < 0 || idxAttention < 0 {
		t.Fatalf("missing Resume/marker/Attention; got:\n%s", out)
	}
	if idxResume >= idxMarker || idxMarker >= idxAttention {
		t.Fatalf("marker not between Resume and Attention; got:\n%s", out)
	}
	if !strings.Contains(out, "later edit(s) to files it names") {
		t.Errorf("marker missing the later-activity reason clause; got:\n%s", out)
	}
	if !strings.Contains(out, "Attention: 0 unconfirmed draft(s), 0 contradiction(s), 1 possibly-stale handoff") {
		t.Errorf("attention slot did not count the possibly-stale handoff; got:\n%s", out)
	}
}

// TestRenderResumeSlotMarkerNamesContradictionEvidence covers reason (a)
// specifically, through Render rather than a later mutating edit: the
// marker names the contradicting record's id.
func TestRenderResumeSlotMarkerNamesContradictionEvidence(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	handoff := mustInsertHandoffAbout(t, s, self, "resume here", nil)
	contradiction := mustContradictRecord(t, s, self, handoff.ID)

	out := mustRender(t, s, fakeProcFS{}, self)
	if !strings.Contains(out, "⚠ possibly stale: contradicted (ids "+contradiction+")") {
		t.Fatalf("marker missing the contradiction reason with evidence id %s; got:\n%s", contradiction, out)
	}
}

// TestRenderResumeSlotMarkerClearsAfterAffirm proves the marker disappears
// once the handoff is affirmed, and reappears (with fresh evidence) if
// contradicted again afterward.
func TestRenderResumeSlotMarkerClearsAfterAffirm(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	handoff := mustInsertHandoffAbout(t, s, self, "resume here", nil)
	mustContradictRecord(t, s, self, handoff.ID)

	beforeAffirm := mustRender(t, s, fakeProcFS{}, self)
	if !strings.Contains(beforeAffirm, "⚠ possibly stale:") {
		t.Fatalf("expected the marker before affirming; got:\n%s", beforeAffirm)
	}

	mustAffirmRecord(t, s, self, handoff.ID)

	afterAffirm := mustRender(t, s, fakeProcFS{}, self)
	if strings.Contains(afterAffirm, "⚠ possibly stale:") {
		t.Fatalf("marker survived a confirm affirm; got:\n%s", afterAffirm)
	}
}

// TestRenderUnflaggedHandoffRendersExactlyAsBefore is DONE WHEN clause 3's
// unflagged half: a handoff with no positive staleness evidence renders the
// Resume slot exactly as it did before this marker existed, and the
// Attention slot's draft/contradiction line carries no possibly-stale
// clause — the existing block goldens (TestRenderResumeSlotCarriesTheHandoffRecordID,
// TestRenderAttentionCountsDraftsAndContradictions) stay byte-identical.
func TestRenderUnflaggedHandoffRendersExactlyAsBefore(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	handoff := mustInsertHandoffAbout(t, s, self, "shipped the resume id", nil)

	out := mustRender(t, s, fakeProcFS{}, self)

	wantLine := "Resume: (id " + handoff.ID + ") shipped the resume id"
	if !strings.Contains(out, wantLine) {
		t.Fatalf("Resume slot != the pre-marker shape; got:\n%s", out)
	}
	if strings.Contains(out, "⚠ possibly stale") {
		t.Errorf("unflagged handoff rendered a stale marker; got:\n%s", out)
	}
	if strings.Contains(out, "possibly-stale handoff") {
		t.Errorf("unflagged handoff's attention slot mentioned possibly-stale; got:\n%s", out)
	}
}

// --- Critic mutation probes (DONE WHEN clause 5) ---
//
// Mutation probe 1 — flag handoffs by age alone: in staleMarker/Render
// (block.go), add a marker whenever time.Since(rec.TS) exceeds some
// threshold, independent of store.HandoffFreshness's reasons -> RED
// (TestRenderUnflaggedHandoffRendersExactlyAsBefore: the unflagged fixture,
// which carries no about[] and no positive evidence, now renders a marker
// it should not); restore the HandoffFreshness-only source -> GREEN.
//
// Mutation probe 2 — ignore a later affirm: in store.latestAffirmInforming
// (internal/store/freshness.go), make it always return ok=false -> RED
// (TestRenderResumeSlotMarkerClearsAfterAffirm: the marker survives the
// confirm affirm instead of clearing); restore it -> GREEN.
//
// Mutation probe 3 — drop the marker from the Resume slot: in resumeSlot
// (block.go), delete the `if marker := staleMarker(staleReasons); marker !=
// "" { line += "\n" + marker }` block -> RED
// (TestRenderResumeSlotShowsPossiblyStaleMarkerAndAttentionCountsIt: "⚠
// possibly stale:" is missing from the Resume slot); restore it -> GREEN.
