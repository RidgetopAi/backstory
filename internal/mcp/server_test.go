package mcp

import (
	"bytes"
	"encoding/json"
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
