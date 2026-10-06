package hermes

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestImportZeroEndedAtNeverWritesZeroTime: a session whose ended_at is 0
// (the epoch) and a message with timestamp 0 end at the last observed
// event time, never at a zero or negative time.
func TestImportZeroEndedAtNeverWritesZeroTime(t *testing.T) {
	st := mustOpenStore(t)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT,
			started_at REAL NOT NULL, ended_at REAL, cwd TEXT, git_branch TEXT, git_repo_root TEXT)`,
		`CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
			role TEXT NOT NULL, content TEXT, tool_call_id TEXT, tool_calls TEXT,
			tool_name TEXT, timestamp REAL NOT NULL, finish_reason TEXT,
			active INTEGER NOT NULL DEFAULT 1, compacted INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO sessions (id, started_at, ended_at, cwd) VALUES ('s-zero', 1790904000, 0, '/tmp/a')`,
		`INSERT INTO messages (session_id, role, content, timestamp) VALUES ('s-zero', 'user', 'hello', 1790904087)`,
		`INSERT INTO messages (session_id, role, content, timestamp) VALUES ('s-zero', 'assistant', 'hi', 0)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	_ = db.Close()
	if _, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	assertNoZeroTimes(t, st, "s-zero")
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
