package codex

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestImportNoEndTimestampNeverWritesZeroTime: a rollout whose last line has
// no timestamp ends the session at its last observed event time.
func TestImportNoEndTimestampNeverWritesZeroTime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "01", "15")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session_meta","timestamp":"2026-01-15T09:00:00Z","payload":{"id":"zero-thread","cwd":"/home/zed/proj","cli_version":"0.1"}}
{"type":"response_item","timestamp":"2026-01-15T09:00:02Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]}}
{"type":"response_item","timestamp":"2026-01-15T09:00:03Z","payload":{"type":"custom_tool_call","name":"apply_patch","input":"x","call_id":"c1"}}
{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}}
{"type":"event_msg","payload":{"type":"task_complete"}}
`
	if err := os.WriteFile(filepath.Join(dir, "rollout-20260115T090000-zero000thread.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	assertNoZeroTimes(t, st, "zero-thread")
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
