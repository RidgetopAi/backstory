package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"

	"github.com/RidgetopAi/backstory/internal/version"
)

// ProtocolVersion is the MCP protocol revision this shim implements, pinned
// as a named constant so `initialize` responses are stable across releases
// (the punch's "protocol version pinned as a named constant" requirement).
const ProtocolVersion = "2025-06-18"

// JSON-RPC 2.0 error codes. CodeNotImplemented is in the -32000..-32099
// "server error" range JSON-RPC reserves for implementation-defined codes.
const (
	CodeParseError     = -32700
	CodeInvalidParams  = -32602
	CodeMethodNotFound = -32601
	CodeInternal       = -32603
	CodeNotImplemented = -32001
)

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("mcp: %d: %s", e.Code, e.Message) }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Dialer connects to the daemon socket. Server calls it lazily, on the
// first tool call that needs the daemon (note/status), never at
// construction — a harness launches the shim at session start and the first
// tool call can arrive minutes later, so dialing eagerly means the
// connection sits idle past the daemon's FirstLineDeadline before it is ever
// used, and every one of those first calls fails with a closed connection
// (task 40008eea).
type Dialer func() (net.Conn, error)

// Server is the stdio<->socket MCP shim: it speaks JSON-RPC 2.0 line by line
// over stdin/stdout, and forwards note/status/recall/confirm tool calls to
// the daemon over a connection it dials lazily via dial. timeline never
// touches the daemon connection at all.
type Server struct {
	dial         Dialer
	daemonConn   net.Conn
	daemonReader *bufio.Reader
}

// NewServer builds a shim that forwards note/status calls to the daemon
// connection dial produces. dial is not called until the first daemon-backed
// tool call, so initialize/tools/list answer even with no daemon listening,
// and a shim that never calls a daemon-backed tool never dials at all.
func NewServer(dial Dialer) *Server {
	return &Server{dial: dial}
}

// Close closes the shim's daemon connection, if one has been dialed. It is a
// no-op if the shim never made a daemon-backed call.
func (s *Server) Close() error {
	if s.daemonConn == nil {
		return nil
	}
	err := s.daemonConn.Close()
	s.daemonConn = nil
	s.daemonReader = nil
	return err
}

// Serve reads line-delimited JSON-RPC 2.0 requests from r and writes
// line-delimited responses to w until r reaches EOF. It returns nil on a
// clean EOF (the punch's "exit 0 on stdin EOF").
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			if err := writeResponse(w, rpcResponse{JSONRPC: "2.0", Error: &RPCError{Code: CodeParseError, Message: err.Error()}}); err != nil {
				return err
			}
			continue
		}

		resp := s.dispatch(req)
		if req.ID == nil {
			continue // a JSON-RPC notification gets no response
		}
		resp.JSONRPC = "2.0"
		resp.ID = req.ID
		if err := writeResponse(w, resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func writeResponse(w io.Writer, resp rpcResponse) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("mcp: marshal response: %w", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("mcp: write response: %w", err)
	}
	return nil
}

func (s *Server) dispatch(req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{Result: mustMarshal(initializeResult(req.Params))}
	case "notifications/initialized":
		return rpcResponse{}
	case "tools/list":
		return rpcResponse{Result: mustMarshal(toolsListEnvelope{Tools: ToolsV0()})}
	case "tools/call":
		return s.dispatchToolsCall(req.Params)
	default:
		return rpcResponse{Error: &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + req.Method}}
	}
}

// toolsListEnvelope mirrors the tools/list response shape: {"tools": [...]}.
type toolsListEnvelope struct {
	Tools []Tool `json:"tools"`
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) dispatchToolsCall(raw json.RawMessage) rpcResponse {
	var p toolsCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return rpcResponse{Error: &RPCError{Code: CodeInvalidParams, Message: "invalid tools/call params: " + err.Error()}}
	}
	result, rerr := s.CallTool(p.Name, p.Arguments)
	if rerr != nil {
		if isProtocolLevelToolError(rerr.Code) {
			return rpcResponse{Error: rerr}
		}
		// The tool itself was reached and rejected the call (bad
		// arguments, a daemon-side failure): report it inside a
		// CallToolResult, per the MCP spec, so Claude Code's session sees
		// the message instead of a bare JSON-RPC error (this punch's DONE
		// WHEN clause 2).
		return rpcResponse{Result: mustMarshal(errorToolResult(rerr.Message))}
	}
	return rpcResponse{Result: mustMarshal(successToolResult(result))}
}

// isProtocolLevelToolError reports whether code names a JSON-RPC protocol
// failure — the requested tool doesn't exist, or isn't implemented — as
// opposed to a known tool being called and rejecting its own arguments or
// backend call. Only protocol failures stay JSON-RPC errors; the tool
// itself was never reached for either of these.
func isProtocolLevelToolError(code int) bool {
	return code == CodeMethodNotFound || code == CodeNotImplemented
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResponse struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ServerInfo      serverInfo     `json:"serverInfo"`
	Capabilities    map[string]any `json:"capabilities"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

// initializeResult answers an initialize request. It echoes the client's
// requested protocolVersion when this shim supports it (today that means
// "equals ProtocolVersion" — there is only the one) rather than always
// answering the pinned constant, per the MCP spec's version-negotiation
// rule; an unrecognized or absent request falls back to ProtocolVersion.
func initializeResult(raw json.RawMessage) initializeResponse {
	pv := ProtocolVersion
	var p initializeParams
	if len(raw) > 0 && json.Unmarshal(raw, &p) == nil && p.ProtocolVersion == ProtocolVersion {
		pv = p.ProtocolVersion
	}
	return initializeResponse{
		ProtocolVersion: pv,
		ServerInfo:      serverInfo{Name: "backstory", Version: version.Version},
		Capabilities:    map[string]any{"tools": map[string]any{}},
	}
}

// mustMarshal marshals v, a value of one of this file's own static result
// types. Those types cannot fail to marshal, so a failure here means a
// programming error, not a runtime condition to recover from.
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mcp: marshal %T: %v", v, err))
	}
	return b
}
