package claude

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestImportAttachesToLiveSessionInsteadOfDuplicating is the punch's
// (25b74537) DONE WHEN clause 3: a run captured live (a session already
// live in the store — origin 'live', harness_session_id equal to the
// transcript's own sessionId) must not get a second, backfilled-origin
// session when its transcript is later backfilled; the importer attaches
// to the live session instead, and never ends it — that lifecycle belongs
// exclusively to the daemon (internal/mcp's SessionRegistry sweep), never
// to backfill.
//
// sess-a.jsonl (testdata/import/projects, also exercised by
// TestImportCreatesOneSessionPerFile) is reused here as the "run" whose
// live session already exists before Import runs — sess-b and sess-c are
// untouched controls proving the dedup is scoped to the one harness_session_id
// that actually collides.
//
// RA-MUTATION-PROBE: importFile's `st.LiveSessionByHarnessSessionID` lookup
// (the `ok` branch) deleted, always falling through to createSession -> RED
// (two sessions end up sharing harness_session_id "sess-a-uuid": the
// pre-existing live one and a new backfilled duplicate, so
// LiveSessionsInProject's count for the project is unchanged only by
// accident of assertion, but the harness_session_id count below catches
// it); restored -> GREEN.
func TestImportAttachesToLiveSessionInsteadOfDuplicating(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "projects")

	if err := st.UpsertProject(store.Project{Key: "/home/alice/my-app", Toplevel: "/home/alice/my-app", FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	pid := 12345
	liveID, err := st.StartSession(store.StartSessionParams{
		Agent:            Agent,
		HarnessSessionID: "sess-a-uuid",
		PID:              &pid,
		CWD:              "/home/alice/my-app",
		ProjectKey:       "/home/alice/my-app",
		StartedAt:        mustParseRFC3339(t, "2026-01-01T00:00:00Z"),
		Origin:           store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession (live): %v", err)
	}

	res, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// sess-b and sess-c (the other two fixtures under root) are still
	// freshly created; sess-a's run attaches to the pre-existing live
	// session instead, so it must not be counted as a new one.
	if res.SessionsCreated != 2 {
		t.Errorf("SessionsCreated = %d, want 2 (sess-b, sess-c only — sess-a attaches to the live session)", res.SessionsCreated)
	}

	var totalForHarnessID int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = ?`, "sess-a-uuid").Scan(&totalForHarnessID); err != nil {
		t.Fatal(err)
	}
	if totalForHarnessID != 1 {
		t.Fatalf("sessions with harness_session_id=sess-a-uuid = %d, want exactly 1 (the live one, no backfilled duplicate)", totalForHarnessID)
	}

	a := sessionByHarnessID(t, st, "sess-a-uuid")
	if a.ID != liveID {
		t.Errorf("sess-a's session id = %q, want the pre-existing live session %q (backfill must attach, not mint a new one)", a.ID, liveID)
	}
	if a.Origin != "live" {
		t.Errorf("sess-a's origin = %q, want it to stay %q: backfill must never change a live session's origin", a.Origin, "live")
	}
	if a.EndedAt.Valid {
		t.Errorf("sess-a's ended_at = %v, want NULL: backfill must never end a live-origin session", a.EndedAt)
	}

	// The SessionStart block's coordination slot (internal/block) and
	// status's other_live_sessions both key off LiveSessionsInProject: the
	// backfill above must have left this project's live-session count
	// exactly where it started (one — the live session), never two.
	live, err := st.LiveSessionsInProject("/home/alice/my-app")
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].ID != liveID {
		ids := make([]string, len(live))
		for i, s := range live {
			ids[i] = s.ID
		}
		t.Errorf("LiveSessionsInProject(/home/alice/my-app) = %v, want exactly [%s] (unchanged by the backfill)", ids, liveID)
	}

	// A tool event from the transcript still gets recorded against the
	// live session (backfill's own content is not skipped, only its
	// session-lifecycle side effects are).
	events, err := st.EventsSinceID("/home/alice/my-app", 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.SessionID == liveID && e.Kind == EventSessionStart {
			found = true
		}
	}
	if !found {
		t.Errorf("no session.start event recorded against the live session %q", liveID)
	}
}
