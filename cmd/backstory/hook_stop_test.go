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

	"github.com/RidgetopAi/backstory/internal/store"
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
func (f *stopFixture) stop(sessionID string, active bool, envPrefix string) (stdout string, exit string) {
	f.t.Helper()
	out := f.hook("stop", envPrefix, map[string]any{
		"session_id": sessionID, "cwd": f.projectDir, "hook_event_name": "Stop", "stop_hook_active": active,
	})
	i := strings.LastIndex(out, "exit=")
	if i < 0 {
		f.t.Fatalf("no exit status in %q", out)
	}
	return out[:i], strings.TrimSpace(out[i:])
}

// writeHandoff inserts a handoff record carrying the session's id, the way
// the note tool does for a live session.
func (f *stopFixture) writeHandoff(sessionID string) {
	f.t.Helper()
	s := mustOpenTestStore(f.t, f.dbPath)
	var id string
	if err := s.DB().QueryRow(`SELECT id FROM sessions WHERE harness_session_id = ? AND origin = 'live'`, sessionID).Scan(&id); err != nil {
		f.t.Fatalf("find session %s: %v", sessionID, err)
	}
	if _, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
		Text: "did the thing", SessionID: id,
	}); err != nil {
		f.t.Fatal(err)
	}
}

// TestHookStopFourCases is the punch's clause 1: edits and no handoff block
// once naming the count; a handoff silences it; stop_hook_active silences it;
// a session with no edits and no failures is silent.
func TestHookStopFourCases(t *testing.T) {
	t.Run("two edits no handoff blocks, then handoff silences", func(t *testing.T) {
		f := newStopFixture(t)
		f.edit("s-edit", "toolu_1", "a.go")
		f.edit("s-edit", "toolu_2", "b.go")

		stdout, exit := f.stop("s-edit", false, "")
		if exit != "exit=0" {
			t.Fatalf("%s", exit)
		}
		var d struct{ Decision, Reason string }
		if err := json.Unmarshal([]byte(stdout), &d); err != nil {
			t.Fatalf("stdout %q is not a JSON decision: %v", stdout, err)
		}
		if d.Decision != "block" || !strings.Contains(d.Reason, "2 file(s)") || !strings.Contains(d.Reason, "note handoff") {
			t.Errorf("decision = %+v, want block naming 2 file(s) and note handoff", d)
		}

		f.writeHandoff("s-edit")
		stdout, exit = f.stop("s-edit", false, "")
		if exit != "exit=0" || stdout != "" {
			t.Errorf("after handoff: stdout %q %s, want silent exit 0", stdout, exit)
		}
	})

	t.Run("stop_hook_active is silent", func(t *testing.T) {
		f := newStopFixture(t)
		f.edit("s-active", "toolu_1", "a.go")
		if stdout, exit := f.stop("s-active", true, ""); exit != "exit=0" || stdout != "" {
			t.Errorf("stdout %q %s, want silent exit 0", stdout, exit)
		}
	})

	t.Run("no edits and no failures is silent", func(t *testing.T) {
		f := newStopFixture(t)
		if stdout, exit := f.stop("s-readonly", false, ""); exit != "exit=0" || stdout != "" {
			t.Errorf("stdout %q %s, want silent exit 0", stdout, exit)
		}
	})

	t.Run("failed commands alone block and are named", func(t *testing.T) {
		f := newStopFixture(t)
		out := f.hook("post-tool-use-failure", "", map[string]any{
			"session_id": "s-fail", "hook_event_name": "PostToolUseFailure", "tool_name": "Bash",
			"tool_use_id": "toolu_f", "tool_input": map[string]any{"command": "false"}, "error": "Exit code 1\nboom",
		})
		if strings.TrimSpace(out) != "exit=0" {
			t.Fatalf("failure hook output %q", out)
		}
		stdout, _ := f.stop("s-fail", false, "")
		if !strings.Contains(stdout, "1 failed command(s)") || !strings.Contains(stdout, `"decision":"block"`) {
			t.Errorf("stdout = %q, want a block naming 1 failed command(s)", stdout)
		}
	})
}

// TestHookStopSilentWhenDisabledOrUnreachable is clause 2: BACKSTORY_NO_SESSION,
// capture off, and no daemon each print nothing and exit 0 — even for a
// session that would otherwise be blocked.
func TestHookStopSilentWhenDisabledOrUnreachable(t *testing.T) {
	f := newStopFixture(t)
	f.edit("s-silent", "toolu_1", "a.go")
	if stdout, _ := f.stop("s-silent", false, ""); !strings.Contains(stdout, "block") {
		t.Fatalf("control: stdout %q, want a block before any silencing", stdout)
	}

	silent := func(name, envPrefix string) {
		t.Helper()
		if stdout, exit := f.stop("s-silent", false, envPrefix); exit != "exit=0" || stdout != "" {
			t.Errorf("%s: stdout %q %s, want silent exit 0", name, stdout, exit)
		}
	}

	silent("BACKSTORY_NO_SESSION", noSessionEnv+"=1")

	flag := filepath.Join(f.runtimeDir, "backstory", "capture-off")
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	silent("capture off", "")
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}

	silent("no daemon", "XDG_RUNTIME_DIR='"+t.TempDir()+"'")
}
