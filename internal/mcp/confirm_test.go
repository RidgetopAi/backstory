package mcp

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestConfirmPromoteEndToEnd is the punch's DONE WHEN clause 1, promote
// path: confirm through tools/call -> socket -> daemon -> store inserts a
// new confirm record whose id and edge (informs -> the draft) the
// CallToolResult text names, and the edge is readable back from the store
// directly. The draft is inserted directly through the store at
// tier=inferred, attributed to a session id distinct from this connection's
// own real session (no inference pass is wired up yet to write one through
// the tool surface itself), so the promoting call is genuinely a different
// session's promotion, not a self-promotion.
func TestConfirmPromoteEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	draftID := mustInsertInferredDraftDirect(t, st, "proj-key", "inferred: chose sqlite")

	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"promote"}`, draftID)))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm promote) error: %+v", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("tools/call(confirm promote) isError = true, want false: %s", result.Content[0].Text)
	}
	var confirmed ConfirmResult
	if err := json.Unmarshal(result.StructuredContent, &confirmed); err != nil {
		t.Fatalf("unmarshal structuredContent as ConfirmResult: %v", err)
	}
	if confirmed.ID == "" {
		t.Fatal("structuredContent has no id")
	}
	if confirmed.ID == draftID {
		t.Fatal("ConfirmResult.ID equals the draft's own id, want a NEW confirm record")
	}
	if confirmed.Tier != string(store.TierAgentDeclared) {
		t.Errorf("ConfirmResult.Tier = %q, want %q", confirmed.Tier, store.TierAgentDeclared)
	}
	if confirmed.EdgeType != string(store.EdgeInforms) {
		t.Errorf("ConfirmResult.EdgeType = %q, want %q", confirmed.EdgeType, store.EdgeInforms)
	}
	if confirmed.TargetID != draftID {
		t.Errorf("ConfirmResult.TargetID = %q, want %q", confirmed.TargetID, draftID)
	}

	// The CallToolResult's rendered text names both the new record id and
	// the edge it created (id + edge_type + target_id all round-trip
	// through the same content[0].text block every successful tool call
	// uses).
	if !strings.Contains(result.Content[0].Text, confirmed.ID) {
		t.Errorf("content[0].text = %q, want it to name the new confirm record id %q", result.Content[0].Text, confirmed.ID)
	}
	if !strings.Contains(result.Content[0].Text, string(store.EdgeInforms)) {
		t.Errorf("content[0].text = %q, want it to name the edge type %q", result.Content[0].Text, store.EdgeInforms)
	}

	rec, err := st.GetRecord(confirmed.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Kind != store.KindConfirm {
		t.Errorf("stored record kind = %q, want %q", rec.Kind, store.KindConfirm)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'informs'`,
		confirmed.ID, draftID).Scan(&n); err != nil {
		t.Fatalf("count informs edge: %v", err)
	}
	if n != 1 {
		t.Errorf("informs edge %s -> %s count = %d, want 1", confirmed.ID, draftID, n)
	}
}

// TestConfirmPromoteSameSessionRejectedEndToEnd drives SCHEMA.md invariant
// 3 through the full tools/call -> socket -> daemon -> store path: the SAME
// session that (stood in for) inferring a draft cannot promote it, and the
// rejection comes back as a CallToolResult with isError:true (not a
// JSON-RPC error), the same shape note's own rejections use.
func TestConfirmPromoteSameSessionRejectedEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	draft := writeInferredDraft(t, st, s, "proj-key", "inferred: chose sqlite")

	before := countStoreRecords(t, st)
	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"promote"}`, draft.draftID)))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm promote) by the drafting session came back as a JSON-RPC error %+v, want a CallToolResult with isError:true", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if !result.IsError {
		t.Fatalf("tools/call(confirm promote) by the drafting session isError = false, want true: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "session") {
		t.Errorf("content[0].text = %q, want it to name the same-session rejection", result.Content[0].Text)
	}
	if len(result.StructuredContent) != 0 {
		t.Errorf("structuredContent = %s, want empty on a rejected call", result.StructuredContent)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected self-promotion, want unchanged %d", got, before)
	}
}

// TestConfirmContradictEndToEnd is DONE WHEN clause 1's contradict path.
func TestConfirmContradictEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	target := mustNoteViaToolsCall(t, s, `{"kind":"outcome","text":"tests are green"}`)
	sessionID := recordSessionID(t, st, target.ID)

	evID, err := st.AppendEvent(store.Event{
		Kind: "tool.result", SessionID: sessionID, Source: "posttooluse", Payload: `{"exit":1}`,
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"contradict","evidence":[%d]}`, target.ID, evID)))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm contradict) error: %+v", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("tools/call(confirm contradict) isError = true, want false: %s", result.Content[0].Text)
	}
	var confirmed ConfirmResult
	if err := json.Unmarshal(result.StructuredContent, &confirmed); err != nil {
		t.Fatalf("unmarshal structuredContent as ConfirmResult: %v", err)
	}
	if confirmed.EdgeType != string(store.EdgeContradicts) {
		t.Errorf("ConfirmResult.EdgeType = %q, want %q", confirmed.EdgeType, store.EdgeContradicts)
	}
	if !strings.Contains(result.Content[0].Text, confirmed.ID) || !strings.Contains(result.Content[0].Text, string(store.EdgeContradicts)) {
		t.Errorf("content[0].text = %q, want it to name the new record id and the contradicts edge", result.Content[0].Text)
	}

	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'contradicts'`,
		confirmed.ID, target.ID).Scan(&n); err != nil {
		t.Fatalf("count contradicts edge: %v", err)
	}
	if n != 1 {
		t.Errorf("contradicts edge %s -> %s count = %d, want 1", confirmed.ID, target.ID, n)
	}
}

// TestConfirmContradictWithNoEvidenceEndToEnd is DONE WHEN clause 2's first
// half, driven through the MCP server's tools/call path.
func TestConfirmContradictWithNoEvidenceEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	target := mustNoteViaToolsCall(t, s, `{"kind":"outcome","text":"tests are green"}`)

	before := countStoreRecords(t, st)
	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"contradict"}`, target.ID)))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm contradict) with no evidence came back as a JSON-RPC error %+v, want a CallToolResult with isError:true", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if !result.IsError {
		t.Fatalf("tools/call(confirm contradict) with no evidence isError = false, want true: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "evidence") {
		t.Errorf("content[0].text = %q, want it to name \"evidence\"", result.Content[0].Text)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected contradict, want unchanged %d", got, before)
	}
	var edgeCount int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&edgeCount); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edgeCount != 0 {
		t.Errorf("edge count = %d after a rejected contradict, want 0", edgeCount)
	}
}

// TestConfirmContradictWithUnknownEvidenceEndToEnd is DONE WHEN clause 2's
// second half, driven through the MCP server's tools/call path: an evidence
// id not present in timeline_events for the caller's project is rejected
// and writes nothing.
func TestConfirmContradictWithUnknownEvidenceEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	target := mustNoteViaToolsCall(t, s, `{"kind":"outcome","text":"tests are green"}`)

	before := countStoreRecords(t, st)
	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"contradict","evidence":[999999]}`, target.ID)))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm contradict) with an unknown evidence id came back as a JSON-RPC error %+v, want a CallToolResult with isError:true", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if !result.IsError {
		t.Fatalf("tools/call(confirm contradict) with an unknown evidence id isError = false, want true: %s", result.Content[0].Text)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected contradict, want unchanged %d", got, before)
	}
	var edgeCount int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&edgeCount); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edgeCount != 0 {
		t.Errorf("edge count = %d after a rejected contradict, want 0", edgeCount)
	}
}

// TestConfirmSupersedeEndToEnd is DONE WHEN clause 1's supersede path.
func TestConfirmSupersedeEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	target := mustNoteViaToolsCall(t, s, `{"kind":"handoff","text":"first handoff"}`)

	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"supersede"}`, target.ID)))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm supersede) error: %+v", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("tools/call(confirm supersede) isError = true, want false: %s", result.Content[0].Text)
	}
	var confirmed ConfirmResult
	if err := json.Unmarshal(result.StructuredContent, &confirmed); err != nil {
		t.Fatalf("unmarshal structuredContent as ConfirmResult: %v", err)
	}
	if confirmed.EdgeType != string(store.EdgeSupersedes) {
		t.Errorf("ConfirmResult.EdgeType = %q, want %q", confirmed.EdgeType, store.EdgeSupersedes)
	}
	if !strings.Contains(result.Content[0].Text, confirmed.ID) || !strings.Contains(result.Content[0].Text, string(store.EdgeSupersedes)) {
		t.Errorf("content[0].text = %q, want it to name the new record id and the supersedes edge", result.Content[0].Text)
	}

	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'supersedes'`,
		confirmed.ID, target.ID).Scan(&n); err != nil {
		t.Fatalf("count supersedes edge: %v", err)
	}
	if n != 1 {
		t.Errorf("supersedes edge %s -> %s count = %d, want 1", confirmed.ID, target.ID, n)
	}
}

// TestConfirmAffirmEndToEnd is DONE WHEN clause 1's affirm path — the
// additive action this punch adds: "still true as of now".
func TestConfirmAffirmEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	target := mustNoteViaToolsCall(t, s, `{"kind":"handoff","text":"resume from here"}`)

	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"affirm","text":"still true"}`, target.ID)))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm affirm) error: %+v", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("tools/call(confirm affirm) isError = true, want false: %s", result.Content[0].Text)
	}
	var confirmed ConfirmResult
	if err := json.Unmarshal(result.StructuredContent, &confirmed); err != nil {
		t.Fatalf("unmarshal structuredContent as ConfirmResult: %v", err)
	}
	if confirmed.EdgeType != string(store.EdgeInforms) {
		t.Errorf("ConfirmResult.EdgeType = %q, want %q", confirmed.EdgeType, store.EdgeInforms)
	}
	if !strings.Contains(result.Content[0].Text, confirmed.ID) || !strings.Contains(result.Content[0].Text, string(store.EdgeInforms)) {
		t.Errorf("content[0].text = %q, want it to name the new record id and the informs edge", result.Content[0].Text)
	}

	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'informs'`,
		confirmed.ID, target.ID).Scan(&n); err != nil {
		t.Fatalf("count informs edge: %v", err)
	}
	if n != 1 {
		t.Errorf("informs edge %s -> %s count = %d, want 1", confirmed.ID, target.ID, n)
	}
}

// TestConfirmMissingRecordIDIsRejectedAndInsertsNothing exercises confirm's
// own required-field validation, the same way note.go's missing-kind test
// does for note, through tools/call.
func TestConfirmMissingRecordIDIsRejectedAndInsertsNothing(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	before := countStoreRecords(t, st)
	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(`{"action":"affirm"}`))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm) with no record_id came back as a JSON-RPC error %+v, want a CallToolResult with isError:true", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if !result.IsError {
		t.Fatalf("tools/call(confirm) with no record_id isError = false, want true: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "record_id") {
		t.Errorf("content[0].text = %q, want it to name \"record_id\"", result.Content[0].Text)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected confirm, want unchanged %d", got, before)
	}
}

// TestConfirmUnknownActionIsRejectedAndInsertsNothing.
func TestConfirmUnknownActionIsRejectedAndInsertsNothing(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	target := mustNoteViaToolsCall(t, s, `{"kind":"note","text":"a target"}`)

	before := countStoreRecords(t, st)
	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"banana"}`, target.ID)))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm) with action=banana came back as a JSON-RPC error %+v, want a CallToolResult with isError:true", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if !result.IsError {
		t.Fatalf("tools/call(confirm) with action=banana isError = false, want true: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "action") {
		t.Errorf("content[0].text = %q, want it to name \"action\"", result.Content[0].Text)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected confirm, want unchanged %d", got, before)
	}
}

// TestConfirmIgnoresForgedTierAndSession mirrors TestNoteIgnoresForgedTierAndSession:
// a confirm carrying {tier:'human-declared'} or {session:'forged'} in its
// arguments is inserted at agent-declared regardless (AGENT-CONTRACT.md
// §The never-list, item 2).
func TestConfirmIgnoresForgedTierAndSession(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	target := mustNoteViaToolsCall(t, s, `{"kind":"note","text":"a target"}`)

	resp := serveOneToolCall(t, s, ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"affirm","tier":"human-declared","session":"forged"}`, target.ID)))
	if resp.Error != nil {
		t.Fatalf("tools/call(confirm) error: %+v", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("tools/call(confirm) isError = true, want false: %s", result.Content[0].Text)
	}
	var confirmed ConfirmResult
	if err := json.Unmarshal(result.StructuredContent, &confirmed); err != nil {
		t.Fatalf("unmarshal structuredContent as ConfirmResult: %v", err)
	}
	if confirmed.Tier != string(store.TierAgentDeclared) {
		t.Fatalf("ConfirmResult.Tier = %q, want %q (forged tier must not apply)", confirmed.Tier, store.TierAgentDeclared)
	}
}

// mustNoteViaToolsCall drives a note through s's tools/call path (not
// Server.CallTool directly) and returns its NoteResult, for confirm tests
// that need a target record already anchored in this connection's session.
func mustNoteViaToolsCall(t *testing.T, s *Server, argsJSON string) NoteResult {
	t.Helper()
	resp := serveOneToolCall(t, s, ToolNote, json.RawMessage(argsJSON))
	if resp.Error != nil {
		t.Fatalf("tools/call(note) target: %+v", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("tools/call(note) target isError = true, want false: %s", result.Content[0].Text)
	}
	var note NoteResult
	if err := json.Unmarshal(result.StructuredContent, &note); err != nil {
		t.Fatalf("unmarshal structuredContent as NoteResult: %v", err)
	}
	return note
}

// mustInsertInferredDraftDirect mints a genuine second store session (a real
// foreign-key-valid row, distinct from whatever session the test's own
// socket connection is using) and inserts a tier=inferred record attributed
// to it — standing in for a draft Phase 5's (not yet built) inference pass
// would have written through some other session entirely.
func mustInsertInferredDraftDirect(t *testing.T, st *store.Store, projectKey, text string) string {
	t.Helper()
	if err := st.UpsertProject(store.Project{Key: projectKey, Toplevel: "/proj", FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	sessionID, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: "/proj", ProjectKey: projectKey, StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity:   store.Identity{Kind: store.IdentityInference},
		Kind:       store.KindNote,
		Text:       text,
		SessionID:  sessionID,
		ProjectKey: projectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord (inferred draft): %v", err)
	}
	return id
}

type inferredDraft struct {
	draftID   string
	sessionID string
}

// writeInferredDraft writes a note through s's tools/call path (establishing/
// using this connection's live session), reads back its session id, then
// inserts a SEPARATE tier=inferred record directly through the store
// attributed to that same session — standing in for a draft Phase 5's (not
// yet built) inference pass would have written through this session.
func writeInferredDraft(t *testing.T, st *store.Store, s *Server, projectKey, text string) inferredDraft {
	t.Helper()
	anchor := mustNoteViaToolsCall(t, s, `{"kind":"note","text":"session anchor"}`)
	sessionID := recordSessionID(t, st, anchor.ID)

	draftID, err := st.InsertRecord(store.InsertRecordParams{
		Identity:   store.Identity{Kind: store.IdentityInference},
		Kind:       store.KindNote,
		Text:       text,
		SessionID:  sessionID,
		ProjectKey: projectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord (inferred draft): %v", err)
	}
	return inferredDraft{draftID: draftID, sessionID: sessionID}
}

func recordSessionID(t *testing.T, st *store.Store, recordID string) string {
	t.Helper()
	rec, err := st.GetRecord(recordID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.SessionID == "" {
		t.Fatalf("record %s has no session_id", recordID)
	}
	return rec.SessionID
}
