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

// TestSearchRecordsFindsHyphenatedTermVariants is proof (1) for task
// 99f5c669: a record containing "wobble-party" as plain text must be found
// by a bare, upper-cased, quoted, and multi-word query — none of which may
// error, since FTS5 treats '-' and '"' as query-language operators unless
// the query text is escaped first (the real-use failure this fixes: recall
// erroring with "no such column: party" on a project named wobble-party).
func TestSearchRecordsFindsHyphenatedTermVariants(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent},
		Kind:     KindNote,
		Text:     "renamed the project to wobble-party ahead of launch",
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}

	for _, query := range []string{
		"wobble-party",
		"WOBBLE-PARTY",
		`"wobble-party"`,
		"wobble-party launch",
	} {
		results, err := s.SearchRecords(query, 10)
		if err != nil {
			t.Fatalf("SearchRecords(%q): %v", query, err)
		}
		var found bool
		for _, r := range results {
			if r.ID == id {
				found = true
			}
		}
		if !found {
			t.Errorf("SearchRecords(%q) did not find the wobble-party record; got %+v", query, results)
		}
	}
}

// TestSearchRecordsNeverErrorsOnFTS5SyntaxCharacters is proof (2) for task
// 99f5c669: none of these queries — each built from a character or keyword
// FTS5's query language treats specially — may return an error. Before the
// fix, several of these (a bare colon, an unbalanced quote, a lone NOT,
// adjacent AND/OR, an unclosed paren) caused an FTS5 "syntax error" or "no
// such column" error because the raw query string reached MATCH unescaped.
func TestSearchRecordsNeverErrorsOnFTS5SyntaxCharacters(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	queries := []string{
		"a:b",
		"foo*",
		"(x, x)",
		"^x",
		`say "hi`,
		"NOT",
		"AND OR",
		"NEAR(a b)",
		"---",
		"'",
		"",
	}
	for _, query := range queries {
		if _, err := s.SearchRecords(query, 10); err != nil {
			t.Errorf("SearchRecords(%q) returned an error: %v", query, err)
		}
	}
}

// TestSearchRecordsInProjectSharesEscapingWithSearchRecords is proof (3)
// for task 99f5c669: both entry points must route through the same
// escaping so a project/file/branch name with a hyphen behaves identically
// whether recall is scoped to a project or not.
func TestSearchRecordsInProjectSharesEscapingWithSearchRecords(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionA := mustStartSessionInProject(t, s, "proj-a")

	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote,
		Text: "shipped wobble-party to proj-a", SessionID: sessionA, ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}

	global, err := s.SearchRecords("wobble-party", 10)
	if err != nil {
		t.Fatalf("SearchRecords: %v", err)
	}
	scoped, err := s.SearchRecordsInProject("proj-a", "wobble-party", 10)
	if err != nil {
		t.Fatalf("SearchRecordsInProject: %v", err)
	}

	var globalFound, scopedFound bool
	for _, r := range global {
		if r.ID == id {
			globalFound = true
		}
	}
	for _, r := range scoped {
		if r.ID == id {
			scopedFound = true
		}
	}
	if !globalFound {
		t.Errorf("SearchRecords(%q) did not find the record", "wobble-party")
	}
	if !scopedFound {
		t.Errorf("SearchRecordsInProject(proj-a, %q) did not find the record", "wobble-party")
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
