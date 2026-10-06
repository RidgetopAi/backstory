package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
)

// TestHookPostToolUseFailureBashRecordsErroredRedactedToolResult is task
// b2613e7a's clause 2: a PostToolUseFailure payload shaped like Claude Code's
// documented one (tool_input, error text "Exit code N\n<output>",
// is_interrupt) goes through `backstory hook post-tool-use-failure` and lands
// as a tool.result with is_error=true, the parsed exit, and the redacted
// error excerpt.
func TestHookPostToolUseFailureBashRecordsErroredRedactedToolResult(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()

	secret := "AKIAIOSFODNN7EXAMPLE" //nolint:gosec // fake AWS key fixture the redactor must catch
	stdout, stderr, exitCode, _ := runHookInDir(t, bin, projectDir, env, []string{"post-tool-use-failure"}, map[string]any{
		"session_id":      "claude-failure-session",
		"cwd":             projectDir,
		"hook_event_name": "PostToolUseFailure",
		"tool_name":       "Bash",
		"tool_use_id":     "toolu_fail_1",
		"tool_input":      map[string]any{"command": "python -m unittest"},
		"error":           "Exit code 1\nFAIL: test_x (tests.T)\nAssertionError: key=" + secret,
		"is_interrupt":    false,
	})
	if exitCode != 0 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want exit 0 and empty stdout", exitCode, stdout, stderr)
	}

	s := mustOpenTestStore(t, dbPath)
	events := queryEventsForProject(t, s, project.Key(projectDir, project.RealGit{}, nil))
	var got *payload.ToolResult
	for _, ev := range events {
		if ev.Kind != payload.KindToolResult {
			continue
		}
		var tr payload.ToolResult
		if err := json.Unmarshal([]byte(ev.Payload), &tr); err != nil {
			t.Fatalf("unmarshal %q: %v", ev.Payload, err)
		}
		got = &tr
	}
	if got == nil {
		t.Fatalf("no tool.result recorded; events: %#v", events)
	}
	if !got.IsError || got.ToolUseID != "toolu_fail_1" {
		t.Errorf("tool.result = %+v, want is_error=true for toolu_fail_1", got)
	}
	if got.Exit == nil || *got.Exit != 1 {
		t.Errorf("exit = %v, want 1 parsed from the error text", got.Exit)
	}
	if !strings.Contains(got.Content, "AssertionError") || strings.Contains(got.Content, secret) {
		t.Errorf("content = %q, want the error excerpt with the secret redacted", got.Content)
	}
}
