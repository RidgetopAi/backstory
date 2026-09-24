package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestLatestRecordReturnsMostRecentByRowid inserts two handoffs and checks
// LatestRecord returns the one inserted last, by rowid — never by ts
// (SCHEMA.md invariant 10 applies to records too: ts is wall-clock and
// informational).
func TestLatestRecordReturnsMostRecentByRowid(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	if _, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "first handoff",
		SessionID: sessionID, ProjectKey: "proj-a",
	}); err != nil {
		t.Fatalf("InsertRecord first: %v", err)
	}
	wantID, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "second handoff",
		SessionID: sessionID, ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("InsertRecord second: %v", err)
	}

	rec, ok, err := s.LatestRecord("proj-a", KindHandoff)
	if err != nil {
		t.Fatalf("LatestRecord: %v", err)
	}
	if !ok {
		t.Fatal("LatestRecord found = false, want true")
	}
	if rec.ID != wantID {
		t.Errorf("LatestRecord.ID = %q, want %q (the second, most recently inserted handoff)", rec.ID, wantID)
	}
}

// TestLatestRecordExcludesTombstoned checks that a tombstoned handoff is
// skipped in favor of the next-most-recent live one.
func TestLatestRecordExcludesTombstoned(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	wantID, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "kept handoff",
		SessionID: sessionID, ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("InsertRecord kept: %v", err)
	}
	tombstonedID, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "tombstoned handoff",
		SessionID: sessionID, ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("InsertRecord tombstoned: %v", err)
	}
	if err := s.TombstoneRecord(tombstonedID, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatalf("TombstoneRecord: %v", err)
	}

	rec, ok, err := s.LatestRecord("proj-a", KindHandoff)
	if err != nil {
		t.Fatalf("LatestRecord: %v", err)
	}
	if !ok {
		t.Fatal("LatestRecord found = false, want true")
	}
	if rec.ID != wantID {
		t.Errorf("LatestRecord.ID = %q, want %q (the tombstoned one must be skipped)", rec.ID, wantID)
	}
}

// TestLatestRecordNotFound checks the not-found case.
func TestLatestRecordNotFound(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")

	_, ok, err := s.LatestRecord("proj-a", KindHandoff)
	if err != nil {
		t.Fatalf("LatestRecord: %v", err)
	}
	if ok {
		t.Fatal("LatestRecord found = true for an empty project, want false")
	}
}

// TestUnconfirmedDraftCount checks that only inferred-tier, unpromoted,
// unexpired, untombstoned records in the project are counted.
func TestUnconfirmedDraftCount(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	mustUpsertProject(t, s, "proj-b")
	now := time.Now()

	mustInsertDraft(t, s, "proj-a", TierInferred, "", nil)              // counted
	mustInsertDraft(t, s, "proj-a", TierInferred, "some-promoter", nil) // promoted: excluded
	expired := now.Add(-time.Hour)
	mustInsertDraft(t, s, "proj-a", TierInferred, "", &expired) // expired: excluded
	future := now.Add(time.Hour)
	mustInsertDraft(t, s, "proj-a", TierInferred, "", &future)  // not yet expired: counted
	mustInsertDraft(t, s, "proj-a", TierAgentDeclared, "", nil) // wrong tier: excluded
	mustInsertDraft(t, s, "proj-b", TierInferred, "", nil)      // wrong project: excluded

	n, err := s.UnconfirmedDraftCount("proj-a", now)
	if err != nil {
		t.Fatalf("UnconfirmedDraftCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("UnconfirmedDraftCount(proj-a) = %d, want 2", n)
	}
}

// TestInsertRecordWithEdgesHappyPathAtomic is proof (2) for task e7951178's
// happy path: InsertRecordWithEdges inserts the record and its supersedes
// edge together — both present after, asserted in one read.
func TestInsertRecordWithEdgesHappyPathAtomic(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	oldID := mustInsertNote(t, s, sessionID, "proj-a", "an old decision")

	newID, err := s.InsertRecordWithEdges(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindDecision, Text: "the new decision",
		SessionID: sessionID, ProjectKey: "proj-a",
	}, []EdgeSpec{{OtherID: oldID, Type: EdgeSupersedes, DeclaredBy: sessionID, Field: "supersedes"}})
	if err != nil {
		t.Fatalf("InsertRecordWithEdges: %v", err)
	}

	if _, err := s.GetRecord(newID); err != nil {
		t.Fatalf("GetRecord(newID): %v", err)
	}
	var edgeCount int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = ?`,
		newID, oldID, string(EdgeSupersedes)).Scan(&edgeCount); err != nil {
		t.Fatalf("count supersedes edge: %v", err)
	}
	if edgeCount != 1 {
		t.Fatalf("supersedes edge count = %d, want 1 (record and edge must land together)", edgeCount)
	}
}

// TestInsertRecordWithEdgesUnknownTargetInsertsNothing is proof (2) for task
// e7951178: an edge whose target id does not exist leaves the records row
// count unchanged — nothing persisted, not the record and not the edge.
func TestInsertRecordWithEdgesUnknownTargetInsertsNothing(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	var before int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM records`).Scan(&before); err != nil {
		t.Fatalf("count records before: %v", err)
	}

	_, err := s.InsertRecordWithEdges(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindDecision, Text: "doomed decision",
		SessionID: sessionID, ProjectKey: "proj-a",
	}, []EdgeSpec{{OtherID: "does-not-exist", Type: EdgeSupersedes, DeclaredBy: sessionID, Field: "supersedes"}})

	var targetErr *UnknownEdgeTargetError
	if !errors.As(err, &targetErr) {
		t.Fatalf("InsertRecordWithEdges error = %v, want *UnknownEdgeTargetError", err)
	}
	if targetErr.Field != "supersedes" {
		t.Errorf("UnknownEdgeTargetError.Field = %q, want %q", targetErr.Field, "supersedes")
	}

	var after int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM records`).Scan(&after); err != nil {
		t.Fatalf("count records after: %v", err)
	}
	if after != before {
		t.Fatalf("records count = %d after a forced edge failure, want unchanged %d", after, before)
	}
	var edgeCount int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&edgeCount); err != nil {
		t.Fatalf("count edges after: %v", err)
	}
	if edgeCount != 0 {
		t.Fatalf("edges count = %d after a forced edge failure, want 0", edgeCount)
	}
}

// TestInsertRecordSetsEventCursorToMaxEventIDAtInsertTime is proof (2) for
// task 214eb30e: InsertRecord sets event_cursor to MAX(timeline_events.id)
// at insert time (0 on an empty timeline), and a later event appended after
// a record never changes that record's already-stored cursor — the same
// append-only guarantee as every other records column.
func TestInsertRecordSetsEventCursorToMaxEventIDAtInsertTime(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	firstID, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: "before any events",
		SessionID: sessionID, ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("InsertRecord first: %v", err)
	}
	first, err := s.GetRecord(firstID)
	if err != nil {
		t.Fatalf("GetRecord(first): %v", err)
	}
	if first.EventCursor != 0 {
		t.Errorf("first.EventCursor (empty timeline) = %d, want 0", first.EventCursor)
	}

	for i := 0; i < 3; i++ {
		if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: "probe", SessionID: sessionID, Source: "shell", Payload: "{}"}); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
	}

	secondID, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: "after three events",
		SessionID: sessionID, ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("InsertRecord second: %v", err)
	}
	second, err := s.GetRecord(secondID)
	if err != nil {
		t.Fatalf("GetRecord(second): %v", err)
	}
	if second.EventCursor != 3 {
		t.Errorf("second.EventCursor (after 3 events) = %d, want 3", second.EventCursor)
	}

	// Two more events land after second was inserted; second's cursor must
	// stay exactly where it was set, never recomputed on read.
	for i := 0; i < 2; i++ {
		if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: "probe", SessionID: sessionID, Source: "shell", Payload: "{}"}); err != nil {
			t.Fatalf("AppendEvent (after second) %d: %v", i, err)
		}
	}
	secondReread, err := s.GetRecord(secondID)
	if err != nil {
		t.Fatalf("GetRecord(second) reread: %v", err)
	}
	if secondReread.EventCursor != 3 {
		t.Errorf("second.EventCursor after later events landed = %d, want unchanged 3", secondReread.EventCursor)
	}

	// The append-only trigger must refuse a direct write to event_cursor,
	// the same as every other non-tombstone column.
	if _, err := s.db.Exec(`UPDATE records SET event_cursor = 999 WHERE id = ?`, secondID); err == nil {
		t.Fatal("UPDATE records.event_cursor succeeded, want records_no_update to abort it")
	}
}

// TestInsertRecordEventCursorNotCallerSupplied is proof (2) for task
// 214eb30e: event_cursor cannot be supplied by a caller. InsertRecordParams
// has deliberately no field for it (SCHEMA.md invariant 2's pattern for
// tier), so unmarshaling a wire-shaped JSON payload that names
// "event_cursor" — the same shape a hostile or buggy MCP client could send
// over the note tool — drops it silently, and InsertRecord still computes
// the real cursor from the timeline, not the caller's number.
func TestInsertRecordEventCursorNotCallerSupplied(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: "probe", SessionID: sessionID, Source: "shell", Payload: "{}"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	wireRequest := []byte(`{"kind":"note","text":"a note","session_id":"` + sessionID + `","project_key":"proj-a","event_cursor":999}`)
	var p InsertRecordParams
	if err := json.Unmarshal(wireRequest, &p); err != nil {
		t.Fatalf("unmarshal wire-shaped request: %v", err)
	}
	p.Identity = Identity{Kind: IdentityAgent}
	p.Kind = KindNote
	p.Text = "a note"
	p.SessionID = sessionID
	p.ProjectKey = "proj-a"

	id, err := s.InsertRecord(p)
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	rec, err := s.GetRecord(id)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.EventCursor == 999 {
		t.Fatal("event_cursor took the caller-supplied 999 — InsertRecordParams must not accept it as a field")
	}
	if rec.EventCursor != 1 {
		t.Errorf("EventCursor = %d, want 1 (the true count of timeline events at insert time)", rec.EventCursor)
	}
}

// TestRecordsForProjectAllOrdersNewestFirstAndIncludesTombstoned checks
// internal/recall's project-anchor query: every record for the project
// (any kind), newest first by sequence, including a tombstoned one that
// RecordsForProject (kind-scoped, tombstone-excluding) would never return.
func TestRecordsForProjectAllOrdersNewestFirstAndIncludesTombstoned(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	mustUpsertProject(t, s, "proj-b")
	sessionA := mustStartSessionInProject(t, s, "proj-a")
	sessionB := mustStartSessionInProject(t, s, "proj-b")

	first := mustInsertNote(t, s, sessionA, "proj-a", "first")
	second, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindDecision, Text: "second",
		SessionID: sessionA, ProjectKey: "proj-a",
	})
	if err != nil {
		t.Fatalf("InsertRecord second: %v", err)
	}
	if err := s.TombstoneRecord(second, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatalf("TombstoneRecord: %v", err)
	}
	third := mustInsertNote(t, s, sessionA, "proj-a", "third")
	_ = mustInsertNote(t, s, sessionB, "proj-b", "other project, must not appear")

	recs, err := s.RecordsForProjectAll("proj-a", 10)
	if err != nil {
		t.Fatalf("RecordsForProjectAll: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("RecordsForProjectAll returned %d records, want 3", len(recs))
	}
	gotIDs := []string{recs[0].ID, recs[1].ID, recs[2].ID}
	wantIDs := []string{third, second, first}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Errorf("RecordsForProjectAll[%d] = %q, want %q (newest-first by sequence): got order %v, want %v",
				i, gotIDs[i], wantIDs[i], gotIDs, wantIDs)
		}
	}
	if recs[1].TombstonedAt == nil {
		t.Errorf("RecordsForProjectAll[1] (id %s) TombstonedAt = nil, want set (it was tombstoned)", recs[1].ID)
	}
}

// TestRecordsByIDsOrdersNewestFirstAndSkipsMissing checks internal/recall's
// edge-walk resolution: an arbitrary set of ids comes back ordered newest
// first by sequence (never ts), and an id naming no record is silently
// skipped rather than erroring.
func TestRecordsByIDsOrdersNewestFirstAndSkipsMissing(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	a := mustInsertNote(t, s, sessionID, "proj-a", "a")
	b := mustInsertNote(t, s, sessionID, "proj-a", "b")
	c := mustInsertNote(t, s, sessionID, "proj-a", "c")

	recs, err := s.RecordsByIDs([]string{a, "does-not-exist", c, b})
	if err != nil {
		t.Fatalf("RecordsByIDs: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("RecordsByIDs returned %d records, want 3 (the missing id skipped)", len(recs))
	}
	gotIDs := []string{recs[0].ID, recs[1].ID, recs[2].ID}
	wantIDs := []string{c, b, a}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Errorf("RecordsByIDs order = %v, want %v (newest-first by sequence)", gotIDs, wantIDs)
		}
	}
}

// TestFindRecordByIDPrefix covers an exact id match, a unique short prefix,
// an ambiguous prefix (reported not-found, never a guess), and no match.
func TestFindRecordByIDPrefix(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	unique := mustInsertNote(t, s, sessionID, "proj-a", "unique-prefixed record")

	t.Run("exact id", func(t *testing.T) {
		rec, ok, err := s.FindRecordByIDPrefix(unique)
		if err != nil {
			t.Fatalf("FindRecordByIDPrefix: %v", err)
		}
		if !ok || rec.ID != unique {
			t.Fatalf("FindRecordByIDPrefix(%s) = %+v, ok=%v, want the exact record", unique, rec, ok)
		}
	})

	t.Run("unique short prefix", func(t *testing.T) {
		prefix := unique[:8]
		rec, ok, err := s.FindRecordByIDPrefix(prefix)
		if err != nil {
			t.Fatalf("FindRecordByIDPrefix: %v", err)
		}
		if !ok || rec.ID != unique {
			t.Fatalf("FindRecordByIDPrefix(%s) = %+v, ok=%v, want the record it uniquely prefixes", prefix, rec, ok)
		}
	})

	t.Run("ambiguous prefix reports not found", func(t *testing.T) {
		// Manufacture a second id sharing unique's first 8 characters so the
		// prefix no longer resolves to exactly one record.
		shared := unique[:8] + "ffffffff-ffff-ffff-ffff-ffffffffffff"
		if _, err := s.db.Exec(`INSERT INTO records
			(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at)
			VALUES (?, ?, ?, ?, 'collider', '[]', ?, ?, '[]', NULL, NULL, NULL)`,
			shared, tsToNanos(time.Now()), string(KindNote), string(TierAgentDeclared), sessionID, "proj-a"); err != nil {
			t.Fatalf("insert colliding record fixture: %v", err)
		}

		_, ok, err := s.FindRecordByIDPrefix(unique[:8])
		if err != nil {
			t.Fatalf("FindRecordByIDPrefix: %v", err)
		}
		if ok {
			t.Fatalf("FindRecordByIDPrefix(%s) found=true with two matching records, want false (ambiguous)", unique[:8])
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, ok, err := s.FindRecordByIDPrefix("no-such-id-prefix")
		if err != nil {
			t.Fatalf("FindRecordByIDPrefix: %v", err)
		}
		if ok {
			t.Fatal("FindRecordByIDPrefix with no matching record found=true, want false")
		}
	})

	t.Run("empty string never matches everything", func(t *testing.T) {
		_, ok, err := s.FindRecordByIDPrefix("")
		if err != nil {
			t.Fatalf("FindRecordByIDPrefix: %v", err)
		}
		if ok {
			t.Fatal(`FindRecordByIDPrefix("") found=true, want false`)
		}
	})
}

// mustInsertDraft inserts a records row directly with a caller-chosen tier,
// promoter and expiry — InsertRecord derives tier from Identity and never
// accepts an inferred tier or a promoter directly (AGENT-CONTRACT.md §The
// never-list), so this test bypasses it on purpose, the same way ts_test.go
// bypasses InsertRecord for a caller-chosen ts.
func mustInsertDraft(t *testing.T, s *Store, projectKey string, tier Tier, promoter string, expiresAt *time.Time) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at)
		VALUES (lower(hex(randomblob(16))), ?, ?, ?, 'draft probe', '[]', NULL, ?, '[]', NULL, ?, ?)`,
		tsToNanos(time.Now()), string(KindNote), string(tier), projectKey, nullable(promoter), nullableTS(expiresAt))
	if err != nil {
		t.Fatalf("insert draft fixture: %v", err)
	}
}
