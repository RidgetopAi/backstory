package store

import (
	"path/filepath"
	"testing"
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
