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

// TestEdgesTouchingReturnsBothDirectionsAndEveryType checks internal/recall's
// edge-walk primitive: edges where the record is either from_id or to_id,
// across different edge types, and none where it is neither endpoint.
func TestEdgesTouchingReturnsBothDirectionsAndEveryType(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	center := mustInsertNote(t, s, sessionID, "proj-a", "center")
	outgoingTarget := mustInsertNote(t, s, sessionID, "proj-a", "informed by center")
	incomingSource := mustInsertNote(t, s, sessionID, "proj-a", "supersedes center")
	unrelatedA := mustInsertNote(t, s, sessionID, "proj-a", "unrelated a")
	unrelatedB := mustInsertNote(t, s, sessionID, "proj-a", "unrelated b")

	if err := s.LinkEdge(center, outgoingTarget, EdgeInforms, sessionID); err != nil {
		t.Fatalf("LinkEdge informs (outgoing): %v", err)
	}
	if err := s.LinkEdge(incomingSource, center, EdgeSupersedes, sessionID); err != nil {
		t.Fatalf("LinkEdge supersedes (incoming): %v", err)
	}
	if err := s.LinkEdge(unrelatedA, unrelatedB, EdgeInforms, sessionID); err != nil {
		t.Fatalf("LinkEdge unrelated: %v", err)
	}

	edges, err := s.EdgesTouching(center)
	if err != nil {
		t.Fatalf("EdgesTouching: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("EdgesTouching(center) returned %d edges, want 2 (the unrelated edge must not appear): %+v", len(edges), edges)
	}

	var sawOutgoing, sawIncoming bool
	for _, e := range edges {
		switch {
		case e.Type == EdgeInforms && e.FromID == center && e.ToID == outgoingTarget:
			sawOutgoing = true
		case e.Type == EdgeSupersedes && e.FromID == incomingSource && e.ToID == center:
			sawIncoming = true
		}
	}
	if !sawOutgoing {
		t.Errorf("EdgesTouching(center) missing the outgoing informs edge: %+v", edges)
	}
	if !sawIncoming {
		t.Errorf("EdgesTouching(center) missing the incoming supersedes edge: %+v", edges)
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
