package claude

import "encoding/json"

// toolDetailField names, per tool, which field of its tool_use.input the
// importer extracts as the event's human-legible detail: a file path for
// the file-editing tools, the shell command for Bash. A tool not in this
// table (e.g. Grep, Glob, WebFetch) still gets a tool.use event; it just
// carries no extracted detail.
var toolDetailField = map[string]string{
	"Edit":      "file_path",
	"Write":     "file_path",
	"Read":      "file_path",
	"MultiEdit": "file_path",
	"Bash":      "command",
}

// toolDetail extracts name's detail string from a tool_use block's raw
// Input object via toolDetailField. It returns "" for a tool not in the
// table, or whose input is missing/malformed/not-a-string for the expected
// field — never an error, since a missing detail must not skip the line.
func toolDetail(name string, input json.RawMessage) string {
	field, ok := toolDetailField[name]
	if !ok || len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	v, _ := m[field].(string)
	return v
}
