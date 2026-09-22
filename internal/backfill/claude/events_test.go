package claude

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

type eventRow struct {
	ID      int64
	TS      int64
	Kind    string
	Source  string
	Payload string
}

// TestEventsInFileOrderWithSkips is the punch's clause 2: events for a
// session appear in FILE ORDER by rowid regardless of each line's own
// timestamp, session.start carries the first non-isMeta user prompt,
// session.end is last, and non-user/assistant lines plus one malformed
// line are skipped without aborting the file.
func TestEventsInFileOrderWithSkips(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "events", "projects")

	res, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.FilesScanned != 1 {
		t.Fatalf("FilesScanned = %d, want 1", res.FilesScanned)
	}
	if res.SessionsCreated != 1 {
		t.Fatalf("SessionsCreated = %d, want 1 (one bad line must not abort the file)", res.SessionsCreated)
	}
	// system, summary, and the malformed "{this is not valid json..." line.
	if res.LinesSkipped != 3 {
		t.Fatalf("LinesSkipped = %d, want 3", res.LinesSkipped)
	}

	sess := sessionByHarnessID(t, st, "sess-e-uuid")

	rows, err := st.DB().Query(`SELECT id, ts, kind, source, payload FROM timeline_events
		WHERE session_id = ? ORDER BY id ASC`, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var events []eventRow
	for rows.Next() {
		var e eventRow
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.Source, &e.Payload); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	wantKinds := []string{
		EventSessionStart,
		EventToolUse,    // tu1 Edit
		EventToolResult, // tu1 result
		EventToolUse,    // tu2 Bash, despite an earlier ts than everything before it
		EventToolResult, // tu2 result
		EventSessionEnd,
	}
	if len(events) != len(wantKinds) {
		t.Fatalf("got %d events, want %d; events=%+v", len(events), len(wantKinds), events)
	}
	for i, e := range events {
		if e.Kind != wantKinds[i] {
			t.Errorf("event[%d].Kind = %q, want %q", i, e.Kind, wantKinds[i])
		}
		if e.Source != Source {
			t.Errorf("event[%d].Source = %q, want %q", i, e.Source, Source)
		}
	}

	// session.start carries the first non-isMeta prompt, never the
	// harness-injected isMeta line that came first in the file.
	var start struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(events[0].Payload), &start); err != nil {
		t.Fatalf("unmarshal session.start payload: %v", err)
	}
	if start.Prompt != "Please refactor foo.go" {
		t.Errorf("session.start prompt = %q, want %q (isMeta line must never become the prompt)", start.Prompt, "Please refactor foo.go")
	}

	// tool.use extracts the right detail per named extractor: Edit -> file
	// path, Bash -> command.
	var toolUse1 struct {
		ToolUseID string `json:"tool_use_id"`
		Name      string `json:"name"`
		Detail    string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(events[1].Payload), &toolUse1); err != nil {
		t.Fatal(err)
	}
	if toolUse1.Name != "Edit" || toolUse1.Detail != "/home/dana/events/foo.go" {
		t.Errorf("tool.use[0] = %+v, want Edit /home/dana/events/foo.go", toolUse1)
	}

	var toolUse2 struct {
		ToolUseID string `json:"tool_use_id"`
		Name      string `json:"name"`
		Detail    string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(events[3].Payload), &toolUse2); err != nil {
		t.Fatal(err)
	}
	if toolUse2.Name != "Bash" || toolUse2.Detail != "go test ./..." {
		t.Errorf("tool.use[1] = %+v, want Bash \"go test ./...\"", toolUse2)
	}

	// tool.result carries is_error and content.
	var result1 struct {
		ToolUseID string `json:"tool_use_id"`
		IsError   bool   `json:"is_error"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(events[2].Payload), &result1); err != nil {
		t.Fatal(err)
	}
	if result1.ToolUseID != "tu1" || result1.IsError || result1.Content != "edited successfully" {
		t.Errorf("tool.result[0] = %+v, want tu1/false/\"edited successfully\"", result1)
	}

	// FILE ORDER, not timestamp order: tu2's tool.use event (events[3]) has
	// an earlier ts than tu1's tool.use event (events[1]) even though its
	// rowid places it after — proving the importer never sorts by ts.
	if !time.Unix(0, events[3].TS).Before(time.Unix(0, events[1].TS)) {
		t.Fatalf("fixture setup: expected events[3].TS before events[1].TS, got %d vs %d", events[3].TS, events[1].TS)
	}
	if events[3].ID <= events[1].ID {
		t.Errorf("events[3].ID (%d) <= events[1].ID (%d), want file order (later ts-earlier line still gets the later rowid)", events[3].ID, events[1].ID)
	}
}
