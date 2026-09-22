package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// subagentsRoot is the shared fixture for this file's tests:
//
//	-home-brian-app/374f122f.jsonl                         main transcript
//	-home-brian-app/374f122f/subagents/agent-abc123ef.jsonl   its subagent
//	-home-brian-app/f2083274.jsonl                          second main transcript
//	-home-brian-app/f2083274/subagents/agent-bad000001.jsonl  malformed subagent (clause 4)
//	-home-brian-app/f2083274/subagents/agent-good000002.jsonl valid sibling subagent
//	-home-brian-app/deadbeef00/subagents/agent-def456ab.jsonl orphan subagent, no deadbeef00.jsonl (clause 4)
//
// modelled directly on the field measurement in the punch: 61 files on
// Brian's desktop, 9 subagent transcripts with no cursor row, one under
// session 374f122f and eight under f2083274.
func subagentsRoot() string {
	return filepath.Join("testdata", "subagents", "projects")
}

// eventPayloadsForSession returns, in rowid order, the payload of every
// timeline event of kind belonging to sessionID.
func eventPayloadsForSession(t *testing.T, st *store.Store, sessionID, kind string) []string {
	t.Helper()
	rows, err := st.DB().Query(`SELECT payload FROM timeline_events
		WHERE session_id = ? AND kind = ? ORDER BY id ASC`, sessionID, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSubagentTranscriptImportsAndAttributesToParentSession is the punch's
// clauses 1 and 2 (task 6047db51): a nested <slug>/<sessionId>/subagents/agent-*.jsonl
// transcript that the old one-level glob never opened at all now produces
// timeline events, attributed to the session whose id is the parent
// directory name (374f122f), each carrying a typed agent_id field that
// names the agent transcript (agent-abc123ef) — distinguishing it from the
// parent session's own main-thread events, which carry no agent_id.
func TestSubagentTranscriptImportsAndAttributesToParentSession(t *testing.T) {
	st := mustOpenStore(t)
	root := subagentsRoot()

	res, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Exactly 2 real sessions: 374f122f and f2083274. The subagent walk
	// must never mint a session of its own (it shares its parent's).
	if res.SessionsCreated != 2 {
		t.Fatalf("SessionsCreated = %d, want 2 (subagent files must not mint their own session)", res.SessionsCreated)
	}
	var totalSessions int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&totalSessions); err != nil {
		t.Fatal(err)
	}
	if totalSessions != 2 {
		t.Fatalf("sessions table has %d rows, want exactly 2", totalSessions)
	}

	parent := sessionByHarnessID(t, st, "374f122f-real")

	toolUses := eventPayloadsForSession(t, st, parent.ID, EventToolUse)
	if len(toolUses) != 1 {
		t.Fatalf("session 374f122f has %d tool.use events, want 1 (from its subagent, attributed here) — got %+v", len(toolUses), toolUses)
	}
	var tu struct {
		ToolUseID string `json:"tool_use_id"`
		Name      string `json:"name"`
		Command   string `json:"command"`
		AgentID   string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(toolUses[0]), &tu); err != nil {
		t.Fatal(err)
	}
	if tu.Name != "Bash" || tu.Command != "go test ./... -run TestFlaky -count=20" {
		t.Errorf("subagent tool.use = %+v, want Bash \"go test ./... -run TestFlaky -count=20\"", tu)
	}
	if tu.AgentID != "agent-abc123ef" {
		t.Errorf("subagent tool.use agent_id = %q, want %q — clause 2's typed marker", tu.AgentID, "agent-abc123ef")
	}

	results := eventPayloadsForSession(t, st, parent.ID, EventToolResult)
	if len(results) != 1 {
		t.Fatalf("session 374f122f has %d tool.result events, want 1", len(results))
	}
	var tr struct {
		ToolUseID string `json:"tool_use_id"`
		Content   string `json:"content"`
		AgentID   string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(results[0]), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.ToolUseID != "sub-tu1" || tr.Content == "" {
		t.Errorf("subagent tool.result = %+v, want tool_use_id sub-tu1 with content", tr)
	}
	if tr.AgentID != "agent-abc123ef" {
		t.Errorf("subagent tool.result agent_id = %q, want %q", tr.AgentID, "agent-abc123ef")
	}

	// The parent's own main-thread events (session.start/session.end) must
	// never carry an agent_id: only the replayed subagent events do.
	starts := eventPayloadsForSession(t, st, parent.ID, EventSessionStart)
	if len(starts) != 1 {
		t.Fatalf("session 374f122f has %d session.start events, want 1", len(starts))
	}
	if json.Valid([]byte(starts[0])) {
		var s map[string]any
		_ = json.Unmarshal([]byte(starts[0]), &s)
		if _, ok := s["agent_id"]; ok {
			t.Errorf("main-thread session.start payload carries agent_id, want none: %s", starts[0])
		}
	}

	// The second session (f2083274) picks up its own valid sibling
	// subagent's tool.use, proving attribution keys off each subagent
	// file's own parent directory name, not a global default.
	parent2 := sessionByHarnessID(t, st, "f2083274-real")
	toolUses2 := eventPayloadsForSession(t, st, parent2.ID, EventToolUse)
	if len(toolUses2) != 1 {
		t.Fatalf("session f2083274 has %d tool.use events, want 1 (from agent-good000002 only)", len(toolUses2))
	}
	var tu2 struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(toolUses2[0]), &tu2); err != nil {
		t.Fatal(err)
	}
	if tu2.Name != "Edit" || tu2.Path != "/home/brian/app2/payment.go" {
		t.Errorf("f2083274 tool.use = %+v, want Edit /home/brian/app2/payment.go", tu2)
	}
	if tu2.AgentID != "agent-good000002" {
		t.Errorf("f2083274 tool.use agent_id = %q, want %q", tu2.AgentID, "agent-good000002")
	}
}

// TestSubagentImportIsIdempotent is the punch's clause 3: the subagent file
// gets its own backfill_cursors row, keyed by its own path — distinct from
// its parent's — and a second Import over the unchanged fixture adds
// exactly zero timeline events and zero sessions.
func TestSubagentImportIsIdempotent(t *testing.T) {
	st := mustOpenStore(t)
	root := subagentsRoot()

	res1, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (round 1): %v", err)
	}
	if res1.EventsCreated == 0 {
		t.Fatalf("round 1 EventsCreated = 0, want > 0 (fixture setup)")
	}

	subagentPath := filepath.Join(root, "-home-brian-app", "374f122f", "subagents", "agent-abc123ef.jsonl")
	parentPath := filepath.Join(root, "-home-brian-app", "374f122f.jsonl")

	subCursor, exists, err := st.GetBackfillCursor(Source, subagentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("no backfill_cursors row for the subagent's own path %s", subagentPath)
	}
	parentCursor, exists, err := st.GetBackfillCursor(Source, parentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("no backfill_cursors row for the parent path %s", parentPath)
	}
	if subCursor.SessionID != parentCursor.SessionID {
		t.Errorf("subagent cursor session_id = %q, parent cursor session_id = %q, want equal (same attributed session)",
			subCursor.SessionID, parentCursor.SessionID)
	}

	var totalCursors int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM backfill_cursors WHERE path = ?`, subagentPath).Scan(&totalCursors); err != nil {
		t.Fatal(err)
	}
	if totalCursors != 1 {
		t.Fatalf("backfill_cursors has %d row(s) for %s, want exactly 1", totalCursors, subagentPath)
	}

	var eventsBefore, sessionsBefore int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&eventsBefore); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionsBefore); err != nil {
		t.Fatal(err)
	}

	res2, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (round 2): %v", err)
	}
	if res2.EventsCreated != 0 {
		t.Errorf("round 2 EventsCreated = %d, want 0 (unchanged fixture, cursors already at EOF)", res2.EventsCreated)
	}
	if res2.SessionsCreated != 0 {
		t.Errorf("round 2 SessionsCreated = %d, want 0", res2.SessionsCreated)
	}

	var eventsAfter, sessionsAfter int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&eventsAfter); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionsAfter); err != nil {
		t.Fatal(err)
	}
	if eventsAfter != eventsBefore {
		t.Errorf("timeline_events grew from %d to %d on an unchanged re-import", eventsBefore, eventsAfter)
	}
	if sessionsAfter != sessionsBefore {
		t.Errorf("sessions grew from %d to %d on an unchanged re-import", sessionsBefore, sessionsAfter)
	}
}

// TestSubagentDegenerateInputsSkippedWithoutFailingImport is the punch's
// clause 4: a subagents/ directory whose parent transcript is absent
// (deadbeef00) and a malformed agent-*.jsonl (agent-bad000001, under
// f2083274) are each skipped without failing the whole Import — every
// other file in the same root, including f2083274's other, valid subagent
// (agent-good000002), still imports.
func TestSubagentDegenerateInputsSkippedWithoutFailingImport(t *testing.T) {
	st := mustOpenStore(t)
	root := subagentsRoot()

	res, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Only the 2 real parent transcripts ever mint a session — the orphan
	// subagent (deadbeef00) must never mint one of its own, and must not
	// abort the run.
	var totalSessions int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&totalSessions); err != nil {
		t.Fatal(err)
	}
	if totalSessions != 2 {
		t.Fatalf("sessions table has %d rows, want exactly 2 (deadbeef00's orphan subagent must not mint one)", totalSessions)
	}

	// The orphan's file never got far enough to be opened at all: no
	// cursor row for it (it is skipped at the parent-lookup step, before
	// any file read — the same "never opened" bug class this punch
	// fixes, now applied deliberately to a file with no valid parent).
	orphanPath := filepath.Join(root, "-home-brian-app", "deadbeef00", "subagents", "agent-def456ab.jsonl")
	if _, exists, err := st.GetBackfillCursor(Source, orphanPath); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Errorf("orphan subagent %s has a backfill_cursors row, want none (its parent transcript does not exist)", orphanPath)
	}

	// The malformed file contributes zero events and no cursor (mirrors
	// importFile's "nothing to anchor on" rule), but must not error out
	// the whole Import, and must not have kept f2083274's other, valid
	// subagent (agent-good000002) or its parent transcript from importing.
	malformedPath := filepath.Join(root, "-home-brian-app", "f2083274", "subagents", "agent-bad000001.jsonl")
	if _, exists, err := st.GetBackfillCursor(Source, malformedPath); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Errorf("malformed subagent %s has a backfill_cursors row, want none (every line failed to parse)", malformedPath)
	}
	if res.LinesSkipped < 1 {
		t.Errorf("LinesSkipped = %d, want >= 1 (the malformed subagent's one garbage line)", res.LinesSkipped)
	}

	parent2 := sessionByHarnessID(t, st, "f2083274-real")
	toolUses2 := eventPayloadsForSession(t, st, parent2.ID, EventToolUse)
	if len(toolUses2) != 1 {
		t.Fatalf("session f2083274 has %d tool.use events, want exactly 1 (agent-good000002's, despite its malformed sibling)", len(toolUses2))
	}

	parent1 := sessionByHarnessID(t, st, "374f122f-real")
	toolUses1 := eventPayloadsForSession(t, st, parent1.ID, EventToolUse)
	if len(toolUses1) != 1 {
		t.Fatalf("session 374f122f has %d tool.use events, want exactly 1 (its own subagent must be unaffected by the other slug's degenerate inputs)", len(toolUses1))
	}
}

// TestSubagentGlobDoesNotRecurseUnboundedDepth is part of clause 4's "the
// walk matches only the known layout and does not recurse to unbounded
// depth": an agent-*.jsonl file nested one level deeper than the known
// layout (subagents/nested/agent-*.jsonl) must never be picked up.
func TestSubagentGlobDoesNotRecurseUnboundedDepth(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "-home-x-y", "sess1", "subagents", "nested", "agent-toodeep.jsonl")
	if err := os.MkdirAll(filepath.Dir(deep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deep, []byte(`{"type":"user","uuid":"u1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := subagentGlob(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("subagentGlob found %v under an extra nesting level, want none (bounded to the known layout only)", files)
	}
}
