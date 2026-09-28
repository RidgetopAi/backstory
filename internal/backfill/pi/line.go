package pi

import (
	"encoding/json"
	"time"
)

// treeLine is one JSON object from a
// ~/.pi/agent/sessions/--<cwd-slug>--/<iso-ts>_<uuid>.jsonl line (Pi 0.86.1
// docs/session-format.md). Unlike Claude's transcript, a Pi transcript is a
// TREE, not a flat log: every line carries ID/ParentID regardless of Type,
// and two lines may share a ParentID (an edited-and-resent turn, or a
// regenerated reply, keeps both branches rather than overwriting one).
//
// Content is deferred as raw JSON because its shape depends on Type/Role:
// an assistant message's content is an array of contentBlock objects
// (text/thinking/toolCall); a toolResult line's content is either a bare
// string or an array of {type, text} blocks, the same two shapes
// contentBlocks/blockText already normalise for Claude's tool_result.
type treeLine struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`

	// type == "session"
	CWD string `json:"cwd"`

	// type == "message"
	Role    string          `json:"role"`
	Text    string          `json:"text"`
	Content json.RawMessage `json:"content"`

	// type == "toolResult"
	ToolCallID string `json:"toolCallId"`
	IsError    bool   `json:"isError"`
}

// contentBlock is one element of an assistant message's content array.
// type == "toolCall" carries ID/Name/Arguments; type == "text"/"thinking"
// carries Text. Arguments is never inspected here (task scope: Pi's
// argument shape is not part of this punch's field list — see pi.go's
// toolUsePayload doc), only carried through parseably for a future punch.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`

	// type == "toolCall"
	ID   string `json:"id"`
	Name string `json:"name"`
}

// parseLine unmarshals one JSONL line into a treeLine. A parse error here
// is what the caller counts as a malformed (skipped) line.
func parseLine(raw []byte) (treeLine, error) {
	var l treeLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return treeLine{}, err
	}
	return l, nil
}

// contentBlocks normalises an assistant message's Content into a slice of
// blocks. Empty input (a non-assistant line, or an assistant line with no
// content array) yields no blocks rather than an error.
func contentBlocks(raw json.RawMessage) ([]contentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// blockText renders a toolResult line's Content (either a bare string or an
// array of {type, text} blocks) down to plain text for storage. An
// unrecognised shape falls back to the raw JSON so no content is silently
// dropped — the same rule internal/backfill/claude's blockText applies to
// Claude's tool_result content.
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
