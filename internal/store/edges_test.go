package store

import (
	"path/filepath"
	"testing"
)

// TestContradictionCountCountsEdgesIntoProjectRecordsOnly checks that only
// `contradicts` edges whose TO record lives in the target project are
// counted, and that a `supersedes` edge into the same project never counts.
func TestContradictionCountCountsEdgesIntoProjectRecordsOnly(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	mustUpsertProject(t, s, "proj-b")
	sessionA := mustStartSessionInProject(t, s, "proj-a")
	sessionB := mustStartSessionInProject(t, s, "proj-b")

	targetA := mustInsertNote(t, s, sessionA, "proj-a", "claim in proj-a")
	evidenceA := mustInsertNote(t, s, sessionA, "proj-a", "contradicting evidence")
	if err := s.LinkEdge(evidenceA, targetA, EdgeContradicts, sessionA); err != nil {
		t.Fatalf("LinkEdge contradicts: %v", err)
	}

	supersededA := mustInsertNote(t, s, sessionA, "proj-a", "an old note")
	supersederA := mustInsertNote(t, s, sessionA, "proj-a", "a new note")
	if err := s.LinkEdge(supersederA, supersededA, EdgeSupersedes, sessionA); err != nil {
		t.Fatalf("LinkEdge supersedes: %v", err)
	}

	targetB := mustInsertNote(t, s, sessionB, "proj-b", "claim in proj-b")
	evidenceB := mustInsertNote(t, s, sessionB, "proj-b", "contradicting evidence in proj-b")
	if err := s.LinkEdge(evidenceB, targetB, EdgeContradicts, sessionB); err != nil {
		t.Fatalf("LinkEdge contradicts proj-b: %v", err)
	}

	n, err := s.ContradictionCount("proj-a")
	if err != nil {
		t.Fatalf("ContradictionCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("ContradictionCount(proj-a) = %d, want 1 (the supersedes edge and proj-b's contradiction must not count)", n)
	}
}

func mustInsertNote(t *testing.T, s *Store, sessionID, projectKey, text string) string {
	t.Helper()
	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: text,
		SessionID: sessionID, ProjectKey: projectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	return id
}
