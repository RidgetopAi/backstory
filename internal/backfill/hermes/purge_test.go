package hermes

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

var purgeHuman = store.Identity{Kind: store.IdentityHuman, Actor: "human"}

func purgeFixture(t *testing.T) (string, time.Time) {
	t.Helper()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ended := t0.Add(10 * time.Minute)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	newFixtureDB(t, dbPath,
		[]fixtureSession{
			{id: "H", cwd: "/work/repo", startedAt: t0, endedAt: &ended},
			{id: "other", cwd: "/work/repo", startedAt: t0, endedAt: &ended},
		},
		[]fixtureMessage{
			{sessionID: "H", role: "user", content: "hi", ts: t0, active: true},
			{sessionID: "H", role: "assistant",
				toolCalls: `[{"id":"call_H","name":"Bash","arguments":{"command":"ls"}}]`,
				ts:        t0.Add(time.Second), active: true},
			{sessionID: "other", role: "user", content: "yo", ts: t0, active: true},
		})
	return dbPath, t0
}

func liveHermesSession(t *testing.T, st *store.Store, t0 time.Time) string {
	t.Helper()
	if err := st.UpsertProject(store.Project{Key: "/work/repo", Toplevel: "/work/repo", FirstSeen: t0}); err != nil {
		t.Fatal(err)
	}
	id, err := st.StartSession(store.StartSessionParams{
		Agent: Agent, HarnessSessionID: "H", CWD: "/work/repo", ProjectKey: "/work/repo",
		StartedAt: t0, Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A live-captured, never-backfilled session that was purged stays empty
// through the first backfill and a second one; no session is minted for it;
// another session in the same state.db still imports.
//
// RA-MUTATION-PROBE: the purge check removed from importSession -> RED.
func TestBackfillDoesNotReimportPurgedLiveSession(t *testing.T) {
	dbPath, t0 := purgeFixture(t)
	st := mustOpenStore(t)
	id := liveHermesSession(t, st, t0)
	if _, err := st.PurgeSessions(store.PurgeScope{SessionID: id}, purgeHuman); err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if n := countRows(t, st, `SELECT COUNT(*) FROM timeline_events WHERE session_id = ?`, id); n != 0 {
			t.Errorf("run %d: purged session has %d events, want 0", run, n)
		}
		if n := countRows(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'H'`); n != 1 {
			t.Errorf("run %d: %d sessions for H, want 1 (the purged one)", run, n)
		}
		if n := countRows(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'other'`); n != 1 {
			t.Errorf("run %d: unrelated session not imported", run)
		}
	}
}

// Positive control: the same flow without the purge attaches and imports.
func TestBackfillAttachesToUnpurgedLiveSession(t *testing.T) {
	dbPath, t0 := purgeFixture(t)
	st := mustOpenStore(t)
	id := liveHermesSession(t, st, t0)
	if _, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM timeline_events WHERE session_id = ?`, id); n == 0 {
		t.Errorf("unpurged live session got no events")
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'H'`); n != 1 {
		t.Errorf("%d sessions for H, want 1", n)
	}
}
