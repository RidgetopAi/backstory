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
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// testDaemonWithGit is testDaemon's counterpart for this file's tests: it
// wires a caller-supplied fakeGit into ServeDaemonConn instead of the
// always-could-not-observe empty one testDaemon uses, so a test can control
// exactly what recordSessionEndGitState observes for cwd.
func testDaemonWithGit(t *testing.T, st *store.Store, harness, cwd, projectKey string, git fakeGit) string {
	t.Helper()
	selfPID := os.Getpid()
	procfs := fakeProcFS{
		status: map[int]ident.Status{selfPID: {PPid: 1, Name: harness}},
		cwd:    map[int]string{selfPID: cwd},
	}
	resolver := &ident.Resolver{ProcFS: procfs, ProjectKey: func(string) string { return projectKey }}

	sessions := NewSessionRegistry()
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, git, nil, sessions)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return sockPath
}

// gitStateEventsForSession returns every session.git_state timeline event
// recorded for sessionID, decoded as payload.SessionGitState.
func gitStateEventsForSession(t *testing.T, st *store.Store, sessionID string) []payload.SessionGitState {
	t.Helper()
	rows, err := st.DB().Query(`SELECT payload FROM timeline_events WHERE kind = ? AND session_id = ?`,
		payload.KindSessionGitState, sessionID)
	if err != nil {
		t.Fatalf("query session.git_state events: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []payload.SessionGitState
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan session.git_state payload: %v", err)
		}
		var p payload.SessionGitState
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("unmarshal session.git_state payload %q: %v", raw, err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate session.git_state events: %v", err)
	}
	return out
}

// endLiveSession starts a live (unknown-harness, HarnessPID == 0) session
// against sockPath, closes its connection, and waits for
// ServeDaemonConn's deferred EOF handling to actually land (LiveSessionsInProject
// no longer lists it) before returning the ended session's id — the same
// wait pattern TestUnknownHarnessSessionEndsWhenItsConnectionCloses uses,
// pulled out here since this file's tests all need it.
func endLiveSession(t *testing.T, st *store.Store, sockPath, projectKey string) string {
	t.Helper()
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

	deadline := time.Now().Add(2 * time.Second)
	for {
		sessions, err := st.LiveSessionsInProject(projectKey)
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
			return result.Session
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s still live 2s after its connection closed", result.Session)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSessionEndRecordsGitStateWithUncommittedCount is DONE WHEN clause 1
// (task c2573b35): ending a live session whose cwd resolves (via the
// injected fake project.Git, never a real git binary) to a repo with 3
// uncommitted files appends exactly one session.git_state event for that
// session, carrying its branch and UncommittedCount == 3.
func TestSessionEndRecordsGitStateWithUncommittedCount(t *testing.T) {
	st := mustOpenStore(t)
	const cwd = "/home/brian/dirty-repo"
	git := fakeGit{cwd: project.State{Branch: "feature/widget", Uncommitted: 3}}
	sockPath := testDaemonWithGit(t, st, "not-a-known-harness", cwd, "proj-key", git)

	sessionID := endLiveSession(t, st, sockPath, "proj-key")

	events := gitStateEventsForSession(t, st, sessionID)
	if len(events) != 1 {
		t.Fatalf("got %d session.git_state events for session %s, want 1", len(events), sessionID)
	}
	got := events[0]
	if got.CouldNotObserve {
		t.Fatalf("CouldNotObserve = true, want false (git.State succeeded)")
	}
	if got.Branch != "feature/widget" {
		t.Errorf("Branch = %q, want %q", got.Branch, "feature/widget")
	}
	if got.UncommittedCount == nil || *got.UncommittedCount != 3 {
		t.Errorf("UncommittedCount = %v, want pointer to 3", got.UncommittedCount)
	}
}

// TestSessionEndRecordsCouldNotObserveNeverZeroCount is DONE WHEN clause 2:
// when the injected Git reports ok == false (git failed, or cwd is not a
// repo — indistinguishable to a caller of Git.State), the session.git_state
// event must carry CouldNotObserve == true and a nil UncommittedCount, never
// a count of 0. A writer that folded "could not observe" into 0 would make a
// dirty-tree flag on This Week indistinguishable from a genuinely clean one
// (SCHEMA.md invariant 7).
//
// RA-MUTATION-PROBE: recordSessionEndGitState's ok==false branch
// (internal/mcp/daemon.go — `p := payload.SessionGitState{CouldNotObserve:
// true}`) replaced with a zero-value count, e.g. `zero := 0; p :=
// payload.SessionGitState{UncommittedCount: &zero}` -> RED (this test:
// CouldNotObserve = false, want true); restored -> GREEN. Manually verified
// during development: both runs pasted in the task's commit history.
func TestSessionEndRecordsCouldNotObserveNeverZeroCount(t *testing.T) {
	st := mustOpenStore(t)
	const cwd = "/home/brian/not-a-repo"
	git := fakeGit{} // empty: cwd has no entry, so State reports ok == false
	sockPath := testDaemonWithGit(t, st, "not-a-known-harness", cwd, "proj-key", git)

	sessionID := endLiveSession(t, st, sockPath, "proj-key")

	events := gitStateEventsForSession(t, st, sessionID)
	if len(events) != 1 {
		t.Fatalf("got %d session.git_state events for session %s, want 1", len(events), sessionID)
	}
	got := events[0]
	if !got.CouldNotObserve {
		t.Fatalf("CouldNotObserve = false, want true (git.State failed)")
	}
	if got.UncommittedCount != nil {
		t.Errorf("UncommittedCount = %v, want nil — could-not-observe must never read as a count of 0", *got.UncommittedCount)
	}
	if got.Branch != "" {
		t.Errorf("Branch = %q, want empty on could-not-observe", got.Branch)
	}
}
