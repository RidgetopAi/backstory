package claude

import (
	"encoding/json"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// fileTools are tool_use blocks whose input carries a file path: the
// importer extracts it into payload.ToolUse.Path. A tool not in this set
// and not Bash (Grep, Glob, WebFetch, ...) still gets a tool.use event; it
// just carries neither Path nor Command.
var fileTools = map[string]bool{
	"Edit":      true,
	"Write":     true,
	"Read":      true,
	"MultiEdit": true,
}

// toolUsePayload builds one tool_use block's shared payload: Path for the
// file-editing tools (extracted from their input's file_path field),
// Command for Bash (from its input's command field).
func toolUsePayload(id, name string, input json.RawMessage) payload.ToolUse {
	p := payload.ToolUse{ToolUseID: id, Name: name}
	switch {
	case fileTools[name]:
		p.Path = inputStringField(input, "file_path")
	case name == "Bash":
		p.Command = inputStringField(input, "command")
	}
	return p
}

// inputStringField extracts field from a tool_use block's raw Input
// object. It returns "" for a missing/malformed/not-a-string field — never
// an error, since a missing detail must not skip the line.
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
