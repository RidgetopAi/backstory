// Command sessionharness is internal/mcp's self-spawned stand-in for a
// harness process that dials the daemon socket more than once (task
// 32c6900d): built to a path literally named after an internal/ident.
// KnownHarnesses entry and exec'd directly, so a real daemon's /proc
// ancestry walk matches it at distance zero, exactly like
// cmd/backstory/testdata/harnessclient.
//
// Unlike harnessclient, sessionharness reads a JSON array of steps from
// stdin and dials the socket once per step — from within this ONE process,
// so every step shares the same real pid — letting a test drive several
// separate daemon connections from a single observed harness process (a
// SessionStart hook call, several PostToolUse calls, a status call, ...)
// exactly the way a real harness's separate hook subprocesses would, minus
// the extra process-per-hook-call indirection this fixture doesn't need:
// what matters to the daemon's session identity is the observed harness
// process, not how many of its own children happened to make the call.
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
