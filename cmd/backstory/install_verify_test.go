package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakePATH returns a PATH value whose first entry is a directory containing
// exactly one file named "backstory" (the argv[0] `sh -c "backstory hook
// session-start"` looks up), followed by the real PATH so `sh` itself still
// resolves.
func fakePATH(t *testing.T, backstoryPath string) string {
	t.Helper()
	dir := t.TempDir()
	link := filepath.Join(dir, "backstory")
	if err := os.Symlink(backstoryPath, link); err != nil {
		t.Fatalf("symlink fake backstory: %v", err)
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// mergeEnv returns base with any variable also set in overrides dropped,
// then overrides appended — avoiding the getenv-scans-first-match ambiguity
// of simply appending a second "PATH=..." (or "HOME=...") onto a slice that
// already carries one from os.Environ().
func mergeEnv(base []string, overrides ...string) []string {
	keys := make(map[string]bool, len(overrides))
	for _, kv := range overrides {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			keys[kv[:i]] = true
		}
	}
	merged := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		i := strings.IndexByte(kv, '=')
		if i >= 0 && keys[kv[:i]] {
			continue
		}
		merged = append(merged, kv)
	}
	return append(merged, overrides...)
}

// runInstallVerifySubprocess runs `backstory install claude` (verification
// ON — no --no-verify) against home and env, returning its stdout, stderr
// and exit code.
func runInstallVerifySubprocess(t *testing.T, bin, home string, env []string, extraArgs ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"install", "claude"}, extraArgs...)...) //nolint:gosec // bin is the binary this test just built
	cmd.Env = mergeEnv(env, "HOME="+home)
	var outBuf, errBuf safeBuffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run backstory install claude: %v (stderr: %s)", err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// TestInstallVerifyPassesWithDaemonRunning is the punch's DONE WHEN clause
// 4's success path: with a daemon started in this test and a fake PATH
// exposing the test binary as `backstory`, `install claude` (verification
// ON) runs the written hook command through sh -c with a synthetic
// SessionStart payload and exits 0.
func TestInstallVerifyPassesWithDaemonRunning(t *testing.T) {
	bin := buildBackstory(t)
	_, _, daemonEnv := startTestDaemon(t, bin) // env carries XDG_RUNTIME_DIR/XDG_DATA_HOME

	home := t.TempDir()
	env := mergeEnv(daemonEnv, "PATH="+fakePATH(t, bin))

	stdout, stderr, exitCode := runInstallVerifySubprocess(t, bin, home, env)
	if exitCode != 0 {
		t.Fatalf("install claude (verify on) with daemon running: exit code = %d, want 0 (stdout: %s, stderr: %s)", exitCode, stdout, stderr)
	}
}

// TestInstallVerifyPassesWithNoDaemonRunning is clause 4's documented
// empty-stdout path: with the daemon stopped, the hook prints nothing (its
// no-daemon failure path) and exits 0 — verification must still pass,
// because "exit 0 with empty stdout" is itself the documented success
// shape, not a failure.
func TestInstallVerifyPassesWithNoDaemonRunning(t *testing.T) {
	bin := buildBackstory(t)

	home := t.TempDir()
	runtimeDir := t.TempDir() // no daemon ever started: no socket file under it
	env := []string{
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"PATH=" + fakePATH(t, bin),
	}

	stdout, stderr, exitCode := runInstallVerifySubprocess(t, bin, home, env)
	if exitCode != 0 {
		t.Fatalf("install claude (verify on) with no daemon running: exit code = %d, want 0 (stdout: %s, stderr: %s)", exitCode, stdout, stderr)
	}
}

// writeFakeBackstory writes a shell script named "backstory" to a fresh
// temp dir and returns that dir, for tests that need `sh -c "backstory hook
// session-start"` to run something other than the real binary.
func writeFakeBackstory(t *testing.T, script string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	path := filepath.Join(dir, "backstory")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture, needs +x
		t.Fatalf("write fake backstory: %v", err)
	}
	return dir
}

// TestInstallVerifyFailsWhenDaemonUpButHookPrintsNothing is clause 5's
// negative case (a): a fake `backstory` on PATH that exits 0 and prints
// nothing, while a real daemon IS reachable, must not be mistaken for the
// documented no-daemon empty-stdout success path — install must exit
// non-zero and name the failing item. RED against 4fd4b6494bcc, where
// verifyHook decided on exit code alone.
func TestInstallVerifyFailsWhenDaemonUpButHookPrintsNothing(t *testing.T) {
	bin := buildBackstory(t)
	_, _, daemonEnv := startTestDaemon(t, bin)

	fakeDir := writeFakeBackstory(t, "#!/bin/sh\nexit 0\n")
	home := t.TempDir()
	env := mergeEnv(daemonEnv, "PATH="+fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	stdout, stderr, exitCode := runInstallVerifySubprocess(t, bin, home, env, "--binary", filepath.Join(fakeDir, "backstory"))
	if exitCode == 0 {
		t.Fatalf("install claude (verify on) with daemon up and a silent fake hook: exit code = 0, want non-zero (stdout: %s)", stdout)
	}
	if !strings.Contains(stderr, "session-start-hook") {
		t.Errorf("stderr = %q, want it to name the failed item (session-start-hook)", stderr)
	}
}

// TestInstallVerifyFailsWhenHookPrintsUnrelatedText is clause 5's negative
// case (b): a fake `backstory` that exits 0 and prints text that is neither
// a rendered block nor the documented empty-state line must fail
// verification. RED against 4fd4b6494bcc.
func TestInstallVerifyFailsWhenHookPrintsUnrelatedText(t *testing.T) {
	bin := buildBackstory(t)

	fakeDir := writeFakeBackstory(t, "#!/bin/sh\necho 'TOTAL GARBAGE: not a block, not the empty-state line'\nexit 0\n")
	home := t.TempDir()
	env := []string{"PATH=" + fakeDir + string(os.PathListSeparator) + os.Getenv("PATH")}

	stdout, stderr, exitCode := runInstallVerifySubprocess(t, bin, home, env, "--binary", filepath.Join(fakeDir, "backstory"))
	if exitCode == 0 {
		t.Fatalf("install claude (verify on) with a fake hook printing unrelated text: exit code = 0, want non-zero (stdout: %s)", stdout)
	}
	if !strings.Contains(stderr, "session-start-hook") {
		t.Errorf("stderr = %q, want it to name the failed item (session-start-hook)", stderr)
	}
}

// TestInstallVerifyFailsWhenHookExitsNonZero is clause 4's failure path: a
// hook command that exits non-zero makes install exit non-zero and report
// which item failed.
func TestInstallVerifyFailsWhenHookExitsNonZero(t *testing.T) {
	bin := buildBackstory(t)

	fakeBinDir := t.TempDir()
	failingScript := "#!/bin/sh\nexit 7\n"
	scriptPath := filepath.Join(fakeBinDir, "backstory")
	if err := os.WriteFile(scriptPath, []byte(failingScript), 0o755); err != nil { //nolint:gosec // test fixture, needs +x
		t.Fatalf("write failing fake backstory: %v", err)
	}

	home := t.TempDir()
	env := []string{"PATH=" + fakeBinDir + string(os.PathListSeparator) + os.Getenv("PATH")}

	stdout, stderr, exitCode := runInstallVerifySubprocess(t, bin, home, env, "--binary", scriptPath)
	if exitCode == 0 {
		t.Fatalf("install claude (verify on) with a failing hook: exit code = 0, want non-zero (stdout: %s)", stdout)
	}
	if !strings.Contains(stderr, "session-start-hook") {
		t.Errorf("stderr = %q, want it to name the failed item (session-start-hook)", stderr)
	}

	// The install itself must be left in place despite the verify failure.
	claudeJSON := filepath.Join(home, ".claude.json")
	if _, err := os.Stat(claudeJSON); err != nil {
		t.Errorf("claude.json missing after a failed verify; install should be left in place: %v", err)
	}
}
