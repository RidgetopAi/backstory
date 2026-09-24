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
//
// Mutation probe (EventsSinceID's `ORDER BY e.id ASC` changed to
// `ORDER BY e.ts ASC`, events.go): "events_test.go:39: EventsSinceID order =
// [2, 1], want [1, 2] (rowid order; ts order would reverse them)" --
// restoring `ORDER BY e.id ASC` turns it back GREEN.
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

// TestEventsForTimelineOrdersBySequenceEvenWhenTSDisagree is `backstory
// timeline`'s DONE WHEN clause 2 (SCHEMA.md invariant 10): two events that
// both pass the --since bound must come back in sequence (insertion) order,
// never resorted by ts, even though the second one inserted here carries an
// EARLIER ts than the first (a backfilled session's file clock lying).
//
// Mutation probe (EventsForTimeline's `ORDER BY e.id ASC` on the outer
// query changed to `ORDER BY ts ASC`, events.go): "events_test.go:...:
// EventsForTimeline order = [second, first], want [first, second] (sequence
// order; ts order would reverse them)" — restoring `ORDER BY id ASC` turns
// it back GREEN.
func TestEventsForTimelineOrdersBySequenceEvenWhenTSDisagree(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	base := time.Now()
	first, err := s.AppendEvent(Event{TS: base.Add(2 * time.Minute), Kind: "tool.use", SessionID: sessionID, Source: "shell", Payload: "{}"})
	if err != nil {
		t.Fatalf("AppendEvent first: %v", err)
	}
	second, err := s.AppendEvent(Event{TS: base.Add(time.Minute), Kind: "tool.use", SessionID: sessionID, Source: "backfill", Payload: "{}"})
	if err != nil {
		t.Fatalf("AppendEvent second: %v", err)
	}

	events, err := s.EventsForTimeline("proj-a", base, "", 0)
	if err != nil {
		t.Fatalf("EventsForTimeline: %v", err)
	}
	if len(events) != 2 || events[0].ID != first || events[1].ID != second {
		t.Fatalf("EventsForTimeline order = %v, want [%d, %d] (sequence order; ts order would reverse them)",
			events, first, second)
	}
}

// TestEventsForTimelineExcludesBeforeSinceBound checks the --since filter
// itself: an event with ts strictly before the bound is dropped, one at or
// after it is kept.
func TestEventsForTimelineExcludesBeforeSinceBound(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	base := time.Now()
	if _, err := s.AppendEvent(Event{TS: base.Add(-time.Hour), Kind: "a", SessionID: sessionID, Source: "shell", Payload: "{}"}); err != nil {
		t.Fatalf("AppendEvent before bound: %v", err)
	}
	atBound, err := s.AppendEvent(Event{TS: base, Kind: "a", SessionID: sessionID, Source: "shell", Payload: "{}"})
	if err != nil {
		t.Fatalf("AppendEvent at bound: %v", err)
	}

	events, err := s.EventsForTimeline("proj-a", base, "", 0)
	if err != nil {
		t.Fatalf("EventsForTimeline: %v", err)
	}
	if len(events) != 1 || events[0].ID != atBound {
		t.Fatalf("EventsForTimeline(proj-a, base, \"\", 0) = %v, want exactly [%d]", events, atBound)
	}
}

// TestEventsForTimelineFiltersByKind checks the --kind filter: an event of
// a different kind is excluded even though it passes the since bound.
func TestEventsForTimelineFiltersByKind(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: "tool.result", SessionID: sessionID, Source: "shell", Payload: "{}"}); err != nil {
		t.Fatalf("AppendEvent tool.result: %v", err)
	}
	wantID, err := s.AppendEvent(Event{TS: time.Now(), Kind: "tool.use", SessionID: sessionID, Source: "shell", Payload: "{}"})
	if err != nil {
		t.Fatalf("AppendEvent tool.use: %v", err)
	}

	events, err := s.EventsForTimeline("proj-a", time.Time{}, "tool.use", 0)
	if err != nil {
		t.Fatalf("EventsForTimeline: %v", err)
	}
	if len(events) != 1 || events[0].ID != wantID {
		t.Fatalf(`EventsForTimeline(proj-a, zero, "tool.use", 0) = %v, want exactly [%d]`, events, wantID)
	}
}

// TestEventsForTimelineLimitKeepsMostRecentInSequenceOrder checks --limit:
// it keeps the most recent N events by sequence, still returned oldest-to-
// newest.
func TestEventsForTimelineLimitKeepsMostRecentInSequenceOrder(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	sessionID := mustStartSessionInProject(t, s, "proj-a")

	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := s.AppendEvent(Event{TS: time.Now(), Kind: "a", SessionID: sessionID, Source: "shell", Payload: "{}"})
		if err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	events, err := s.EventsForTimeline("proj-a", time.Time{}, "", 2)
	if err != nil {
		t.Fatalf("EventsForTimeline: %v", err)
	}
	if len(events) != 2 || events[0].ID != ids[1] || events[1].ID != ids[2] {
		t.Fatalf("EventsForTimeline(proj-a, zero, \"\", 2) = %v, want exactly [%d, %d]", events, ids[1], ids[2])
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
