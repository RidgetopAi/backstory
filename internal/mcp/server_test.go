package mcp

import (
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

// TestServeInitializeAndToolsListRoundTrip drives Server.Serve directly (no
// subprocess, no daemon) over the JSON-RPC 2.0 framing: cmd/backstory's
// mcp_test.go covers the same two methods end-to-end through a real
// subprocess; this is the cheaper, daemon-less unit-level check of the
// dispatch itself.
func TestServeInitializeAndToolsListRoundTrip(t *testing.T) {
	s := NewServer(nil)
	in := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n",
	)
	var out bytes.Buffer
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("Serve wrote %d lines, want 2:\n%s", len(lines), out.String())
	}

	var initResp rpcResponse
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("unmarshal initialize response: %v", err)
	}
	if initResp.Error != nil {
		t.Fatalf("initialize response error: %+v", initResp.Error)
	}
	var initResult initializeResponse
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if initResult.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", initResult.ProtocolVersion, ProtocolVersion)
	}

	var listResp rpcResponse
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}
	var listResult toolsListEnvelope
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	if len(listResult.Tools) != 5 {
		t.Errorf("tools/list returned %d tools, want 5", len(listResult.Tools))
	}
}

// TestServeSkipsResponseForNotifications confirms a request with no "id"
// (a JSON-RPC notification, e.g. notifications/initialized) gets no
// response line at all — a client blocking on a reply to a notification
// would hang forever if Serve got this wrong.
func TestServeSkipsResponseForNotifications(t *testing.T) {
	s := NewServer(nil)
	in := strings.NewReader(
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
			`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n",
	)
	var out bytes.Buffer
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("Serve wrote %d lines for one notification + one request, want 1:\n%s", len(lines), out.String())
	}
	var resp rpcResponse
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ID == nil {
		t.Fatal("the one response line has no id; want it to answer the tools/list request, not the notification")
	}
}

// TestCallToolUnknownToolReturnsMethodNotFound covers CallTool's default
// case, which neither the daemon-backed note/status tests nor the
// not-implemented tests reach.
func TestCallToolUnknownToolReturnsMethodNotFound(t *testing.T) {
	s := NewServer(nil)
	_, rerr := s.CallTool("does-not-exist", json.RawMessage(`{}`))
	if rerr == nil {
		t.Fatal("CallTool(unknown tool) = nil error, want CodeMethodNotFound")
	}
	if rerr.Code != CodeMethodNotFound {
		t.Errorf("error code = %d, want %d (CodeMethodNotFound)", rerr.Code, CodeMethodNotFound)
	}
}

// TestDispatchToolsCallUnknownMethodReturnsError confirms an
// unrecognized top-level JSON-RPC method (not tools/call at all) gets a
// JSON-RPC error rather than being silently ignored.
func TestDispatchUnknownMethodReturnsError(t *testing.T) {
	s := NewServer(nil)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"not/a/real/method"}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("response has no error, want CodeMethodNotFound")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("error code = %d, want %d (CodeMethodNotFound)", resp.Error.Code, CodeMethodNotFound)
	}
}

// requireCallToolResult unmarshals raw — the `result` a tools/call response
// carries — against the MCP spec's CallToolResult shape and fails t if it
// doesn't match: a non-empty content array whose first block is a
// non-empty "text" block. This is this punch's DONE WHEN clause 3 check:
// the daemon's raw payload object (the pre-fix shape) has no "content"
// key, so Content comes back nil here and this fails outright rather than
// silently accepting it.
func requireCallToolResult(t *testing.T, raw json.RawMessage) CallToolResult {
	t.Helper()
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("tools/call result is not a JSON object: %v\nraw: %s", err, raw)
	}
	var result CallToolResult
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

// serveOneToolCall drives Server.Serve with a single tools/call request for
// name/arguments and returns the raw JSON-RPC response — the same wire path
// a harness drives over stdio, minus the subprocess (cmd/backstory's
// mcp_test.go covers the subprocess form of this same request).
func serveOneToolCall(t *testing.T, s *Server, name string, arguments json.RawMessage) rpcResponse {
	t.Helper()
	reqLine, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": arguments},
	})
	if err != nil {
		t.Fatalf("marshal tools/call request: %v", err)
	}
	var out bytes.Buffer
	if err := s.Serve(bytes.NewReader(append(reqLine, '\n')), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("unmarshal tools/call response: %v\nraw: %s", err, out.String())
	}
	return resp
}

// TestToolsCallStatusResultMatchesCallToolResultShape is this punch's DONE
// WHEN clause 1/3, driven through Server.Serve (not CallTool directly)
// against a real test daemon: status's tools/call result has a non-empty
// content array whose text carries the payload, and structuredContent
// equals the parsed StatusResult.
func TestToolsCallStatusResultMatchesCallToolResultShape(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	resp := serveOneToolCall(t, s, ToolStatus, json.RawMessage(`{}`))
	if resp.Error != nil {
		t.Fatalf("tools/call(status) error: %+v", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("tools/call(status) isError = true, want false: %s", result.Content[0].Text)
	}
	var status StatusResult
	if err := json.Unmarshal(result.StructuredContent, &status); err != nil {
		t.Fatalf("unmarshal structuredContent as StatusResult: %v", err)
	}
	if status.ProjectKey != "proj-key" {
		t.Errorf("structuredContent.project_key = %q, want proj-key", status.ProjectKey)
	}
	if !strings.Contains(result.Content[0].Text, "proj-key") {
		t.Errorf("content[0].text = %q, want it to contain the project key", result.Content[0].Text)
	}
}

// TestToolsCallNoteResultMatchesCallToolResultShape is the same DONE WHEN
// clause 1 check for note specifically: content[0].text must name the new
// record's id and its tier, per the punch's own wording.
func TestToolsCallNoteResultMatchesCallToolResultShape(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	resp := serveOneToolCall(t, s, ToolNote, json.RawMessage(`{"kind":"note","text":"shape-checked via tools/call"}`))
	if resp.Error != nil {
		t.Fatalf("tools/call(note) error: %+v", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("tools/call(note) isError = true, want false: %s", result.Content[0].Text)
	}
	var note NoteResult
	if err := json.Unmarshal(result.StructuredContent, &note); err != nil {
		t.Fatalf("unmarshal structuredContent as NoteResult: %v", err)
	}
	if note.ID == "" {
		t.Fatal("structuredContent has no id")
	}
	if !strings.Contains(result.Content[0].Text, note.ID) {
		t.Errorf("content[0].text = %q, want it to contain the record id %q", result.Content[0].Text, note.ID)
	}
	if !strings.Contains(result.Content[0].Text, note.Tier) {
		t.Errorf("content[0].text = %q, want it to contain the tier %q", result.Content[0].Text, note.Tier)
	}
}

// TestToolsCallRejectedNoteIsErrorResultNotRPCError is this punch's DONE
// WHEN clause 2: note with a missing required field ("kind") comes back as
// a CallToolResult with isError:true and the message in content, not as a
// JSON-RPC error.
func TestToolsCallRejectedNoteIsErrorResultNotRPCError(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	resp := serveOneToolCall(t, s, ToolNote, json.RawMessage(`{"text":"no kind field"}`))
	if resp.Error != nil {
		t.Fatalf("tools/call(note, missing kind) came back as a JSON-RPC error %+v, want a CallToolResult with isError:true", resp.Error)
	}
	result := requireCallToolResult(t, resp.Result)
	if !result.IsError {
		t.Fatalf("tools/call(note, missing kind) isError = false, want true: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "kind") {
		t.Errorf("content[0].text = %q, want it to name %q", result.Content[0].Text, "kind")
	}
	if len(result.StructuredContent) != 0 {
		t.Errorf("structuredContent = %s, want empty on a rejected call", result.StructuredContent)
	}
}

// TestToolsCallUnknownToolStaysRPCError confirms an unknown tool name is
// still a JSON-RPC protocol error, not a CallToolResult — the tool was
// never reached at all, unlike a known tool rejecting its own arguments.
func TestToolsCallUnknownToolStaysRPCError(t *testing.T) {
	s := NewServer(nil)
	resp := serveOneToolCall(t, s, "does-not-exist", json.RawMessage(`{}`))
	if resp.Error == nil {
		t.Fatal("tools/call(unknown tool) has no error, want a JSON-RPC error")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("error code = %d, want %d (CodeMethodNotFound)", resp.Error.Code, CodeMethodNotFound)
	}
	if len(resp.Result) != 0 {
		t.Errorf("result = %s, want empty alongside a JSON-RPC error", resp.Result)
	}
}

// TestInitializeEchoesClientProtocolVersionWhenSupported confirms
// initialize answers with the client's own requested protocolVersion when
// it matches what this shim supports, rather than always answering the
// pinned constant regardless of what was asked.
func TestInitializeEchoesClientProtocolVersionWhenSupported(t *testing.T) {
	s := NewServer(nil)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + ProtocolVersion + `"}}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	var result initializeResponse
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, ProtocolVersion)
	}
}

// TestInitializeFallsBackForUnsupportedClientProtocolVersion confirms a
// client requesting a version this shim doesn't support still gets a valid
// answer: the shim's own pinned ProtocolVersion, not the client's
// unsupported request echoed back verbatim.
func TestInitializeFallsBackForUnsupportedClientProtocolVersion(t *testing.T) {
	s := NewServer(nil)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	var result initializeResponse
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %q, want fallback to %q", result.ProtocolVersion, ProtocolVersion)
	}
}
