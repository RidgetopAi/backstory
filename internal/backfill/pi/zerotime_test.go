package pi

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestImportNoEndTimestampNeverWritesZeroTime: a tree whose last node has no
// timestamp ends the session at its last observed event time.
func TestImportNoEndTimestampNeverWritesZeroTime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "--home-zed-proj--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"root-z","parentId":"","timestamp":"2026-05-01T00:00:00Z","type":"session","cwd":"/home/zed/proj"}
{"id":"u1","parentId":"root-z","timestamp":"2026-05-01T00:00:01Z","type":"message","role":"user","text":"go"}
{"id":"a1","parentId":"u1","timestamp":"2026-05-01T00:00:02Z","type":"message","role":"assistant","content":[{"type":"toolCall","id":"c1","name":"Bash"}]}
{"id":"r1","parentId":"a1","type":"toolResult","toolCallId":"c1","content":"ok"}
`
	if err := os.WriteFile(filepath.Join(dir, "2026-05-01T00-00-00Z_root-z.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	assertNoZeroTimes(t, st, "root-z")
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
