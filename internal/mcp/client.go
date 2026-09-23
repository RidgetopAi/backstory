package mcp

import "encoding/json"

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
// JSON-RPC error.
func (s *Server) callDaemon(method string, params json.RawMessage) (json.RawMessage, *RPCError) {
	if s.daemonConn == nil {
		return nil, &RPCError{Code: CodeInternal, Message: "mcp: no daemon connection"}
	}
	req := DaemonRequest{Method: method, Params: params}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, &RPCError{Code: CodeInternal, Message: err.Error()}
	}
	if _, err := s.daemonConn.Write(append(b, '\n')); err != nil {
		return nil, &RPCError{Code: CodeInternal, Message: "mcp: write daemon request: " + err.Error()}
	}

	line, err := s.daemonReader.ReadBytes('\n')
	if err != nil {
		return nil, &RPCError{Code: CodeInternal, Message: "mcp: read daemon response: " + err.Error()}
	}
	var resp DaemonResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, &RPCError{Code: CodeInternal, Message: "mcp: decode daemon response: " + err.Error()}
	}
	if resp.Error != nil {
		return nil, &RPCError{Code: rpcCodeForDaemonError(resp.Error.Code), Message: resp.Error.Message}
	}
	return resp.Result, nil
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
