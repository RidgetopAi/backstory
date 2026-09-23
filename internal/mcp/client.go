package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
)

// CallTool executes one MCP tool by name given its raw JSON arguments
// object, returning the tool's structured JSON result or a JSON-RPC error.
// Exported so tests can drive note/status end-to-end (socket -> daemon ->
// store) without constructing wire-format JSON-RPC envelopes; server.go's
// tools/call handling is a thin wrapper around this.
func (s *Server) CallTool(name string, args json.RawMessage) (json.RawMessage, *RPCError) {
	switch name {
	case ToolNote:
		return s.callNote(args)
	case ToolStatus:
		return s.callStatus()
	case ToolRecall:
		return s.callRecall(args)
	case ToolTimeline, ToolConfirm:
		return nil, notImplementedError(name)
	default:
		return nil, &RPCError{Code: CodeMethodNotFound, Message: "unknown tool " + name}
	}
}

func notImplementedError(tool string) *RPCError {
	return &RPCError{Code: CodeNotImplemented, Message: tool + " not implemented in v0.0"}
}

func (s *Server) callNote(args json.RawMessage) (json.RawMessage, *RPCError) {
	var p NoteParams
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, &RPCError{Code: CodeInvalidParams, Message: "invalid note arguments: " + err.Error()}
	}
	// Re-serialize through NoteParams' own field set rather than forwarding
	// args verbatim: any key not in NoteParams (tier, session, ...) is
	// dropped here, before the request ever reaches the daemon
	// (AGENT-CONTRACT.md §The never-list, item 2).
	clean, err := json.Marshal(p)
	if err != nil {
		return nil, &RPCError{Code: CodeInternal, Message: err.Error()}
	}
	return s.callDaemon(daemonMethodNote, clean)
}

func (s *Server) callStatus() (json.RawMessage, *RPCError) {
	return s.callDaemon(daemonMethodStatus, nil)
}

// callRecall re-serializes args through RecallParams' own field set before
// forwarding, the same rule callNote applies to NoteParams: a "project"
// field a caller declares is dropped here, before the request ever reaches
// the daemon, never trusted as the anchor (AGENT-CONTRACT.md §Observed
// identity — this punch's WHAT TO BUILD: "never a declared one").
func (s *Server) callRecall(args json.RawMessage) (json.RawMessage, *RPCError) {
	var p RecallParams
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, &RPCError{Code: CodeInvalidParams, Message: "invalid recall arguments: " + err.Error()}
		}
	}
	clean, err := json.Marshal(p)
	if err != nil {
		return nil, &RPCError{Code: CodeInternal, Message: err.Error()}
	}
	return s.callDaemon(daemonMethodRecall, clean)
}

// callDaemon sends one DaemonRequest line to the daemon and reads back
// exactly one DaemonResponse line, translating a daemon-side error into a
// JSON-RPC error. The daemon connection is dialed lazily (on the first call
// that reaches here) and then reused across subsequent calls. If sending or
// reading the request fails — the daemon may have closed an idle connection
// between two tool calls, since sessions are per connection and the daemon
// still reaps a peer that goes quiet — callDaemon closes the dead
// connection, re-dials once, and retries the same request once before
// surfacing an error (task 40008eea).
func (s *Server) callDaemon(method string, params json.RawMessage) (json.RawMessage, *RPCError) {
	req := DaemonRequest{Method: method, Params: params}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, &RPCError{Code: CodeInternal, Message: err.Error()}
	}
	line := append(b, '\n')

	resp, rerr := s.sendRecvOnce(line)
	if rerr != nil {
		s.closeDaemonConn()
		resp, rerr = s.sendRecvOnce(line)
	}
	if rerr != nil {
		return nil, rerr
	}
	if resp.Error != nil {
		return nil, &RPCError{Code: rpcCodeForDaemonError(resp.Error.Code), Message: resp.Error.Message}
	}
	return resp.Result, nil
}

// sendRecvOnce ensures a daemon connection is dialed, writes line to it, and
// reads back exactly one DaemonResponse line. It never retries itself —
// callDaemon owns the one-retry policy — so a caller can tell a fresh
// dial+write+read attempt apart from a retry of the same attempt.
func (s *Server) sendRecvOnce(line []byte) (DaemonResponse, *RPCError) {
	if err := s.ensureDaemonConn(); err != nil {
		return DaemonResponse{}, &RPCError{Code: CodeInternal, Message: "mcp: " + err.Error()}
	}
	if _, err := s.daemonConn.Write(line); err != nil {
		return DaemonResponse{}, &RPCError{Code: CodeInternal, Message: "mcp: write daemon request: " + err.Error()}
	}
	respLine, err := s.daemonReader.ReadBytes('\n')
	if err != nil {
		return DaemonResponse{}, &RPCError{Code: CodeInternal, Message: "mcp: read daemon response: " + err.Error()}
	}
	var resp DaemonResponse
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return DaemonResponse{}, &RPCError{Code: CodeInternal, Message: "mcp: decode daemon response: " + err.Error()}
	}
	return resp, nil
}

// ensureDaemonConn dials the daemon on the first call and reuses that
// connection on every subsequent call; it is a no-op once daemonConn is set.
func (s *Server) ensureDaemonConn() error {
	if s.daemonConn != nil {
		return nil
	}
	if s.dial == nil {
		return errors.New("no daemon connection")
	}
	conn, err := s.dial()
	if err != nil {
		return fmt.Errorf("dial daemon: %w", err)
	}
	s.daemonConn = conn
	s.daemonReader = bufio.NewReader(conn)
	return nil
}

// closeDaemonConn drops the current daemon connection so the next call
// re-dials from scratch. Used only to recover from a dead connection;
// Server.Close is the public, caller-invoked equivalent.
func (s *Server) closeDaemonConn() {
	if s.daemonConn != nil {
		_ = s.daemonConn.Close()
	}
	s.daemonConn = nil
	s.daemonReader = nil
}

// rpcCodeForDaemonError maps a DaemonError.Code to the JSON-RPC error code
// the shim surfaces to its own caller. "invalid-params" is the only daemon
// error a well-formed caller can act on by changing its arguments; every
// other daemon-side failure is opaque from the tool caller's point of view.
func rpcCodeForDaemonError(code string) int {
	if code == "invalid-params" {
		return CodeInvalidParams
	}
	return CodeInternal
}
