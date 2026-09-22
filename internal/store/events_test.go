package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestEventsSinceIDOrdersByRowIDNotTS is the punch's DONE WHEN clause 4
// (SCHEMA.md invariant 10): backfilled events inserted with wall-clock ts
// that LIE — a later-inserted row (higher rowid) carries an EARLIER ts than
// an earlier-inserted row — must still be read back in rowid (insertion)
// order, never ts order. A ts-ordered read would reverse these two.
func TestEventsSinceIDOrdersByRowIDNotTS(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	base := time.Now()
	firstID, err := s.AppendEvent(Event{TS: base, Kind: "a", SessionID: sessionID, Source: "backfill", Payload: "{}"})
	if err != nil {
		t.Fatalf("AppendEvent first: %v", err)
	}
	secondID, err := s.AppendEvent(Event{
		TS: base.Add(-time.Hour), Kind: "b", SessionID: sessionID, Source: "backfill", Payload: "{}",
	})
	if err != nil {
		t.Fatalf("AppendEvent second: %v", err)
	}

	events, err := s.EventsSinceID("proj-a", 0)
	if err != nil {
		t.Fatalf("EventsSinceID: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("EventsSinceID returned %d events, want 2", len(events))
	}
	if events[0].ID != firstID || events[1].ID != secondID {
		t.Fatalf("EventsSinceID order = [%d, %d], want [%d, %d] (rowid order; ts order would reverse them)",
			events[0].ID, events[1].ID, firstID, secondID)
	}
}

// TestEventsSinceIDExcludesAtOrBeforeSinceID checks the boundary: an event
// whose id equals sinceID is excluded, one strictly greater is included.
func TestEventsSinceIDExcludesAtOrBeforeSinceID(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	boundaryID, err := s.AppendEvent(Event{TS: time.Now(), Kind: "a", SessionID: sessionID, Source: "shell", Payload: "{}"})
	if err != nil {
		t.Fatalf("AppendEvent boundary: %v", err)
	}
	afterID, err := s.AppendEvent(Event{TS: time.Now(), Kind: "b", SessionID: sessionID, Source: "shell", Payload: "{}"})
	if err != nil {
		t.Fatalf("AppendEvent after: %v", err)
	}

	events, err := s.EventsSinceID("proj-a", boundaryID)
	if err != nil {
		t.Fatalf("EventsSinceID: %v", err)
	}
	if len(events) != 1 || events[0].ID != afterID {
		t.Fatalf("EventsSinceID(proj-a, %d) = %v, want exactly [%d]", boundaryID, events, afterID)
	}
}

// TestEventsSinceIDScopesToProject makes sure an event whose session
// belongs to a different project is never returned, even with sinceID == 0.
func TestEventsSinceIDScopesToProject(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	mustUpsertProject(t, s, "proj-b")
	sessionA := mustStartSessionInProject(t, s, "proj-a")
	sessionB := mustStartSessionInProject(t, s, "proj-b")

	wantID, err := s.AppendEvent(Event{TS: time.Now(), Kind: "a", SessionID: sessionA, Source: "shell", Payload: "{}"})
	if err != nil {
		t.Fatalf("AppendEvent proj-a: %v", err)
	}
	if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: "b", SessionID: sessionB, Source: "shell", Payload: "{}"}); err != nil {
		t.Fatalf("AppendEvent proj-b: %v", err)
	}

	events, err := s.EventsSinceID("proj-a", 0)
	if err != nil {
		t.Fatalf("EventsSinceID: %v", err)
	}
	if len(events) != 1 || events[0].ID != wantID {
		t.Fatalf("EventsSinceID(proj-a, 0) = %v, want exactly [%d]", events, wantID)
	}
}

func mustStartSessionInProject(t *testing.T, s *Store, projectKey string) string {
	t.Helper()
	id, err := s.StartSession(StartSessionParams{
		Agent: "claude", CWD: "/proj", ProjectKey: projectKey, StartedAt: time.Now(), Origin: OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return id
}
