package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// mustInsertInferredDraft inserts a tier=inferred record attributed to
// sessionID — the shape promote's target takes once Phase 5 inference
// exists (SCHEMA.md's records.tier / promoter columns), built directly
// through InsertRecord since no inference pass is wired up yet.
func mustInsertInferredDraft(t *testing.T, s *Store, sessionID, projectKey, text string) string {
	t.Helper()
	id, err := s.InsertRecord(InsertRecordParams{
		Identity:   Identity{Kind: IdentityInference},
		Kind:       KindNote,
		Text:       text,
		SessionID:  sessionID,
		ProjectKey: projectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord (inferred draft): %v", err)
	}
	return id
}

func countRecords(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM records`).Scan(&n); err != nil {
		t.Fatalf("count records: %v", err)
	}
	return n
}

func countEdges(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&n); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	return n
}

// TestConfirmPromoteYieldsAgentDeclaredTierNeverHuman is the punch's DONE
// WHEN clause 3, first half: an agent-identity promote of a valid draft
// (tier=inferred, drafted by a DIFFERENT session) inserts a new confirm
// record at agent-declared, records the promoter, and mints an edge to the
// draft.
func TestConfirmPromoteYieldsAgentDeclaredTierNeverHuman(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	draftSession := mustStartSessionInProject(t, s, "proj-a")
	promoterSession := mustStartSessionInProject(t, s, "proj-a")
	draftID := mustInsertInferredDraft(t, s, draftSession, "proj-a", "inferred: chose sqlite")

	id, err := s.Confirm(ConfirmParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: promoterSession},
		Action:     ConfirmPromote,
		RecordID:   draftID,
		SessionID:  promoterSession,
		ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("Confirm(promote): %v", err)
	}

	rec, err := s.GetRecord(id)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Tier != TierAgentDeclared {
		t.Errorf("promoted confirm record tier = %q, want %q (never human-declared)", rec.Tier, TierAgentDeclared)
	}
	if rec.Kind != KindConfirm {
		t.Errorf("promoted confirm record kind = %q, want %q", rec.Kind, KindConfirm)
	}
	if rec.Promoter != promoterSession {
		t.Errorf("promoted confirm record promoter = %q, want %q", rec.Promoter, promoterSession)
	}

	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'informs'`,
		id, draftID).Scan(&n); err != nil {
		t.Fatalf("count informs edge: %v", err)
	}
	if n != 1 {
		t.Errorf("informs edge %s -> %s count = %d, want 1", id, draftID, n)
	}
}

// TestConfirmPromoteSameSessionRejected is the punch's DONE WHEN clause 3,
// second half (SCHEMA.md invariant 3): a draft inferred from session S
// cannot be promoted by session S. Nothing is written on rejection.
func TestConfirmPromoteSameSessionRejected(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionS := mustStartSessionInProject(t, s, "proj-a")
	draftID := mustInsertInferredDraft(t, s, sessionS, "proj-a", "inferred: chose sqlite")

	before := countRecords(t, s)
	_, err := s.Confirm(ConfirmParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: sessionS},
		Action:     ConfirmPromote,
		RecordID:   draftID,
		SessionID:  sessionS,
		ProjectKey: "proj-a",
	})
	if !errors.Is(err, ErrPromoteSameSession) {
		t.Fatalf("Confirm(promote) by the drafting session = %v, want ErrPromoteSameSession", err)
	}
	if got := countRecords(t, s); got != before {
		t.Errorf("record count = %d after a rejected self-promotion, want unchanged %d", got, before)
	}
}

// TestConfirmPromoteRequiresInferredTier rejects promoting a record that is
// not a draft at all (already agent-declared) — "promote a draft" has
// nothing to promote otherwise.
func TestConfirmPromoteRequiresInferredTier(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	session := mustStartSessionInProject(t, s, "proj-a")
	other := mustStartSessionInProject(t, s, "proj-a")
	declaredID := mustInsertNote(t, s, session, "proj-a", "already declared")

	before := countRecords(t, s)
	_, err := s.Confirm(ConfirmParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: other},
		Action:     ConfirmPromote,
		RecordID:   declaredID,
		SessionID:  other,
		ProjectKey: "proj-a",
	})
	if !errors.Is(err, ErrPromoteNotADraft) {
		t.Fatalf("Confirm(promote) of an agent-declared record = %v, want ErrPromoteNotADraft", err)
	}
	if got := countRecords(t, s); got != before {
		t.Errorf("record count = %d after a rejected promote, want unchanged %d", got, before)
	}
}

// TestConfirmContradictRequiresEvidence is the punch's DONE WHEN clause 2,
// first half: contradict with no evidence is rejected and writes nothing.
func TestConfirmContradictRequiresEvidence(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	session := mustStartSessionInProject(t, s, "proj-a")
	targetID := mustInsertNote(t, s, session, "proj-a", "tests are green")

	beforeRecords, beforeEdges := countRecords(t, s), countEdges(t, s)
	_, err := s.Confirm(ConfirmParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: session},
		Action:     ConfirmContradict,
		RecordID:   targetID,
		SessionID:  session,
		ProjectKey: "proj-a",
	})
	if !errors.Is(err, ErrContradictRequiresEvidence) {
		t.Fatalf("Confirm(contradict) with no evidence = %v, want ErrContradictRequiresEvidence", err)
	}
	if got := countRecords(t, s); got != beforeRecords {
		t.Errorf("record count = %d after a rejected contradict, want unchanged %d", got, beforeRecords)
	}
	if got := countEdges(t, s); got != beforeEdges {
		t.Errorf("edge count = %d after a rejected contradict, want unchanged %d", got, beforeEdges)
	}
}

// TestConfirmContradictUnknownEvidenceRejected is the punch's DONE WHEN
// clause 2, second half: an evidence id not present in timeline_events for
// the caller's project is rejected and writes nothing — including an id
// that exists but belongs to a DIFFERENT project (positive evidence must be
// this project's own).
func TestConfirmContradictUnknownEvidenceRejected(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	mustUpsertProject(t, s, "proj-b")
	sessionA := mustStartSessionInProject(t, s, "proj-a")
	sessionB := mustStartSessionInProject(t, s, "proj-b")
	targetID := mustInsertNote(t, s, sessionA, "proj-a", "tests are green")

	otherProjectEventID, err := s.AppendEvent(Event{
		TS: time.Now(), Kind: "tool.result", SessionID: sessionB, Source: "posttooluse", Payload: `{"exit":1}`,
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	for name, evidence := range map[string][]int64{
		"nonexistent event id":       {999999},
		"event from another project": {otherProjectEventID},
	} {
		t.Run(name, func(t *testing.T) {
			beforeRecords, beforeEdges := countRecords(t, s), countEdges(t, s)
			_, err := s.Confirm(ConfirmParams{
				Identity:   Identity{Kind: IdentityAgent, Actor: sessionA},
				Action:     ConfirmContradict,
				RecordID:   targetID,
				Evidence:   evidence,
				SessionID:  sessionA,
				ProjectKey: "proj-a",
			})
			var evErr *UnknownEvidenceError
			if !errors.As(err, &evErr) {
				t.Fatalf("Confirm(contradict) with %s = %v, want *UnknownEvidenceError", name, err)
			}
			if got := countRecords(t, s); got != beforeRecords {
				t.Errorf("record count = %d after a rejected contradict, want unchanged %d", got, beforeRecords)
			}
			if got := countEdges(t, s); got != beforeEdges {
				t.Errorf("edge count = %d after a rejected contradict, want unchanged %d", got, beforeEdges)
			}
		})
	}
}

// TestConfirmContradictWithValidEvidenceMintsContradictsEdge is the
// positive-evidence path DONE WHEN clause 2 implies must still work: a real
// evidence id in this project's own timeline is accepted.
func TestConfirmContradictWithValidEvidenceMintsContradictsEdge(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	session := mustStartSessionInProject(t, s, "proj-a")
	targetID := mustInsertNote(t, s, session, "proj-a", "tests are green")
	evID, err := s.AppendEvent(Event{
		TS: time.Now(), Kind: "tool.result", SessionID: session, Source: "posttooluse", Payload: `{"exit":1}`,
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	id, err := s.Confirm(ConfirmParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: session},
		Action:     ConfirmContradict,
		RecordID:   targetID,
		Evidence:   []int64{evID},
		SessionID:  session,
		ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("Confirm(contradict) with valid evidence: %v", err)
	}

	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'contradicts'`,
		id, targetID).Scan(&n); err != nil {
		t.Fatalf("count contradicts edge: %v", err)
	}
	if n != 1 {
		t.Errorf("contradicts edge %s -> %s count = %d, want 1", id, targetID, n)
	}
}

// TestConfirmSupersedeRejectsCrossProjectTarget: both records must be in
// the caller's own observed project.
func TestConfirmSupersedeRejectsCrossProjectTarget(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	mustUpsertProject(t, s, "proj-b")
	sessionA := mustStartSessionInProject(t, s, "proj-a")
	sessionB := mustStartSessionInProject(t, s, "proj-b")
	targetInB := mustInsertNote(t, s, sessionB, "proj-b", "an old handoff in proj-b")

	before := countRecords(t, s)
	_, err := s.Confirm(ConfirmParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: sessionA},
		Action:     ConfirmSupersede,
		RecordID:   targetInB,
		SessionID:  sessionA,
		ProjectKey: "proj-a",
	})
	var crossErr *CrossProjectRecordError
	if !errors.As(err, &crossErr) {
		t.Fatalf("Confirm(supersede) across projects = %v, want *CrossProjectRecordError", err)
	}
	if got := countRecords(t, s); got != before {
		t.Errorf("record count = %d after a rejected cross-project supersede, want unchanged %d", got, before)
	}
}

// TestConfirmSupersedeMintsSupersedesEdge is the same-project happy path.
func TestConfirmSupersedeMintsSupersedesEdge(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	session := mustStartSessionInProject(t, s, "proj-a")
	targetID := mustInsertNote(t, s, session, "proj-a", "an old handoff")

	id, err := s.Confirm(ConfirmParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: session},
		Action:     ConfirmSupersede,
		RecordID:   targetID,
		SessionID:  session,
		ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("Confirm(supersede): %v", err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'supersedes'`,
		id, targetID).Scan(&n); err != nil {
		t.Fatalf("count supersedes edge: %v", err)
	}
	if n != 1 {
		t.Errorf("supersedes edge %s -> %s count = %d, want 1", id, targetID, n)
	}
}

// TestConfirmAffirmMintsInformsEdge: affirm needs no special validation and
// mints an informs edge, meaning "still true as of now" (AGENT-CONTRACT.md
// §confirm — the additive action this punch adds).
func TestConfirmAffirmMintsInformsEdge(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	session := mustStartSessionInProject(t, s, "proj-a")
	handoffID := mustInsertNote(t, s, session, "proj-a", "handoff: ship the affirm action next")

	id, err := s.Confirm(ConfirmParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: session},
		Action:     ConfirmAffirm,
		RecordID:   handoffID,
		Text:       "still true",
		SessionID:  session,
		ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("Confirm(affirm): %v", err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'informs'`,
		id, handoffID).Scan(&n); err != nil {
		t.Fatalf("count informs edge: %v", err)
	}
	if n != 1 {
		t.Errorf("informs edge %s -> %s count = %d, want 1", id, handoffID, n)
	}
}

// TestConfirmUnknownRecordIDRejected: an action naming an unknown record_id
// fails the same way note's unknown link id does — nothing written.
func TestConfirmUnknownRecordIDRejected(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	session := mustStartSessionInProject(t, s, "proj-a")

	before := countRecords(t, s)
	_, err := s.Confirm(ConfirmParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: session},
		Action:     ConfirmAffirm,
		RecordID:   "does-not-exist",
		SessionID:  session,
		ProjectKey: "proj-a",
	})
	var edgeErr *UnknownEdgeTargetError
	if !errors.As(err, &edgeErr) {
		t.Fatalf("Confirm with an unknown record_id = %v, want *UnknownEdgeTargetError", err)
	}
	if got := countRecords(t, s); got != before {
		t.Errorf("record count = %d after a rejected confirm, want unchanged %d", got, before)
	}
}
