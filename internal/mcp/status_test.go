package mcp

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// testDaemonCwdErr is testDaemon's counterpart for the punch's acceptance
// clause 2 (task f2718b5b): the fake ProcFS resolves a known harness for the
// dialing pid but has no cwd entry for it, so ProcFS.Cwd errors exactly like
// /proc/<harness_pid>/cwd does under a mount-sandboxed systemd --user unit's
// implicit user namespace (measured d38ca301).
func testDaemonCwdErr(t *testing.T, st *store.Store, harness string) string {
	t.Helper()
	selfPID := os.Getpid()
	procfs := fakeProcFS{
		status: map[int]ident.Status{selfPID: {PPid: 1, Name: harness}},
		// deliberately no cwd entry for selfPID.
	}
	// ProjectKey is deliberately nil: Resolve must never call it once the
	// cwd read has failed (internal/ident/resolver_test.go asserts this
	// directly), so a non-nil func here would mask a regression by
	// supplying a project key Resolve had no business computing.
	resolver := &ident.Resolver{ProcFS: procfs}

	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, nil)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return sockPath
}

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

// TestStatusReportsReasonWhenCwdUnreadable is the punch's acceptance clause
// 2 (task f2718b5b): a caller whose harness was found but whose cwd could
// not be read gets the reason back from status, and no ProjectKey — not the
// same silent empty-everything shape as "no harness found" at all.
func TestStatusReportsReasonWhenCwdUnreadable(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemonCwdErr(t, st, "claude")
	shim := dialShim(t, sockPath)

	raw, rerr := shim.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status): %v", rerr)
	}
	var result StatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal StatusResult: %v", err)
	}

	if result.Harness != "claude" {
		t.Errorf("Harness = %q, want claude", result.Harness)
	}
	if result.ProjectKey != "" {
		t.Errorf("ProjectKey = %q, want empty (cwd unreadable)", result.ProjectKey)
	}
	if result.Reason == "" {
		t.Error("Reason is empty, want a non-empty reason naming the cwd error")
	}
}

// TestStatusReportsNoReasonWhenCwdReadable is
// TestStatusReportsReasonWhenCwdUnreadable's control: a caller with a
// readable cwd gets no reason back from status.
func TestStatusReportsNoReasonWhenCwdReadable(t *testing.T) {
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
	if result.Reason != "" {
		t.Errorf("Reason = %q, want empty for a readable cwd", result.Reason)
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

// TestSessionEndsWhenItsConnectionCloses guards the other half of DONE WHEN
// clause 4's "lists a second live session ... when one exists": a session
// whose shim connection has closed must stop being live, or status would
// report every peer that ever connected as still present forever. This
// dials the daemon directly (rather than through dialShim/t.Cleanup) so the
// test can close A's connection mid-test and observe ServeDaemonConn's
// deferred EndSession actually landing before asserting on B's view.
func TestSessionEndsWhenItsConnectionCloses(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")

	var connA net.Conn
	shimA := NewServer(func() (net.Conn, error) {
		c, err := net.Dial("unix", sockPath)
		if err != nil {
			return nil, err
		}
		connA = c
		return c, nil
	})
	statusA, rerr := shimA.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status) A: %v", rerr)
	}
	var resultA StatusResult
	if err := json.Unmarshal(statusA, &resultA); err != nil {
		t.Fatalf("unmarshal StatusResult A: %v", err)
	}

	shimB := dialShim(t, sockPath)
	if _, rerr := shimB.CallTool(ToolNote, json.RawMessage(`{"kind":"note","text":"session B is live"}`)); rerr != nil {
		t.Fatalf("CallTool(note) B: %v", rerr)
	}

	if err := connA.Close(); err != nil {
		t.Fatalf("close connection A: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		sessions, err := st.LiveSessionsInProject("proj-key")
		if err != nil {
			t.Fatalf("LiveSessionsInProject: %v", err)
		}
		stillLive := false
		for _, s := range sessions {
			if s.ID == resultA.Session {
				stillLive = true
			}
		}
		if !stillLive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session A (%s) still live 2s after its connection closed, want ended_at set", resultA.Session)
		}
		time.Sleep(10 * time.Millisecond)
	}

	statusB, rerr := shimB.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status) B: %v", rerr)
	}
	var resultB StatusResult
	if err := json.Unmarshal(statusB, &resultB); err != nil {
		t.Fatalf("unmarshal StatusResult B: %v", err)
	}
	for _, other := range resultB.OtherLiveSessions {
		if other.Session == resultA.Session {
			t.Errorf("status B's other_live_sessions still lists ended session A (%s)", resultA.Session)
		}
	}
}

// TestTimelineConfirmAreNotImplementedAndNeverTouchTheStore is DONE WHEN
// clause 4's last half (task d6ddfce3 narrowed this from
// recall/timeline/confirm to just timeline/confirm: recall is implemented
// as of this punch — see internal/mcp/recall_test.go for its own coverage).
func TestTimelineConfirmAreNotImplementedAndNeverTouchTheStore(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	before := countStoreRecords(t, st)
	for _, tool := range []string{ToolTimeline, ToolConfirm} {
		_, rerr := shim.CallTool(tool, json.RawMessage(`{}`))
		if rerr == nil {
			t.Fatalf("CallTool(%s) = nil error, want CodeNotImplemented", tool)
		}
		if rerr.Code != CodeNotImplemented {
			t.Errorf("CallTool(%s) error code = %d, want %d (CodeNotImplemented)", tool, rerr.Code, CodeNotImplemented)
		}
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after timeline/confirm, want unchanged %d (they must never touch the store)", got, before)
	}
}
