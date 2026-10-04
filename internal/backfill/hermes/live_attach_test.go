package hermes

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

func countRows(t *testing.T, st *store.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// A live session carrying harness session id S: the backfill attaches S's
// events to it and mints no second session; a session with no live match is
// still imported as its own backfilled session.
func TestImportAttachesToLiveSessionWithSameHarnessSessionID(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ended := t0.Add(10 * time.Minute)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	newFixtureDB(t, dbPath,
		[]fixtureSession{
			{id: "S", cwd: "/work/repo", startedAt: t0, endedAt: &ended},
			{id: "other", cwd: "/work/repo", startedAt: t0, endedAt: &ended},
		},
		[]fixtureMessage{
			{sessionID: "S", role: "user", content: "hi", ts: t0, active: true},
			{sessionID: "S", role: "assistant",
				toolCalls: `[{"id":"call_S","name":"Bash","arguments":{"command":"ls"}}]`,
				ts:        t0.Add(time.Second), active: true},
			{sessionID: "other", role: "user", content: "yo", ts: t0, active: true},
		})

	st := mustOpenStore(t)
	if err := st.UpsertProject(store.Project{Key: "/work/repo", Toplevel: "/work/repo", FirstSeen: t0}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	liveID, err := st.StartSession(store.StartSessionParams{
		Agent: Agent, HarnessSessionID: "S", CWD: "/work/repo", ProjectKey: "/work/repo",
		StartedAt: t0, Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	res, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.SessionsCreated != 1 {
		t.Errorf("SessionsCreated = %d, want 1 (only the unmatched session)", res.SessionsCreated)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'S'`); n != 1 {
		t.Errorf("sessions for S = %d, want 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM timeline_events WHERE session_id = ? AND kind = 'tool.use'`, liveID); n != 1 {
		t.Errorf("tool.use events on live session = %d, want 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM sessions WHERE id = ? AND ended_at IS NULL`, liveID); n != 1 {
		t.Errorf("live session was ended by the backfill")
	}
	other, ok, err := st.SessionByHarnessSessionID("other")
	if err != nil || !ok || other.Origin != store.OriginBackfilled {
		t.Fatalf("unmatched session: ok=%v err=%v origin=%v, want own backfilled session", ok, err, other.Origin)
	}

	// Rerun: still exactly one session for S, no duplicate tool.use.
	if _, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatalf("re-Import: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'S'`); n != 1 {
		t.Errorf("after rerun sessions for S = %d, want 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM timeline_events WHERE session_id = ? AND kind = 'tool.use'`, liveID); n != 1 {
		t.Errorf("after rerun tool.use events = %d, want 1", n)
	}
}
