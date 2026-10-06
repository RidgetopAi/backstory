package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

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
// harnessCodex is the `--harness` flag value that switches session-start and
// post-tool-use to the Codex wire shapes (task 03e19dd4, decision 3e14db82):
// SessionStart's stdout wrapped in the hookSpecificOutput envelope Codex's
// hook contract expects instead of Claude's bare block text, and
// post-tool-use's apply_patch/Bash-only mapping instead of Claude's
// every-tool-gets-an-event default. Detected from an explicit flag, never
// guessed from the payload's own shape — the payload is attacker-controlled
// input, exactly like every other field AGENT-CONTRACT.md §Observed identity
// already refuses to trust.
const harnessCodex = "codex"

// A hook must never break a harness boot (AGENT-CONTRACT.md §The
// SessionStart block): every failure path here — an unknown subcommand, an
// unreadable payload, no daemon socket, a daemon error — prints nothing to
// stdout, writes at most one line to stderr, and still exits 0.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "backstory hook: usage: backstory hook session-start|post-tool-use|post-tool-use-failure [--harness codex]")
		return 0
	}
	sub := args[0]
	if sub != "session-start" && sub != "post-tool-use" && sub != "post-tool-use-failure" {
		_, _ = fmt.Fprintln(stderr, "backstory hook: usage: backstory hook session-start|post-tool-use|post-tool-use-failure [--harness codex]")
		return 0
	}

	harness, err := parseHookHarness(args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook:", err)
		return 0
	}

	switch sub {
	case "session-start":
		return runSessionStartHook(stdin, stdout, stderr, harness)
	case "post-tool-use-failure":
		return runPostToolUseFailureHook(stdin, stderr, harness)
	default:
		return runPostToolUseHook(stdin, stderr, harness)
	}
}

// parseHookHarness reads the one flag `backstory hook` accepts after its
// subcommand: --harness, defaulting to "" (Claude's shape, unchanged). Any
// other flag or argument is a usage error rather than silently ignored, so a
// typo (`--harnes codex`) fails loudly on stderr instead of quietly falling
// back to Claude's wire shape for a Codex payload.
func parseHookHarness(args []string) (string, error) {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	harness := fs.String("harness", "", "harness the payload came from (codex switches to the Codex wire shapes)")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("parse flags: %w", err)
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return *harness, nil
}

// sessionStartEnvelope is the JSON stdout shape Codex's SessionStart hook
// contract expects (docs https://learn.chatgpt.com/docs/hooks): the block
// Claude's path prints as bare text wrapped so Codex knows which hook it
// belongs to and where to splice it into context. AdditionalContext carries
// exactly the same rendered block requestBlock returns for the Claude path
// against the same store — the daemon renders one warm block per project,
// never a harness-specific one.
type sessionStartEnvelope struct {
	HookSpecificOutput sessionStartHookSpecificOutput `json:"hookSpecificOutput"`
}

type sessionStartHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

func runSessionStartHook(stdin io.Reader, stdout, stderr io.Writer, harness string) int {
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

	if harness == harnessCodex {
		envelope := sessionStartEnvelope{HookSpecificOutput: sessionStartHookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: block,
		}}
		b, err := json.Marshal(envelope)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory hook: marshal session-start envelope:", err)
			return 0
		}
		_, _ = fmt.Fprintln(stdout, string(b))
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
	// The installer's hook verification sets BACKSTORY_NO_SESSION so it gets
	// an answer without minting a session or project for the build dir. The
	// daemon only honours it on read-only methods; a real agent's hook never
	// has the variable set.
	if os.Getenv(noSessionEnv) != "" {
		req.NoSession = true
	}
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
	// Error and IsInterrupt are the PostToolUseFailure payload's own fields:
	// Claude Code reports a failed tool call with the error text instead of
	// a tool_response.
	Error       string `json:"error"`
	IsInterrupt bool   `json:"is_interrupt"`
}

// failureExitPattern reads the exit code Claude Code prefixes a failed Bash
// call's error text with ("Exit code 1\n<output>").
var failureExitPattern = regexp.MustCompile(`^\s*Exit code (\d+)`)

// buildPostToolUseFailureParams normalises one PostToolUseFailure payload:
// the same Path/Command extraction as a successful call, IsError always set,
// Exit only when the error text states one, and the error text as the output
// the daemon redacts and excerpts exactly like a Bash result's.
func buildPostToolUseFailureParams(p postToolUsePayload) mcp.PostToolUseParams {
	params := buildPostToolUseParams(p)
	params.IsError = true
	params.Interrupted = p.IsInterrupt
	params.Output = payload.ElideMiddle(p.Error, payload.ToolOutputWireMaxRunes)
	if m := failureExitPattern.FindStringSubmatch(p.Error); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			params.Exit = &n
		}
	}
	return params
}

// runPostToolUseFailureHook is `backstory hook post-tool-use-failure`: the
// failure twin of runPostToolUseHook, with the same never-fail guarantees.
// Only Claude reports failures this way; a Codex payload records nothing.
func runPostToolUseFailureHook(stdin io.Reader, stderr io.Writer, harness string) int {
	if off, err := captureOff(); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook: check capture-off flag:", err)
		return 0
	} else if off || harness == harnessCodex {
		return 0
	}
	var payload postToolUsePayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook: decode post-tool-use-failure payload:", err)
		return 0
	}
	if payload.ToolName == "" {
		_, _ = fmt.Fprintln(stderr, "backstory hook: post-tool-use-failure payload missing tool_name")
		return 0
	}
	params := buildPostToolUseFailureParams(payload)
	if _, err := callDaemon(payload.SessionID, mcp.DaemonMethodPostToolUse, params); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook:", err)
	}
	return 0
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
	// Stdout, Stderr and Interrupted are what real Claude Code sends for a
	// Bash call (measured on real transcripts: interrupted, isImage,
	// noOutputExpected, persistedOutputPath, persistedOutputSize, stderr,
	// stdout — and NO exit_code). Output/Interrupted feed the tool.result's
	// outcome (task c9ab6d28).
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	Interrupted bool   `json:"interrupted"`
}

// bashOutput joins a Bash tool_response's stdout and stderr into the one
// output text the daemon redacts and excerpts, cut to the wire cap (head and
// tail kept) so the request line stays small. A response that is a bare
// JSON string (a harness that reports output as text) is taken as the
// output itself.
func bashOutput(raw json.RawMessage, tr toolResponseExit) string {
	out := tr.Stdout
	if tr.Stderr != "" {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += tr.Stderr
	}
	if out == "" {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			out = text
		}
	}
	return payload.ElideMiddle(out, payload.ToolOutputWireMaxRunes)
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

// bashToolNameLiteral is the tool_name both Claude and Codex report for a
// shell command — the one PostToolUse field this task's two harnesses agree
// on, per the original description's "the same JSON shape as Claude's
// hooks". buildPostToolUseParams's Bash branch and runCodexPostToolUseHook's
// dispatch both key off this single constant rather than each repeating the
// "Bash" literal.
const bashToolNameLiteral = "Bash"

// backstoryNoteToolPattern matches Backstory's own note MCP tool however the
// harness prefixes it (mcp__backstory__note for Claude, mcp_backstory_note,
// backstory.note, ...). It requires the backstory server segment, so another
// server's tool that happens to be called note never matches.
var backstoryNoteToolPattern = regexp.MustCompile(`^(?:mcp[_.:-]+)?backstory[_.:-]+note$`)

// noteRecordID extracts the created record's id from a Backstory note call's
// tool_response. The MCP result reaches a hook in one of several shapes: the
// NoteResult object itself, an MCP CallToolResult carrying it as
// structuredContent or as JSON text in a content block, a bare content-block
// array, or a JSON string. It returns "" when no id can be read (a failed
// note call carries none) — never a guess.
func noteRecordID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return noteRecordID(json.RawMessage(text))
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		for _, b := range blocks {
			if id := noteRecordID(json.RawMessage(b.Text)); id != "" {
				return id
			}
		}
		return ""
	}
	var obj struct {
		ID         string          `json:"id"`
		IsError    bool            `json:"isError"`
		Structured json.RawMessage `json:"structuredContent"`
		Content    json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &obj) != nil || obj.IsError {
		return ""
	}
	if obj.ID != "" {
		return obj.ID
	}
	if id := noteRecordID(obj.Structured); id != "" {
		return id
	}
	return noteRecordID(obj.Content)
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
	case p.ToolName == bashToolNameLiteral:
		params.Command = inputStringField(p.ToolInput, "command")
		var tr toolResponseExit
		if len(p.ToolResponse) > 0 && json.Unmarshal(p.ToolResponse, &tr) == nil {
			params.Exit = tr.ExitCode
			params.Interrupted = tr.Interrupted
			params.Output = bashOutput(p.ToolResponse, tr)
		}
	case backstoryNoteToolPattern.MatchString(p.ToolName):
		params.RecordID = noteRecordID(p.ToolResponse)
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
func runPostToolUseHook(stdin io.Reader, stderr io.Writer, harness string) int {
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

	if harness == harnessCodex {
		return runCodexPostToolUseHook(payload, stderr)
	}

	params := buildPostToolUseParams(payload)
	if _, err := callDaemon(payload.SessionID, mcp.DaemonMethodPostToolUse, params); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook:", err)
		return 0
	}
	return 0
}

// codexApplyPatchTool is the tool_name Codex's PostToolUse hook reports for
// every file edit, add, or delete — a single tool covering what Claude
// splits across Edit/Write/MultiEdit/NotebookEdit. Its tool_input carries the
// patch text under the same "command" field Bash's tool_input uses (Codex
// docs https://learn.chatgpt.com/docs/hooks), not a "file_path" field the way
// Claude's file tools do.
const codexApplyPatchTool = "apply_patch"

// applyPatchFileHeaders are the apply_patch patch-text line prefixes that
// each open a new file section, mapped to nothing beyond "this line names a
// touched path" — apply_patch does not distinguish an add from an update
// from a delete in what gets recorded here, exactly like Claude's own
// MutatingFileTools does not distinguish an Edit from a Write. All three are
// scanned by the SAME loop in parseApplyPatchPaths, not three separate
// special-cased branches, specifically so dropping any one of them (e.g. Add
// File) is a one-line regression a mutation test can catch, not three
// independent chances to silently miss one.
var applyPatchFileHeaders = []string{
	"*** Update File: ",
	"*** Add File: ",
	"*** Delete File: ",
}

// parseApplyPatchPaths extracts every file path apply_patch's own patch text
// touches, in the order their header lines appear. A line that matches none
// of applyPatchFileHeaders (a hunk body, a context line, "*** Begin Patch")
// contributes nothing — this never tries to interpret the patch body itself,
// only the header lines that name what it touches.
func parseApplyPatchPaths(patch string) []string {
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		for _, header := range applyPatchFileHeaders {
			if rest, ok := strings.CutPrefix(line, header); ok {
				paths = append(paths, strings.TrimSpace(rest))
				break
			}
		}
	}
	return paths
}

// runCodexPostToolUseHook is post-tool-use's Codex mapping (task 03e19dd4,
// decision 3e14db82): apply_patch becomes one observed file-write event per
// touched path (DONE WHEN clause 2), Bash is recorded exactly like Claude's
// Bash already is (DONE WHEN clause 2, reusing buildPostToolUseParams's own
// Bash branch — Codex's tool_response carries the same exit_code field
// Claude's does), and any other tool_name — an MCP tool, or anything this
// mapping does not yet know — records NOTHING and never dials the daemon at
// all (DONE WHEN clause 3): never guess at a wire shape this hook has not
// been told the meaning of.
func runCodexPostToolUseHook(p postToolUsePayload, stderr io.Writer) int {
	switch p.ToolName {
	case codexApplyPatchTool:
		patch := inputStringField(p.ToolInput, "command")
		for _, path := range parseApplyPatchPaths(patch) {
			params := mcp.PostToolUseParams{ToolUseID: p.ToolUseID, ToolName: p.ToolName, Path: path}
			if _, err := callDaemon(p.SessionID, mcp.DaemonMethodPostToolUse, params); err != nil {
				_, _ = fmt.Fprintln(stderr, "backstory hook:", err)
				return 0
			}
		}
	case bashToolNameLiteral:
		params := buildPostToolUseParams(p)
		if _, err := callDaemon(p.SessionID, mcp.DaemonMethodPostToolUse, params); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory hook:", err)
			return 0
		}
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
