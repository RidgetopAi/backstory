package main

import (
	"bufio"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcp.RPCError   `json:"error,omitempty"`
}

// requireCallToolResult unmarshals raw — a tools/call response's `result`
// — as an mcp.CallToolResult and fails t unless it matches the MCP spec
// shape this punch requires: a non-empty content array whose first block
// is a non-empty "text" block. Before the fix, `result` was the daemon's
// raw payload object with no "content" key at all, which unmarshals here
// with Content == nil — so a regression back to that shape fails this
// check directly (DONE WHEN clauses 1 and 3).
func requireCallToolResult(t *testing.T, raw json.RawMessage) mcp.CallToolResult {
	t.Helper()
	var result mcp.CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal CallToolResult: %v\nraw: %s", err, raw)
	}
	if len(result.Content) == 0 {
		t.Fatalf("tools/call result.content is empty, want at least one block\nraw: %s", raw)
	}
	first := result.Content[0]
	if first.Type != "text" {
		t.Fatalf("content[0].type = %q, want %q\nraw: %s", first.Type, "text", raw)
	}
	if first.Text == "" {
		t.Fatalf("content[0].text is empty\nraw: %s", raw)
	}
	return result
}

// TestMCPSubprocessSpeaksStdioAgainstADaemonStartedInTheTest is the punch's
// DONE WHEN clause 5: `backstory mcp` as a subprocess speaks initialize ->
// tools/list -> tools/call(note) -> tools/call(status) over stdio against a
// daemon started in the test; exit 0 on stdin EOF.
func TestMCPSubprocessSpeaksStdioAgainstADaemonStartedInTheTest(t *testing.T) {
	bin := buildBackstory(t)

	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	env := testXDGEnv(
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XDG_DATA_HOME="+dataDir,
	)

	daemonCmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built
	daemonCmd.Env = env
	var daemonOut safeBuffer
	daemonCmd.Stdout = &daemonOut
	daemonCmd.Stderr = &daemonOut
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = daemonCmd.Process.Signal(syscall.SIGTERM)
		_ = daemonCmd.Wait()
	})

	sockPath := filepath.Join(runtimeDir, "backstory", "sock")
	waitForFile(t, sockPath, 2*time.Second)

	mcpCmd := exec.Command(bin, "mcp") //nolint:gosec // bin is the binary this test just built
	mcpCmd.Env = env
	stdin, err := mcpCmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := mcpCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var mcpErr safeBuffer
	mcpCmd.Stderr = &mcpErr
	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start backstory mcp: %v", err)
	}

	enc := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	sendAndRead := func(req rpcRequest) rpcResponse {
		t.Helper()
		if err := enc.Encode(req); err != nil {
			t.Fatalf("encode %s request: %v", req.Method, err)
		}
		if !scanner.Scan() {
			t.Fatalf("no response to %s: %v", req.Method, scanner.Err())
		}
		var resp rpcResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s response %q: %v", req.Method, scanner.Text(), err)
		}
		return resp
	}

	initResp := sendAndRead(rpcRequest{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if initResult.ProtocolVersion != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", initResult.ProtocolVersion, mcp.ProtocolVersion)
	}

	listResp := sendAndRead(rpcRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	if listResp.Error != nil {
		t.Fatalf("tools/list error: %+v", listResp.Error)
	}
	var listResult struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	wantNames := []string{mcp.ToolRecall, mcp.ToolNote, mcp.ToolTimeline, mcp.ToolConfirm, mcp.ToolStatus}
	if len(listResult.Tools) != len(wantNames) {
		t.Fatalf("tools/list returned %d tools, want %d", len(listResult.Tools), len(wantNames))
	}
	for i, name := range wantNames {
		if listResult.Tools[i].Name != name {
			t.Errorf("tools/list[%d].Name = %q, want %q", i, listResult.Tools[i].Name, name)
		}
	}

	// DONE WHEN clause 1: note's result.content[0].text names both the new
	// record's id and its tier, and structuredContent is the same NoteResult
	// a caller can parse directly.
	noteResp := sendAndRead(rpcRequest{
		JSONRPC: "2.0", ID: 3, Method: "tools/call",
		Params: map[string]any{
			"name":      mcp.ToolNote,
			"arguments": map[string]any{"kind": "decision", "text": "the subprocess shim works end to end"},
		},
	})
	if noteResp.Error != nil {
		t.Fatalf("tools/call(note) error: %+v", noteResp.Error)
	}
	noteCall := requireCallToolResult(t, noteResp.Result)
	if noteCall.IsError {
		t.Fatalf("tools/call(note) isError = true, want false: %s", noteCall.Content[0].Text)
	}
	var noteResult mcp.NoteResult
	if err := json.Unmarshal(noteCall.StructuredContent, &noteResult); err != nil {
		t.Fatalf("unmarshal note structuredContent: %v", err)
	}
	if noteResult.ID == "" {
		t.Error("note result has no id")
	}
	if noteResult.Tier != "agent-declared" {
		t.Errorf("note result tier = %q, want agent-declared (a subprocess is always a socket peer, never human)", noteResult.Tier)
	}
	if !strings.Contains(noteCall.Content[0].Text, noteResult.ID) {
		t.Errorf("note content[0].text = %q, want it to contain the record id %q", noteCall.Content[0].Text, noteResult.ID)
	}
	if !strings.Contains(noteCall.Content[0].Text, noteResult.Tier) {
		t.Errorf("note content[0].text = %q, want it to contain the tier %q", noteCall.Content[0].Text, noteResult.Tier)
	}

	statusResp := sendAndRead(rpcRequest{
		JSONRPC: "2.0", ID: 4, Method: "tools/call",
		Params: map[string]any{"name": mcp.ToolStatus, "arguments": map[string]any{}},
	})
	if statusResp.Error != nil {
		t.Fatalf("tools/call(status) error: %+v", statusResp.Error)
	}
	statusCall := requireCallToolResult(t, statusResp.Result)
	if statusCall.IsError {
		t.Fatalf("tools/call(status) isError = true, want false: %s", statusCall.Content[0].Text)
	}
	var statusResult mcp.StatusResult
	if err := json.Unmarshal(statusCall.StructuredContent, &statusResult); err != nil {
		t.Fatalf("unmarshal status structuredContent: %v", err)
	}
	if statusResult.Session == "" {
		t.Error("status result has no session id")
	}
	if statusResult.Kind != "agent" {
		t.Errorf("status result kind = %q, want agent", statusResult.Kind)
	}

	recallResp := sendAndRead(rpcRequest{
		JSONRPC: "2.0", ID: 5, Method: "tools/call",
		Params: map[string]any{"name": mcp.ToolRecall, "arguments": map[string]any{}},
	})
	if recallResp.Error != nil {
		t.Fatalf("tools/call(recall) error: %+v", recallResp.Error)
	}
	recallCall := requireCallToolResult(t, recallResp.Result)
	if recallCall.IsError {
		t.Fatalf("tools/call(recall) isError = true, want false: %s", recallCall.Content[0].Text)
	}
	var recallResult mcp.RecallResult
	if err := json.Unmarshal(recallCall.StructuredContent, &recallResult); err != nil {
		t.Fatalf("unmarshal recall structuredContent: %v", err)
	}
	if recallResult.ProjectKey == "" {
		t.Error("recall result has no project_key")
	}

	// DONE WHEN clause 2: a tool call the daemon rejects (note with a
	// missing required field) comes back as a CallToolResult with
	// isError:true and the message in content, not as a JSON-RPC error.
	rejectResp := sendAndRead(rpcRequest{
		JSONRPC: "2.0", ID: 6, Method: "tools/call",
		Params: map[string]any{
			"name":      mcp.ToolNote,
			"arguments": map[string]any{"text": "missing the required kind field"},
		},
	})
	if rejectResp.Error != nil {
		t.Fatalf("tools/call(note, missing kind) came back as a JSON-RPC error %+v, want a CallToolResult with isError:true", rejectResp.Error)
	}
	rejectCall := requireCallToolResult(t, rejectResp.Result)
	if !rejectCall.IsError {
		t.Fatalf("tools/call(note, missing kind) isError = false, want true: %s", rejectCall.Content[0].Text)
	}
	if !strings.Contains(rejectCall.Content[0].Text, "kind") {
		t.Errorf("reject content[0].text = %q, want it to name %q", rejectCall.Content[0].Text, "kind")
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("close mcp stdin: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- mcpCmd.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("backstory mcp exited with error after stdin EOF (want exit 0): %v\nstderr:\n%s", err, mcpErr.String())
		}
	case <-time.After(2 * time.Second):
		_ = mcpCmd.Process.Kill()
		t.Fatalf("backstory mcp did not exit within 2s of stdin EOF\nstderr:\n%s", mcpErr.String())
	}
}
