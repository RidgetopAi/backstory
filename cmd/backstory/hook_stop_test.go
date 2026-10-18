package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stopShellDone ends every command's output in the harness shell below.
const stopShellDone = "__BACKSTORY_STOP_TEST_DONE__"

// stopFixture is one test daemon plus ONE long-lived process named "claude"
// (a copy of the system shell), which every hook command runs under. The
// daemon's real /proc ancestry walk therefore finds the same harness process
// for every hook — one session, exactly like a real Claude Code process whose
// hook subprocesses share it as their ancestor.
type stopFixture struct {
	t          *testing.T
	bin        string
	dbPath     string
	runtimeDir string
	env        []string
	projectDir string
	stdin      io.WriteCloser
	stdout     *bufio.Reader
}

func newStopFixture(t *testing.T) *stopFixture {
	t.Helper()
	bin := buildBackstory(t)
	dbPath, runtimeDir, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()

	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh: %v", err)
	}
	if shPath, err = filepath.EvalSymlinks(shPath); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(shPath) //nolint:gosec // the system shell, copied to give it a harness-named path
	if err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(t.TempDir(), harnessName)
	if err := os.WriteFile(claude, src, 0o700); err != nil { //nolint:gosec // test-owned temp binary
		t.Fatal(err)
	}

	cmd := exec.Command(claude, "-c", `while IFS= read -r l; do eval "$l"; echo `+stopShellDone+`; done`) //nolint:gosec // claude is the temp copy of sh this test just wrote
	cmd.Dir = projectDir
	cmd.Env = env
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Wait() })

	return &stopFixture{t: t, bin: bin, dbPath: dbPath, runtimeDir: runtimeDir, env: env,
		projectDir: projectDir, stdin: in, stdout: bufio.NewReader(out)}
}

// sh runs one command line inside the harness shell and returns what it
// wrote to stdout.
func (f *stopFixture) sh(line string) string {
	f.t.Helper()
	if _, err := io.WriteString(f.stdin, line+"\n"); err != nil {
		f.t.Fatal(err)
	}
	var out strings.Builder
	for {
		l, err := f.stdout.ReadString('\n')
		if err != nil {
			f.t.Fatalf("read harness shell: %v (so far %q)", err, out.String())
		}
		if strings.TrimSpace(l) == stopShellDone {
			return out.String()
		}
		out.WriteString(l)
	}
}

// hook runs `backstory hook <sub>` under the harness shell with payload on
// stdin, prefixed by envPrefix (e.g. "FOO=1"), and returns its stdout.
func (f *stopFixture) hook(sub, envPrefix string, payload map[string]any) string {
	f.t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	pf := filepath.Join(f.t.TempDir(), "payload.json")
	if err := os.WriteFile(pf, b, 0o600); err != nil {
		f.t.Fatal(err)
	}
	return f.sh(envPrefix + " '" + f.bin + "' hook " + sub + " < '" + pf + "'; echo exit=$?")
}

func (f *stopFixture) edit(sessionID, toolUseID, file string) {
	f.t.Helper()
	p := filepath.Join(f.projectDir, file)
	out := f.hook("post-tool-use", "", map[string]any{
		"session_id": sessionID, "cwd": f.projectDir, "hook_event_name": "PostToolUse",
		"tool_name": "Edit", "tool_use_id": toolUseID,
		"tool_input":    map[string]any{"file_path": p, "old_string": "a", "new_string": "b"},
		"tool_response": map[string]any{"filePath": p, "success": true},
	})
	if strings.TrimSpace(out) != "exit=0" {
		f.t.Fatalf("post-tool-use output %q", out)
	}
}

// stop runs the stop hook and returns its stdout with the trailing exit
// status line split off.
func (f *stopFixture) stop(sessionID string, envPrefix string) (stdout string, exit string) {
	f.t.Helper()
	out := f.hook("stop", envPrefix, map[string]any{
		"session_id": sessionID, "cwd": f.projectDir, "hook_event_name": "Stop", "stop_hook_active": false,
	})
	i := strings.LastIndex(out, "exit=")
	if i < 0 {
		f.t.Fatalf("no exit status in %q", out)
	}
	return out[:i], strings.TrimSpace(out[i:])
}

// TestHookStopIsSilentNoOp: the Stop handoff nudge is retired. A session with
// edits and failed commands and no handoff — exactly what used to be blocked
// at the end of every turn — now prints nothing and exits 0, so a stale
// registration left by an older install is harmless.
func TestHookStopIsSilentNoOp(t *testing.T) {
	f := newStopFixture(t)
	f.edit("s-stop", "toolu_1", "a.go")
	f.edit("s-stop", "toolu_2", "b.go")
	out := f.hook("post-tool-use-failure", "", map[string]any{
		"session_id": "s-stop", "hook_event_name": "PostToolUseFailure", "tool_name": "Bash",
		"tool_use_id": "toolu_f", "tool_input": map[string]any{"command": "false"}, "error": "Exit code 1\nboom",
	})
	if strings.TrimSpace(out) != "exit=0" {
		t.Fatalf("failure hook output %q", out)
	}
	if stdout, exit := f.stop("s-stop", ""); exit != "exit=0" || stdout != "" {
		t.Errorf("stop: stdout %q %s, want silent exit 0", stdout, exit)
	}
}

// gitInitProject makes the fixture's project dir a git repo with one commit
// and one clean tracked file, before any session exists.
func (f *stopFixture) gitInitProject() {
	f.t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...) //nolint:gosec // literal git args in a test-owned temp repo
		cmd.Dir = f.projectDir
		if out, err := cmd.CombinedOutput(); err != nil {
			f.t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
}
