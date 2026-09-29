package pi

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

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
	ID               string
	Agent            string
	HarnessSessionID sql.NullString
	PID              sql.NullInt64
	CWD              string
	ProjectKey       sql.NullString
	StartedAt        int64
	EndedAt          sql.NullInt64
	Origin           string
}

func sessionByHarnessID(t *testing.T, st *store.Store, harnessID string) sessionRow {
	t.Helper()
	var r sessionRow
	err := st.DB().QueryRow(`SELECT id, agent, harness_session_id, pid, cwd, project_key, started_at, ended_at, origin
		FROM sessions WHERE harness_session_id = ?`, harnessID).
		Scan(&r.ID, &r.Agent, &r.HarnessSessionID, &r.PID, &r.CWD, &r.ProjectKey, &r.StartedAt, &r.EndedAt, &r.Origin)
	if err != nil {
		t.Fatalf("query session harness_session_id=%s: %v", harnessID, err)
	}
	return r
}

// eventRow is one timeline_events row read back in rowid order — the only
// ordering SCHEMA.md invariant 10 recognises.
type eventRow struct {
	Kind    string
	Payload string
}

func eventsForHarnessSession(t *testing.T, st *store.Store, harnessID string) []eventRow {
	t.Helper()
	sess := sessionByHarnessID(t, st, harnessID)
	rows, err := st.DB().Query(`SELECT kind, payload FROM timeline_events WHERE session_id = ? ORDER BY id ASC`, sess.ID)
	if err != nil {
		t.Fatalf("query events for session %s: %v", sess.ID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []eventRow
	for rows.Next() {
		var e eventRow
		if err := rows.Scan(&e.Kind, &e.Payload); err != nil {
			t.Fatalf("scan event: %v", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func toolUseIDs(t *testing.T, events []eventRow, kind string) []string {
	t.Helper()
	var ids []string
	for _, e := range events {
		if e.Kind != kind {
			continue
		}
		switch kind {
		case EventToolUse:
			var p payload.ToolUse
			if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
				t.Fatalf("unmarshal tool.use payload: %v", err)
			}
			ids = append(ids, p.ToolUseID)
		case EventToolResult:
			var p payload.ToolResult
			if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
				t.Fatalf("unmarshal tool.result payload: %v", err)
			}
			ids = append(ids, p.ToolUseID)
		}
	}
	return ids
}

// TestImportCreatesOneSessionPerFileCWDFromSessionLine is the punch's clause
// 2: --home-wrong-slug--'s directory slug decodes to /home/wrong/slug, but
// its session line's own cwd is /home/alice/real-project — the session and
// its project_key must land on the cwd, never the slug. Alongside it,
// --home-bob-widgets-- imports a session whose slug happens to agree with
// its cwd, plus a blank line and a malformed line that must be skipped, not
// fatal, and a model_change line that must be skipped as structural.
func TestImportCreatesOneSessionPerFileCWDFromSessionLine(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "sessions")

	res, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.FilesScanned != 2 {
		t.Errorf("FilesScanned = %d, want 2", res.FilesScanned)
	}
	if res.SessionsCreated != 2 {
		t.Errorf("SessionsCreated = %d, want 2", res.SessionsCreated)
	}
	if res.LinesSkipped != 2 {
		t.Errorf("LinesSkipped = %d, want 2 (root-b's malformed line and its model_change line; the blank line doesn't count)", res.LinesSkipped)
	}

	a := sessionByHarnessID(t, st, "root-a")
	if a.Agent != "pi" {
		t.Errorf("root-a agent = %q, want pi", a.Agent)
	}
	if a.Origin != "backfilled" {
		t.Errorf("root-a origin = %q, want backfilled", a.Origin)
	}
	if a.CWD != "/home/alice/real-project" {
		t.Errorf("root-a cwd = %q, want /home/alice/real-project (from the session line, not the --home-wrong-slug-- directory)", a.CWD)
	}
	if !a.ProjectKey.Valid || a.ProjectKey.String != "/home/alice/real-project" {
		t.Errorf("root-a project_key = %v, want /home/alice/real-project", a.ProjectKey)
	}
	if a.PID.Valid {
		t.Errorf("root-a pid = %v, want NULL", a.PID)
	}
	if !a.EndedAt.Valid {
		t.Errorf("root-a ended_at = %v, want set (eof)", a.EndedAt)
	}

	aEvents := eventsForHarnessSession(t, st, "root-a")
	wantKindsA := []string{EventSessionStart, EventToolUse, EventToolResult, EventSessionEnd}
	if got := eventKinds(aEvents); !equalStrings(got, wantKindsA) {
		t.Errorf("root-a event kinds = %v, want %v", got, wantKindsA)
	}
	var startA payload.SessionStart
	if err := json.Unmarshal([]byte(aEvents[0].Payload), &startA); err != nil {
		t.Fatal(err)
	}
	if startA.Prompt != "Fix the login bug" {
		t.Errorf("root-a session.start prompt = %q, want %q", startA.Prompt, "Fix the login bug")
	}
	var resultA payload.ToolResult
	if err := json.Unmarshal([]byte(aEvents[2].Payload), &resultA); err != nil {
		t.Fatal(err)
	}
	if resultA.IsError {
		t.Errorf("root-a tool.result is_error = true, want false")
	}

	b := sessionByHarnessID(t, st, "root-b")
	if b.CWD != "/home/bob/widgets" {
		t.Errorf("root-b cwd = %q, want /home/bob/widgets", b.CWD)
	}
	if !b.ProjectKey.Valid || b.ProjectKey.String != "/home/bob/widgets" {
		t.Errorf("root-b project_key = %v, want /home/bob/widgets", b.ProjectKey)
	}

	bEvents := eventsForHarnessSession(t, st, "root-b")
	var resultB payload.ToolResult
	found := false
	for _, e := range bEvents {
		if e.Kind != EventToolResult {
			continue
		}
		if err := json.Unmarshal([]byte(e.Payload), &resultB); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("root-b: no tool.result event found")
	}
	if !resultB.IsError {
		t.Errorf("root-b tool.result is_error = false, want true (the fixture's toolResult carries isError:true)")
	}
	if resultB.ToolUseID != "call-b1" {
		t.Errorf("root-b tool.result tool_use_id = %q, want call-b1", resultB.ToolUseID)
	}
}

func eventKinds(events []eventRow) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestImportBranchedTreeDeterministicUnderShuffledLines is the punch's
// clause 1: --home-carol-proj-- and --home-carol-proj-shuffled-- hold the
// SAME tree (one assistant message, "asst1", with two children — an
// edit-and-resend branch) as two files whose lines are in different order
// on disk. Importing each into its own fresh store must produce the exact
// same tool.use/tool.result id sequence: the walk is driven by id/parentId,
// never by file position.
func TestImportBranchedTreeDeterministicUnderShuffledLines(t *testing.T) {
	// One store, one Import over both files: they carry distinct session ids
	// (root-c, root-c-shuf) so neither is deduped away, and each is compared.
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: filepath.Join("testdata", "branch", "sessions"), Git: fakeGit{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Both runs actually imported both the canonical and shuffled files
	// (Root globs both subdirectories); root-c is the canonical file's own
	// harness_session_id and is identical in both files' content, so
	// comparing its resulting event sequence across the two stores is the
	// determinism proof clause 1 wants. userB1 sorts before userB2 (equal
	// timestamps, id tie-break), so call-b1's pair must precede call-b2's.
	wantUseOrder := []string{"call-b1", "call-b2"}
	wantResultOrder := []string{"call-b1", "call-b2"}

	var canonEvents []eventRow
	for _, id := range []string{"root-c", "root-c-shuf"} {
		wantUseOrder, wantResultOrder := wantUseOrder, wantResultOrder
		if id == "root-c-shuf" {
			// tool ids are deduped store-wide, so the shuffled copy's calls
			// are named call-s1/call-s2 — same tree shape, same order.
			wantUseOrder, wantResultOrder = []string{"call-s1", "call-s2"}, []string{"call-s1", "call-s2"}
		}
		events := eventsForHarnessSession(t, st, id)
		if id == "root-c" {
			canonEvents = events
		} else if len(events) != len(canonEvents) {
			t.Fatalf("shuffled event count = %d, canonical = %d", len(events), len(canonEvents))
		} else {
			for i := range events {
				if events[i].Kind != canonEvents[i].Kind {
					t.Fatalf("shuffled event %d kind = %s, canonical %s", i, events[i].Kind, canonEvents[i].Kind)
				}
			}
		}
		kinds := eventKinds(events)
		wantKinds := []string{EventSessionStart, EventToolUse, EventToolResult, EventToolUse, EventToolResult, EventSessionEnd}
		if !equalStrings(kinds, wantKinds) {
			t.Fatalf("event kinds = %v, want %v", kinds, wantKinds)
		}
		useIDs := toolUseIDs(t, events, EventToolUse)
		resultIDs := toolUseIDs(t, events, EventToolResult)
		if !equalStrings(useIDs, wantUseOrder) {
			t.Fatalf("tool.use id order = %v, want %v (branch userB1 must sort before userB2 by id tie-break)", useIDs, wantUseOrder)
		}
		if !equalStrings(resultIDs, wantResultOrder) {
			t.Fatalf("tool.result id order = %v, want %v", resultIDs, wantResultOrder)
		}

		var start payload.SessionStart
		if err := json.Unmarshal([]byte(events[0].Payload), &start); err != nil {
			t.Fatal(err)
		}
		if start.Prompt != "Let's build a widget" {
			t.Errorf("session.start prompt = %q, want %q (the first user message in walk order, not file order)", start.Prompt, "Let's build a widget")
		}
	}
}

// TestImportPairsToolResultsByIDNotOrder is the punch's clause 3 and
// clause 5's second mutation target: asst-e1 issues two toolCalls
// (call-1, call-2) in one message, but their toolResult nodes appear in
// the OPPOSITE order in the tree (call-2's result sorts first). Pairing by
// id must still attribute each result to its own call; pairing by the
// order results are encountered would swap them — call-1 would wrongly
// come back isError:true (call-2's actual outcome) instead of its own
// isError:false.
func TestImportPairsToolResultsByIDNotOrder(t *testing.T) {
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: filepath.Join("testdata", "pairing", "sessions"), Git: fakeGit{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	events := eventsForHarnessSession(t, st, "root-e")
	results := map[string]payload.ToolResult{}
	for _, e := range events {
		if e.Kind != EventToolResult {
			continue
		}
		var p payload.ToolResult
		if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
			t.Fatal(err)
		}
		results[p.ToolUseID] = p
	}
	if len(results) != 2 {
		t.Fatalf("got %d tool.result events, want 2: %+v", len(results), results)
	}
	r1, ok := results["call-1"]
	if !ok {
		t.Fatal("no tool.result for call-1")
	}
	if r1.IsError {
		t.Errorf("call-1 tool.result is_error = true, want false (call-1's own result, not call-2's)")
	}
	if r1.Content != "call-1 ok" {
		t.Errorf("call-1 tool.result content = %q, want %q", r1.Content, "call-1 ok")
	}
	r2, ok := results["call-2"]
	if !ok {
		t.Fatal("no tool.result for call-2")
	}
	if !r2.IsError {
		t.Errorf("call-2 tool.result is_error = false, want true (call-2's own result, not call-1's)")
	}
	if r2.Content != "call-2 failed" {
		t.Errorf("call-2 tool.result content = %q, want %q", r2.Content, "call-2 failed")
	}
}

// TestImportIsIdempotent is the punch's clause 4: re-running Import against
// the same root adds zero sessions and zero events.
func TestImportIsIdempotent(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "sessions")

	res1, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (first run): %v", err)
	}
	if res1.SessionsCreated == 0 || res1.EventsCreated == 0 {
		t.Fatalf("first run created nothing: %+v", res1)
	}

	var sessionsBefore, eventsBefore int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionsBefore); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&eventsBefore); err != nil {
		t.Fatal(err)
	}

	res2, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (second run): %v", err)
	}
	if res2.SessionsCreated != 0 {
		t.Errorf("second run SessionsCreated = %d, want 0", res2.SessionsCreated)
	}
	if res2.EventsCreated != 0 {
		t.Errorf("second run EventsCreated = %d, want 0", res2.EventsCreated)
	}

	var sessionsAfter, eventsAfter int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionsAfter); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&eventsAfter); err != nil {
		t.Fatal(err)
	}
	if sessionsAfter != sessionsBefore {
		t.Errorf("sessions count changed on rerun: %d -> %d", sessionsBefore, sessionsAfter)
	}
	if eventsAfter != eventsBefore {
		t.Errorf("timeline_events count changed on rerun: %d -> %d", eventsBefore, eventsAfter)
	}
}

// TestImportProviderAgnostic is the punch's clause 3: a local-llama
// transcript imports identically (same counts, same event kind sequence)
// to a hosted-provider transcript of the same shape — the importer never
// branches on provider/modelId.
func TestImportProviderAgnostic(t *testing.T) {
	hosted := mustOpenStore(t)
	resHosted, err := Import(hosted, Options{Root: filepath.Join("testdata", "provider", "hosted", "sessions"), Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import hosted: %v", err)
	}

	local := mustOpenStore(t)
	resLocal, err := Import(local, Options{Root: filepath.Join("testdata", "provider", "local", "sessions"), Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import local: %v", err)
	}

	if resHosted.SessionsCreated != resLocal.SessionsCreated || resHosted.EventsCreated != resLocal.EventsCreated ||
		resHosted.LinesSkipped != resLocal.LinesSkipped || resHosted.FilesScanned != resLocal.FilesScanned {
		t.Fatalf("hosted result %+v != local result %+v", resHosted, resLocal)
	}

	hostedEvents := eventsForHarnessSession(t, hosted, "root-h")
	localEvents := eventsForHarnessSession(t, local, "root-l")
	if !equalStrings(eventKinds(hostedEvents), eventKinds(localEvents)) {
		t.Fatalf("hosted event kinds %v != local event kinds %v", eventKinds(hostedEvents), eventKinds(localEvents))
	}
}
