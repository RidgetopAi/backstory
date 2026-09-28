package codex

import (
	"encoding/json"
	"strings"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// toolUsePayload builds one response_item's tool.use payload. Command is
// populated for the "shell" function_call (its Arguments is a JSON object
// carrying the argv to run) — a freeform custom_tool_call such as
// apply_patch carries its body as a raw string Input rather than structured
// Arguments, so it gets Name and ToolUseID only; no importer here needs to
// parse a patch body to extract a path.
func toolUsePayload(item responseItem) payload.ToolUse {
	p := payload.ToolUse{ToolUseID: item.CallID, Name: item.Name}
	if item.Type == "function_call" && item.Name == "shell" {
		p.Command = shellCommand(item.Arguments)
	}
	return p
}

// shellCommand extracts the argv a "shell" function_call's Arguments
// carries (a JSON-encoded {"command": [...]}, the same shape codex-cli
// sends today) and joins it into a display string. Returns "" for anything
// that doesn't parse — never an error, since a missing detail must not skip
// the tool.use event itself.
func shellCommand(arguments string) string {
	if arguments == "" {
		return ""
	}
	var args struct {
		Command []string `json:"command"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return ""
	}
	return strings.Join(args.Command, " ")
}
