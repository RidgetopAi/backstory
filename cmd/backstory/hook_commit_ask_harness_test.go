package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// harnessCall runs `hook post-tool-use --harness <harness>` with payload and
// returns stdout, requiring exit 0.
func (f *stopFixture) harnessCall(harness, envPrefix string, payload map[string]any) string {
	f.t.Helper()
	payload["cwd"], payload["hook_event_name"] = f.projectDir, "PostToolUse"
	out := f.hook("post-tool-use --harness "+harness, envPrefix, payload)
	i := strings.LastIndex(out, "exit=")
	if i < 0 || strings.TrimSpace(out[i:]) != "exit=0" {
		f.t.Fatalf("%s post-tool-use output %q, want exit=0", harness, out)
	}
	return out[:i]
}

// codexBash is the Codex shape: tool_name Bash, string command, plain-string
// tool_response, no exit code.
func (f *stopFixture) codexBash(sid, id, command, envPrefix string) string {
	f.t.Helper()
	return f.harnessCall("codex", envPrefix, map[string]any{
		"session_id": sid, "tool_name": "Bash", "tool_use_id": id,
		"tool_input": map[string]any{"command": command}, "tool_response": "[main abc] x",
	})
}

// piBash is the Pi extension's shape; optIn adds backstory_commit_ask.
func (f *stopFixture) piBash(sid, id, command string, optIn bool, envPrefix string) string {
	f.t.Helper()
	p := map[string]any{
		"session_id": sid, "tool_name": "Bash", "tool_use_id": id,
		"tool_input": map[string]any{"command": command}, "tool_response": map[string]any{"stdout": "ok"},
	}
	if optIn {
		p["backstory_commit_ask"] = true
	}
	return f.harnessCall("pi", envPrefix, p)
}

// wantOnlyEnvelope fails unless stdout is exactly {"hookSpecificOutput":{
// "hookEventName","additionalContext"}} — Codex's output schema has
// additionalProperties:false, so any extra key voids the context.
func wantOnlyEnvelope(t *testing.T, stdout string) {
	t.Helper()
	wantAsk(t, stdout)
	var top map[string]map[string]any
	if err := json.Unmarshal([]byte(stdout), &top); err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || len(top["hookSpecificOutput"]) != 2 {
		t.Errorf("envelope has extra keys: %s", stdout)
	}
}

func TestCodexCommitAskOncePerSession(t *testing.T) {
	f := newStopFixture(t)
	f.gitInitProject()
	if out := f.codexBash("c-sess", "t0", "ls", ""); out != "" {
		t.Fatalf("warm-up printed %q", out)
	}
	f.realCommit("x")
	wantOnlyEnvelope(t, f.codexBash("c-sess", "t1", "git commit -m x", ""))
	f.realCommit("y")
	if out := f.codexBash("c-sess", "t2", "git commit -m y", ""); out != "" {
		t.Errorf("second commit printed %q, want nothing", out)
	}

	g := newStopFixture(t)
	g.gitInitProject()
	g.codexBash("c-fresh", "t0", "ls", "")
	g.realCommit("x")
	wantOnlyEnvelope(t, g.codexBash("c-fresh", "t1", "git commit -m x", ""))
}

func TestPiCommitAskNeedsOptIn(t *testing.T) {
	f := newStopFixture(t)
	f.gitInitProject()
	f.piBash("p-sess", "t0", "ls", false, "")

	f.realCommit("x")
	if out := f.piBash("p-sess", "t1", "git commit -m x", false, ""); out != "" {
		t.Fatalf("pi without opt-in printed %q, want nothing", out)
	}
	// The un-opted call did not settle the ask nor move last-seen HEAD.
	wantOnlyEnvelope(t, f.piBash("p-sess", "t2", "git commit -m x", true, ""))
	f.realCommit("y")
	if out := f.piBash("p-sess", "t3", "git commit -m y", true, ""); out != "" {
		t.Errorf("second opted-in commit printed %q, want nothing", out)
	}

	g := newStopFixture(t)
	g.gitInitProject()
	g.piBash("p-direct", "t0", "ls", true, "")
	g.realCommit("x")
	wantOnlyEnvelope(t, g.piBash("p-direct", "t1", "git commit -m x", true, ""))
}

func TestCodexAndPiCommitAskSilentWhenDisabledOrUnreachable(t *testing.T) {
	f := newStopFixture(t)
	f.gitInitProject()
	f.codexBash("s", "w", "ls", "")
	f.piBash("s", "w2", "ls", true, "")
	f.realCommit("x")

	calls := map[string]func(env string) string{
		"codex": func(env string) string { return f.codexBash("s", "c", "git commit -m x", env) },
		"pi":    func(env string) string { return f.piBash("s", "c", "git commit -m x", true, env) },
	}
	for name, call := range calls {
		silent := func(label, env string) {
			t.Helper()
			if out := call(env); out != "" {
				t.Errorf("%s/%s: stdout %q, want nothing", name, label, out)
			}
		}
		silent("off", "BACKSTORY_OFF=1")
		silent("nosession", noSessionEnv+"=1")
		flag := filepath.Join(f.runtimeDir, "backstory", "capture-off")
		if err := os.WriteFile(flag, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		silent("captureoff", "")
		if err := os.Remove(flag); err != nil {
			t.Fatal(err)
		}
		silent("nodaemon", "XDG_RUNTIME_DIR='"+t.TempDir()+"'")
	}

	// Codex non-commit Bash and apply_patch print nothing even with HEAD moved.
	if out := f.codexBash("s", "n", "git status", ""); out != "" {
		t.Errorf("codex non-commit Bash printed %q", out)
	}
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hi\n*** End Patch"
	if out := f.harnessCall("codex", "", map[string]any{
		"session_id": "s", "tool_name": "apply_patch", "tool_use_id": "ap",
		"tool_input": map[string]any{"command": patch}, "tool_response": "ok",
	}); out != "" {
		t.Errorf("codex apply_patch printed %q", out)
	}

	// Control: nothing above settled the ask. The non-commit call absorbed the
	// earlier HEAD move, so make a fresh one.
	f.realCommit("z")
	wantOnlyEnvelope(t, f.codexBash("s", "ctl", "git commit -m x", ""))

	t.Run("non-git cwd", func(t *testing.T) {
		g := newStopFixture(t)
		g.codexBash("ng", "w", "ls", "")
		g.piBash("ng2", "w", "ls", true, "")
		if out := g.codexBash("ng", "c", "git commit -m x", ""); out != "" {
			t.Errorf("codex stdout %q", out)
		}
		if out := g.piBash("ng2", "c", "git commit -m x", true, ""); out != "" {
			t.Errorf("pi stdout %q", out)
		}
	})
}
