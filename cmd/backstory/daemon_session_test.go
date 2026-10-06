package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// sessionHarnessStep is one step of testdata/sessionharness's stdin
// protocol: dial the daemon socket, send this DaemonRequest, read back its
// raw result, before moving to the next step (a fresh connection every
// time, exactly like a real SessionStart hook call, PostToolUse hook call,
// or MCP shim call each are).
type sessionHarnessStep struct {
	Session string          `json:"session,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// buildSessionHarness compiles testdata/sessionharness to a binary whose
// path's final component is exactly name, the same pattern
// buildHarnessClient (daemon_test.go) and buildMCPHarness (mcp_idle_test.go)
// use so the daemon's /proc ancestry walk matches it at distance zero.
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

// runSessionHarness execs bin with cwd set to dir against sockPath's
// daemon, feeding steps as JSON on stdin, and decodes stdout as one raw
// DaemonResponse.Result per step, in order. Every step dials from WITHIN
// this one process (dir set once, for the whole process), so the daemon
// observes the identical harness process across every step, the same way a
// single real Claude Code process's several hook subprocess children all
// share it as their common ancestor.
func runSessionHarness(t *testing.T, bin, sockPath, dir string, steps []sessionHarnessStep) []json.RawMessage {
	t.Helper()
	in, err := json.Marshal(steps)
	if err != nil {
		t.Fatalf("marshal steps: %v", err)
	}

	cmd := exec.Command(bin, sockPath) //nolint:gosec // bin is the binary this test just built
	cmd.Dir = dir
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

// eventSessionRow is queryEventSessionsForProject's row shape: enough to
// check every event a harness process wrote carries the SAME session id.
type eventSessionRow struct {
	Kind      string
	SessionID string
}

func queryEventSessionsForProject(t *testing.T, s *store.Store, projectKey string) []eventSessionRow {
	t.Helper()
	rows, err := s.DB().Query(`SELECT e.kind, e.session_id FROM timeline_events e
		JOIN sessions sess ON sess.id = e.session_id
		WHERE sess.project_key = ? ORDER BY e.id`, projectKey)
	if err != nil {
		t.Fatalf("query event sessions for project %s: %v", projectKey, err)
	}
	defer func() { _ = rows.Close() }()
	var out []eventSessionRow
	for rows.Next() {
		var r eventSessionRow
		if err := rows.Scan(&r.Kind, &r.SessionID); err != nil {
			t.Fatalf("scan event session row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate event session rows: %v", err)
	}
	return out
}

// TestOneHarnessProcessSharesOneSessionAcrossHookAndMCPCalls is the punch's
// (32c6900d) DONE WHEN clause 1: from one test-spawned harness-named helper
// process, a SessionStart hook call, five PostToolUse hook calls, and an
// MCP status call — all separate daemon connections, exactly like a real
// Claude Code session's separate hook subprocesses and its long-lived MCP
// shim connection — produce exactly ONE live session in the store, every
// event they write carries that session's id, and the SessionStart block
// rendered afterwards says 1 session (not 5, not 7 — what a per-connection
// session model reports).
//
// Each PostToolUse step also declares a different "session" join key value
// than the SessionStart step, exercising DONE WHEN clause 4's "a declared
// session id in a hook payload never merges or splits sessions on its own"
// from the merge side: observed identity (this one real harness process)
// decides the session, regardless of what each connection's payload claims.
//
// RA-MUTATION-PROBE: internal/mcp.SessionRegistry.SessionFor's cache-hit
// branch deleted (every connection mints a fresh session via start()) ->
// RED (block says "6 sessions", not "1 sessions"; the live-session count
// below is 6, not 1); restored -> GREEN.
func TestOneHarnessProcessSharesOneSessionAcrossHookAndMCPCalls(t *testing.T) {
	bin := buildBackstory(t)
	dbPath, runtimeDir, _ := startTestDaemon(t, bin)
	sockPath := filepath.Join(runtimeDir, "backstory", "sock")
	harnessBin := buildSessionHarness(t, harnessName)
	projectDir := t.TempDir()

	steps := []sessionHarnessStep{
		{Session: "hook-session-start", Method: mcp.DaemonMethodBlock},
	}
	for i := 1; i <= 5; i++ {
		params, err := json.Marshal(mcp.PostToolUseParams{
			ToolUseID: "tu" + string(rune('0'+i)),
			ToolName:  "Read",
		})
		if err != nil {
			t.Fatalf("marshal post_tool_use params %d: %v", i, err)
		}
		steps = append(steps, sessionHarnessStep{
			Session: "hook-post-tool-use", // deliberately NOT "hook-session-start"
			Method:  mcp.DaemonMethodPostToolUse,
			Params:  params,
		})
	}
	// The MCP status call: not one of the five frozen tool RPCs, but the raw
	// daemon-side method the shim's `status` tool forwards to
	// (internal/mcp/daemon.go's unexported daemonMethodStatus == "status").
	steps = append(steps, sessionHarnessStep{Session: "mcp-shim-status", Method: "status"})
	steps = append(steps, sessionHarnessStep{Session: "hook-session-start", Method: mcp.DaemonMethodBlock})

	results := runSessionHarness(t, harnessBin, sockPath, projectDir, steps)
	if len(results) != len(steps) {
		t.Fatalf("got %d results, want %d", len(results), len(steps))
	}

	var statusResult mcp.StatusResult
	if err := json.Unmarshal(results[6], &statusResult); err != nil {
		t.Fatalf("unmarshal status result: %v", err)
	}
	if statusResult.Session == "" {
		t.Fatal("status result Session is empty")
	}

	var finalBlock mcp.BlockResult
	if err := json.Unmarshal(results[7], &finalBlock); err != nil {
		t.Fatalf("unmarshal final block result: %v", err)
	}
	// The reading session is excluded from Delta's session count and "0 files
	// touched" is never printed, so one shared session leaves no Delta line.
	if strings.Contains(finalBlock.Block, "Delta:") || strings.Contains(finalBlock.Block, "0 files touched") {
		t.Errorf("final block = %q, want no Delta line (the only session is the reader)", finalBlock.Block)
	}

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := queryEventSessionsForProject(t, s, projectKey)
	if len(events) != 5 {
		t.Fatalf("project has %d timeline events, want 5 (one per post_tool_use call): %#v", len(events), events)
	}
	for _, ev := range events {
		if ev.SessionID != statusResult.Session {
			t.Errorf("event %s has session_id %q, want the shared session %q", ev.Kind, ev.SessionID, statusResult.Session)
		}
	}

	liveSessions, err := s.LiveSessionsInProject(projectKey)
	if err != nil {
		t.Fatalf("LiveSessionsInProject: %v", err)
	}
	if len(liveSessions) != 1 {
		ids := make([]string, len(liveSessions))
		for i, sess := range liveSessions {
			ids[i] = sess.ID
		}
		t.Fatalf("project has %d live sessions after 7 connections from one harness process, want 1: %v", len(liveSessions), ids)
	}
	if liveSessions[0].ID != statusResult.Session {
		t.Errorf("the one live session is %q, want the shared session %q", liveSessions[0].ID, statusResult.Session)
	}
}
