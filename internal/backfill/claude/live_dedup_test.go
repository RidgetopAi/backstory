package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
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

// TestBackfillEnrichesContentlessLiveToolResult is task c9ab6d28's DONE WHEN
// clause 4: live capture stored a content-less tool.result for a tool id;
// backfilling a transcript whose tool_result for the same id has
// is_error:true and content 'Exit code 2 …' must leave exactly ONE
// tool.result for that id, carrying is_error, exit 2 and an excerpt.
//
// RA-MUTATION-PROBE: make appendToolEvents skip (not enrich) an existing
// tool.result -> RED (the surviving event has no is_error/exit/content).
func TestBackfillEnrichesContentlessLiveToolResult(t *testing.T) {
	st := mustOpenStore(t)
	root := t.TempDir()
	dir := filepath.Join(root, "-home-alice-my-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := strings.Join([]string{
		`{"type":"user","uuid":"u1","sessionId":"sess-x","cwd":"/home/alice/my-app","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"run it"}}`,
		`{"type":"assistant","uuid":"u2","parentUuid":"u1","sessionId":"sess-x","cwd":"/home/alice/my-app","timestamp":"2026-01-01T00:00:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_fail","name":"Bash","input":{"command":"false"}}]}}`,
		`{"type":"user","uuid":"u3","parentUuid":"u2","sessionId":"sess-x","cwd":"/home/alice/my-app","timestamp":"2026-01-01T00:00:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_fail","is_error":true,"content":"Exit code 2\nmake: *** [test] Error 2"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sess-x.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := st.UpsertProject(store.Project{Key: "/home/alice/my-app", Toplevel: "/home/alice/my-app", FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	liveID, err := st.StartSession(store.StartSessionParams{
		Agent: Agent, HarnessSessionID: "sess-x", CWD: "/home/alice/my-app",
		ProjectKey: "/home/alice/my-app", StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	liveEv, err := st.AppendEvent(store.Event{
		TS: time.Now(), Kind: EventToolResult, SessionID: liveID, Source: "posttooluse",
		Payload: `{"tool_use_id":"toolu_fail"}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Import(st, Options{Root: root, Git: fakeGit{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	rows, err := st.DB().Query(`SELECT id, payload FROM timeline_events
		WHERE kind = ? AND json_extract(payload, '$.tool_use_id') = 'toolu_fail'`, EventToolResult)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []payload.ToolResult
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		if id != liveEv {
			t.Errorf("surviving tool.result id = %d, want the live event %d enriched in place", id, liveEv)
		}
		var tr payload.ToolResult
		if err := json.Unmarshal([]byte(raw), &tr); err != nil {
			t.Fatal(err)
		}
		got = append(got, tr)
	}
	if len(got) != 1 {
		t.Fatalf("tool.result events for toolu_fail = %d, want exactly 1", len(got))
	}
	tr := got[0]
	if !tr.IsError {
		t.Error("is_error = false, want true")
	}
	if tr.Exit == nil || *tr.Exit != 2 {
		t.Errorf("exit = %v, want 2", tr.Exit)
	}
	if !strings.Contains(tr.Content, "make: *** [test] Error 2") {
		t.Errorf("content = %q, want the output excerpt", tr.Content)
	}
}
