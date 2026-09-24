package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestConfirmPromoteEndToEnd is the punch's DONE WHEN clause 1, promote
// path: confirm through the shim -> socket -> daemon -> store inserts a new
// confirm record whose id and edge (informs -> the draft) the CallToolResult
// text names, and the edge is readable back from the store directly. The
// draft is inserted directly through the store at tier=inferred, attributed
// to a session id distinct from this connection's own real session (no
// inference pass is wired up yet to write one through the tool surface
// itself), so the promoting call is genuinely a different session's
// promotion, not a self-promotion.
func TestConfirmPromoteEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	draftID := mustInsertInferredDraftDirect(t, st, "proj-key", "inferred: chose sqlite")

	raw, rerr := shim.CallTool(ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"promote"}`, draftID)))
	if rerr != nil {
		t.Fatalf("CallTool(confirm promote): %v", rerr)
	}
	var result ConfirmResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal ConfirmResult: %v", err)
	}
	if result.ID == "" {
		t.Fatal("ConfirmResult.ID is empty")
	}
	if result.ID == draftID {
		t.Fatal("ConfirmResult.ID equals the draft's own id, want a NEW confirm record")
	}
	if result.Tier != string(store.TierAgentDeclared) {
		t.Errorf("ConfirmResult.Tier = %q, want %q", result.Tier, store.TierAgentDeclared)
	}
	if result.EdgeType != string(store.EdgeInforms) {
		t.Errorf("ConfirmResult.EdgeType = %q, want %q", result.EdgeType, store.EdgeInforms)
	}
	if result.TargetID != draftID {
		t.Errorf("ConfirmResult.TargetID = %q, want %q", result.TargetID, draftID)
	}

	// The CallToolResult's rendered text names both the new record id and
	// the edge it created (id + edge_type + target_id all round-trip
	// through the same JSON text block every successful tool call uses).
	if !strings.Contains(string(raw), result.ID) {
		t.Errorf("CallToolResult text = %q, want it to name the new confirm record id %q", raw, result.ID)
	}
	if !strings.Contains(string(raw), string(store.EdgeInforms)) {
		t.Errorf("CallToolResult text = %q, want it to name the edge type %q", raw, store.EdgeInforms)
	}

	rec, err := st.GetRecord(result.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Kind != store.KindConfirm {
		t.Errorf("stored record kind = %q, want %q", rec.Kind, store.KindConfirm)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'informs'`,
		result.ID, draftID).Scan(&n); err != nil {
		t.Fatalf("count informs edge: %v", err)
	}
	if n != 1 {
		t.Errorf("informs edge %s -> %s count = %d, want 1", result.ID, draftID, n)
	}
}

// TestConfirmPromoteSameSessionRejectedEndToEnd drives SCHEMA.md invariant
// 3 through the full shim -> socket -> daemon -> store path: the SAME
// session that (stood in for) inferring a draft cannot promote it.
func TestConfirmPromoteSameSessionRejectedEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	draft := writeInferredDraft(t, st, shim, "proj-key", "inferred: chose sqlite")

	before := countStoreRecords(t, st)
	_, rerr := shim.CallTool(ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"promote"}`, draft.draftID)))
	if rerr == nil {
		t.Fatal("CallTool(confirm promote) by the drafting session = nil error, want an error")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected self-promotion, want unchanged %d", got, before)
	}
}

// TestConfirmContradictEndToEnd is DONE WHEN clause 1's contradict path.
func TestConfirmContradictEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	targetRaw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"outcome","text":"tests are green"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) target: %v", rerr)
	}
	var target NoteResult
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}
	sessionID := recordSessionID(t, st, target.ID)

	evID, err := st.AppendEvent(store.Event{
		Kind: "tool.result", SessionID: sessionID, Source: "posttooluse", Payload: `{"exit":1}`,
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	raw, rerr := shim.CallTool(ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"contradict","evidence":[%d]}`, target.ID, evID)))
	if rerr != nil {
		t.Fatalf("CallTool(confirm contradict): %v", rerr)
	}
	var result ConfirmResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal ConfirmResult: %v", err)
	}
	if result.EdgeType != string(store.EdgeContradicts) {
		t.Errorf("ConfirmResult.EdgeType = %q, want %q", result.EdgeType, store.EdgeContradicts)
	}
	if !strings.Contains(string(raw), result.ID) || !strings.Contains(string(raw), string(store.EdgeContradicts)) {
		t.Errorf("CallToolResult text = %q, want it to name the new record id and the contradicts edge", raw)
	}

	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'contradicts'`,
		result.ID, target.ID).Scan(&n); err != nil {
		t.Fatalf("count contradicts edge: %v", err)
	}
	if n != 1 {
		t.Errorf("contradicts edge %s -> %s count = %d, want 1", result.ID, target.ID, n)
	}
}

// TestConfirmContradictWithNoEvidenceEndToEnd is DONE WHEN clause 2's first
// half, driven through the MCP server.
func TestConfirmContradictWithNoEvidenceEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	targetRaw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"outcome","text":"tests are green"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) target: %v", rerr)
	}
	var target NoteResult
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}

	before := countStoreRecords(t, st)
	_, rerr = shim.CallTool(ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"contradict"}`, target.ID)))
	if rerr == nil {
		t.Fatal("CallTool(confirm contradict) with no evidence = nil error, want an error")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
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
// second half, driven through the MCP server: an evidence id not present in
// timeline_events for the caller's project is rejected and writes nothing.
func TestConfirmContradictWithUnknownEvidenceEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	targetRaw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"outcome","text":"tests are green"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) target: %v", rerr)
	}
	var target NoteResult
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}

	before := countStoreRecords(t, st)
	_, rerr = shim.CallTool(ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"contradict","evidence":[999999]}`, target.ID)))
	if rerr == nil {
		t.Fatal("CallTool(confirm contradict) with an unknown evidence id = nil error, want an error")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected contradict, want unchanged %d", got, before)
	}
}

// TestConfirmSupersedeEndToEnd is DONE WHEN clause 1's supersede path.
func TestConfirmSupersedeEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	targetRaw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"handoff","text":"first handoff"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) target: %v", rerr)
	}
	var target NoteResult
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}

	raw, rerr := shim.CallTool(ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"supersede"}`, target.ID)))
	if rerr != nil {
		t.Fatalf("CallTool(confirm supersede): %v", rerr)
	}
	var result ConfirmResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal ConfirmResult: %v", err)
	}
	if result.EdgeType != string(store.EdgeSupersedes) {
		t.Errorf("ConfirmResult.EdgeType = %q, want %q", result.EdgeType, store.EdgeSupersedes)
	}
	if !strings.Contains(string(raw), result.ID) || !strings.Contains(string(raw), string(store.EdgeSupersedes)) {
		t.Errorf("CallToolResult text = %q, want it to name the new record id and the supersedes edge", raw)
	}

	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'supersedes'`,
		result.ID, target.ID).Scan(&n); err != nil {
		t.Fatalf("count supersedes edge: %v", err)
	}
	if n != 1 {
		t.Errorf("supersedes edge %s -> %s count = %d, want 1", result.ID, target.ID, n)
	}
}

// TestConfirmAffirmEndToEnd is DONE WHEN clause 1's affirm path — the
// additive action this punch adds: "still true as of now".
func TestConfirmAffirmEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	targetRaw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"handoff","text":"resume from here"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) target: %v", rerr)
	}
	var target NoteResult
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}

	raw, rerr := shim.CallTool(ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"affirm","text":"still true"}`, target.ID)))
	if rerr != nil {
		t.Fatalf("CallTool(confirm affirm): %v", rerr)
	}
	var result ConfirmResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal ConfirmResult: %v", err)
	}
	if result.EdgeType != string(store.EdgeInforms) {
		t.Errorf("ConfirmResult.EdgeType = %q, want %q", result.EdgeType, store.EdgeInforms)
	}
	if !strings.Contains(string(raw), result.ID) || !strings.Contains(string(raw), string(store.EdgeInforms)) {
		t.Errorf("CallToolResult text = %q, want it to name the new record id and the informs edge", raw)
	}

	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'informs'`,
		result.ID, target.ID).Scan(&n); err != nil {
		t.Fatalf("count informs edge: %v", err)
	}
	if n != 1 {
		t.Errorf("informs edge %s -> %s count = %d, want 1", result.ID, target.ID, n)
	}
}

// TestConfirmMissingRecordIDIsRejectedAndInsertsNothing exercises confirm's
// own required-field validation, the same way note.go's missing-kind test
// does for note.
func TestConfirmMissingRecordIDIsRejectedAndInsertsNothing(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	before := countStoreRecords(t, st)
	_, rerr := shim.CallTool(ToolConfirm, json.RawMessage(`{"action":"affirm"}`))
	if rerr == nil {
		t.Fatal("CallTool(confirm) with no record_id = nil error, want an error naming \"record_id\"")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
	}
	if !strings.Contains(rerr.Message, "record_id") {
		t.Errorf("error message = %q, want it to name \"record_id\"", rerr.Message)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected confirm, want unchanged %d", got, before)
	}
}

// TestConfirmUnknownActionIsRejectedAndInsertsNothing.
func TestConfirmUnknownActionIsRejectedAndInsertsNothing(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	targetRaw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"note","text":"a target"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) target: %v", rerr)
	}
	var target NoteResult
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}

	before := countStoreRecords(t, st)
	_, rerr = shim.CallTool(ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"banana"}`, target.ID)))
	if rerr == nil {
		t.Fatal("CallTool(confirm) with action=banana = nil error, want an error naming \"action\"")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
	}
	if !strings.Contains(rerr.Message, "action") {
		t.Errorf("error message = %q, want it to name \"action\"", rerr.Message)
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
	shim := dialShim(t, sockPath)

	targetRaw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"note","text":"a target"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) target: %v", rerr)
	}
	var target NoteResult
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}

	raw, rerr := shim.CallTool(ToolConfirm, json.RawMessage(
		fmt.Sprintf(`{"record_id":%q,"action":"affirm","tier":"human-declared","session":"forged"}`, target.ID)))
	if rerr != nil {
		t.Fatalf("CallTool(confirm): %v", rerr)
	}
	var result ConfirmResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal ConfirmResult: %v", err)
	}
	if result.Tier != string(store.TierAgentDeclared) {
		t.Fatalf("ConfirmResult.Tier = %q, want %q (forged tier must not apply)", result.Tier, store.TierAgentDeclared)
	}
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

// writeInferredDraft writes a note through shim (establishing/using this
// connection's live session), reads back its session id, then inserts a
// SEPARATE tier=inferred record directly through the store attributed to
// that same session — standing in for a draft Phase 5's (not yet built)
// inference pass would have written through this session.
func writeInferredDraft(t *testing.T, st *store.Store, shim *Server, projectKey, text string) inferredDraft {
	t.Helper()
	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"note","text":"session anchor"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) session anchor: %v", rerr)
	}
	var anchor NoteResult
	if err := json.Unmarshal(raw, &anchor); err != nil {
		t.Fatalf("unmarshal anchor: %v", err)
	}
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
