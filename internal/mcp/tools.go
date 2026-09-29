// Package mcp is the Backstory MCP stdio shim: a hand-rolled JSON-RPC 2.0
// server over stdin/stdout that speaks the five frozen v0 tools
// (AGENT-CONTRACT.md §The five tools) and forwards note/status to the
// daemon over its unix socket. No headers, no ids, no declared identity: the
// shim never sends an Identity of its own (AGENT-CONTRACT.md §Observed
// identity), it only relays tool arguments.
package mcp

import "encoding/json"

// Tool names, frozen at v0. No `discover`; the descriptions carry the
// contract (AGENT-CONTRACT.md §The five tools).
const (
	ToolRecall   = "recall"
	ToolNote     = "note"
	ToolTimeline = "timeline"
	ToolConfirm  = "confirm"
	ToolStatus   = "status"
)

// Tool is one entry of a tools/list response: a name, a human description,
// and a JSON Schema for its arguments.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ToolsV0 returns the five tools frozen at v0, in this fixed order. The
// shape is snapshot-tested byte-for-byte against testdata/tools-v0.json
// (schema_test.go): removing a field or changing a field's type is a
// breaking change; adding a new optional field is not
// (AGENT-CONTRACT.md §The five tools — "additive-only thereafter").
func ToolsV0() []Tool {
	return []Tool{
		{
			Name: ToolRecall,
			Description: "Anchor (project, path, ref, id, or free text) to an ordered, " +
				"trust-annotated narrative at an altitude, under a token budget.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {
						"type": "string",
						"description": "free text, a path, a ref, or a record id — the recall anchor"
					},
					"project": {
						"type": "string",
						"description": "project key override; defaults to the caller's own project"
					},
					"altitude": {
						"type": "string",
						"enum": ["summary", "detail"],
						"description": "narrative altitude"
					},
					"budget_tokens": {
						"type": "integer",
						"description": "token budget for the returned narrative"
					}
				},
				"required": []
			}`),
		},
		{
			Name:        ToolNote,
			Description: "The one write. Returns the record id and its provenance tier.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"kind": {
						"type": "string",
						"enum": ["decision", "outcome", "handoff", "note", "claim"]
					},
					"text": {
						"type": "string"
					},
					"about": {
						"type": "array",
						"items": {"type": "string"},
						"description": "paths this record is about"
					},
					"supersedes": {
						"type": "string",
						"description": "id of the record this one supersedes; set it whenever this record replaces or corrects an earlier one"
					},
					"evidence": {
						"type": "array",
						"items": {"type": "integer"},
						"description": "timeline_events ids cited as evidence"
					},
					"links": {
						"type": "array",
						"items": {"type": "string"}
					},
					"expires": {
						"type": "string",
						"description": "RFC 3339 timestamp; claims only"
					}
				},
				"required": ["kind", "text"]
			}`),
		},
		{
			Name: ToolTimeline,
			Description: "Events for my session / this project / since <t>, filtered — the " +
				"observed truth an agent cites as evidence.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"scope": {
						"type": "string",
						"enum": ["session", "project"]
					},
					"since": {
						"type": "string",
						"description": "RFC 3339 timestamp"
					},
					"kind": {
						"type": "string",
						"description": "timeline_events.kind filter"
					},
					"limit": {
						"type": "integer"
					}
				},
				"required": []
			}`),
		},
		{
			Name: ToolConfirm,
			Description: "Promote a draft, flag a contradiction with evidence, or mark " +
				"supersession — kept separate from note so the never-list is enforceable per tool.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"record_id": {
						"type": "string"
					},
					"action": {
						"type": "string",
						"enum": ["promote", "contradict", "supersede", "affirm"]
					},
					"evidence": {
						"type": "array",
						"items": {"type": "integer"},
						"description": "timeline_events ids cited as evidence"
					},
					"text": {
						"type": "string",
						"description": "optional explanation"
					}
				},
				"required": ["record_id", "action"]
			}`),
		},
		{
			Name: ToolStatus,
			Description: "Who I am (session, project as the daemon sees them), who else is " +
				"live here, my budget, capture on/off.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {},
				"required": []
			}`),
		},
	}
}
