package hermes

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"

	_ "modernc.org/sqlite"
)

// fakeGit never matches any cwd, so project.Key falls back to returning cwd
// itself for every fixture path (none of it is a real git working tree).
type fakeGit struct{}

func (fakeGit) Repo(string) (project.Repo, bool)   { return project.Repo{}, false }
func (fakeGit) State(string) (project.State, bool) { return project.State{}, false }

func mustOpenStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

type fixtureSession struct {
	id, parentID, cwd, gitBranch, gitRepoRoot string
	startedAt                                 time.Time
	endedAt                                   *time.Time
}

type fixtureMessage struct {
	sessionID, role, content, toolCallID, toolCalls string
	ts                                              time.Time
	active                                          bool
}

const fixtureTSLayout = "2006-01-02 15:04:05.999999"

// newFixtureDB builds a state.db at path (WAL journaled, like a real
// long-running agent process would use) from sessions/messages, then closes
// it so Import sees a fully committed, quiescent file.
func newFixtureDB(t *testing.T, path string, sessions []fixtureSession, messages []fixtureMessage) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, stmt := range []string{
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT,
			started_at TEXT NOT NULL, ended_at TEXT, cwd TEXT, git_branch TEXT,
			git_repo_root TEXT, model TEXT, title TEXT,
			message_count INTEGER, tool_call_count INTEGER)`,
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
			role TEXT NOT NULL, content TEXT, tool_call_id TEXT, tool_calls TEXT,
			tool_name TEXT, timestamp TEXT NOT NULL, finish_reason TEXT,
			active INTEGER NOT NULL DEFAULT 1, compacted INTEGER NOT NULL DEFAULT 0)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create fixture schema: %v", err)
		}
	}

	for _, s := range sessions {
		var endedAt any
		if s.endedAt != nil {
			endedAt = s.endedAt.Format(fixtureTSLayout)
		}
		if _, err := db.Exec(`INSERT INTO sessions
			(id, source, parent_session_id, started_at, ended_at, cwd, git_branch, git_repo_root, model, title, message_count, tool_call_count)
			VALUES (?, 'cli', ?, ?, ?, ?, ?, ?, 'gpt', 'title', 0, 0)`,
			s.id, nullIfEmpty(s.parentID), s.startedAt.Format(fixtureTSLayout), endedAt,
			nullIfEmpty(s.cwd), nullIfEmpty(s.gitBranch), nullIfEmpty(s.gitRepoRoot)); err != nil {
			t.Fatalf("insert fixture session %s: %v", s.id, err)
		}
	}
	for _, m := range messages {
		active := 0
		if m.active {
			active = 1
		}
		if _, err := db.Exec(`INSERT INTO messages
			(session_id, role, content, tool_call_id, tool_calls, tool_name, timestamp, finish_reason, active, compacted)
			VALUES (?, ?, ?, ?, ?, NULL, ?, NULL, ?, 0)`,
			m.sessionID, m.role, nullIfEmpty(m.content), nullIfEmpty(m.toolCallID), nullIfEmpty(m.toolCalls),
			m.ts.Format(fixtureTSLayout), active); err != nil {
			t.Fatalf("insert fixture message: %v", err)
		}
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// twoLinkedSessionsFixture is the punch's DONE WHEN clause 1 fixture: two
// sessions, "sess-child" linked to "sess-parent" via parent_session_id, each
// with one paired tool call, plus an inactive assistant tool call (and its
// inactive result) on the parent that must be skipped entirely.
func twoLinkedSessionsFixture(t0 time.Time) ([]fixtureSession, []fixtureMessage) {
	t1 := t0.Add(time.Minute)
	ended := t0.Add(10 * time.Minute)
	childEnded := t0.Add(12 * time.Minute)

	sessions := []fixtureSession{
		{id: "sess-parent", cwd: "/work/repo", gitBranch: "main", gitRepoRoot: "/work/repo",
			startedAt: t0, endedAt: &ended},
		{id: "sess-child", parentID: "sess-parent", cwd: "/work/repo/sub", gitBranch: "main", gitRepoRoot: "/work/repo",
			startedAt: t1, endedAt: &childEnded},
	}
	messages := []fixtureMessage{
		{sessionID: "sess-parent", role: "user", content: "list the files", ts: t0, active: true},
		{sessionID: "sess-parent", role: "assistant",
			toolCalls: `[{"id":"call_1","name":"Bash","arguments":{"command":"ls -la"}}]`,
			ts:        t0.Add(time.Second), active: true},
		{sessionID: "sess-parent", role: "tool", toolCallID: "call_1", content: "file1\nfile2",
			ts: t0.Add(2 * time.Second), active: true},
		// Inactive pair: must not produce any tool.use/tool.result event.
		{sessionID: "sess-parent", role: "assistant",
			toolCalls: `[{"id":"call_2","name":"Read","arguments":{"path":"/work/repo/secret"}}]`,
			ts:        t0.Add(3 * time.Second), active: false},
		{sessionID: "sess-parent", role: "tool", toolCallID: "call_2", content: "should never appear",
			ts: t0.Add(4 * time.Second), active: false},

		{sessionID: "sess-child", role: "user", content: "sub task", ts: t1, active: true},
		{sessionID: "sess-child", role: "assistant",
			toolCalls: `[{"id":"call_3","name":"Write","arguments":{"path":"/work/repo/sub/out.txt"}}]`,
			ts:        t1.Add(time.Second), active: true},
		{sessionID: "sess-child", role: "tool", toolCallID: "call_3", content: "ok",
			ts: t1.Add(2 * time.Second), active: true},
	}
	return sessions, messages
}

// parentSessionIDOf reads sessions.parent_session_id directly: store.
// Session.ParentSessionID is a struct field but SessionByHarnessSessionID's
// own SELECT list doesn't populate it (that accessor was written for the
// codex importer's parent lookup, which only ever needs id/cwd/project_key),
// so a caller that needs the link itself reads the column back raw — the
// same workaround internal/backfill/codex's own tests use.
func parentSessionIDOf(t *testing.T, st *store.Store, sessionID string) string {
	t.Helper()
	var parentID sql.NullString
	if err := st.DB().QueryRow(`SELECT parent_session_id FROM sessions WHERE id = ?`, sessionID).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	return parentID.String
}

func toolEventIDs(t *testing.T, st *store.Store, sessionID, kind string) []string {
	t.Helper()
	rows, err := st.DB().Query(`SELECT json_extract(payload, '$.tool_use_id') FROM timeline_events
		WHERE session_id = ? AND kind = ? ORDER BY id ASC`, sessionID, kind)
	if err != nil {
		t.Fatalf("query %s events for %s: %v", kind, sessionID, err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func countAllEvents(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countAllSessions(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestImportCreatesLinkedSessionsWithPairedToolEventsSkippingInactive is the
// punch's DONE WHEN clause 1.
func TestImportCreatesLinkedSessionsWithPairedToolEventsSkippingInactive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	sessions, messages := twoLinkedSessionsFixture(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	newFixtureDB(t, dbPath, sessions, messages)

	st := mustOpenStore(t)
	res, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.SessionsCreated != 2 {
		t.Errorf("SessionsCreated = %d, want 2", res.SessionsCreated)
	}
	if res.MessagesSkipped != 2 {
		t.Errorf("MessagesSkipped = %d, want 2 (the inactive assistant+tool pair)", res.MessagesSkipped)
	}

	parent, ok, err := st.SessionByHarnessSessionID("sess-parent")
	if err != nil || !ok {
		t.Fatalf("sess-parent not imported: ok=%v err=%v", ok, err)
	}
	if parent.ParentSessionID != "" {
		t.Errorf("parent session has ParentSessionID %q, want empty", parent.ParentSessionID)
	}
	if parent.ProjectKey != "/work/repo" {
		t.Errorf("parent ProjectKey = %q, want /work/repo", parent.ProjectKey)
	}

	child, ok, err := st.SessionByHarnessSessionID("sess-child")
	if err != nil || !ok {
		t.Fatalf("sess-child not imported: ok=%v err=%v", ok, err)
	}
	if got := parentSessionIDOf(t, st, child.ID); got != parent.ID {
		t.Errorf("child parent_session_id = %q, want parent's id %q", got, parent.ID)
	}

	if got := toolEventIDs(t, st, parent.ID, EventToolUse); len(got) != 1 || got[0] != "call_1" {
		t.Errorf("parent tool.use ids = %v, want [call_1]", got)
	}
	if got := toolEventIDs(t, st, parent.ID, EventToolResult); len(got) != 1 || got[0] != "call_1" {
		t.Errorf("parent tool.result ids = %v, want [call_1]", got)
	}
	if got := toolEventIDs(t, st, child.ID, EventToolUse); len(got) != 1 || got[0] != "call_3" {
		t.Errorf("child tool.use ids = %v, want [call_3]", got)
	}
	if got := toolEventIDs(t, st, child.ID, EventToolResult); len(got) != 1 || got[0] != "call_3" {
		t.Errorf("child tool.result ids = %v, want [call_3]", got)
	}

	// 2 sessions x (session.start + tool.use + tool.result + session.end).
	if got := countAllEvents(t, st); got != 8 {
		t.Errorf("total events = %d, want 8", got)
	}
	if res.EventsCreated != 8 {
		t.Errorf("EventsCreated = %d, want 8", res.EventsCreated)
	}

	var startPayload string
	if err := st.DB().QueryRow(`SELECT payload FROM timeline_events WHERE session_id = ? AND kind = ?`,
		parent.ID, EventSessionStart).Scan(&startPayload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(startPayload), []byte("secret")) {
		t.Errorf("session.start payload leaked inactive-message content: %s", startPayload)
	}
}

// TestReadOnlyDSNRejectsWrites pins down the actual mechanism behind DONE
// WHEN clause 2, not just its externally-observable effect: a connection
// opened with the exact DSN Import uses refuses a write outright (SQLite
// returns "attempt to write a readonly database"), independent of whether
// the write would have changed any bytes on disk. The bytes/mtime and
// non-blocking assertions in TestImportOpensDBReadOnly hold just as well
// for a read-write connection that happens to only ever issue SELECTs
// (WAL-mode readers never block, or get blocked by, a writer either way) —
// this test is what actually goes red if Import ever starts opening
// state.db read-write.
func TestReadOnlyDSNRejectsWrites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	sessions, messages := twoLinkedSessionsFixture(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	newFixtureDB(t, dbPath, sessions, messages)

	db, err := sql.Open("sqlite", readOnlyDSN(dbPath))
	if err != nil {
		t.Fatalf("open with readOnlyDSN: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`UPDATE sessions SET title = 'mutated' WHERE id = 'sess-parent'`); err == nil {
		t.Fatal("write through readOnlyDSN's connection succeeded, want it rejected read-only")
	}
}

// TestImportOpensDBReadOnly is the punch's DONE WHEN clause 2: the fixture
// file's bytes and mtime are unchanged after import, and a concurrent
// writer holding the DB is not blocked by the importer's own read.
func TestImportOpensDBReadOnly(t *testing.T) {
	t.Run("file untouched", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "state.db")
		sessions, messages := twoLinkedSessionsFixture(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
		newFixtureDB(t, dbPath, sessions, messages)

		before, err := os.ReadFile(dbPath) //nolint:gosec // dbPath is this test's own t.TempDir() fixture
		if err != nil {
			t.Fatal(err)
		}
		infoBefore, err := os.Stat(dbPath)
		if err != nil {
			t.Fatal(err)
		}

		st := mustOpenStore(t)
		if _, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
			t.Fatalf("Import: %v", err)
		}

		after, err := os.ReadFile(dbPath) //nolint:gosec // dbPath is this test's own t.TempDir() fixture
		if err != nil {
			t.Fatal(err)
		}
		infoAfter, err := os.Stat(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("state.db bytes changed after import (%d bytes -> %d bytes)", len(before), len(after))
		}
		if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
			t.Errorf("state.db mtime changed after import: %v -> %v", infoBefore.ModTime(), infoAfter.ModTime())
		}
	})

	t.Run("concurrent writer is not blocked", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "state.db")
		sessions, messages := twoLinkedSessionsFixture(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
		newFixtureDB(t, dbPath, sessions, messages)

		writer, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(1000)&_txlock=immediate")
		if err != nil {
			t.Fatalf("open writer: %v", err)
		}
		defer func() { _ = writer.Close() }()

		tx, err := writer.Begin()
		if err != nil {
			t.Fatalf("writer BEGIN IMMEDIATE: %v", err)
		}
		if _, err := tx.Exec(`UPDATE sessions SET title = 'still writing' WHERE id = 'sess-parent'`); err != nil {
			t.Fatalf("writer UPDATE: %v", err)
		}
		// tx now holds the write lock, uncommitted.

		st := mustOpenStore(t)
		done := make(chan error, 1)
		go func() {
			_, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}})
			done <- err
		}()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Import while a writer held the DB: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Import did not return while a concurrent writer held the DB open — it is blocking the writer, or being blocked by it")
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("writer commit (importer must not have blocked this): %v", err)
		}
	})
}

// TestImportIsIdempotentAndMissingFileIsNoop is the punch's DONE WHEN
// clause 3.
func TestImportIsIdempotentAndMissingFileIsNoop(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	sessions, messages := twoLinkedSessionsFixture(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	newFixtureDB(t, dbPath, sessions, messages)

	st := mustOpenStore(t)
	first, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("first Import: %v", err)
	}
	if first.SessionsCreated == 0 || first.EventsCreated == 0 {
		t.Fatalf("first Import created nothing: %+v", first)
	}

	sessionsAfterFirst := countAllSessions(t, st)
	eventsAfterFirst := countAllEvents(t, st)

	second, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if second.SessionsCreated != 0 {
		t.Errorf("second Import SessionsCreated = %d, want 0", second.SessionsCreated)
	}
	if second.EventsCreated != 0 {
		t.Errorf("second Import EventsCreated = %d, want 0", second.EventsCreated)
	}
	if got := countAllSessions(t, st); got != sessionsAfterFirst {
		t.Errorf("sessions after rerun = %d, want unchanged %d", got, sessionsAfterFirst)
	}
	if got := countAllEvents(t, st); got != eventsAfterFirst {
		t.Errorf("events after rerun = %d, want unchanged %d", got, eventsAfterFirst)
	}

	t.Run("missing state.db is a no-op", func(t *testing.T) {
		st := mustOpenStore(t)
		res, err := Import(st, Options{Path: filepath.Join(t.TempDir(), "does-not-exist", "state.db"), Git: fakeGit{}, Workspaces: []string{}})
		if err != nil {
			t.Fatalf("Import with missing state.db: %v", err)
		}
		if res != (Result{}) {
			t.Errorf("Import with missing state.db = %+v, want zero Result", res)
		}
	})
}

// TestImportCapsOversizedToolOutput is task c9ab6d28's DONE WHEN clause 5
// (Hermes half): a tool message with 10 KB of content stores an excerpt
// bounded by payload.ToolOutputExcerptMaxRunes.
func TestImportCapsOversizedToolOutput(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	big := "HEAD" + strings.Repeat("o", 10*1024) + "TAIL"
	newFixtureDB(t, dbPath,
		[]fixtureSession{{id: "sess-big", cwd: "/work/repo", gitBranch: "main", gitRepoRoot: "/work/repo", startedAt: t0}},
		[]fixtureMessage{
			{sessionID: "sess-big", role: "assistant", toolCalls: `[{"id":"call_big","name":"Bash","arguments":{"command":"ls"}}]`, ts: t0.Add(time.Second), active: true},
			{sessionID: "sess-big", role: "tool", toolCallID: "call_big", content: big, ts: t0.Add(2 * time.Second), active: true},
		})

	st := mustOpenStore(t)
	if _, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	found, ok, err := st.FindToolResult("call_big")
	if err != nil || !ok {
		t.Fatalf("FindToolResult: ok=%v err=%v", ok, err)
	}
	content := found.Payload.Content
	if n := utf8.RuneCountInString(content); n > payload.ToolOutputExcerptMaxRunes {
		t.Errorf("stored content is %d runes, want at most %d", n, payload.ToolOutputExcerptMaxRunes)
	}
	if !strings.HasPrefix(content, "HEAD") || !strings.HasSuffix(content, "TAIL") {
		t.Errorf("excerpt lacks head/tail: %.30q", content)
	}
}
