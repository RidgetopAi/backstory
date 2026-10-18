package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// bash runs `hook post-tool-use` for a Bash tool call reporting command, and
// returns its stdout. exit, when non-nil, is sent as the tool_response's
// exit_code (real Claude Code usually omits it).
func (f *stopFixture) bash(sessionID, toolUseID, command string, exit *int, envPrefix string) string {
	f.t.Helper()
	resp := map[string]any{"stdout": "", "stderr": "", "interrupted": false}
	if exit != nil {
		resp["exit_code"] = *exit
	}
	out := f.hook("post-tool-use", envPrefix, map[string]any{
		"session_id": sessionID, "cwd": f.projectDir, "hook_event_name": "PostToolUse",
		"tool_name": "Bash", "tool_use_id": toolUseID,
		"tool_input": map[string]any{"command": command}, "tool_response": resp,
	})
	i := strings.LastIndex(out, "exit=")
	if i < 0 || strings.TrimSpace(out[i:]) != "exit=0" {
		f.t.Fatalf("post-tool-use output %q, want exit=0", out)
	}
	return out[:i]
}

// realCommit makes a commit in the fixture repo with git run directly (no
// hook call), moving HEAD.
func (f *stopFixture) realCommit(msg string) {
	f.t.Helper()
	f.sh("git -c user.email=t@example.com -c user.name=t commit -q --allow-empty -m '" + msg + "'")
}

// writeNote inserts a note record carrying the session's id.
func (f *stopFixture) writeNote(sessionID string) {
	f.t.Helper()
	s := mustOpenTestStore(f.t, f.dbPath)
	var id string
	if err := s.DB().QueryRow(`SELECT id FROM sessions WHERE harness_session_id = ? AND origin = 'live'`, sessionID).Scan(&id); err != nil {
		f.t.Fatalf("find session %s: %v", sessionID, err)
	}
	if _, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindNote,
		Text: "shipped the thing", SessionID: id,
	}); err != nil {
		f.t.Fatal(err)
	}
}

// wantAsk fails unless stdout is exactly one PostToolUse additionalContext
// envelope naming an outcome, with no decision/block field anywhere.
func wantAsk(t *testing.T, stdout string) {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout, err)
	}
	for _, banned := range []string{"decision", "reason", "continue", "stopReason"} {
		if _, ok := env[banned]; ok {
			t.Errorf("stdout carries %q; the ask must be context, not a block: %s", banned, stdout)
		}
	}
	hso, _ := env["hookSpecificOutput"].(map[string]any)
	ctx, _ := hso["additionalContext"].(string)
	if hso["hookEventName"] != "PostToolUse" || !strings.Contains(ctx, "outcome") {
		t.Errorf("hookSpecificOutput = %v, want PostToolUse additionalContext naming an outcome", hso)
	}
	if _, ok := hso["decision"]; ok {
		t.Errorf("hookSpecificOutput carries a decision: %s", stdout)
	}
}

// warm opens the session (and its session-start git observation) before the
// first commit, the way SessionStart does in a real harness.
func (f *stopFixture) warm(sid string) {
	f.t.Helper()
	if out := f.bash(sid, "toolu_warm", "ls", nil, ""); out != "" {
		f.t.Fatalf("warm-up call printed %q", out)
	}
}

// Clause 1: the session's own `git commit` that moved HEAD asks once, as
// context; a second commit is silent; a fresh session asks again.
func TestCommitAskOncePerSession(t *testing.T) {
	f := newStopFixture(t)
	f.gitInitProject()
	f.warm("s-commit")

	f.realCommit("x")
	wantAsk(t, f.bash("s-commit", "toolu_c1", "git commit -m x", nil, ""))

	f.realCommit("y")
	if out := f.bash("s-commit", "toolu_c2", "git commit -m y", nil, ""); out != "" {
		t.Errorf("second commit in the same session printed %q, want nothing", out)
	}

	fresh := newStopFixture(t)
	fresh.gitInitProject()
	fresh.warm("s-fresh")
	fresh.realCommit("x")
	wantAsk(t, fresh.bash("s-fresh", "toolu_c1", "git commit -m x", nil, ""))
}

// Clause 2: only the session's own successful commit counts.
func TestCommitAskIgnoresOtherHeadMovesAndNoOpCommits(t *testing.T) {
	t.Run("commit made outside the session, then a non-commit call", func(t *testing.T) {
		f := newStopFixture(t)
		f.gitInitProject()
		f.warm("s-outside")
		f.realCommit("other agent")
		if out := f.bash("s-outside", "toolu_1", "git status", nil, ""); out != "" {
			t.Errorf("non-commit call after an outside commit printed %q, want nothing", out)
		}
		// The outside move is absorbed: a later commit that moves nothing is still silent.
		if out := f.bash("s-outside", "toolu_2", "git commit -m nothing", nil, ""); out != "" {
			t.Errorf("failed commit after an absorbed outside move printed %q, want nothing", out)
		}
	})

	t.Run("failed commit with HEAD unchanged", func(t *testing.T) {
		f := newStopFixture(t)
		f.gitInitProject()
		f.warm("s-failed")
		one := 1
		if out := f.bash("s-failed", "toolu_1", "git commit -m x", &one, ""); out != "" {
			t.Errorf("failed commit (exit 1) printed %q, want nothing", out)
		}
		if out := f.bash("s-failed", "toolu_2", "git commit -m x", nil, ""); out != "" {
			t.Errorf("commit that moved nothing printed %q, want nothing", out)
		}
		// The failures did not use up the ask.
		f.realCommit("real")
		wantAsk(t, f.bash("s-failed", "toolu_3", "git commit -m real", nil, ""))
	})

	t.Run("session already wrote a record before its first commit", func(t *testing.T) {
		f := newStopFixture(t)
		f.gitInitProject()
		f.warm("s-noted")
		f.writeNote("s-noted")
		f.realCommit("x")
		if out := f.bash("s-noted", "toolu_1", "git commit -m x", nil, ""); out != "" {
			t.Errorf("commit after a note printed %q, want nothing", out)
		}
	})
}

// Clause 5: every disabled or unobservable path is silent and exits 0 on a
// call that would otherwise ask.
func TestCommitAskSilentWhenDisabledOrUnreachable(t *testing.T) {
	f := newStopFixture(t)
	f.gitInitProject()
	f.warm("s-silent")
	f.realCommit("x")

	silent := func(name, envPrefix string) {
		t.Helper()
		if out := f.bash("s-silent", "toolu_"+name, "git commit -m x", nil, envPrefix); out != "" {
			t.Errorf("%s: stdout %q, want nothing", name, out)
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

	// Control: none of the above used up the ask.
	wantAsk(t, f.bash("s-silent", "toolu_control", "git commit -m x", nil, ""))

	t.Run("non-git cwd", func(t *testing.T) {
		g := newStopFixture(t)
		g.warm("s-nogit")
		if out := g.bash("s-nogit", "toolu_1", "git commit -m x", nil, ""); out != "" {
			t.Errorf("stdout %q, want nothing", out)
		}
	})
}
