package codex

import (
	"encoding/json"
	"time"
)

// rolloutLine is one JSON object from a
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl line (decision
// 3e14db82, format measured on the desk against codex-cli 0.151.0). Payload
// is deferred as raw JSON because its shape depends on Type: session_meta,
// turn_context, response_item and event_msg each carry a different set of
// fields (see sessionMetaPayload, turnContextPayload, responseItem).
type rolloutLine struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Ordinal   int64           `json:"ordinal"`
	Payload   json.RawMessage `json:"payload"`
}

// sessionMetaPayload is a rolloutLine's payload when Type == "session_meta":
// the session's own identity. ParentThreadID is set only on a subagent
// rollout — another rollout's own session_meta.ID — and is how that
// subagent's session links back to its parent (task e9cb97dd). ModelProvider
// is read but never branched on: an unknown/local provider must import
// identically to a known one (DONE WHEN clause 4).
type sessionMetaPayload struct {
	ID             string `json:"id"`
	CWD            string `json:"cwd"`
	CLIVersion     string `json:"cli_version"`
	ModelProvider  string `json:"model_provider"`
	ParentThreadID string `json:"parent_thread_id"`
	Source         string `json:"source"`
}

// turnContextPayload is a rolloutLine's payload when Type == "turn_context".
// Only CWD is used, as a fallback when no session_meta line in the file
// carries one.
type turnContextPayload struct {
	CWD    string `json:"cwd"`
	Model  string `json:"model"`
	TurnID string `json:"turn_id"`
}

// responseItem is a rolloutLine's payload when Type == "response_item": the
// fields populated depend on its own Type — "message" (Role, Content),
// "function_call" (Name, Arguments, CallID), "function_call_output"
// (CallID, Output), "custom_tool_call" (Name, Input, CallID) — a freeform
// tool such as apply_patch, whose body is a raw string rather than JSON
// Arguments — and "custom_tool_call_output" (CallID, Output).
type responseItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	CallID    string          `json:"call_id"`
	Output    string          `json:"output"`
	Input     string          `json:"input"`
}

// contentBlock is one element of a message response_item's Content array:
// "input_text" for a user turn, "output_text" for an assistant turn, each
// carrying Text.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// parseLine unmarshals one JSONL line into a rolloutLine. A parse error here
// is what the caller counts as a malformed (skipped) line.
func parseLine(raw []byte) (rolloutLine, error) {
	var l rolloutLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return rolloutLine{}, err
	}
	return l, nil
}

func parseSessionMeta(raw json.RawMessage) (sessionMetaPayload, error) {
	var m sessionMetaPayload
	if err := json.Unmarshal(raw, &m); err != nil {
		return sessionMetaPayload{}, err
	}
	return m, nil
}

func parseTurnContext(raw json.RawMessage) (turnContextPayload, error) {
	var t turnContextPayload
	if err := json.Unmarshal(raw, &t); err != nil {
		return turnContextPayload{}, err
	}
	return t, nil
}

func parseResponseItem(raw json.RawMessage) (responseItem, error) {
	var it responseItem
	if err := json.Unmarshal(raw, &it); err != nil {
		return responseItem{}, err
	}
	return it, nil
}

// contentText normalises a message item's Content into plain text: a bare
// JSON string is returned as-is, an array of {type, text} blocks is joined
// in order. An unrecognised shape returns "" rather than guessing.
func contentText(raw json.RawMessage) string {
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
	return ""
}
