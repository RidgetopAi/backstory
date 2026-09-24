package mcp

import "encoding/json"

// ContentBlock is one block of a CallToolResult.content array (MCP spec
// 2025-06-18 §Tools). This shim only ever produces "text" blocks: every
// tool payload, success or failure, is carried as JSON or plain text for a
// client to render.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CallToolResult is the spec shape every tools/call response's `result`
// must have: Claude Code renders Content and ignores a bare payload object,
// which is why sending the daemon's raw JSON as `result` shows up as an
// empty tool body (this punch's GOAL). StructuredContent mirrors Content's
// text as a parsed object for a client that wants it directly.
// IsError marks a tool-level failure — the daemon reached and rejected the
// call, e.g. a missing required field — as opposed to a JSON-RPC protocol
// error (unknown tool, unsupported method): the MCP spec puts a tool
// failure's message in Content with IsError:true precisely so an agent
// sees it, rather than a transport-level error it can't act on the same
// way.
type CallToolResult struct {
	Content           []ContentBlock  `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// successToolResult wraps a tool's successful JSON payload as a
// CallToolResult: one text block carrying the JSON (so it always renders
// as non-empty text) plus the same payload as StructuredContent.
func successToolResult(payload json.RawMessage) CallToolResult {
	return CallToolResult{
		Content:           []ContentBlock{{Type: "text", Text: string(payload)}},
		StructuredContent: payload,
	}
}

// errorToolResult wraps a tool-level failure message as a CallToolResult
// with IsError set, per the MCP spec's "report tool errors inside the
// result" rule.
func errorToolResult(message string) CallToolResult {
	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: message}},
		IsError: true,
	}
}
