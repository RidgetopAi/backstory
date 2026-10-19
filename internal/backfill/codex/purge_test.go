package codex

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

var purgeHuman = store.Identity{Kind: store.IdentityHuman, Actor: "human"}

func scalar(t *testing.T, st *store.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// liveCodexSession captures harness id H live (no backfill cursor).
func liveCodexSession(t *testing.T, st *store.Store, h, cwd string) string {
	t.Helper()
	if err := st.UpsertProject(store.Project{Key: cwd, Toplevel: cwd}); err != nil {
		t.Fatal(err)
	}
	id, err := st.StartSession(store.StartSessionParams{
		Agent: Agent, HarnessSessionID: h, CWD: cwd, ProjectKey: cwd,
		StartedAt: time.Date(2026, 1, 16, 9, 0, 0, 0, time.UTC), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A live-captured, never-backfilled session that was purged stays empty
// through the first backfill and a second one; no session is minted for it;
// another thread in the same root still imports.
//
// RA-MUTATION-PROBE: capture.RefusePurgedRun removed from importFile -> RED.
func TestBackfillDoesNotReimportPurgedLiveSession(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "sessions")
	id := liveCodexSession(t, st, "other-thread-uuid", "/home/erin/other-proj")
	if _, err := st.PurgeSessions(store.PurgeScope{SessionID: id}, purgeHuman); err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if n := scalar(t, st, `SELECT COUNT(*) FROM timeline_events WHERE session_id = ?`, id); n != 0 {
			t.Errorf("run %d: purged session has %d events, want 0", run, n)
		}
		if n := scalar(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'other-thread-uuid'`); n != 1 {
			t.Errorf("run %d: %d sessions for H, want 1 (the purged one)", run, n)
		}
		if n := scalar(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'main-thread-uuid'`); n != 1 {
			t.Errorf("run %d: unrelated thread not imported", run)
		}
	}
}

// Positive control: the same flow without the purge attaches and imports.
func TestBackfillAttachesToUnpurgedLiveSession(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "sessions")
	id := liveCodexSession(t, st, "other-thread-uuid", "/home/erin/other-proj")
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM timeline_events WHERE session_id = ?`, id); n == 0 {
		t.Errorf("unpurged live session got no events")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'other-thread-uuid'`); n != 1 {
		t.Errorf("%d sessions for H, want 1", n)
	}
}
