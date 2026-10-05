package mcp

import "encoding/json"

// DaemonRequest is one line the shim sends the daemon over the socket
// connection socket.Server hands its Handler. Session and Actor are the
// connection's declared join-key fields (socket.DeclaredFields):
// AGENT-CONTRACT.md is explicit these are join keys only, never identity —
// the daemon-side handler never uses them to pick a tier.
type DaemonRequest struct {
	Session string          `json:"session,omitempty"`
	Actor   string          `json:"actor,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	// NoSession asks the daemon not to mint a session (or project row) for
	// this connection. It is honoured only on the read-only status method
	// (NoSessionMethods); the installer sets it so its own health probe
	// does not record the build directory as the user's first memory.
	NoSession bool `json:"no_session,omitempty"`
}

// DaemonError is a daemon-side method failure. Code is a short machine-
// readable name (e.g. "invalid-params"), never an errno or a Go error string
// alone.
type DaemonError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DaemonResponse is one line the daemon sends back per DaemonRequest line,
// synchronous and in order: exactly one of Result or Error is set.
type DaemonResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *DaemonError    `json:"error,omitempty"`
}
