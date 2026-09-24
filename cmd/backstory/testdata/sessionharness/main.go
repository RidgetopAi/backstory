// Command sessionharness is daemon_session_test.go's stand-in for a real
// harness process that dials the daemon socket several times over its
// lifetime — a SessionStart hook call, several PostToolUse hook calls, an
// MCP status call, all separate connections a real Claude Code session
// would make, but real ones nonetheless (task 32c6900d). It is built to a
// path literally named after an internal/ident.KnownHarnesses entry and
// exec'd directly, so the daemon's /proc ancestry walk matches it at
// distance zero, the same pattern cmd/backstory/testdata/harnessclient uses.
//
// It reads a JSON array of steps from stdin and dials the socket once per
// step, all from within this one process, so every step shares the same
// real pid and /proc start time — what the daemon's session identity
// actually keys on.
//
// Usage: sessionharness <socket-path>
// Stdin: a JSON array of {"session": "...", "method": "...", "params": {...}}
// Stdout: a JSON array of each step's raw DaemonResponse.Result, in order.
// Any dial, write, read, decode or daemon-side error aborts immediately
// with a non-zero exit and a message on stderr.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

type step struct {
	Session string          `json:"session,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sessionharness <socket-path> (steps JSON array on stdin)")
		os.Exit(2)
	}
	sockPath := os.Args[1]

	var steps []step
	if err := json.NewDecoder(os.Stdin).Decode(&steps); err != nil {
		fmt.Fprintln(os.Stderr, "sessionharness: decode steps:", err)
		os.Exit(1)
	}

	results := make([]json.RawMessage, 0, len(steps))
	for i, s := range steps {
		result, err := runStep(sockPath, s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sessionharness: step %d (%s): %v\n", i, s.Method, err)
			os.Exit(1)
		}
		results = append(results, result)
	}

	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		fmt.Fprintln(os.Stderr, "sessionharness: encode results:", err)
		os.Exit(1)
	}
}

func runStep(sockPath string, s step) (json.RawMessage, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	req := mcp.DaemonRequest{Session: s.Session, Method: s.Method, Params: s.Params}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var resp mcp.DaemonResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("daemon error: %s", resp.Error.Message)
	}
	return resp.Result, nil
}
