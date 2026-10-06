package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

func countOne(t *testing.T, st *store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// TestBackfillAfterLiveSessionEndedAddsNoSession is task a757b754's DONE WHEN
// clause 1: a live session with harness_session_id H ends (daemon sweep at
// restart); backfilling H's transcript adds no session row and no duplicate
// events, and two more runs change nothing.
//
// RA-MUTATION-PROBE: match only live sessions again (importFile's lookup via
// LiveSessionByHarnessSessionID) -> RED (a second session row for H).
func TestBackfillAfterLiveSessionEndedAddsNoSession(t *testing.T) {
	st := mustOpenStore(t)
	root := t.TempDir()
	dir := filepath.Join(root, "-home-alice-my-app")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	transcript := strings.Join([]string{
		`{"type":"user","uuid":"u1","sessionId":"sess-h","cwd":"/home/alice/my-app","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","uuid":"u2","parentUuid":"u1","sessionId":"sess-h","cwd":"/home/alice/my-app","timestamp":"2026-01-01T00:00:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sess-h.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProject(store.Project{Key: "/home/alice/my-app", Toplevel: "/home/alice/my-app", FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	pid := 4242
	liveID, err := st.StartSession(store.StartSessionParams{
		Agent: Agent, HarnessSessionID: "sess-h", PID: &pid, CWD: "/home/alice/my-app",
		ProjectKey: "/home/alice/my-app", StartedAt: mustParseRFC3339(t, "2025-12-31T23:59:00Z"), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EndSession(liveID, mustParseRFC3339(t, "2026-01-01T00:10:00Z"), "swept"); err != nil {
		t.Fatal(err)
	}

	snapshot := func() (sessions, events int) {
		return countOne(t, st, `SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'sess-h'`),
			countOne(t, st, `SELECT COUNT(*) FROM timeline_events`)
	}
	var baseEvents int
	for run := 1; run <= 3; run++ {
		if _, err := Import(st, Options{Root: root, Git: fakeGit{}}); err != nil {
			t.Fatalf("Import run %d: %v", run, err)
		}
		sessions, events := snapshot()
		if sessions != 1 {
			t.Fatalf("run %d: sessions for sess-h = %d, want 1 (the ended live one)", run, sessions)
		}
		if run == 1 {
			baseEvents = events
		} else if events != baseEvents {
			t.Fatalf("run %d: events = %d, want %d unchanged", run, events, baseEvents)
		}
	}
	if n := countOne(t, st, `SELECT COUNT(*) FROM timeline_events WHERE kind = 'tool.use' AND session_id = ?`, liveID); n != 1 {
		t.Errorf("tool.use on the original session = %d, want 1", n)
	}
	if n := countOne(t, st, `SELECT COUNT(*) FROM timeline_events WHERE kind = 'session.end'`); n != 0 {
		t.Errorf("session.end events = %d, want 0: the daemon owns a live-origin session's end", n)
	}
}
