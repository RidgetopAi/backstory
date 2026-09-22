package claude

import (
	"encoding/json"
	"time"
)

// transcriptLine is one JSON object from a ~/.claude/projects/<slug>/<sessionId>.jsonl
// line (PLAN.md §Phase 3). Only lines with Type "user" or "assistant" are
// imported; every other Type, and any line that fails to unmarshal into
// this shape at all, is counted as skipped by the caller.
type transcriptLine struct {
	Type       string        `json:"type"`
	UUID       string        `json:"uuid"`
	ParentUUID string        `json:"parentUuid"`
	SessionID  string        `json:"sessionId"`
	CWD        string        `json:"cwd"`
	GitBranch  string        `json:"gitBranch"`
	Version    string        `json:"version"`
	Timestamp  time.Time     `json:"timestamp"`
	IsMeta     bool          `json:"isMeta"`
	Message    *messageField `json:"message"`
}

// messageField is transcriptLine.Message. Content is deferred as raw JSON
// because it is either a bare string (a plain-text turn) or an array of
// contentBlock objects (text/thinking/tool_use/tool_result) — see
// parseContentBlocks.
type messageField struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// contentBlock is one element of an assistant or user message.content
// array. The fields present depend on Type: text blocks carry Text;
// tool_use blocks carry ID/Name/Input; tool_result blocks carry
// ToolUseID/Content/IsError.
type contentBlock struct {
	Type string `json:"type"`

	// type == "text" | "thinking"
	Text string `json:"text"`

	// type == "tool_use"
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// type == "tool_result"
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// parseLine unmarshals one JSONL line into a transcriptLine. A parse error
// here is what the caller counts as a malformed (skipped) line.
func parseLine(raw []byte) (transcriptLine, error) {
	var l transcriptLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return transcriptLine{}, err
	}
	return l, nil
}

// contentBlocks normalises a message's Content into a slice of blocks: a
// bare JSON string becomes a single synthetic {type: "text"} block, so
// callers never need to branch on the two shapes transcripts use.
func contentBlocks(raw json.RawMessage) ([]contentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []contentBlock{{Type: "text", Text: s}}, nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// blockText renders a tool_result block's Content (itself either a bare
// string or an array of {type, text} blocks) down to plain text for
// storage. An unrecognised shape falls back to the raw JSON so no content
// is silently dropped.
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		out := ""
		for i, b := range blocks {
			if i > 0 {
				out += "\n"
			}
			out += b.Text
		}
		return out
	}
	return string(raw)
}
