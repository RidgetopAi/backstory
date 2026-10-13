package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

const offHandoffText = "OFF-TEST-HANDOFF unique resume text"

// withoutOffEnv strips BACKSTORY_OFF from env and appends value when non-nil,
// so every test here is independent of the invoking environment.
func withOffEnv(env []string, value *string) []string {
	env = envWithout(env, mcp.OffEnv)
	if value != nil {
		env = append(env, mcp.OffEnv+"="+*value)
	}
	return env
}

func strPtr(s string) *string { return &s }

// countingSocket listens where `backstory` looks for the daemon socket under
// a fresh XDG_RUNTIME_DIR and counts every connection made to it.
func countingSocket(t *testing.T) (env []string, conns *atomic.Int64) {
	t.Helper()
	runtimeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runtimeDir, "backstory"), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(runtimeDir, "backstory", "sock"))
	if err != nil {
		t.Fatal(err)
	}
	conns = new(atomic.Int64)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return testXDGEnv("XDG_RUNTIME_DIR=" + runtimeDir), conns
}

// seedHandoff files a handoff for projectDir's project in the daemon's store.
func seedHandoff(t *testing.T, dbPath, projectDir string) {
	t.Helper()
	s := mustOpenTestStore(t, dbPath)
	key := project.Key(projectDir, project.RealGit{}, nil)
	if err := s.UpsertProject(store.Project{Key: key, Toplevel: projectDir, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sid, err := s.StartSession(store.StartSessionParams{Agent: "claude", CWD: projectDir, ProjectKey: key, StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
		Text: offHandoffText, SessionID: sid, ProjectKey: key,
	}); err != nil {
		t.Fatal(err)
	}
}

func sessionStartIn(t *testing.T, bin, dir string, env []string) (string, string) {
	t.Helper()
	stdout, stderr, code, _ := runHookInDir(t, bin, dir, env, []string{"session-start"}, map[string]any{"session_id": "off-sess"})
	if code != 0 {
		t.Fatalf("session-start exit %d (stderr: %s)", code, stderr)
	}
	return stdout, stderr
}

func TestSessionStartBackstoryOffInjectsNothingAndDialsNothing(t *testing.T) {
	bin := buildBackstoryHarness(t)
	// A real store holding a handoff, to prove it is withheld and not just absent.
	dbPath, _, daemonEnv := startTestDaemon(t, bin)
	projectDir := t.TempDir()
	seedHandoff(t, dbPath, projectDir)

	if out, _ := sessionStartIn(t, bin, projectDir, withOffEnv(daemonEnv, strPtr("0"))); !strings.Contains(out, offHandoffText) {
		t.Fatalf("control (BACKSTORY_OFF=0) lacks the handoff; the off assertion would be vacuous:\n%s", out)
	}

	env, conns := countingSocket(t)
	for _, harnessArgs := range [][]string{{"session-start"}, {"session-start", "--harness", "codex"}} {
		stdout, stderr, code, _ := runHookInDir(t, bin, projectDir, withOffEnv(env, strPtr("1")), harnessArgs, map[string]any{"session_id": "off-sess"})
		if code != 0 || stdout != "" || stderr != "" {
			t.Errorf("%v: code=%d stdout=%q stderr=%q, want 0 and silent", harnessArgs, code, stdout, stderr)
		}
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("daemon connections with BACKSTORY_OFF=1 = %d, want 0", n)
	}
}

func TestPostToolUseBackstoryOffDialsNothingAndRecordsNothing(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, daemonEnv := startTestDaemon(t, bin)
	projectDir := t.TempDir()
	key := project.Key(projectDir, project.RealGit{}, nil)
	count := func() int {
		var n int
		if err := mustOpenTestStore(t, dbPath).DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	payload := func(id string) map[string]any {
		return map[string]any{"session_id": "off-ptu", "tool_name": "Edit", "tool_use_id": id,
			"tool_input": map[string]any{"file_path": filepath.Join(projectDir, "f.go")}}
	}

	// Against the real daemon: unchanged count with OFF=1.
	before := count()
	for _, sub := range []string{"post-tool-use", "post-tool-use-failure", "stop"} {
		runHookInDir(t, bin, projectDir, withOffEnv(daemonEnv, strPtr("1")), []string{sub}, payload("toolu_off_"+sub))
	}
	if after := count(); after != before {
		t.Errorf("timeline_events %d -> %d with BACKSTORY_OFF=1, want unchanged", before, after)
	}
	if evs := queryEventsForProject(t, mustOpenTestStore(t, dbPath), key); len(evs) != 0 {
		t.Errorf("project has events with BACKSTORY_OFF=1: %#v", evs)
	}

	// Control: OFF=0 records.
	runHookInDir(t, bin, projectDir, withOffEnv(daemonEnv, strPtr("0")), []string{"post-tool-use"}, payload("toolu_on"))
	if after := count(); after <= before {
		t.Errorf("control (BACKSTORY_OFF=0) recorded nothing: %d -> %d", before, after)
	}

	// And no connection at all.
	env, conns := countingSocket(t)
	for _, sub := range []string{"post-tool-use", "post-tool-use-failure", "stop"} {
		runHookInDir(t, bin, projectDir, withOffEnv(env, strPtr("1")), []string{sub}, payload("toolu_dial_"+sub))
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("daemon connections = %d, want 0", n)
	}
}

// mcpSession drives `backstory mcp` as a spec-shaped client and returns the
// tools/list result and the CallToolResult of each named call.
func mcpSession(t *testing.T, bin, dir string, env []string, calls map[string]map[string]any) (tools []mcp.Tool, results map[string]mcp.CallToolResult) {
	t.Helper()
	cmd := exec.Command(bin, "mcp") //nolint:gosec // bin is the binary this test just built
	cmd.Env = env
	cmd.Dir = dir
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	id := 0
	send := func(method string, params any) int {
		id++
		_ = enc.Encode(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
		return id
	}
	send("initialize", map[string]any{"protocolVersion": mcp.ProtocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}})
	_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	listID := send("tools/list", nil)
	callIDs := map[string]int{}
	for name, args := range calls {
		callIDs[name] = send("tools/call", map[string]any{"name": name, "arguments": args})
	}
	cmd.Stdin = &in
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("backstory mcp: %v (stderr: %s)", err, stderr.String())
	}
	byID := map[int]rpcResponse{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		var r rpcResponse
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("decode %q: %v", sc.Text(), err)
		}
		byID[r.ID] = r
	}
	var list struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(byID[listID].Result, &list); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	results = map[string]mcp.CallToolResult{}
	for name, cid := range callIDs {
		r := byID[cid]
		if r.Error != nil {
			t.Fatalf("%s: JSON-RPC error %+v", name, r.Error)
		}
		results[name] = requireCallToolResult(t, r.Result)
	}
	return list.Tools, results
}

var offCalls = map[string]map[string]any{
	mcp.ToolRecall: {"query": "anything"},
	mcp.ToolNote:   {"kind": "decision", "text": "should not be written"},
}

func TestMCPBackstoryOffAnswersListAndRefusesCallsWithoutDialing(t *testing.T) {
	bin := buildBackstoryHarness(t)
	env, conns := countingSocket(t)
	tools, results := mcpSession(t, bin, t.TempDir(), withOffEnv(env, strPtr("1")), offCalls)
	if len(tools) != len(mcp.ToolsV0()) {
		t.Errorf("tools/list returned %d tools, want %d", len(tools), len(mcp.ToolsV0()))
	}
	for name, r := range results {
		if r.IsError || !strings.Contains(r.Content[0].Text, "Backstory is off for this session") {
			t.Errorf("%s result = %+v, want a normal text result saying Backstory is off", name, r)
		}
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("daemon connections = %d, want 0", n)
	}
}

func TestBackstoryOffZeroOrUnsetBehavesAsToday(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, daemonEnv := startTestDaemon(t, bin)
	projectDir := t.TempDir()
	seedHandoff(t, dbPath, projectDir)

	for name, v := range map[string]*string{"unset": nil, "zero": strPtr("0")} {
		t.Run(name, func(t *testing.T) {
			env := withOffEnv(daemonEnv, v)
			if out, _ := sessionStartIn(t, bin, projectDir, env); !strings.Contains(out, offHandoffText) {
				t.Errorf("session-start lacks the ledger text:\n%s", out)
			}
			_, results := mcpSession(t, bin, projectDir, env, map[string]map[string]any{mcp.ToolRecall: {"query": "OFF-TEST-HANDOFF"}})
			if got := results[mcp.ToolRecall].Content[0].Text; strings.Contains(got, "Backstory is off") {
				t.Errorf("recall says Backstory is off: %s", got)
			}
		})
	}

	// The dialed-daemon half, on the counting socket.
	for name, v := range map[string]*string{"unset": nil, "zero": strPtr("0")} {
		env, conns := countingSocket(t)
		sessionStartIn2 := func() {
			_, _, _, _ = runHookInDir(t, bin, projectDir, withOffEnv(env, v), []string{"session-start"}, map[string]any{"session_id": "x"})
		}
		sessionStartIn2()
		_, _ = mcpSession(t, bin, projectDir, withOffEnv(env, v), map[string]map[string]any{mcp.ToolRecall: {"query": "q"}})
		if n := conns.Load(); n < 2 {
			t.Errorf("%s: daemon connections = %d, want >= 2 (hook and mcp both dial)", name, n)
		}
	}
}
