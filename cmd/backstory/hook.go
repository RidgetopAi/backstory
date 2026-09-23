package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/payload"
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
// SessionStart block, and prints it to stdout. post-tool-use reads a
// PostToolUse payload from stdin and records it as a live timeline event;
// it never prints anything to stdout (a PostToolUse hook's stdout is not
// injected as context the way SessionStart's is).
//
// A hook must never break a harness boot (AGENT-CONTRACT.md §The
// SessionStart block): every failure path here — an unknown subcommand, an
// unreadable payload, no daemon socket, a daemon error — prints nothing to
// stdout, writes at most one line to stderr, and still exits 0.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "backstory hook: usage: backstory hook session-start|post-tool-use")
		return 0
	}
	switch args[0] {
	case "session-start":
		return runSessionStartHook(stdin, stdout, stderr)
	case "post-tool-use":
		return runPostToolUseHook(stdin, stderr)
	default:
		_, _ = fmt.Fprintln(stderr, "backstory hook: usage: backstory hook session-start|post-tool-use")
		return 0
	}
}

func runSessionStartHook(stdin io.Reader, stdout, stderr io.Writer) int {
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
	resp, err := callDaemon(harnessSessionID, mcp.DaemonMethodBlock, nil)
	if err != nil {
		return "", err
	}
	var result mcp.BlockResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("decode block result: %w", err)
	}
	return result.Block, nil
}

// callDaemon dials the daemon socket and sends one DaemonRequest line,
// declaring harnessSessionID as the "session" join key (socket.
// DeclaredFields) exactly like `backstory mcp` and requestBlock do. The
// daemon resolver's own SO_PEERCRED + /proc walk decides identity, tier and
// project from the connection alone — nothing sent here can change any of
// them.
func callDaemon(harnessSessionID, method string, params any) (mcp.DaemonResponse, error) {
	sockPath, err := socketPath()
	if err != nil {
		return mcp.DaemonResponse{}, err
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return mcp.DaemonResponse{}, fmt.Errorf("dial daemon socket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	req := mcp.DaemonRequest{Session: harnessSessionID, Method: method}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return mcp.DaemonResponse{}, fmt.Errorf("marshal %s params: %w", method, err)
		}
		req.Params = p
	}
	b, err := json.Marshal(req)
	if err != nil {
		return mcp.DaemonResponse{}, fmt.Errorf("marshal %s request: %w", method, err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return mcp.DaemonResponse{}, fmt.Errorf("write daemon request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return mcp.DaemonResponse{}, fmt.Errorf("read daemon response: %w", err)
	}
	var resp mcp.DaemonResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return mcp.DaemonResponse{}, fmt.Errorf("decode daemon response: %w", err)
	}
	if resp.Error != nil {
		return mcp.DaemonResponse{}, fmt.Errorf("daemon: %s", resp.Error.Message)
	}
	return resp, nil
}

// postToolUsePayload is a Claude Code PostToolUse hook's JSON payload.
// CWD, TranscriptPath and HookEventName are accepted so a real harness
// payload decodes cleanly, even though nothing here reads them: the
// connection's own SO_PEERCRED + /proc identity decides the event's
// project, never anything the payload declares (AGENT-CONTRACT.md §Observed
// identity), and ToolUseID is the join key backfill's own dedup needs, not
// an identity field.
type postToolUsePayload struct {
	SessionID      string          `json:"session_id"`
	CWD            string          `json:"cwd"`
	TranscriptPath string          `json:"transcript_path"`
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
}

// postToolUseFileTools is the tool_input field name PostToolUse extracts a
// path from, for tools that carry one. It mirrors internal/backfill/claude/
// tools.go's fileTools set (payload.MutatingFileTools plus Read) so a tool
// use captured live and the same tool use later backfilled from a
// transcript agree on which tools get a Path: two independent definitions
// of that set disagreeing is exactly task 8ba5487a's class of bug.
var postToolUseFileTools = buildPostToolUseFileTools()

func buildPostToolUseFileTools() map[string]bool {
	m := map[string]bool{"Read": true}
	for name := range payload.MutatingFileTools {
		m[name] = true
	}
	return m
}

// toolResponseExit is a PostToolUse payload's tool_response.exit_code field,
// present only for a Bash tool call the harness itself observed exit (the
// exact field name Claude Code's hook JSON uses). ExitCode is a pointer so a
// response that omits it is distinguishable from one that observed a real
// 0 — the SCHEMA.md rule this hook must never violate: "a writer must never
// invent 0".
type toolResponseExit struct {
	ExitCode *int `json:"exit_code"`
}

// inputStringField extracts field from a tool_use block's raw tool_input
// object, mirroring internal/backfill/claude/tools.go's own helper of the
// same name. It returns "" for a missing/malformed/not-a-string field —
// never an error, since a hook must never fail the tool call it is
// reporting on.
func inputStringField(input json.RawMessage, field string) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	v, _ := m[field].(string)
	return v
}

// buildPostToolUseParams normalises one PostToolUse payload into the wire
// shape callDaemon sends: Path for a file-editing tool (or Read), Command
// for Bash, Exit only when the payload's tool_response actually carried
// exit_code. A tool outside both groups (Grep, Glob, WebFetch, ...) gets
// neither Path nor Command — it still becomes a tool.use event server-side,
// exactly like backfill's own toolUsePayload.
func buildPostToolUseParams(p postToolUsePayload) mcp.PostToolUseParams {
	params := mcp.PostToolUseParams{ToolUseID: p.ToolUseID, ToolName: p.ToolName}
	switch {
	case postToolUseFileTools[p.ToolName]:
		params.Path = inputStringField(p.ToolInput, "file_path")
	case p.ToolName == "Bash":
		params.Command = inputStringField(p.ToolInput, "command")
		var tr toolResponseExit
		if len(p.ToolResponse) > 0 && json.Unmarshal(p.ToolResponse, &tr) == nil {
			params.Exit = tr.ExitCode
		}
	}
	return params
}

// runPostToolUseHook is `backstory hook post-tool-use`. It never prints to
// stdout: a PostToolUse hook's stdout is not injected as context the way
// SessionStart's is, so there is nothing to render. Every failure path —
// capture-off, no daemon socket, a decode or daemon error — writes at most
// one line to stderr and exits 0 (a hook must never break a harness boot,
// AGENT-CONTRACT.md §The SessionStart block); with the capture-off flag file
// present it returns before decoding stdin at all, so a slow or malformed
// payload can never delay it.
func runPostToolUseHook(stdin io.Reader, stderr io.Writer) int {
	if off, err := captureOff(); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook: check capture-off flag:", err)
		return 0
	} else if off {
		return 0
	}

	var payload postToolUsePayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook: decode post-tool-use payload:", err)
		return 0
	}
	if payload.ToolName == "" {
		_, _ = fmt.Fprintln(stderr, "backstory hook: post-tool-use payload missing tool_name")
		return 0
	}

	params := buildPostToolUseParams(payload)
	if _, err := callDaemon(payload.SessionID, mcp.DaemonMethodPostToolUse, params); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook:", err)
		return 0
	}
	return 0
}

// captureOff reports whether the capture-off flag file
// (AGENT-CONTRACT.md §User-only powers, the "omarchy toggle"-style
// precedent) is present. Any error other than the file's absence is
// reported to the caller rather than silently treated as "capture is on" —
// a permissions problem on the runtime dir should be visible, not silently
// swallowed into "record everything".
func captureOff() (bool, error) {
	path, err := captureOffPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
