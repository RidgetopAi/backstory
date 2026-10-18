package store

import (
	"testing"
)

func unrecordedReason(t *testing.T, s *Store, h Record) (FreshnessReason, bool) {
	t.Helper()
	reasons, err := s.HandoffFreshness(h, nil)
	if err != nil {
		t.Fatalf("HandoffFreshness: %v", err)
	}
	for _, r := range reasons {
		if r.Kind == FreshnessLaterUnrecordedWork {
			return r, true
		}
	}
	return FreshnessReason{}, false
}

func mustInsertOutcome(t *testing.T, s *Store, sessionID string) {
	t.Helper()
	if _, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindOutcome, Text: "did the thing",
		SessionID: sessionID, ProjectKey: freshnessProjectKey,
	}); err != nil {
		t.Fatalf("InsertRecord outcome: %v", err)
	}
}

// editTwice appends 3 mutating events over 2 distinct paths for sessionID.
func editTwice(t *testing.T, s *Store, sessionID string) {
	t.Helper()
	mustAppendToolUseEvent(t, s, sessionID, "Edit", "/proj/a.go")
	mustAppendToolUseEvent(t, s, sessionID, "Write", "/proj/b.go")
	mustAppendToolUseEvent(t, s, sessionID, "Edit", "/proj/a.go")
}

// DONE WHEN (1): later session B, 3 edits on 2 paths, no record, flags an
// empty-about handoff with sessions=1, files=2.
func TestUnrecordedWorkFlagsEmptyAboutHandoff(t *testing.T) {
	s, a := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, a, nil)
	b := mustStartSessionInProject(t, s, freshnessProjectKey)
	editTwice(t, s, b)

	r, ok := unrecordedReason(t, s, h)
	if !ok {
		t.Fatal("no later-unrecorded-work reason")
	}
	if r.Sessions != 1 || r.Files != 2 {
		t.Fatalf("sessions=%d files=%d, want 1/2", r.Sessions, r.Files)
	}
	if len(r.EventIDs) != 3 {
		t.Errorf("event ids = %v, want 3", r.EventIDs)
	}
}

// DONE WHEN (2): the clearing cases.
func TestUnrecordedWorkClearedCases(t *testing.T) {
	t.Run("B wrote an outcome", func(t *testing.T) {
		s, a := newFreshnessFixture(t)
		h := mustInsertHandoff(t, s, a, nil)
		b := mustStartSessionInProject(t, s, freshnessProjectKey)
		editTwice(t, s, b)
		mustInsertOutcome(t, s, b)
		if r, ok := unrecordedReason(t, s, h); ok {
			t.Fatalf("flagged despite B's record: %+v", r)
		}
	})
	t.Run("B only read", func(t *testing.T) {
		s, a := newFreshnessFixture(t)
		h := mustInsertHandoff(t, s, a, nil)
		b := mustStartSessionInProject(t, s, freshnessProjectKey)
		mustAppendToolUseEvent(t, s, b, "Read", "/proj/a.go")
		mustAppendToolUseEvent(t, s, b, "Grep", "/proj/b.go")
		if r, ok := unrecordedReason(t, s, h); ok {
			t.Fatalf("flagged on read-only work: %+v", r)
		}
	})
	t.Run("edits by the handoff's own session", func(t *testing.T) {
		s, a := newFreshnessFixture(t)
		h := mustInsertHandoff(t, s, a, nil)
		editTwice(t, s, a)
		if r, ok := unrecordedReason(t, s, h); ok {
			t.Fatalf("flagged on own-session edits: %+v", r)
		}
	})
	t.Run("affirm after B's edits", func(t *testing.T) {
		s, a := newFreshnessFixture(t)
		h := mustInsertHandoff(t, s, a, nil)
		b := mustStartSessionInProject(t, s, freshnessProjectKey)
		editTwice(t, s, b)
		mustAffirm(t, s, a, h.ID)
		if r, ok := unrecordedReason(t, s, h); ok {
			t.Fatalf("flagged though affirm postdates the edits: %+v", r)
		}
		// edits after the affirm flag again.
		mustAppendToolUseEvent(t, s, b, "Edit", "/proj/c.go")
		r, ok := unrecordedReason(t, s, h)
		if !ok || r.Sessions != 1 || r.Files != 1 {
			t.Fatalf("post-affirm edit: %+v ok=%v, want sessions=1 files=1", r, ok)
		}
	})
}

// DONE WHEN (3): a session in a different home never flags H.
func TestUnrecordedWorkIgnoresOtherHome(t *testing.T) {
	s, a := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, a, nil)
	mustUpsertProject(t, s, "proj-other")
	other := mustStartSessionInProject(t, s, "proj-other")
	editTwice(t, s, other)
	if r, ok := unrecordedReason(t, s, h); ok {
		t.Fatalf("other home's edits flagged H: %+v", r)
	}
}
