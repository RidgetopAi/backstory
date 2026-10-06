package mcp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// DaemonMethodShellEmit is `backstory shell emit`'s daemon-side method (task
// 7fe84ffb, PLAN.md §Phase 3: "Bash preexec/precmd integration emitting
// {cmd, cwd, exit, duration}"). Like DaemonMethodBlock and
// DaemonMethodPostToolUse it is exported because cmd/backstory's `shell
// emit` subcommand builds a DaemonRequest for it directly, bypassing the mcp
// shim's JSON-RPC tools/call indirection entirely — it is not one of the
// five frozen v0 tools either.
const DaemonMethodShellEmit = "shell_emit"

// shellSource is every event this method writes' timeline_events.source
// (SCHEMA.md's "shell" enum value, store/events.go's Event.Source), never
// "posttooluse" or "backfill" — a command observed live via the shell
// integration must stay distinguishable from a PostToolUse Bash capture and
// from a replayed transcript.
const shellSource = "shell"

// ShellEmitParams is shell_emit's argument shape: exactly what `backstory
// shell emit`'s flags carry, and nothing else. There is deliberately no
// session, harness, or project field here — this connection's own sessionID
// (bound at connect time to the caller's SO_PEERCRED + /proc identity,
// AGENT-CONTRACT.md §Observed identity) is the only thing that ever decides
// which project this event lands in, exactly like post_tool_use and note. A
// hostile or buggy `backstory shell emit` invocation could still claim any
// Cmd/CWD/Exit/DurationMS it likes, but it can never redirect where the
// resulting event is attributed.
type ShellEmitParams struct {
	Cmd        string `json:"cmd"`
	CWD        string `json:"cwd,omitempty"`
	Exit       int    `json:"exit"`
	DurationMS int    `json:"duration_ms"`
}

// ShellEmitResult is shell_emit's return value: how many timeline events
// this call recorded (always 1 today — a shell command is one event, unlike
// post_tool_use's Bash case, which can record two). `backstory shell emit`
// never reads it (the bash snippet backgrounds and discards the call's own
// exit status and output), but a typed result keeps this method consistent
// with the others.
type ShellEmitResult struct {
	EventsRecorded int `json:"events_recorded"`
}

// handleShellEmit records one observed shell command as a timeline event on
// sessionID — the connection's own daemon-minted session, never anything a
// request line claims (DONE WHEN clause 5). captureOff is checked before
// anything else, exactly like handleNote and handlePostToolUse (task
// 9c62f9dc, SCHEMA.md invariant 8: "honoured on every write path") — the
// bash snippet never checks it client-side at all, relying entirely on the
// daemon refusing the write here (DONE WHEN clause 4).
//
// A command beginning with a space is never stored either, checked HERE
// rather than trusted to the snippet's own HISTCONTROL=ignorespace
// convention: shell_emit is reachable from any socket peer, exactly like
// note and post_tool_use, so a caller that is not Backstory's own bash
// snippet (or one that bypassed it) could otherwise still record one. This
// is a quiet skip (EventsRecorded: 0, no error) rather than invalid-params:
// ignorespace is a recording convention the command's own author opted
// into, not a malformed request.
func handleShellEmit(st *store.Store, sessionID, observedCWD string, raw json.RawMessage, captureOff func() (bool, error)) DaemonResponse {
	if off, err := captureOff(); err != nil {
		return errResponse("internal", err.Error())
	} else if off {
		return errResponse("capture-off", "capture is paused; no events are written")
	}

	var p ShellEmitParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResponse("invalid-params", "invalid shell_emit params: "+err.Error())
	}
	if p.Cmd == "" {
		return errResponse("invalid-params", `missing required field "cmd"`)
	}
	if strings.HasPrefix(p.Cmd, " ") {
		result, err := json.Marshal(ShellEmitResult{EventsRecorded: 0})
		if err != nil {
			return errResponse("internal", err.Error())
		}
		return DaemonResponse{Result: result}
	}

	// The claimed cwd is never trusted: the event records the emit process's
	// own /proc cwd (id.CWD), or nothing when that could not be observed.
	sc := payload.ShellCommand{Cmd: p.Cmd, CWD: observedCWD, Exit: p.Exit, DurationMS: p.DurationMS}
	scBytes, err := json.Marshal(sc)
	if err != nil {
		return errResponse("internal", err.Error())
	}
	if _, err := st.AppendEvent(store.Event{
		TS: time.Now(), Kind: payload.KindShellCommand, SessionID: sessionID,
		Source: shellSource, Payload: string(scBytes),
	}); err != nil {
		return errResponse("internal", err.Error())
	}

	result, err := json.Marshal(ShellEmitResult{EventsRecorded: 1})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: result}
}
