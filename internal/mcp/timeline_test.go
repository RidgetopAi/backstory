package mcp

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// callerSession dials shim, triggers session start via status, and returns
// the session id the daemon minted for it — the id st.AppendEvent needs to
// attribute a fixture event to the same session/project a later timeline
// call observes for that same shim.
func callerSession(t *testing.T, shim *Server) string {
	t.Helper()
	raw, rerr := shim.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status): %v", rerr)
	}
	var result StatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal StatusResult: %v", err)
	}
	if result.Session == "" {
		t.Fatal("callerSession: empty session id")
	}
	return result.Session
}

// appendTimelineEvent inserts a fixture timeline event for sessionID and
// returns its id.
func appendTimelineEvent(t *testing.T, st *store.Store, sessionID, kind, source string, ts time.Time) int64 {
	t.Helper()
	id, err := st.AppendEvent(store.Event{
		TS: ts, Kind: kind, SessionID: sessionID, Source: source,
		Payload: `{"n":1}`,
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	return id
}

func callTimeline(t *testing.T, shim *Server, argsJSON string) TimelineResult {
	t.Helper()
	raw, rerr := shim.CallTool(ToolTimeline, json.RawMessage(argsJSON))
	if rerr != nil {
		t.Fatalf("CallTool(timeline, %s): %v", argsJSON, rerr)
	}
	var result TimelineResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal TimelineResult: %v", err)
	}
	return result
}

// TestTimelineNoArgsReturnsCallersProjectEvents is DONE WHEN clause 1's
// first half: timeline with no args returns the caller's OWN project's
// events, each carrying id/ts/kind/source/payload.
func TestTimelineNoArgsReturnsCallersProjectEvents(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)
	sessionID := callerSession(t, shim)

	now := time.Now().UTC()
	id1 := appendTimelineEvent(t, st, sessionID, "post_tool_use", "posttooluse", now)
	id2 := appendTimelineEvent(t, st, sessionID, "note", "socket2", now.Add(time.Second))

	result := callTimeline(t, shim, `{}`)

	if result.ProjectKey != "proj-key" {
		t.Errorf("ProjectKey = %q, want proj-key", result.ProjectKey)
	}
	if result.Scope != timelineScopeProject {
		t.Errorf("Scope = %q, want %q (default)", result.Scope, timelineScopeProject)
	}
	if len(result.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2: %+v", len(result.Events), result.Events)
	}
	if result.Events[0].ID != id1 || result.Events[1].ID != id2 {
		t.Errorf("Events ids = [%d,%d], want [%d,%d] (oldest to newest)", result.Events[0].ID, result.Events[1].ID, id1, id2)
	}
	for _, e := range result.Events {
		if e.TS == "" {
			t.Error("event TS is empty")
		}
		if e.Kind == "" {
			t.Error("event Kind is empty")
		}
		if e.Source == "" {
			t.Error("event Source is empty")
		}
		if len(e.Payload) == 0 {
			t.Error("event Payload is empty")
		}
	}
}

// TestTimelineProjectScopeExcludesOtherProjects is DONE WHEN clause 1's
// project isolation: scope=project (the default) never returns another
// project's events, even though both share the same store.
func TestTimelineProjectScopeExcludesOtherProjects(t *testing.T) {
	st := mustOpenStore(t)

	sockPathA := testDaemon(t, st, "claude", "/home/brian/proj-a", "proj-a")
	shimA := dialShim(t, sockPathA)
	sessionA := callerSession(t, shimA)

	sockPathB := testDaemon(t, st, "claude", "/home/brian/proj-b", "proj-b")
	shimB := dialShim(t, sockPathB)
	sessionB := callerSession(t, shimB)

	now := time.Now().UTC()
	idA := appendTimelineEvent(t, st, sessionA, "post_tool_use", "posttooluse", now)
	appendTimelineEvent(t, st, sessionB, "post_tool_use", "posttooluse", now.Add(time.Second))

	result := callTimeline(t, shimA, `{}`)

	if result.ProjectKey != "proj-a" {
		t.Errorf("ProjectKey = %q, want proj-a", result.ProjectKey)
	}
	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1: %+v", len(result.Events), result.Events)
	}
	if result.Events[0].ID != idA {
		t.Errorf("Events[0].ID = %d, want %d (proj-a's own event, not proj-b's)", result.Events[0].ID, idA)
	}
}

// TestTimelineScopeSessionExcludesOtherSessions is DONE WHEN clause 1's
// second half: scope=session returns only the caller's OWN session's
// events, even when another session in the SAME project has events too.
func TestTimelineScopeSessionExcludesOtherSessions(t *testing.T) {
	st := mustOpenStore(t)

	sockPathA := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shimA := dialShim(t, sockPathA)
	sessionA := callerSession(t, shimA)

	sockPathB := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shimB := dialShim(t, sockPathB)
	sessionB := callerSession(t, shimB)

	if sessionA == sessionB {
		t.Fatal("sessionA == sessionB, want two distinct sessions")
	}

	now := time.Now().UTC()
	idA := appendTimelineEvent(t, st, sessionA, "post_tool_use", "posttooluse", now)
	appendTimelineEvent(t, st, sessionB, "post_tool_use", "posttooluse", now.Add(time.Second))

	result := callTimeline(t, shimA, `{"scope":"session"}`)

	if result.Scope != timelineScopeSession {
		t.Errorf("Scope = %q, want session", result.Scope)
	}
	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1: %+v", len(result.Events), result.Events)
	}
	if result.Events[0].ID != idA {
		t.Errorf("Events[0].ID = %d, want %d (session A's own event, not session B's)", result.Events[0].ID, idA)
	}
}

// TestTimelineSinceFiltersOlderEvents is DONE WHEN clause 1's since filter.
func TestTimelineSinceFiltersOlderEvents(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)
	sessionID := callerSession(t, shim)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	appendTimelineEvent(t, st, sessionID, "post_tool_use", "posttooluse", base)
	idNew := appendTimelineEvent(t, st, sessionID, "post_tool_use", "posttooluse", base.Add(time.Hour))

	since := base.Add(30 * time.Minute)
	result := callTimeline(t, shim, fmt.Sprintf(`{"since":%q}`, since.Format(time.RFC3339)))

	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1: %+v", len(result.Events), result.Events)
	}
	if result.Events[0].ID != idNew {
		t.Errorf("Events[0].ID = %d, want %d (only the event at/after since)", result.Events[0].ID, idNew)
	}
}

// TestTimelineKindFilter is DONE WHEN clause 1's kind filter.
func TestTimelineKindFilter(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)
	sessionID := callerSession(t, shim)

	now := time.Now().UTC()
	appendTimelineEvent(t, st, sessionID, "post_tool_use", "posttooluse", now)
	idNote := appendTimelineEvent(t, st, sessionID, "note", "socket2", now.Add(time.Second))

	result := callTimeline(t, shim, `{"kind":"note"}`)

	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1: %+v", len(result.Events), result.Events)
	}
	if result.Events[0].ID != idNote {
		t.Errorf("Events[0].ID = %d, want %d (kind=note only)", result.Events[0].ID, idNote)
	}
	if result.Events[0].Kind != "note" {
		t.Errorf("Events[0].Kind = %q, want note", result.Events[0].Kind)
	}
}

// TestTimelineLimitCapsAndKeepsNewest is DONE WHEN clause 1's limit cap:
// limit keeps the most recent N events, still returned oldest to newest.
func TestTimelineLimitCapsAndKeepsNewest(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)
	sessionID := callerSession(t, shim)

	now := time.Now().UTC()
	appendTimelineEvent(t, st, sessionID, "post_tool_use", "posttooluse", now)
	id2 := appendTimelineEvent(t, st, sessionID, "post_tool_use", "posttooluse", now.Add(time.Second))
	id3 := appendTimelineEvent(t, st, sessionID, "post_tool_use", "posttooluse", now.Add(2*time.Second))

	result := callTimeline(t, shim, `{"limit":2}`)

	if len(result.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2: %+v", len(result.Events), result.Events)
	}
	if result.Events[0].ID != id2 || result.Events[1].ID != id3 {
		t.Errorf("Events ids = [%d,%d], want [%d,%d] (most recent two, oldest to newest)", result.Events[0].ID, result.Events[1].ID, id2, id3)
	}
}

// TestTimelineEventIDRoundTripsIntoNoteEvidence is DONE WHEN clause 2: an id
// timeline returns is accepted by note kind=outcome evidence, and the
// stored record cites it.
func TestTimelineEventIDRoundTripsIntoNoteEvidence(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)
	sessionID := callerSession(t, shim)

	evID := appendTimelineEvent(t, st, sessionID, "post_tool_use", "posttooluse", time.Now().UTC())

	result := callTimeline(t, shim, `{}`)
	if len(result.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(result.Events))
	}
	if result.Events[0].ID != evID {
		t.Fatalf("Events[0].ID = %d, want %d", result.Events[0].ID, evID)
	}

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(fmt.Sprintf(
		`{"kind":"outcome","text":"did the thing","evidence":[%d]}`, result.Events[0].ID)))
	if rerr != nil {
		t.Fatalf("CallTool(note, evidence=[%d]): %v", result.Events[0].ID, rerr)
	}
	var noteResult NoteResult
	if err := json.Unmarshal(raw, &noteResult); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}

	rec, err := st.GetRecord(noteResult.ID)
	if err != nil {
		t.Fatalf("GetRecord(%s): %v", noteResult.ID, err)
	}
	if len(rec.Evidence) != 1 || rec.Evidence[0] != evID {
		t.Errorf("rec.Evidence = %v, want [%d]", rec.Evidence, evID)
	}
}

// TestTimelineInvalidScopeIsInvalidParams guards the scope enum: only
// session and project are accepted wire values.
func TestTimelineInvalidScopeIsInvalidParams(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	_, rerr := shim.CallTool(ToolTimeline, json.RawMessage(`{"scope":"bogus"}`))
	if rerr == nil {
		t.Fatal("CallTool(timeline, scope=bogus) = nil error, want CodeInvalidParams")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
	}
}
