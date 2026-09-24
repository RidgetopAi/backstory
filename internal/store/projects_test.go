package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestProjectsSeenSinceFiltersByRecencyAndDeduplicates is proof that
// ProjectsSeenSince reads recency off sessions.started_at, not
// projects.first_seen: a project with an old session only is excluded, one
// with a recent session is included exactly once even with multiple recent
// sessions, and a session with no project_key is ignored.
func TestProjectsSeenSinceFiltersByRecencyAndDeduplicates(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	seedGroupTestProjects(t, s, "proj-recent", "proj-old", "proj-multi")

	now := time.Now()
	weekAgo := now.Add(-7 * 24 * time.Hour)

	mustStartSessionForProject(t, s, "proj-recent", now.Add(-1*time.Hour))
	mustStartSessionForProject(t, s, "proj-old", weekAgo.Add(-48*time.Hour))
	mustStartSessionForProject(t, s, "proj-multi", now.Add(-2*time.Hour))
	mustStartSessionForProject(t, s, "proj-multi", now.Add(-3*time.Hour))
	// A session with no project_key at all must never surface as a seen key.
	if _, err := s.StartSession(StartSessionParams{
		Agent: "claude", CWD: "/tmp/no-project", StartedAt: now, Origin: OriginLive,
	}); err != nil {
		t.Fatalf("StartSession(no project): %v", err)
	}

	got, err := s.ProjectsSeenSince(weekAgo)
	if err != nil {
		t.Fatalf("ProjectsSeenSince: %v", err)
	}
	want := []string{"proj-multi", "proj-recent"}
	if len(got) != len(want) {
		t.Fatalf("ProjectsSeenSince(weekAgo) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ProjectsSeenSince(weekAgo) = %v, want %v", got, want)
		}
	}
}

// mustStartSessionForProject starts a session for projectKey at startedAt,
// failing the test on error.
func mustStartSessionForProject(t *testing.T, s *Store, projectKey string, startedAt time.Time) {
	t.Helper()
	if _, err := s.StartSession(StartSessionParams{
		Agent:      "claude",
		CWD:        "/tmp/" + projectKey,
		ProjectKey: projectKey,
		StartedAt:  startedAt,
		Origin:     OriginLive,
	}); err != nil {
		t.Fatalf("StartSession(%s): %v", projectKey, err)
	}
}
