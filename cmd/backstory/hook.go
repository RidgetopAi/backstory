package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

// sessionStartPayload is a Claude Code SessionStart hook's JSON payload.
// Only SessionID is used: it becomes the connection's declared "session"
// join key (socket.DeclaredFields), recorded on the session as
// harness_session_id — a join key, never identity (AGENT-CONTRACT.md
// §Observed identity). cwd, transcript_path, source and hook_event_name are
// accepted so a real harness payload decodes cleanly, even though nothing
// here reads them.
type sessionStartPayload struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
	Source         string `json:"source"`
	HookEventName  string `json:"hook_event_name"`
}

// runHook is the `backstory hook` subcommand. session-start reads a Claude
// Code SessionStart payload from stdin, asks the daemon for the rendered
// SessionStart block, and prints it to stdout.
//
// A hook must never break a harness boot (AGENT-CONTRACT.md §The
// SessionStart block): every failure path here — an unknown subcommand, an
// unreadable payload, no daemon socket, a daemon error — prints nothing to
// stdout, writes exactly one line to stderr, and still exits 0.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "session-start" {
		_, _ = fmt.Fprintln(stderr, "backstory hook: usage: backstory hook session-start")
		return 0
	}

	var payload sessionStartPayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook: decode session-start payload:", err)
		return 0
	}

	block, err := requestBlock(payload.SessionID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook:", err)
		return 0
	}

	_, _ = fmt.Fprintln(stdout, block)
	return 0
}

// requestBlock dials the daemon socket exactly like `backstory mcp` does
// and asks for the rendered SessionStart block, declaring harnessSessionID
// as the "session" join key (socket.DeclaredFields) the same way the mcp
// shim declares its own. The daemon resolver's own SO_PEERCRED + /proc walk
// still decides identity, tier and project — nothing in this request can
// change any of them, whatever session id or other field the payload
// claims.
func requestBlock(harnessSessionID string) (string, error) {
	sockPath, err := socketPath()
	if err != nil {
		return "", err
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return "", fmt.Errorf("dial daemon socket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	req := mcp.DaemonRequest{Session: harnessSessionID, Method: mcp.DaemonMethodBlock}
	b, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal block request: %w", err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return "", fmt.Errorf("write daemon request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return "", fmt.Errorf("read daemon response: %w", err)
	}
	var resp mcp.DaemonResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return "", fmt.Errorf("decode daemon response: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("daemon: %s", resp.Error.Message)
	}
	var result mcp.BlockResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("decode block result: %w", err)
	}
	return result.Block, nil
}
