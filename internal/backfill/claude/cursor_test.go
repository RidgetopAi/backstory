package claude

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// copyDir clones src into a fresh directory under t.TempDir() and returns
// it — cursor idempotence tests need a fixture copy they can append lines
// to without mutating the checked-in testdata.
func copyDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(path) //nolint:gosec // path comes from filepath.WalkDir over this test's own testdata
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600) //nolint:gosec // target is derived from filepath.WalkDir over this test's own testdata into t.TempDir(), never external input
	})
	if err != nil {
		t.Fatalf("copyDir(%s): %v", src, err)
	}
	return dst
}

func countTable(t *testing.T, st *store.Store, table string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil { //nolint:gosec // table is a fixed literal passed by this test's own call sites, never external input
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestImportIsIdempotentAndIncremental is the punch's clause 4: rerunning
// the importer over an unchanged root inserts nothing, and appending lines
// to one file imports only those new lines on the next run.
func TestImportIsIdempotentAndIncremental(t *testing.T) {
	st := mustOpenStore(t)
	root := copyDir(t, filepath.Join("testdata", "cursor", "projects"))

	res1, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (first run): %v", err)
	}
	if res1.FilesScanned != 2 || res1.SessionsCreated != 2 || res1.EventsCreated != 4 || res1.LinesSkipped != 0 {
		t.Fatalf("first run result = %+v, want {FilesScanned:2 SessionsCreated:2 EventsCreated:4 LinesSkipped:0}", res1)
	}
	sessionsAfterFirst := countTable(t, st, "sessions")
	eventsAfterFirst := countTable(t, st, "timeline_events")
	if sessionsAfterFirst != 2 {
		t.Fatalf("sessions rows after first run = %d, want 2", sessionsAfterFirst)
	}
	if eventsAfterFirst != 4 {
		t.Fatalf("timeline_events rows after first run = %d, want 4", eventsAfterFirst)
	}

	// Rerun over the exact same root: zero new sessions, zero new events.
	res2, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (rerun, unchanged): %v", err)
	}
	if res2.SessionsCreated != 0 {
		t.Errorf("rerun SessionsCreated = %d, want 0", res2.SessionsCreated)
	}
	if res2.EventsCreated != 0 {
		t.Errorf("rerun EventsCreated = %d, want 0", res2.EventsCreated)
	}
	if got := countTable(t, st, "sessions"); got != sessionsAfterFirst {
		t.Errorf("sessions rows after unchanged rerun = %d, want %d (unchanged)", got, sessionsAfterFirst)
	}
	if got := countTable(t, st, "timeline_events"); got != eventsAfterFirst {
		t.Errorf("timeline_events rows after unchanged rerun = %d, want %d (unchanged)", got, eventsAfterFirst)
	}

	// Append exactly two new lines (one tool_use, one tool_result) to
	// sess-g.jsonl and rerun: exactly two new events for that session, and
	// nothing else changes.
	sessG := filepath.Join(root, "-home-frank-cursor", "sess-g.jsonl")
	appendLines(t, sessG,
		`{"type":"assistant","uuid":"g-uuid-3","sessionId":"sess-g-uuid","cwd":"/home/frank/cursor","timestamp":"2026-06-01T00:00:02Z","isMeta":false,"message":{"role":"assistant","content":[{"type":"tool_use","id":"tu3","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","uuid":"g-uuid-4","sessionId":"sess-g-uuid","cwd":"/home/frank/cursor","timestamp":"2026-06-01T00:00:03Z","isMeta":false,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu3","content":"foo.go","is_error":false}]}}`,
	)

	res3, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (rerun, appended): %v", err)
	}
	if res3.SessionsCreated != 0 {
		t.Errorf("appended-rerun SessionsCreated = %d, want 0 (existing session, not a new one)", res3.SessionsCreated)
	}
	// 2 new tool events (tu3's tool.use and tool.result) PLUS a re-minted
	// session.end: the transcript grew, so the session's "last event" must
	// move to reflect that, and it does so by appending a new session.end
	// rather than rewriting the first one (append-only, SCHEMA.md
	// invariant 10 — rowid is the only ordering, nothing is ever mutated
	// in place).
	if res3.EventsCreated != 3 {
		t.Errorf("appended-rerun EventsCreated = %d, want exactly 3 (2 tool events + a re-minted session.end)", res3.EventsCreated)
	}
	if got := countTable(t, st, "sessions"); got != sessionsAfterFirst {
		t.Errorf("sessions rows after appended rerun = %d, want %d (unchanged, exactly one session for the file)", got, sessionsAfterFirst)
	}
	if got, want := countTable(t, st, "timeline_events"), eventsAfterFirst+3; got != want {
		t.Errorf("timeline_events rows after appended rerun = %d, want %d", got, want)
	}

	sessH := sessionByHarnessID(t, st, "sess-h-uuid")
	var hEvents int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events WHERE session_id = ?`, sessH.ID).Scan(&hEvents); err != nil {
		t.Fatal(err)
	}
	if hEvents != 2 {
		t.Errorf("sess-h event count = %d, want 2 (untouched by sess-g's append)", hEvents)
	}

	// sess-g: the earlier session.end (minted on the first run) stays —
	// append-only — and the re-minted session.end from the appended rerun
	// is the LAST event by rowid, after the two new tool events.
	sessGRow := sessionByHarnessID(t, st, "sess-g-uuid")
	gRows, err := st.DB().Query(`SELECT kind FROM timeline_events WHERE session_id = ? ORDER BY id ASC`, sessGRow.ID)
	if err != nil {
		t.Fatal(err)
	}
	var gKinds []string
	for gRows.Next() {
		var k string
		if err := gRows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		gKinds = append(gKinds, k)
	}
	if err := gRows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = gRows.Close()

	wantGKinds := []string{
		EventSessionStart, EventSessionEnd, // first run: sess-g had no tool_use/tool_result
		EventToolUse, EventToolResult, EventSessionEnd, // appended rerun
	}
	if len(gKinds) != len(wantGKinds) {
		t.Fatalf("sess-g events = %v, want %v", gKinds, wantGKinds)
	}
	for i, k := range gKinds {
		if k != wantGKinds[i] {
			t.Errorf("sess-g event[%d] = %q, want %q (kinds=%v)", i, k, wantGKinds[i], gKinds)
		}
	}
	if got := gKinds[len(gKinds)-1]; got != EventSessionEnd {
		t.Errorf("sess-g's last event by rowid = %q, want %q (re-minted session.end)", got, EventSessionEnd)
	}

	// sessions.ended_at must move to the appended last line's timestamp
	// (2026-06-01T00:00:03Z), not stay pinned to the first run's.
	wantEndedAt := mustParseRFC3339(t, "2026-06-01T00:00:03Z")
	if !sessGRow.EndedAt.Valid || !time.Unix(0, sessGRow.EndedAt.Int64).UTC().Equal(wantEndedAt) {
		t.Errorf("sess-g ended_at = %v, want %s", sessGRow.EndedAt, wantEndedAt)
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // path is this test's own tempdir copy
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatalf("append to %s: %v", path, err)
		}
	}
}
