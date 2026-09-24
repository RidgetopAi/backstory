package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSearchRecordsMatchesAndExcludesAbsentWord(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	present, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent},
		Kind:     KindDecision,
		Text:     "switched the store to modernc sqlite for no-cgo builds",
	})
	if err != nil {
		t.Fatalf("InsertRecord present: %v", err)
	}
	absent, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent},
		Kind:     KindDecision,
		Text:     "renamed the CLI flag for the budget setting",
	})
	if err != nil {
		t.Fatalf("InsertRecord absent: %v", err)
	}

	results, err := s.SearchRecords("modernc", 10)
	if err != nil {
		t.Fatalf("SearchRecords: %v", err)
	}

	var gotPresent, gotAbsent bool
	for _, r := range results {
		if r.ID == present {
			gotPresent = true
		}
		if r.ID == absent {
			gotAbsent = true
		}
	}
	if !gotPresent {
		t.Errorf("SearchRecords(%q) did not return the record containing that word", "modernc")
	}
	if gotAbsent {
		t.Errorf("SearchRecords(%q) returned a record that does not contain that word", "modernc")
	}
}

// TestSearchRecordsTiebreaksEqualRankByTsAscending is proof (4) for task
// 05b03d4a: it inserts the later record FIRST (row/rowid order is the
// REVERSE of ts order) so a passing result depends on search.go's `, r.ts
// ASC` tiebreak actually running, not on rowid/insertion order happening to
// already agree with ts order. Both records share identical text, so FTS5
// ranks them equally and rank alone cannot order them. This test is RED if
// the `, r.ts ASC` tiebreak is removed from search.go's ORDER BY.
//
// Mutation probe (ORDER BY rank, r.ts ASC -> ORDER BY rank): "search_test.go:77:
// SearchRecords order = [<laterID>, <earlierID>], want [<earlierID>,
// <laterID>] (equal-rank ties broken by ts ascending)" -- restoring the
// tiebreak turns this back GREEN.
func TestSearchRecordsTiebreaksEqualRankByTsAscending(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	sessionID := mustStartSession(t, s)

	earlier := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Hour)

	// Insert later-first: rowid order is the reverse of ts order.
	laterID := insertRecordFixture(t, s, sessionID, later, "tiebreak probe shared text")
	earlierID := insertRecordFixture(t, s, sessionID, earlier, "tiebreak probe shared text")

	results, err := s.SearchRecords("tiebreak", 10)
	if err != nil {
		t.Fatalf("SearchRecords: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("SearchRecords(%q) returned %d results, want 2", "tiebreak", len(results))
	}
	if results[0].ID != earlierID || results[1].ID != laterID {
		t.Fatalf("SearchRecords order = [%s, %s], want [%s, %s] (equal-rank ties broken by ts ascending)",
			results[0].ID, results[1].ID, earlierID, laterID)
	}
}

// TestSearchRecordsInProjectScopesToOneProject checks internal/recall's
// free-text anchor query: a match in a different project never appears,
// even though records_fts itself is not project-scoped (SearchRecords has
// no project filter at all — SearchRecordsInProject exists specifically to
// add one).
func TestSearchRecordsInProjectScopesToOneProject(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	mustUpsertProject(t, s, "proj-b")
	sessionA := mustStartSessionInProject(t, s, "proj-a")
	sessionB := mustStartSessionInProject(t, s, "proj-b")

	inA, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: "widget refactor in proj-a",
		SessionID: sessionA, ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("InsertRecord proj-a: %v", err)
	}
	if _, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: "widget refactor in proj-b",
		SessionID: sessionB, ProjectKey: "proj-b",
	}); err != nil {
		t.Fatalf("InsertRecord proj-b: %v", err)
	}

	results, err := s.SearchRecordsInProject("proj-a", "widget", 10)
	if err != nil {
		t.Fatalf("SearchRecordsInProject: %v", err)
	}
	if len(results) != 1 || results[0].ID != inA {
		t.Fatalf("SearchRecordsInProject(proj-a, widget) = %+v, want exactly proj-a's own match", results)
	}
}

func TestSearchRecordsExcludesTombstoned(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent},
		Kind:     KindNote,
		Text:     "ephemeral note about the sandbox probe",
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}

	results, err := s.SearchRecords("sandbox", 10)
	if err != nil {
		t.Fatalf("SearchRecords before tombstone: %v", err)
	}
	if len(results) != 1 || results[0].ID != id {
		t.Fatalf("SearchRecords before tombstone = %+v, want exactly the inserted record", results)
	}

	if err := s.TombstoneRecord(id, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatalf("TombstoneRecord: %v", err)
	}

	results, err = s.SearchRecords("sandbox", 10)
	if err != nil {
		t.Fatalf("SearchRecords after tombstone: %v", err)
	}
	for _, r := range results {
		if r.ID == id {
			t.Errorf("SearchRecords returned a tombstoned record: %+v", r)
		}
	}
}
