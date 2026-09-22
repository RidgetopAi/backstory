package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLiveSessionsInProjectExcludesEndedAndOtherProjects(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	mustUpsertProject(t, s, "proj-a")
	mustUpsertProject(t, s, "proj-b")

	live, err := s.StartSession(StartSessionParams{
		Agent: "claude", CWD: "/proj", ProjectKey: "proj-a", StartedAt: time.Now(), Origin: OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession live: %v", err)
	}
	ended, err := s.StartSession(StartSessionParams{
		Agent: "claude", CWD: "/proj", ProjectKey: "proj-a", StartedAt: time.Now(), Origin: OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession ended: %v", err)
	}
	if err := s.EndSession(ended, time.Now(), "exit"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if _, err := s.StartSession(StartSessionParams{
		Agent: "codex", CWD: "/other", ProjectKey: "proj-b", StartedAt: time.Now(), Origin: OriginLive,
	}); err != nil {
		t.Fatalf("StartSession other project: %v", err)
	}

	sessions, err := s.LiveSessionsInProject("proj-a")
	if err != nil {
		t.Fatalf("LiveSessionsInProject: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("LiveSessionsInProject(proj-a) = %d sessions, want 1", len(sessions))
	}
	if sessions[0].ID != live {
		t.Errorf("LiveSessionsInProject(proj-a)[0].ID = %q, want %q", sessions[0].ID, live)
	}
	if sessions[0].Agent != "claude" {
		t.Errorf("Agent = %q, want claude", sessions[0].Agent)
	}
}

func mustUpsertProject(t *testing.T, s *Store, key string) {
	t.Helper()
	if err := s.UpsertProject(Project{Key: key, Toplevel: key, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", key, err)
	}
}

func TestLiveSessionsInProjectEmptyForUnknownProject(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	sessions, err := s.LiveSessionsInProject("nope")
	if err != nil {
		t.Fatalf("LiveSessionsInProject: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("LiveSessionsInProject(nope) = %d sessions, want 0", len(sessions))
	}
}

func TestRemainingWriteBudgetDecreasesWithWritesAndFloorsAtZero(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	sessionID := mustStartSession(t, s)

	remaining, err := s.RemainingWriteBudget(sessionID)
	if err != nil {
		t.Fatalf("RemainingWriteBudget: %v", err)
	}
	if remaining != MaxRecordsPerSessionPerMinute {
		t.Fatalf("RemainingWriteBudget before any writes = %d, want %d", remaining, MaxRecordsPerSessionPerMinute)
	}

	if _, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: "one", SessionID: sessionID,
	}); err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	remaining, err = s.RemainingWriteBudget(sessionID)
	if err != nil {
		t.Fatalf("RemainingWriteBudget: %v", err)
	}
	if remaining != MaxRecordsPerSessionPerMinute-1 {
		t.Fatalf("RemainingWriteBudget after 1 write = %d, want %d", remaining, MaxRecordsPerSessionPerMinute-1)
	}
}

func TestRemainingWriteBudgetEmptySessionReportsFullCap(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	remaining, err := s.RemainingWriteBudget("")
	if err != nil {
		t.Fatalf("RemainingWriteBudget(\"\"): %v", err)
	}
	if remaining != MaxRecordsPerSessionPerMinute {
		t.Fatalf("RemainingWriteBudget(\"\") = %d, want %d", remaining, MaxRecordsPerSessionPerMinute)
	}
}
