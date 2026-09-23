package mcp

import (
	"encoding/json"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// DaemonMethodPostToolUse is the PostToolUse hook's daemon-side method
// (task 04b1cb40, PLAN.md §Phase 3: "PostToolUse capture for Tier A"). Like
// DaemonMethodBlock it is exported because cmd/backstory's `hook
// post-tool-use` subcommand builds a DaemonRequest for it directly,
// bypassing the mcp shim's JSON-RPC tools/call indirection entirely — it is
// not one of the five frozen v0 tools either.
const DaemonMethodPostToolUse = "post_tool_use"

// postToolUseSource is every event this method writes' timeline_events.source
// (store/events.go's Event.Source enum: "posttooluse"), never
// claude.EventSource ("backfill") — the two must stay distinguishable so a
// project's delta can tell live-observed history from a replayed transcript,
// and so backfill's own dedup (internal/backfill/claude's appendToolEvents)
// has something live-captured to find.
const postToolUseSource = "posttooluse"

// bashToolName is the one tool_name PostToolUseParams.Exit applies to: a
// command's exit code is a Bash concept, not a file-tool one.
const bashToolName = "Bash"

// PostToolUseParams is post_tool_use's argument shape. It carries only what
// cmd/backstory/hook.go already extracted from the tool's own tool_input and
// tool_response — never a cwd, project, or session field: this connection's
// sessionID (bound at connect time to the caller's SO_PEERCRED + /proc
// identity, AGENT-CONTRACT.md §Observed identity) is the only thing that
// ever decides which project an event lands in, exactly like note and block.
// A hostile or buggy payload could still send an about-face ToolName/Path/
// Command, but it can never redirect where the resulting event is attributed.
type PostToolUseParams struct {
	ToolUseID string `json:"tool_use_id,omitempty"`
	ToolName  string `json:"tool_name"`
	Path      string `json:"path,omitempty"`
	Command   string `json:"command,omitempty"`
	// Exit is nil unless the PostToolUse payload's tool_response carried a
	// real exit code (SCHEMA.md: a writer must never invent 0 — Phase 3 live
	// capture is the writer that comment already anticipated).
	Exit *int `json:"exit,omitempty"`
}

// PostToolUseResult is post_tool_use's return value: how many timeline
// events this call recorded (1 for a file tool, up to 2 for Bash — tool.use
// plus tool.result). cmd/backstory's hook subcommand never reads it — a
// PostToolUse hook's stdout is not injected as context the way SessionStart's
// is — but a typed result (rather than an empty {}) keeps this method
// consistent with note/status/recall/block, and gives a future caller
// (status, a panel) something to read without a wire change.
type PostToolUseResult struct {
	EventsRecorded int `json:"events_recorded"`
}

// handlePostToolUse records one PostToolUse hook invocation as observed
// timeline events on sessionID — the connection's own daemon-minted session,
// never anything a request line claims. A file-editing tool (or any tool
// outside the Bash/file-tool groups) gets exactly one tool.use event
// (payload.ToolUse, DONE WHEN clause 1: "exactly one new timeline event").
// Bash additionally gets one tool.result event carrying Exit exactly as
// p.Exit was observed — nil stays nil, never coerced to 0 (DONE WHEN clause
// 2).
func handlePostToolUse(st *store.Store, sessionID string, raw json.RawMessage) DaemonResponse {
	var p PostToolUseParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResponse("invalid-params", "invalid post_tool_use params: "+err.Error())
	}
	if p.ToolName == "" {
		return errResponse("invalid-params", `missing required field "tool_name"`)
	}

	n := 0
	now := time.Now()

	tu := payload.ToolUse{ToolUseID: p.ToolUseID, Name: p.ToolName, Path: p.Path, Command: p.Command}
	tuBytes, err := json.Marshal(tu)
	if err != nil {
		return errResponse("internal", err.Error())
	}
	if _, err := st.AppendEvent(store.Event{
		TS: now, Kind: payload.KindToolUse, SessionID: sessionID,
		Source: postToolUseSource, Payload: string(tuBytes),
	}); err != nil {
		return errResponse("internal", err.Error())
	}
	n++

	if p.ToolName == bashToolName {
		tr := payload.ToolResult{ToolUseID: p.ToolUseID, Exit: p.Exit}
		trBytes, err := json.Marshal(tr)
		if err != nil {
			return errResponse("internal", err.Error())
		}
		if _, err := st.AppendEvent(store.Event{
			TS: now, Kind: payload.KindToolResult, SessionID: sessionID,
			Source: postToolUseSource, Payload: string(trBytes),
		}); err != nil {
			return errResponse("internal", err.Error())
		}
		n++
	}

	result, err := json.Marshal(PostToolUseResult{EventsRecorded: n})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: result}
}
