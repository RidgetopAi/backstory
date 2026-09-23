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

// Server is the stdio<->socket MCP shim: it speaks JSON-RPC 2.0 line by line
// over stdin/stdout, and forwards note/status/recall tool calls to the
// daemon over daemonConn. timeline/confirm never touch daemonConn at all.
type Server struct {
	daemonConn   net.Conn
	daemonReader *bufio.Reader
}

// NewServer builds a shim that forwards note/status calls over an
// already-connected daemonConn (dialed by the caller, e.g. cmd/backstory's
// `mcp` subcommand or a test's own net.Dial against a test daemon).
func NewServer(daemonConn net.Conn) *Server {
	return &Server{daemonConn: daemonConn, daemonReader: bufio.NewReader(daemonConn)}
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
		return rpcResponse{Result: mustMarshal(initializeResult())}
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
		return rpcResponse{Error: rerr}
	}
	return rpcResponse{Result: result}
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

func initializeResult() initializeResponse {
	return initializeResponse{
		ProtocolVersion: ProtocolVersion,
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
