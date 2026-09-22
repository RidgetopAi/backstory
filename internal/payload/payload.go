// Package payload defines the typed JSON shapes for every timeline_events
// row Backstory's daemon writes: session.start, session.end, tool.use, and
// tool.result. It is the one contract every writer marshals against and
// every reader unmarshals against (task 8ba5487a): before this package
// existed, internal/backfill/claude wrote a tool.use file path under the
// key `detail` while internal/block's delta slot read `path` — two private
// json-tagged structs, silently disagreeing, so a project with 3,323 tool
// events rendered "0 files touched". The same class of bug also affected
// session.start's branch: the backfill wrote `git_branch`, the block's
// coordination slot read `branch`.
package payload

// Event kinds: the timeline_events.kind value each payload type below
// belongs to (SCHEMA.md "Event kinds enum" is free text in v0; these are
// the values every writer/reader in this codebase actually uses).
const (
	KindSessionStart = "session.start"
	KindSessionEnd   = "session.end"
	KindToolUse      = "tool.use"
	KindToolResult   = "tool.result"
)

// SessionStart is the session.start event payload: the session's opening
// prompt plus the harness version and git branch observed at start.
type SessionStart struct {
	Prompt    string `json:"prompt,omitempty"`
	Version   string `json:"version,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
}

// SessionEnd is the session.end event payload.
type SessionEnd struct {
	Reason string `json:"reason,omitempty"`
}

// ToolUse is the tool.use event payload: Path is set for the file-editing
// tools (Edit/Write/Read/MultiEdit/NotebookEdit), Command is set for Bash. A
// tool outside both groups (Grep, Glob, WebFetch, ...) still gets a
// tool.use event; it carries neither.
type ToolUse struct {
	ToolUseID string `json:"tool_use_id,omitempty"`
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Command   string `json:"command,omitempty"`
}

// MutatingFileTools are the tool_use names whose Path names a file they
// changed on disk. Read also populates Path (it is a real, first-class
// event — the This Week view may want it later) but does not belong here:
// it observed the file, it did not change it. This is the ONE definition
// of "changed a file" in this codebase (task 393d174c): the SessionStart
// delta's "files touched" figure counts only these, never every tool that
// merely carries a path — on Brian's real history that distinction was 105
// files changed versus 411 paths opened, a 3.9x overstatement.
var MutatingFileTools = map[string]bool{
	"Edit":         true,
	"Write":        true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

// IsMutatingFileTool reports whether name is a tool that changes a file's
// contents on disk, per MutatingFileTools.
func IsMutatingFileTool(name string) bool {
	return MutatingFileTools[name]
}

// ToolResult is the tool.result event payload. Exit is nil unless the
// writer observed a real process exit code: a Bash tool_result replayed
// from a Claude transcript never carries one (Phase 3 live PostToolUse
// capture will), so a writer must never invent 0 — a set-but-zero Exit and
// an unset Exit are distinguishable on purpose.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	Exit      *int   `json:"exit,omitempty"`
	Content   string `json:"content,omitempty"`
}
