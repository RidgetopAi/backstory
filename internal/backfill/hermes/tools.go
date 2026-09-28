package hermes

import (
	"encoding/json"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// toolUsePayload builds one tool_calls entry's tool.use payload: Path when
// its arguments carry a "path" or "file_path" field, Command when they
// carry a "command" field. Hermes's own tool-argument shape is not fixed by
// the punch, so this looks for the field names generically rather than
// branching on specific tool names the way the Claude/Codex importers do on
// their own harnesses' fixed tool sets — a tool call outside both still gets
// a tool.use event; it just carries neither.
func toolUsePayload(call toolCallJSON) payload.ToolUse {
	p := payload.ToolUse{ToolUseID: call.ID, Name: call.Name}
	p.Path = argumentField(call.Arguments, "path", "file_path")
	p.Command = argumentField(call.Arguments, "command")
	return p
}

// argumentField returns the first of keys that call's Arguments object
// carries as a non-empty string, or "" if none match or Arguments is not a
// JSON object — never an error, since a missing detail must not skip the
// tool.use event itself.
func argumentField(arguments json.RawMessage, keys ...string) string {
	if len(arguments) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(arguments, &m); err != nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
