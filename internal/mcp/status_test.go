package mcp

import (
	"bufio"
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

	sessions := NewSessionRegistry()
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, fakeGit{}, nil, sessions)
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
//
// Task 32c6900d changed what "a second live session" means: a store session
// is now one observed harness PROCESS, not one connection (dialShim's own
// backing connection would no longer do — two dials from this same test
// process resolve to the identical observed identity and now correctly
// share one session). Proving two distinct sessions exist requires two
// genuinely different real processes, so this uses sessionharness
// (session_test.go) instead of two dialShim instances against the same
// testDaemon.
func TestStatusListsSecondLiveSessionInSameProject(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemonRealProcFS(t, st, "proj-key")
	bin := buildSessionHarness(t, sessionHarnessName)

	resultsA := runSessionHarness(t, bin, sockPath, []harnessStep{{Method: daemonMethodStatus}})
	var resultA StatusResult
	if err := json.Unmarshal(resultsA[0], &resultA); err != nil {
		t.Fatalf("unmarshal StatusResult A: %v", err)
	}

	// Process A has already exited by now (sessionharness dials, calls its
	// one step, and exits) — its session must still be live, since a known
	// harness's session no longer ends when a connection to it closes
	// (task 32c6900d). Process B, a second real harness process, must be
	// able to see it.
	resultsB := runSessionHarness(t, bin, sockPath, []harnessStep{{Method: daemonMethodStatus}})
	var resultB StatusResult
	if err := json.Unmarshal(resultsB[0], &resultB); err != nil {
		t.Fatalf("unmarshal StatusResult B: %v", err)
	}
	if resultB.Session == resultA.Session {
		t.Fatalf("session B = %q, same as session A; want distinct sessions", resultB.Session)
	}

	found := false
	for _, other := range resultB.OtherLiveSessions {
		if other.Session == resultA.Session {
			found = true
		}
		if other.Session == resultB.Session {
			t.Errorf("status B lists its own session %q in other_live_sessions", other.Session)
		}
	}
	if !found {
		t.Errorf("status B's other_live_sessions = %+v, want it to include session A (%q)",
			resultB.OtherLiveSessions, resultA.Session)
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

// TestUnknownHarnessSessionEndsWhenItsConnectionCloses guards the other half
// of DONE WHEN clause 4's "lists a second live session ... when one
// exists": a session whose connection has closed must stop being live, or
// status would report every peer that ever connected as still present
// forever. Task 32c6900d narrowed this to callers the daemon could not
// attribute to a known harness process (id.HarnessPID == 0): the fake
// resolver here reports a process name absent from ident.KnownHarnesses, so
// this exercises exactly the "unknown harness keeps today's per-connection
// behaviour" branch — see TestKnownHarnessSessionOutlivesConnectionClose
// (same file) for the known-harness case this punch changed.
//
// This dials the daemon directly (rather than through dialShim/t.Cleanup)
// so the test can close A's connection mid-test and observe
// ServeDaemonConn's deferred EndSession actually landing before asserting
// on B's view.
func TestUnknownHarnessSessionEndsWhenItsConnectionCloses(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "not-a-known-harness", "/home/brian/proj", "proj-key")

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

// TestKnownHarnessSessionOutlivesConnectionClose is task 32c6900d's DONE
// WHEN clause 1 from the other direction: a caller the daemon DID attribute
// to a known harness process must NOT have its session end just because one
// connection to it closed — the harness process almost always outlives any
// single short-lived hook connection, so ending the session here would
// recreate the per-connection churn this task removes. Contrast with
// TestUnknownHarnessSessionEndsWhenItsConnectionCloses (same file).
//
// RA-MUTATION-PROBE: ServeDaemonConn's `else if id.HarnessPID == 0` guard
// (mcp/daemon.go) replaced with unconditional EndSession-on-close (i.e. the
// pre-task-32c6900d behaviour) -> RED (session ends immediately, this test
// times out waiting for it to still be live); restored -> GREEN.
func TestKnownHarnessSessionOutlivesConnectionClose(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	req := DaemonRequest{Method: daemonMethodStatus}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		t.Fatalf("write request: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp DaemonResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("daemon error: %s", resp.Error.Message)
	}
	var result StatusResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal StatusResult: %v", err)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}

	// Give the daemon a moment to have observed the close, then confirm the
	// session is STILL live — the opposite assertion from the unknown-harness
	// test, and the whole point of this task's fix.
	time.Sleep(100 * time.Millisecond)
	sessions, err := st.LiveSessionsInProject("proj-key")
	if err != nil {
		t.Fatalf("LiveSessionsInProject: %v", err)
	}
	stillLive := false
	for _, s := range sessions {
		if s.ID == result.Session {
			stillLive = true
		}
	}
	if !stillLive {
		t.Fatalf("known-harness session %s ended when its only connection closed, want it to stay live", result.Session)
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
