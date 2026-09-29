package claude

import (
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

var purgeHuman = store.Identity{Kind: store.IdentityHuman, Actor: "human"}

func eventCountForHarness(t *testing.T, st *store.Store, harnessID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events e JOIN sessions s ON s.id = e.session_id
		WHERE s.harness_session_id = ?`, harnessID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestImportSkipsPurgedSession is DONE WHEN clause 4: after `purge`, a
// re-run of the importer over the same transcript (cursor present) creates
// zero events for the purged session, while a non-purged session still
// imports.
//
// RA-MUTATION-PROBE: the SessionPurged check in importFile removed -> RED.
func TestImportSkipsPurgedSession(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "projects")
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}}); err != nil {
		t.Fatal(err)
	}
	a := sessionByHarnessID(t, st, "sess-a-uuid")
	if _, err := st.PurgeSessions(store.PurgeScope{SessionID: a.ID}, purgeHuman); err != nil {
		t.Fatal(err)
	}
	// Drop the cursor progress so the whole transcript would be re-read:
	// only the purge stamp may keep it out.
	if _, err := st.DB().Exec(`UPDATE backfill_cursors SET byte_offset = 0, last_uuid = NULL WHERE session_id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}}); err != nil {
		t.Fatal(err)
	}
	if n := eventCountForHarness(t, st, "sess-a-uuid"); n != 0 {
		t.Errorf("purged session has %d events after re-import, want 0", n)
	}
	if n := eventCountForHarness(t, st, "sess-b-uuid"); n == 0 {
		t.Errorf("non-purged session sess-b has no events")
	}
}

// TestImportSkipsPurgedSessionWithNoCursor covers a live-captured session
// purged before its transcript was ever imported: the harness_session_id
// match keeps the transcript out, and a fresh session still imports.
//
// RA-MUTATION-PROBE: the HarnessSessionPurged check removed -> RED.
func TestImportSkipsPurgedSessionWithNoCursor(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "projects")
	if err := st.UpsertProject(store.Project{Key: "/home/alice/my-app", Toplevel: "/home/alice/my-app"}); err != nil {
		t.Fatal(err)
	}
	id, err := st.StartSession(store.StartSessionParams{
		Agent: Agent, HarnessSessionID: "sess-a-uuid", CWD: "/home/alice/my-app",
		ProjectKey: "/home/alice/my-app", StartedAt: mustParseRFC3339(t, "2026-01-01T00:00:00Z"), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PurgeSessions(store.PurgeScope{SessionID: id}, purgeHuman); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}}); err != nil {
		t.Fatal(err)
	}
	if n := eventCountForHarness(t, st, "sess-a-uuid"); n != 0 {
		t.Errorf("purged live session got %d events from backfill, want 0", n)
	}
	if n := eventCountForHarness(t, st, "sess-c-uuid"); n == 0 {
		t.Errorf("non-purged session sess-c has no events")
	}
}
