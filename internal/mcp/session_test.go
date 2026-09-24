package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// sessionHarnessName is the ident.KnownHarnesses entry every real-subprocess
// test in this file builds testdata/sessionharness to. Asserted against
// ident.KnownHarnesses rather than assumed, so this file breaks loudly
// instead of silently if that table ever drops "claude".
const sessionHarnessName = "claude"

// buildSessionHarness compiles testdata/sessionharness to a binary whose
// path's final component is exactly name — Linux sets a process's
// /proc/<pid>/status "Name:" (comm) from the basename passed to execve, so
// a real daemon's /proc ancestry walk matches it at distance zero, the same
// pattern cmd/backstory/daemon_test.go's buildHarnessClient uses.
func buildSessionHarness(t *testing.T, name string) string {
	t.Helper()
	found := false
	for _, h := range ident.KnownHarnesses {
		if h == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("harness name %q is not in ident.KnownHarnesses %v", name, ident.KnownHarnesses)
	}

	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/sessionharness") //nolint:gosec // fixed source path, fixed tmp-dir output path
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build sessionharness: %v\n%s", err, out)
	}
	return bin
}

// testDaemonRealProcFS is testDaemon's real-/proc counterpart: its resolver
// walks the actual /proc filesystem instead of a fake keyed on this one test
// process's own pid, so a genuinely separate process spawned against it
// (buildSessionHarness) gets its own real observed harness identity — a
// fake resolver keyed on a single pid can never produce two distinct
// harness identities the way DONE WHEN clause 2 requires.
func testDaemonRealProcFS(t *testing.T, st *store.Store, projectKey string) string {
	t.Helper()
	resolver := &ident.Resolver{ProcFS: ident.RealProcFS{}, ProjectKey: func(string) string { return projectKey }}
	sessions := NewSessionRegistry()

	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, ident.RealProcFS{}, nil, sessions)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return sockPath
}

// harnessStep is one step of a sessionharness invocation's stdin protocol
// (testdata/sessionharness/main.go): dial, send this DaemonRequest, read
// back its raw result.
type harnessStep struct {
	Session string          `json:"session,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// runSessionHarness execs bin (a sessionharness binary) against sockPath,
// feeding steps as JSON on stdin, and decodes stdout as one raw
// DaemonResponse.Result per step, in order. Every step dials sockPath from
// WITHIN this one process, so the daemon observes the identical harness
// process (pid + /proc start time) across every step.
func runSessionHarness(t *testing.T, bin, sockPath string, steps []harnessStep) []json.RawMessage {
	t.Helper()
	in, err := json.Marshal(steps)
	if err != nil {
		t.Fatalf("marshal steps: %v", err)
	}

	cmd := exec.Command(bin, sockPath) //nolint:gosec // bin is the binary this test just built
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run sessionharness: %v\nstderr:\n%s", err, stderr.String())
	}

	var results []json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("decode sessionharness output %q: %v", stdout.String(), err)
	}
	return results
}

// TestKnownHarnessConnectionsShareOneSession is the punch's (32c6900d) DONE
// WHEN clause 1's core mechanism: several separate connections from ONE
// observed harness process — even ones declaring different "session" join
// key values, exactly like a real SessionStart hook call and a later
// PostToolUse hook call from the same Claude Code session would — resolve
// to the SAME store session, not one session per connection.
//
// RA-MUTATION-PROBE: SessionRegistry.SessionFor's cache-hit branch (`if sid,
// ok := r.sessions[key]; ok { return sid, nil }`) deleted, so every call
// falls through to start() -> RED (this test: statuses report two different
// sessions); restored -> GREEN.
func TestKnownHarnessConnectionsShareOneSession(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemonRealProcFS(t, st, "proj-key")
	bin := buildSessionHarness(t, sessionHarnessName)

	results := runSessionHarness(t, bin, sockPath, []harnessStep{
		{Session: "hook-declared-1", Method: DaemonMethodBlock},
		{Session: "hook-declared-1", Method: daemonMethodStatus},
		{Session: "hook-declared-2", Method: daemonMethodStatus},
	})
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	var status1, status2 StatusResult
	if err := json.Unmarshal(results[1], &status1); err != nil {
		t.Fatalf("unmarshal status 1: %v", err)
	}
	if err := json.Unmarshal(results[2], &status2); err != nil {
		t.Fatalf("unmarshal status 2: %v", err)
	}
	if status1.Session == "" {
		t.Fatal("status 1's Session is empty")
	}
	if status1.Session != status2.Session {
		t.Errorf("two connections from the same harness process got different sessions (%q vs %q) despite only their declared session id differing — observed identity, not a declared id, must decide this",
			status1.Session, status2.Session)
	}

	if got := countSessions(t, st); got != 1 {
		t.Errorf("session count = %d after 3 connections from one harness process, want 1", got)
	}
}

// TestTwoHarnessProcessesGetTwoSessions is the punch's DONE WHEN clause 2:
// two different harness-named helper processes in the same project get two
// live sessions — proven here with two separate sessionharness invocations,
// each its own real OS process. Both processes deliberately declare the
// SAME "session" join key value, which doubles as DONE WHEN clause 4's
// other half: a declared id shared by two different observed harness
// processes must never merge them into one session.
//
// Process A has exited (runSessionHarness waits for it) by the time B
// dials, so task 25b74537's exit sweep — triggered by B's own connection —
// correctly ends and evicts A's session before B's status call: B must NOT
// see it in other_live_sessions. (Before that task, a known harness's
// session never ended on its own, so B's status listed A forever; see
// TestHarnessExitEndsAndEvictsSession, internal/mcp/status_test.go, for the
// dedicated proof of that eviction.) The row itself is never deleted, only
// ended: the store still holds exactly 2 sessions.
func TestTwoHarnessProcessesGetTwoSessions(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemonRealProcFS(t, st, "proj-key")
	bin := buildSessionHarness(t, sessionHarnessName)

	const declared = "shared-declared-session-id"
	resultsA := runSessionHarness(t, bin, sockPath, []harnessStep{{Session: declared, Method: daemonMethodStatus}})
	resultsB := runSessionHarness(t, bin, sockPath, []harnessStep{{Session: declared, Method: daemonMethodStatus}})

	var statusA, statusB StatusResult
	if err := json.Unmarshal(resultsA[0], &statusA); err != nil {
		t.Fatalf("unmarshal status A: %v", err)
	}
	if err := json.Unmarshal(resultsB[0], &statusB); err != nil {
		t.Fatalf("unmarshal status B: %v", err)
	}
	if statusA.Session == "" || statusB.Session == "" {
		t.Fatalf("empty session: A=%q B=%q", statusA.Session, statusB.Session)
	}
	if statusA.Session == statusB.Session {
		t.Fatalf("two different harness processes (both declaring the same session id %q) got the SAME store session %q, want distinct sessions",
			declared, statusA.Session)
	}

	for _, other := range statusB.OtherLiveSessions {
		if other.Session == statusA.Session {
			t.Errorf("process B's other_live_sessions still lists process A's session %q, want it evicted (A had already exited)", other.Session)
		}
		if other.Session == statusB.Session {
			t.Errorf("process B's status lists its own session %q in other_live_sessions", other.Session)
		}
	}

	if got := countSessions(t, st); got != 2 {
		t.Errorf("session count = %d for two different harness processes, want 2", got)
	}
}

// dialWithIdentity opens a fresh unix listener, accepts exactly one
// connection under the given (fabricated) ident.Identity via
// ServeDaemonConn, sends one status DaemonRequest, and returns the
// resulting StatusResult. st, sessions and procfs are shared across calls
// so a test can simulate several connections against the SAME daemon state
// without needing genuinely different real processes for every identity it
// wants to try — used here only for scenarios a real process cannot
// deterministically reproduce (a specific /proc start time), mirroring
// internal/ident/resolver_test.go's fakeProcFS pattern one layer up the
// stack. procfs backs task 25b74537's exit sweep exactly like a real
// daemon's ServeDaemonConn call does; a scenario that must never trip the
// sweep reports every fabricated identity's pid as alive there.
func dialWithIdentity(t *testing.T, st *store.Store, sessions *SessionRegistry, procfs ident.ProcFS, id ident.Identity) StatusResult {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, nil, sessions)
	}()

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
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeDaemonConn did not return within 2s of the connection closing")
	}
	return result
}

// TestReusedHarnessPidWithDifferentStartTicksIsNewSession is the punch's
// (25b74537) DONE WHEN clause 2: a harness pid reused by a new process —
// observed as the same HarnessPID but a different
// ident.Identity.HarnessStartTicks, exactly what internal/ident's real
// /proc-backed start-time read would report for two unrelated processes
// that happen to share a recycled pid — must be treated as a new session,
// never the old one. The same pid AND the same start ticks, by contrast,
// must still reuse the original session: fakeProcFS reports pid 4242 alive
// at ticks 1000 for the whole test, exactly what a real, continuously-alive
// process's /proc entry would report, so the sweep never has reason to
// evict the entry the "repeat" dial expects to find still cached — proving
// the sweep does not evict a session just because a DIFFERENT dial in
// between claimed a different start time for the same pid.
//
// RA-MUTATION-PROBE: SessionRegistry.sweep's `st.StartTicks != key.
// StartTicks` half of the staleness check deleted (leaving only the
// procfs.Status error case) -> RED (the reused-pid dial's stale entry is
// never evicted, so status keeps reporting the exited identity as
// pid-current); restored -> GREEN.
func TestReusedHarnessPidWithDifferentStartTicksIsNewSession(t *testing.T) {
	st := mustOpenStore(t)
	if err := st.UpsertProject(store.Project{Key: "proj-key", Toplevel: "/proj", FirstSeen: time.Now()}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	sessions := NewSessionRegistry()
	const pid = 4242
	procfs := fakeProcFS{status: map[int]ident.Status{pid: {StartTicks: 1000}}}

	base := ident.Identity{Kind: ident.KindAgent, Harness: "claude", HarnessPID: pid, ProjectKey: "proj-key"}

	firstProcess := base
	firstProcess.HarnessStartTicks = 1000
	sidFirst := dialWithIdentity(t, st, sessions, procfs, firstProcess).Session

	reusedPid := base
	reusedPid.HarnessStartTicks = 2000
	sidReused := dialWithIdentity(t, st, sessions, procfs, reusedPid).Session

	repeat := base
	repeat.HarnessStartTicks = 1000
	sidRepeat := dialWithIdentity(t, st, sessions, procfs, repeat).Session

	if sidFirst == sidReused {
		t.Fatalf("pid %d reused by a process with a different /proc start time got the same session %q, want a new one",
			base.HarnessPID, sidFirst)
	}
	if sidRepeat != sidFirst {
		t.Fatalf("same pid %d and the same start time got a different session (%q vs %q), want the original session reused",
			base.HarnessPID, sidRepeat, sidFirst)
	}
}

// TestStillAliveHarnessSessionsRemainMutuallyVisible guards against an
// over-eager sweep: two DIFFERENT, both genuinely-still-alive (per
// fakeProcFS) fabricated harness processes must keep seeing each other in
// other_live_sessions across several connections each, exactly like
// TestTwoHarnessProcessesGetTwoSessions' two real processes would if
// neither had exited yet. Complements task 25b74537's exit sweep tests
// (which all prove eviction) with the "never evicts a still-alive one"
// half.
func TestStillAliveHarnessSessionsRemainMutuallyVisible(t *testing.T) {
	st := mustOpenStore(t)
	if err := st.UpsertProject(store.Project{Key: "proj-key", Toplevel: "/proj", FirstSeen: time.Now()}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	sessions := NewSessionRegistry()
	procfs := fakeProcFS{status: map[int]ident.Status{
		5001: {StartTicks: 100},
		5002: {StartTicks: 200},
	}}

	idA := ident.Identity{Kind: ident.KindAgent, Harness: "claude", HarnessPID: 5001, HarnessStartTicks: 100, ProjectKey: "proj-key"}
	idB := ident.Identity{Kind: ident.KindAgent, Harness: "claude", HarnessPID: 5002, HarnessStartTicks: 200, ProjectKey: "proj-key"}

	resultA := dialWithIdentity(t, st, sessions, procfs, idA)
	// A second dial from each identity re-triggers the sweep without either
	// process having "exited" in the fake — neither entry may be evicted.
	resultB := dialWithIdentity(t, st, sessions, procfs, idB)
	resultA2 := dialWithIdentity(t, st, sessions, procfs, idA)
	resultB2 := dialWithIdentity(t, st, sessions, procfs, idB)

	if resultA2.Session != resultA.Session {
		t.Fatalf("process A's session changed across still-alive dials (%q vs %q), want it reused", resultA.Session, resultA2.Session)
	}
	if resultB2.Session != resultB.Session {
		t.Fatalf("process B's session changed across still-alive dials (%q vs %q), want it reused", resultB.Session, resultB2.Session)
	}

	foundBInA := false
	for _, other := range resultA2.OtherLiveSessions {
		if other.Session == resultB.Session {
			foundBInA = true
		}
	}
	if !foundBInA {
		t.Errorf("still-alive process A's other_live_sessions = %+v, want it to include still-alive process B's session %q",
			resultA2.OtherLiveSessions, resultB.Session)
	}

	foundAInB := false
	for _, other := range resultB2.OtherLiveSessions {
		if other.Session == resultA.Session {
			foundAInB = true
		}
	}
	if !foundAInB {
		t.Errorf("still-alive process B's other_live_sessions = %+v, want it to include still-alive process A's session %q",
			resultB2.OtherLiveSessions, resultA.Session)
	}
}
