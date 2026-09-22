package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInsertRecordUnderCapsSucceeds(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	sessionID := mustStartSession(t, s)

	id, err := s.InsertRecord(InsertRecordParams{
		Identity:  Identity{Kind: IdentityAgent},
		Kind:      KindNote,
		Text:      "well under both caps",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("InsertRecord under caps: %v", err)
	}
	if id == "" {
		t.Fatal("InsertRecord under caps returned empty id")
	}
}

func TestInsertRecordExceedingTextBytesCapIsRefused(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	sessionID := mustStartSession(t, s)

	text := strings.Repeat("a", MaxRecordTextBytes+1)
	_, err := s.InsertRecord(InsertRecordParams{
		Identity:  Identity{Kind: IdentityAgent},
		Kind:      KindNote,
		Text:      text,
		SessionID: sessionID,
	})

	var capErr *CapError
	if !errors.As(err, &capErr) {
		t.Fatalf("InsertRecord over the text-bytes cap returned %v, want *CapError", err)
	}
	if capErr.Cap != CapMaxTextBytes {
		t.Errorf("CapError.Cap = %q, want %q", capErr.Cap, CapMaxTextBytes)
	}

	assertNoRecordsForSession(t, s, sessionID)
}

func TestInsertRecordExceedingRateCapIsRefused(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	sessionID := mustStartSession(t, s)

	for i := 0; i < MaxRecordsPerSessionPerMinute; i++ {
		if _, err := s.InsertRecord(InsertRecordParams{
			Identity:  Identity{Kind: IdentityAgent},
			Kind:      KindNote,
			Text:      "filling the rate cap",
			SessionID: sessionID,
		}); err != nil {
			t.Fatalf("InsertRecord %d/%d filling the cap: %v", i+1, MaxRecordsPerSessionPerMinute, err)
		}
	}

	before := countRecordsForSession(t, s, sessionID)

	_, err := s.InsertRecord(InsertRecordParams{
		Identity:  Identity{Kind: IdentityAgent},
		Kind:      KindNote,
		Text:      "one over the rate cap",
		SessionID: sessionID,
	})

	var capErr *CapError
	if !errors.As(err, &capErr) {
		t.Fatalf("InsertRecord over the rate cap returned %v, want *CapError", err)
	}
	if capErr.Cap != CapRecordsPerMinute {
		t.Errorf("CapError.Cap = %q, want %q", capErr.Cap, CapRecordsPerMinute)
	}

	after := countRecordsForSession(t, s, sessionID)
	if after != before {
		t.Errorf("record count changed from %d to %d on a refused write, want unchanged", before, after)
	}
}

func mustStartSession(t *testing.T, s *Store) string {
	t.Helper()
	id, err := s.StartSession(StartSessionParams{
		Agent:     "test-harness",
		CWD:       "/tmp/project",
		StartedAt: time.Now(),
		Origin:    OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return id
}

func countRecordsForSession(t *testing.T, s *Store, sessionID string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM records WHERE session_id = ?`, sessionID).Scan(&n); err != nil {
		t.Fatalf("count records for session: %v", err)
	}
	return n
}

func assertNoRecordsForSession(t *testing.T, s *Store, sessionID string) {
	t.Helper()
	if n := countRecordsForSession(t, s, sessionID); n != 0 {
		t.Errorf("records for session = %d, want 0 after a refused insert", n)
	}
}
