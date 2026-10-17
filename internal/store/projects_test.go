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

func mustInsertProjectRecord(t *testing.T, s *Store, key, text string, edges ...EdgeSpec) string {
	t.Helper()
	id, err := s.InsertRecordWithEdges(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent, Actor: "seed"}, Kind: KindNote,
		Text: text, ProjectKey: key,
	}, edges)
	if err != nil {
		t.Fatalf("InsertRecord(%s): %v", text, err)
	}
	return id
}

func summaryFor(t *testing.T, s *Store, key string) ProjectSummary {
	t.Helper()
	sums, err := s.ProjectSummaries()
	if err != nil {
		t.Fatal(err)
	}
	for _, ps := range sums {
		if ps.Key == key {
			return ps
		}
	}
	t.Fatalf("project %s missing from summaries %+v", key, sums)
	return ProjectSummary{}
}

// TestProjectSummariesFullyPurgedProjectReadsEmpty: after a full purge the
// row stays but shows 0 sessions, 0 records and no last activity.
//
// RA-MUTATION-PROBE: the purged_at filter dropped from the sessions count,
// from last_ts, or the tombstoned_at filter dropped -> RED.
func TestProjectSummariesFullyPurgedProjectReadsEmpty(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	seedProjectPurgeSession(t, s, "proj-k", 1)
	seedProjectPurgeSession(t, s, "proj-k", 1)
	mustInsertProjectRecord(t, s, "proj-k", "one")
	mustInsertProjectRecord(t, s, "proj-k", "two")
	if got := summaryFor(t, s, "proj-k"); got.Sessions != 2 || got.Records != 2 || got.LastActivity.IsZero() {
		t.Fatalf("before purge = %+v", got)
	}
	if _, err := s.PurgeSessions(PurgeScope{ProjectKey: "proj-k"}, Identity{Kind: IdentityHuman}); err != nil {
		t.Fatal(err)
	}
	got := summaryFor(t, s, "proj-k")
	if got.Sessions != 0 || got.Records != 0 || !got.LastActivity.IsZero() {
		t.Errorf("after purge = %+v, want 0 sessions, 0 records, zero last activity", got)
	}
}

// TestProjectSummariesPartialPurgeCountsRemainder: one session purged by id
// (the newest) and one record tombstoned; a superseded record still counts
// and last activity is the latest unpurged session start.
//
// RA-MUTATION-PROBE: each filter dropped, or records filtered on current
// status instead of tombstones -> RED.
func TestProjectSummariesPartialPurgeCountsRemainder(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	human := Identity{Kind: IdentityHuman}
	if err := s.UpsertProject(Project{Key: "proj-p", Toplevel: "/x", FirstSeen: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, day := range []int{2, 3, 5} {
		id, err := s.StartSession(StartSessionParams{
			Agent: "claude", CWD: "/x", ProjectKey: "proj-p",
			StartedAt: time.Date(2026, 1, day, 9, 0, 0, 0, time.UTC), Origin: OriginLive,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	old := mustInsertProjectRecord(t, s, "proj-p", "old")
	mustInsertProjectRecord(t, s, "proj-p", "new", EdgeSpec{OtherID: old, Type: EdgeSupersedes, DeclaredBy: "seed"})
	doomed := mustInsertProjectRecord(t, s, "proj-p", "doomed")
	if _, err := s.PurgeSessions(PurgeScope{SessionID: ids[2]}, human); err != nil {
		t.Fatal(err)
	}
	if err := s.TombstoneRecord(doomed, human); err != nil {
		t.Fatal(err)
	}
	got := summaryFor(t, s, "proj-p")
	want := time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC)
	if got.Sessions != 2 || got.Records != 2 || !got.LastActivity.Equal(want) {
		t.Errorf("summary = %+v, want 2 sessions, 2 records (superseded counts), last %s", got, want)
	}
}
