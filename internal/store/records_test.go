package store

import (
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
