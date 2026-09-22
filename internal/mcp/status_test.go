package mcp

import (
	"encoding/json"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/store"
)

// TestStatusReportsCallerIdentity is DONE WHEN clause 4's first half: status
// returns Kind/Harness/ProjectKey/session for the caller.
func TestStatusReportsCallerIdentity(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	raw, rerr := shim.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status): %v", rerr)
	}
	var result StatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal StatusResult: %v", err)
	}

	if result.Kind != ident.KindAgent.String() {
		t.Errorf("Kind = %q, want %q", result.Kind, ident.KindAgent.String())
	}
	if result.Harness != "claude" {
		t.Errorf("Harness = %q, want claude", result.Harness)
	}
	if result.ProjectKey != "proj-key" {
		t.Errorf("ProjectKey = %q, want proj-key", result.ProjectKey)
	}
	if result.Session == "" {
		t.Error("Session is empty, want the daemon-minted session id")
	}
}

// TestStatusListsSecondLiveSessionInSameProject is DONE WHEN clause 4's
// second half: status lists a second live session in the same project when
// one exists in the store.
func TestStatusListsSecondLiveSessionInSameProject(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")

	shimA := dialShim(t, sockPath)
	statusA, rerr := shimA.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status) A: %v", rerr)
	}
	var resultA StatusResult
	if err := json.Unmarshal(statusA, &resultA); err != nil {
		t.Fatalf("unmarshal StatusResult A: %v", err)
	}

	shimB := dialShim(t, sockPath)
	// A note from B keeps B's connection (and therefore its session) alive
	// on the daemon side long enough for A's status call to observe it.
	if _, rerr := shimB.CallTool(ToolNote, json.RawMessage(`{"kind":"note","text":"session B is live"}`)); rerr != nil {
		t.Fatalf("CallTool(note) B: %v", rerr)
	}
	statusB, rerr := shimB.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status) B: %v", rerr)
	}
	var resultB StatusResult
	if err := json.Unmarshal(statusB, &resultB); err != nil {
		t.Fatalf("unmarshal StatusResult B: %v", err)
	}
	if resultB.Session == resultA.Session {
		t.Fatalf("session B = %q, same as session A; want distinct sessions", resultB.Session)
	}

	statusA2, rerr := shimA.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status) A again: %v", rerr)
	}
	var resultA2 StatusResult
	if err := json.Unmarshal(statusA2, &resultA2); err != nil {
		t.Fatalf("unmarshal StatusResult A2: %v", err)
	}

	found := false
	for _, other := range resultA2.OtherLiveSessions {
		if other.Session == resultB.Session {
			found = true
		}
		if other.Session == resultA.Session {
			t.Errorf("status A lists its own session %q in other_live_sessions", other.Session)
		}
	}
	if !found {
		t.Errorf("status A's other_live_sessions = %+v, want it to include session B (%q)",
			resultA2.OtherLiveSessions, resultB.Session)
	}
}

// TestStatusReportsRemainingBudget is DONE WHEN clause 4's third half.
func TestStatusReportsRemainingBudget(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	statusBefore, rerr := shim.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status) before: %v", rerr)
	}
	var before StatusResult
	if err := json.Unmarshal(statusBefore, &before); err != nil {
		t.Fatalf("unmarshal StatusResult before: %v", err)
	}
	if before.RemainingBudget != store.MaxRecordsPerSessionPerMinute {
		t.Fatalf("RemainingBudget before any writes = %d, want %d", before.RemainingBudget, store.MaxRecordsPerSessionPerMinute)
	}

	if _, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"note","text":"spend one unit of budget"}`)); rerr != nil {
		t.Fatalf("CallTool(note): %v", rerr)
	}

	statusAfter, rerr := shim.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status) after: %v", rerr)
	}
	var after StatusResult
	if err := json.Unmarshal(statusAfter, &after); err != nil {
		t.Fatalf("unmarshal StatusResult after: %v", err)
	}
	if after.RemainingBudget != before.RemainingBudget-1 {
		t.Fatalf("RemainingBudget after 1 write = %d, want %d", after.RemainingBudget, before.RemainingBudget-1)
	}
}

// TestRecallTimelineConfirmAreNotImplementedAndNeverTouchTheStore is DONE
// WHEN clause 4's last half.
func TestRecallTimelineConfirmAreNotImplementedAndNeverTouchTheStore(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	before := countStoreRecords(t, st)
	for _, tool := range []string{ToolRecall, ToolTimeline, ToolConfirm} {
		_, rerr := shim.CallTool(tool, json.RawMessage(`{}`))
		if rerr == nil {
			t.Fatalf("CallTool(%s) = nil error, want CodeNotImplemented", tool)
		}
		if rerr.Code != CodeNotImplemented {
			t.Errorf("CallTool(%s) error code = %d, want %d (CodeNotImplemented)", tool, rerr.Code, CodeNotImplemented)
		}
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after recall/timeline/confirm, want unchanged %d (they must never touch the store)", got, before)
	}
}
