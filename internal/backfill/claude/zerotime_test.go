package claude

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestImportNoEndTimestampNeverWritesZeroTime: a transcript whose trailing
// lines carry no timestamp (a summary line) must end the session at its
// last observed event time, never at Go's zero time.
func TestImportNoEndTimestampNeverWritesZeroTime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-home-zed-proj")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","uuid":"u1","sessionId":"sess-zero","cwd":"/home/zed/proj","version":"2.1.0","timestamp":"2026-03-01T00:00:00Z","message":{"role":"user","content":"hi"}}
{"type":"assistant","uuid":"u2","sessionId":"sess-zero","timestamp":"2026-03-01T00:00:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"summary","uuid":"u3","sessionId":"sess-zero"}
`
	if err := os.WriteFile(filepath.Join(dir, "sess-zero.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	assertNoZeroTimes(t, st, "sess-zero")
}

// TestImportNoTimestampAtAllWritesNothing: no usable time anywhere means no
// session row — nothing is invented.
func TestImportNoTimestampAtAllWritesNothing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-home-zed-proj")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","uuid":"u1","sessionId":"sess-none","cwd":"/home/zed/proj","message":{"role":"user","content":"hi"}}
`
	if err := os.WriteFile(filepath.Join(dir, "sess-none.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil || n != 0 {
		t.Errorf("sessions = %d (err %v), want 0", n, err)
	}
}

// assertNoZeroTimes checks the zero-time contract (task 456410f0): the
// harness session has no event with ts <= 0, and its ended_at (when set)
// equals its last event time and is positive.
func assertNoZeroTimes(t *testing.T, st *store.Store, harnessID string) {
	t.Helper()
	var sid string
	var endedAt sql.NullInt64
	if err := st.DB().QueryRow(`SELECT id, ended_at FROM sessions WHERE harness_session_id = ?`, harnessID).Scan(&sid, &endedAt); err != nil {
		t.Fatalf("session %s: %v", harnessID, err)
	}
	var bad, total int
	var maxTS sql.NullInt64
	if err := st.DB().QueryRow(`SELECT COUNT(CASE WHEN ts <= 0 THEN 1 END), COUNT(*), MAX(ts) FROM timeline_events WHERE session_id = ?`, sid).Scan(&bad, &total, &maxTS); err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Fatal("no events imported")
	}
	if bad != 0 {
		t.Errorf("%d events with ts <= 0", bad)
	}
	if !endedAt.Valid || endedAt.Int64 <= 0 {
		t.Fatalf("ended_at = %v, want a positive time", endedAt)
	}
	if endedAt.Int64 != maxTS.Int64 {
		t.Errorf("ended_at = %d, want last event time %d", endedAt.Int64, maxTS.Int64)
	}
}
