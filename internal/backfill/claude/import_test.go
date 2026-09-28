package claude

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// fakeGit never matches any cwd, so project.Key falls back to returning cwd
// itself for every resolved path in these fixtures (none of testdata is a
// real git working tree) — the same fallback AGENT-CONTRACT.md names for a
// non-git directory.
type fakeGit struct{}

func (fakeGit) Repo(string) (project.Repo, bool) { return project.Repo{}, false }

// State is unused by this package's tests but required to satisfy
// project.Git.
func (fakeGit) State(string) (project.State, bool) { return project.State{}, false }

func mustOpenStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

type sessionRow struct {
	ID               string
	Agent            string
	HarnessSessionID sql.NullString
	PID              sql.NullInt64
	CWD              string
	ProjectKey       sql.NullString
	StartedAt        int64
	EndedAt          sql.NullInt64
	Origin           string
}

func sessionByHarnessID(t *testing.T, st *store.Store, harnessID string) sessionRow {
	t.Helper()
	var r sessionRow
	err := st.DB().QueryRow(`SELECT id, agent, harness_session_id, pid, cwd, project_key, started_at, ended_at, origin
		FROM sessions WHERE harness_session_id = ?`, harnessID).
		Scan(&r.ID, &r.Agent, &r.HarnessSessionID, &r.PID, &r.CWD, &r.ProjectKey, &r.StartedAt, &r.EndedAt, &r.Origin)
	if err != nil {
		t.Fatalf("query session harness_session_id=%s: %v", harnessID, err)
	}
	return r
}

func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm.UTC()
}

// TestImportCreatesOneSessionPerFile is the punch's clause 1: three
// transcripts across two projects — one whose cwd's path contains a dash
// (an ambiguous slug), one whose file carries no cwd on any line at all —
// each becomes exactly one backfilled session, correctly identified.
func TestImportCreatesOneSessionPerFile(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "projects")

	res, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.FilesScanned != 3 {
		t.Errorf("FilesScanned = %d, want 3", res.FilesScanned)
	}
	if res.SessionsCreated != 3 {
		t.Errorf("SessionsCreated = %d, want 3", res.SessionsCreated)
	}

	var totalSessions int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&totalSessions); err != nil {
		t.Fatal(err)
	}
	if totalSessions != 3 {
		t.Fatalf("sessions table has %d rows, want exactly 3", totalSessions)
	}

	// sess-a: cwd field present, and its path contains a dash ("my-app")
	// that would be indistinguishable from the slug's own separator —
	// proves the importer used cwd, not a naive slug->path reversal
	// (which would have produced /home/alice/my/app).
	a := sessionByHarnessID(t, st, "sess-a-uuid")
	if a.Agent != "claude" {
		t.Errorf("sess-a agent = %q, want claude", a.Agent)
	}
	if a.Origin != "backfilled" {
		t.Errorf("sess-a origin = %q, want backfilled", a.Origin)
	}
	if a.CWD != "/home/alice/my-app" {
		t.Errorf("sess-a cwd = %q, want /home/alice/my-app (dash preserved)", a.CWD)
	}
	if !a.ProjectKey.Valid || a.ProjectKey.String != "/home/alice/my-app" {
		t.Errorf("sess-a project_key = %v, want /home/alice/my-app", a.ProjectKey)
	}
	if a.PID.Valid {
		t.Errorf("sess-a pid = %v, want NULL", a.PID)
	}
	wantStart := mustParseRFC3339(t, "2026-01-01T00:00:00Z")
	wantEnd := mustParseRFC3339(t, "2026-01-01T00:00:10Z")
	if got := time.Unix(0, a.StartedAt).UTC(); !got.Equal(wantStart) {
		t.Errorf("sess-a started_at = %s, want %s", got, wantStart)
	}
	if !a.EndedAt.Valid || !time.Unix(0, a.EndedAt.Int64).UTC().Equal(wantEnd) {
		t.Errorf("sess-a ended_at = %v, want %s", a.EndedAt, wantEnd)
	}

	// sess-b: normal cwd, no ambiguity — the project sess-c's slug fallback
	// must land on the same key.
	b := sessionByHarnessID(t, st, "sess-b-uuid")
	if !b.ProjectKey.Valid || b.ProjectKey.String != "/home/bob/widgets" {
		t.Errorf("sess-b project_key = %v, want /home/bob/widgets", b.ProjectKey)
	}

	// sess-c: no cwd on any line — project_key must come from the slug
	// (-home-bob-widgets -> /home/bob/widgets), landing on sess-b's key.
	c := sessionByHarnessID(t, st, "sess-c-uuid")
	if c.CWD != "/home/bob/widgets" {
		t.Errorf("sess-c cwd (from slug fallback) = %q, want /home/bob/widgets", c.CWD)
	}
	if !c.ProjectKey.Valid || c.ProjectKey.String != "/home/bob/widgets" {
		t.Errorf("sess-c project_key = %v, want /home/bob/widgets (same as sess-b)", c.ProjectKey)
	}
	if c.PID.Valid {
		t.Errorf("sess-c pid = %v, want NULL", c.PID)
	}

	// None of the three imported sessions are live: LiveSessionsInProject
	// must not list any of them (SCHEMA.md: ended_at IS NULL is "live").
	live, err := st.LiveSessionsInProject("/home/alice/my-app")
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Errorf("LiveSessionsInProject(/home/alice/my-app) = %v, want none", live)
	}
	live, err = st.LiveSessionsInProject("/home/bob/widgets")
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Errorf("LiveSessionsInProject(/home/bob/widgets) = %v, want none (sess-b and sess-c both backfilled)", live)
	}
}
