package codex

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// fakeGit never matches any cwd, so project.Key falls back to returning cwd
// itself for every resolved path in these fixtures (none of testdata is a
// real git working tree) — the same fallback AGENT-CONTRACT.md names for a
// non-git directory.
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

type sessionRow struct {
	ID              string
	Agent           string
	CWD             string
	ProjectKey      sql.NullString
	ParentSessionID sql.NullString
	EndedAt         sql.NullInt64
	Origin          string
}

func sessionByHarnessID(t *testing.T, st *store.Store, harnessID string) sessionRow {
	t.Helper()
	var r sessionRow
	err := st.DB().QueryRow(`SELECT id, agent, cwd, project_key, parent_session_id, ended_at, origin
		FROM sessions WHERE harness_session_id = ?`, harnessID).
		Scan(&r.ID, &r.Agent, &r.CWD, &r.ProjectKey, &r.ParentSessionID, &r.EndedAt, &r.Origin)
	if err != nil {
		t.Fatalf("query session harness_session_id=%s: %v", harnessID, err)
	}
	return r
}

func toolUseIDsInOrder(t *testing.T, st *store.Store, sessionID, kind string) []string {
	t.Helper()
	rows, err := st.DB().Query(`SELECT json_extract(payload, '$.tool_use_id') FROM timeline_events
		WHERE session_id = ? AND kind = ? ORDER BY id ASC`, sessionID, kind)
	if err != nil {
		t.Fatalf("query %s events for session %s: %v", kind, sessionID, err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan %s tool_use_id: %v", kind, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func countEvents(t *testing.T, st *store.Store, sessionID, kind string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events WHERE session_id = ? AND kind = ?`,
		sessionID, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestImportCreatesSessionsWithProjectKeysAndPairedToolEvents is the
// punch's DONE WHEN clause 1: three rollouts under one tree — a main
// session, its parent_thread_id subagent (whose own tool call is a
// custom_tool_call apply_patch), and an unrelated main session in a
// different cwd — import as 3 sessions with cwd-derived project keys, the
// subagent linked to its parent, and the right count of paired tool events.
// The main session's two function_calls complete out of order (call_2's
// output line precedes call_1's): pairing on each output's own call_id,
// never on position among the batch's calls, is what DONE WHEN clause 5's
// critic mutation targets.
func TestImportCreatesSessionsWithProjectKeysAndPairedToolEvents(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "sessions")

	res, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.FilesScanned != 3 {
		t.Errorf("FilesScanned = %d, want 3", res.FilesScanned)
	}
	if res.SessionsCreated != 3 {
		t.Errorf("SessionsCreated = %d, want 3", res.SessionsCreated)
	}
	if res.PartialFiles != 0 {
		t.Errorf("PartialFiles = %d, want 0", res.PartialFiles)
	}

	var totalSessions int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&totalSessions); err != nil {
		t.Fatal(err)
	}
	if totalSessions != 3 {
		t.Fatalf("sessions table has %d rows, want exactly 3", totalSessions)
	}

	main := sessionByHarnessID(t, st, "main-thread-uuid")
	if main.Agent != "codex" {
		t.Errorf("main session agent = %q, want codex", main.Agent)
	}
	if main.CWD != "/home/erin/codex-proj" {
		t.Errorf("main session cwd = %q, want /home/erin/codex-proj", main.CWD)
	}
	if !main.ProjectKey.Valid || main.ProjectKey.String != "/home/erin/codex-proj" {
		t.Errorf("main session project_key = %v, want /home/erin/codex-proj", main.ProjectKey)
	}
	if main.ParentSessionID.Valid {
		t.Errorf("main session parent_session_id = %v, want NULL", main.ParentSessionID)
	}
	if main.Origin != "backfilled" {
		t.Errorf("main session origin = %q, want backfilled", main.Origin)
	}
	if !main.EndedAt.Valid {
		t.Error("main session ended_at is NULL, want set")
	}

	other := sessionByHarnessID(t, st, "other-thread-uuid")
	if !other.ProjectKey.Valid || other.ProjectKey.String != "/home/erin/other-proj" {
		t.Errorf("other session project_key = %v, want /home/erin/other-proj (a different cwd from main)", other.ProjectKey)
	}

	sub := sessionByHarnessID(t, st, "sub-thread-uuid")
	if !sub.ParentSessionID.Valid || sub.ParentSessionID.String != main.ID {
		t.Errorf("subagent session parent_session_id = %v, want %s (the main session)", sub.ParentSessionID, main.ID)
	}

	// Main session: 2 tool.use (call_1, call_2) and 2 tool.result — pairing
	// keyed on call_id, so the two tool.result payloads carry call_2 then
	// call_1 (file order), never call_1 then call_2 (which a positional
	// pairing bug would produce).
	if n := countEvents(t, st, main.ID, EventToolUse); n != 2 {
		t.Errorf("main session tool.use count = %d, want 2", n)
	}
	gotResults := toolUseIDsInOrder(t, st, main.ID, EventToolResult)
	wantResults := []string{"call_2", "call_1"}
	if len(gotResults) != len(wantResults) {
		t.Fatalf("main session tool.result count = %d, want %d", len(gotResults), len(wantResults))
	}
	for i, want := range wantResults {
		if gotResults[i] != want {
			t.Errorf("main session tool.result[%d].tool_use_id = %q, want %q (pairing must follow each line's own call_id, not position)", i, gotResults[i], want)
		}
	}

	// Subagent session: 1 tool.use/tool.result pair for its custom_tool_call
	// apply_patch, call_id call_3, attributed to the subagent's OWN session
	// (unlike Claude's Task-tool subagents, which attribute to the parent).
	if n := countEvents(t, st, sub.ID, EventToolUse); n != 1 {
		t.Errorf("subagent session tool.use count = %d, want 1", n)
	}
	subResults := toolUseIDsInOrder(t, st, sub.ID, EventToolResult)
	if len(subResults) != 1 || subResults[0] != "call_3" {
		t.Errorf("subagent session tool.result = %v, want [call_3]", subResults)
	}
	var applyPatchName string
	if err := st.DB().QueryRow(`SELECT json_extract(payload, '$.name') FROM timeline_events
		WHERE session_id = ? AND kind = ?`, sub.ID, EventToolUse).Scan(&applyPatchName); err != nil {
		t.Fatal(err)
	}
	if applyPatchName != "apply_patch" {
		t.Errorf("subagent tool.use name = %q, want apply_patch", applyPatchName)
	}

	// Other session: 1 tool.use/tool.result pair, call_4.
	if n := countEvents(t, st, other.ID, EventToolUse); n != 1 {
		t.Errorf("other session tool.use count = %d, want 1", n)
	}
}

// TestImportIsIdempotent is the punch's DONE WHEN clause 2: re-running
// backfill on the same tree adds zero sessions and zero events. This is
// what DONE WHEN clause 5's other critic mutation (drop the idempotency
// check) targets.
func TestImportIsIdempotent(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "sessions")

	first, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("first Import: %v", err)
	}
	if first.SessionsCreated == 0 || first.EventsCreated == 0 {
		t.Fatalf("first Import created nothing: %+v", first)
	}

	var sessionsAfterFirst, eventsAfterFirst int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionsAfterFirst); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&eventsAfterFirst); err != nil {
		t.Fatal(err)
	}

	second, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if second.SessionsCreated != 0 {
		t.Errorf("second Import SessionsCreated = %d, want 0", second.SessionsCreated)
	}
	if second.EventsCreated != 0 {
		t.Errorf("second Import EventsCreated = %d, want 0", second.EventsCreated)
	}

	var sessionsAfterSecond, eventsAfterSecond int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionsAfterSecond); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&eventsAfterSecond); err != nil {
		t.Fatal(err)
	}
	if sessionsAfterSecond != sessionsAfterFirst {
		t.Errorf("sessions after rerun = %d, want unchanged %d", sessionsAfterSecond, sessionsAfterFirst)
	}
	if eventsAfterSecond != eventsAfterFirst {
		t.Errorf("events after rerun = %d, want unchanged %d", eventsAfterSecond, eventsAfterFirst)
	}
}

// TestImportPartialFileReportsPartialWithoutFailing is the punch's DONE
// WHEN clause 3: a rollout whose final line is truncated (no trailing
// newline — the harness may still be mid-write on it) imports every
// complete line, reports the file as partial, and the run does not fail.
func TestImportPartialFileReportsPartialWithoutFailing(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "partial", "sessions")

	res, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.PartialFiles != 1 {
		t.Errorf("PartialFiles = %d, want 1", res.PartialFiles)
	}
	if res.SessionsCreated != 1 {
		t.Fatalf("SessionsCreated = %d, want 1", res.SessionsCreated)
	}

	sess := sessionByHarnessID(t, st, "partial-thread-uuid")
	// The truncated function_call_output line never completed, so it is
	// simply absent, not a malformed-line error: only call_p1's tool.use
	// made it in.
	if n := countEvents(t, st, sess.ID, EventToolUse); n != 1 {
		t.Errorf("tool.use count = %d, want 1 (the truncated output line must not import)", n)
	}
	if n := countEvents(t, st, sess.ID, EventToolResult); n != 0 {
		t.Errorf("tool.result count = %d, want 0 (its line was truncated)", n)
	}

	// Rerunning without the file changing must not re-report partial as a
	// new discovery each time, but it also must not have advanced past the
	// truncated bytes: still 0 tool.result events, still reported partial.
	res2, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if res2.PartialFiles != 1 {
		t.Errorf("second Import PartialFiles = %d, want 1 (still incomplete on disk)", res2.PartialFiles)
	}
	if n := countEvents(t, st, sess.ID, EventToolResult); n != 0 {
		t.Errorf("tool.result count after second Import = %d, want 0", n)
	}
}

// TestImportUnknownModelProviderImportsIdentically is the punch's DONE WHEN
// clause 4: a rollout with an unknown model_provider (e.g. a local model)
// imports exactly like one with a known provider — the importer must never
// branch on that field.
func TestImportUnknownModelProviderImportsIdentically(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "unknownprovider", "sessions")

	res, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.SessionsCreated != 2 {
		t.Fatalf("SessionsCreated = %d, want 2", res.SessionsCreated)
	}

	known := sessionByHarnessID(t, st, "known-thread-uuid")
	unknown := sessionByHarnessID(t, st, "unknown-thread-uuid")

	knownUse := countEvents(t, st, known.ID, EventToolUse)
	unknownUse := countEvents(t, st, unknown.ID, EventToolUse)
	if knownUse != unknownUse || knownUse != 1 {
		t.Errorf("tool.use counts = known %d, unknown %d, want 1 and 1 (identical regardless of model_provider)", knownUse, unknownUse)
	}
	knownResult := countEvents(t, st, known.ID, EventToolResult)
	unknownResult := countEvents(t, st, unknown.ID, EventToolResult)
	if knownResult != unknownResult || knownResult != 1 {
		t.Errorf("tool.result counts = known %d, unknown %d, want 1 and 1", knownResult, unknownResult)
	}
	if known.Origin != unknown.Origin {
		t.Errorf("origin differs: known %q, unknown %q", known.Origin, unknown.Origin)
	}
}

// TestImportCapsOversizedToolOutput is task c9ab6d28's DONE WHEN clause 5
// (Codex half): a function_call_output with 10 KB of output stores an
// excerpt bounded by payload.ToolOutputExcerptMaxRunes.
func TestImportCapsOversizedToolOutput(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "01", "15")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := "HEAD" + strings.Repeat("o", 10*1024) + "TAIL"
	lines := []string{
		`{"type":"session_meta","timestamp":"2026-01-15T09:00:00Z","ordinal":1,"payload":{"id":"big-thread","cwd":"/home/erin/codex-proj","cli_version":"0.151.0","model_provider":"openai","source":"cli"}}`,
		`{"type":"response_item","timestamp":"2026-01-15T09:00:01Z","ordinal":2,"payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"ls\"]}","call_id":"call_big"}}`,
		`{"type":"response_item","timestamp":"2026-01-15T09:00:02Z","ordinal":3,"payload":{"type":"function_call_output","call_id":"call_big","output":"` + big + `"}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-20260115T090000-big000thread.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
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
