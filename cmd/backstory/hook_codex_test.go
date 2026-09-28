package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
)

// codexSessionStartEnvelope mirrors sessionStartEnvelope (cmd/backstory/hook.go)
// for test-side decoding — a private copy, not a reuse of the production
// type, so this test proves the actual wire JSON shape rather than merely
// round-tripping through the same Go struct the production code marshals.
type codexSessionStartEnvelope struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// TestHookSessionStartCodexEnvelopeMatchesClaudeWarmBlock is the punch's
// DONE WHEN clause 1 (task 03e19dd4): golden Codex SessionStart fixtures —
// one "startup", one "resume" — fed to `backstory hook session-start
// --harness codex` produce stdout whose hookSpecificOutput.hookEventName is
// "SessionStart" and whose additionalContext equals byte-for-byte the same
// warm block `backstory hook session-start` (no --harness, Claude's shape)
// prints for the same store.
//
// "For the same store" is proven by giving the Claude control call and each
// Codex fixture their OWN fresh project directory, all against the one
// daemon started here: every session-start call itself perturbs its
// project's own history the instant it returns (the connection's own
// session gets recorded), so two calls sharing one project would never see
// byte-identical prior state — but a fresh project's first-ever call is
// deterministic content (block.HeaderLine + block.EmptyProjectLine)
// regardless of which project it is, which is exactly the "for the same
// store" the daemon's block renderer promises: the store, not the caller's
// harness, decides the content.
//
// buildBackstoryHarness (a real "claude"-named binary) with startTestDaemon
// run WITHOUT a fake ancestry override is required here, not
// noHarnessAncestry: AnchoredFakeProcFS.Cwd returns the injected hop's own
// declared Cwd field, never the subprocess's real cmd.Dir, so under a fake
// ancestry every call in this test would resolve to the SAME (empty) cwd —
// and thus the same project — regardless of which t.TempDir() each
// subprocess actually ran in, defeating the per-project isolation this test
// depends on. With no override, the daemon's real /proc walk reads each
// subprocess's own real cwd, exactly like hook_posttooluse_test.go's project
// tests already do.
func TestHookSessionStartCodexEnvelopeMatchesClaudeWarmBlock(t *testing.T) {
	bin := buildBackstoryHarness(t)
	_, _, env := startTestDaemon(t, bin)

	claudeProjectDir := t.TempDir()
	claudeStdout, claudeStderr, claudeExit, _ := runHookInDir(t, bin, claudeProjectDir, env,
		[]string{"session-start"}, map[string]any{
			"session_id":      "claude-warm-block-reader",
			"cwd":             claudeProjectDir,
			"transcript_path": filepath.Join(claudeProjectDir, "transcript.jsonl"),
			"source":          "startup",
			"hook_event_name": "SessionStart",
		})
	if claudeExit != 0 {
		t.Fatalf("claude session-start exit code = %d, want 0 (stderr: %s)", claudeExit, claudeStderr)
	}
	wantBlock := strings.TrimSpace(claudeStdout)
	if wantBlock == "" {
		t.Fatalf("claude session-start stdout is empty, want the rendered block")
	}

	for _, tc := range []struct {
		name   string
		source string
	}{
		{"startup", "startup"},
		{"resume", "resume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codexProjectDir := t.TempDir()
			stdout, stderr, exitCode, _ := runHookInDir(t, bin, codexProjectDir, env,
				[]string{"session-start", "--harness", "codex"}, map[string]any{
					"session_id": "codex-" + tc.name + "-session",
					"source":     tc.source,
					"cwd":        codexProjectDir,
					"model":      "gpt-5-codex",
				})
			if exitCode != 0 {
				t.Fatalf("codex session-start exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
			}

			var envelope codexSessionStartEnvelope
			if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope); err != nil {
				t.Fatalf("unmarshal codex session-start stdout %q: %v", stdout, err)
			}
			if envelope.HookSpecificOutput.HookEventName != "SessionStart" {
				t.Errorf("hookSpecificOutput.hookEventName = %q, want %q",
					envelope.HookSpecificOutput.HookEventName, "SessionStart")
			}
			if envelope.HookSpecificOutput.AdditionalContext != wantBlock {
				t.Errorf("hookSpecificOutput.additionalContext = %q, want %q (the Claude path's own warm block for the same store)",
					envelope.HookSpecificOutput.AdditionalContext, wantBlock)
			}
		})
	}
}

// TestHookPostToolUseCodexApplyPatchRecordsFileWriteEventsPerTouchedPath is
// the punch's DONE WHEN clause 2's apply_patch half: a Codex apply_patch
// PostToolUse fixture whose patch text touches two files — one under
// "*** Update File:", one under "*** Add File:" — records exactly two
// tool.use events, one per path, and the SessionStart block then counts both
// as touched (apply_patch joined payload.MutatingFileTools alongside
// Claude's Edit/Write).
func TestHookPostToolUseCodexApplyPatchRecordsFileWriteEventsPerTouchedPath(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()
	updatedPath := filepath.Join(projectDir, "existing.go")
	addedPath := filepath.Join(projectDir, "new.go")

	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: " + updatedPath,
		"@@",
		"-old line",
		"+new line",
		"*** Add File: " + addedPath,
		"+package main",
		"*** End Patch",
		"",
	}, "\n")

	applyPatchPayload := map[string]any{
		"session_id":  "codex-apply-patch-session",
		"cwd":         projectDir,
		"tool_name":   "apply_patch",
		"tool_use_id": "call_apply_patch_1",
		"tool_input": map[string]any{
			"command": patch,
		},
		"tool_response": map[string]any{
			"output": "Success. Updated the following files:\nM " + updatedPath + "\nA " + addedPath,
		},
	}

	stdout, stderr, exitCode, _ := runHookInDir(t, bin, projectDir, env,
		[]string{"post-tool-use", "--harness", "codex"}, applyPatchPayload)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := queryEventsForProject(t, s, projectKey)
	if len(events) != 2 {
		t.Fatalf("project has %d timeline events after one apply_patch touching two files, want exactly 2: %#v", len(events), events)
	}

	var gotPaths []string
	for _, ev := range events {
		if ev.Kind != payload.KindToolUse {
			t.Errorf("event kind = %q, want %q for every apply_patch event", ev.Kind, payload.KindToolUse)
			continue
		}
		if ev.Source != "posttooluse" {
			t.Errorf("event source = %q, want %q (not backfill)", ev.Source, "posttooluse")
		}
		var tu payload.ToolUse
		if err := json.Unmarshal([]byte(ev.Payload), &tu); err != nil {
			t.Fatalf("unmarshal tool.use payload %q: %v", ev.Payload, err)
		}
		if tu.Name != "apply_patch" {
			t.Errorf("tool.use name = %q, want %q", tu.Name, "apply_patch")
		}
		gotPaths = append(gotPaths, tu.Path)
	}
	wantPaths := []string{updatedPath, addedPath}
	if len(gotPaths) != len(wantPaths) || gotPaths[0] != wantPaths[0] || gotPaths[1] != wantPaths[1] {
		t.Errorf("recorded paths = %v, want %v (Update File then Add File, in header order)", gotPaths, wantPaths)
	}

	block, _, blockExit, _ := runHookInDir(t, bin, projectDir, env, []string{"session-start"}, map[string]any{
		"session_id": "codex-apply-patch-block-reader",
		"cwd":        projectDir,
	})
	if blockExit != 0 {
		t.Fatalf("session-start exit code = %d, want 0", blockExit)
	}
	if !strings.Contains(block, "2 files touched") {
		t.Errorf("block = %q, want it to contain %q", block, "2 files touched")
	}
}

// TestHookPostToolUseCodexBashRecordsCommandExitEvent is the punch's DONE
// WHEN clause 2's Bash half: a Codex Bash PostToolUse fixture whose
// tool_response carries exit_code 1 records the command and a tool.result
// event whose exit is exactly 1 (never 0, never nil) — the same mapping
// Claude's own Bash capture already gets (internal/mcp/hook.go's
// handlePostToolUse), reused unchanged for Codex.
func TestHookPostToolUseCodexBashRecordsCommandExitEvent(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()

	bashPayload := map[string]any{
		"session_id":  "codex-bash-session",
		"cwd":         projectDir,
		"tool_name":   "Bash",
		"tool_use_id": "call_bash_exit_1",
		"tool_input": map[string]any{
			"command": "exit 1",
		},
		"tool_response": map[string]any{
			"output":    "",
			"exit_code": 1,
		},
	}

	stdout, stderr, exitCode, _ := runHookInDir(t, bin, projectDir, env,
		[]string{"post-tool-use", "--harness", "codex"}, bashPayload)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := queryEventsForProject(t, s, projectKey)

	var gotResultCount int
	for _, ev := range events {
		if ev.Kind != payload.KindToolResult {
			continue
		}
		gotResultCount++
		var tr payload.ToolResult
		if err := json.Unmarshal([]byte(ev.Payload), &tr); err != nil {
			t.Fatalf("unmarshal tool.result payload %q: %v", ev.Payload, err)
		}
		if tr.Exit == nil {
			t.Fatalf("tool.result exit = nil, want 1")
		}
		if *tr.Exit != 1 {
			t.Errorf("tool.result exit = %d, want 1", *tr.Exit)
		}
	}
	if gotResultCount != 1 {
		t.Fatalf("project has %d tool.result events after one Bash exit-1 capture, want exactly 1 command-exit event: %#v", gotResultCount, events)
	}
}

// TestHookPostToolUseCodexUnknownToolNameRecordsZeroEventsExit0 is the
// punch's DONE WHEN clause 3: an unknown tool_name — neither apply_patch nor
// Bash, e.g. an MCP tool Codex's PostToolUse also fires for — records zero
// events and exits 0. This mapping never guesses at a tool it has not been
// told the shape of, unlike Claude's path (which records a bare tool.use
// event for every tool_name, including one outside its own known groups).
func TestHookPostToolUseCodexUnknownToolNameRecordsZeroEventsExit0(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()

	stdout, stderr, exitCode, _ := runHookInDir(t, bin, projectDir, env,
		[]string{"post-tool-use", "--harness", "codex"}, map[string]any{
			"session_id":  "codex-unknown-tool-session",
			"cwd":         projectDir,
			"tool_name":   "mcp__some_server__some_tool",
			"tool_use_id": "call_unknown_1",
			"tool_input":  map[string]any{"anything": "goes here"},
		})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := queryEventsForProject(t, s, projectKey)
	if len(events) != 0 {
		t.Errorf("project has %d timeline events after one unknown-tool_name codex PostToolUse call, want 0: %#v", len(events), events)
	}
}
