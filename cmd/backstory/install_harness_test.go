package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runBackstory runs bin with args against a temp $HOME, returning its
// stdout, stderr and exit code.
func runBackstory(t *testing.T, bin, home string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec // bin is the binary this test just built
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	var outBuf, errBuf safeBuffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run backstory %v: %v (stderr: %s)", args, err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// TestInstallUnknownHarnessListsValidNames is the punch's DONE WHEN clause
// 2's first half: `backstory install nosuch` exits non-zero and names every
// valid harness in stderr.
func TestInstallUnknownHarnessListsValidNames(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()

	_, stderr, exitCode := runBackstory(t, bin, home, "install", "nosuch")
	if exitCode == 0 {
		t.Fatalf("install nosuch: exit code = 0, want non-zero (stderr: %s)", stderr)
	}
	for _, want := range []string{"claude", "codex", "hermes", "pi", "agents"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("install nosuch: stderr = %q, want it to list %q", stderr, want)
		}
	}
}

// TestInstallCheckClaudeReportsBeforeAndAfterInstall is clause 3:
// `backstory install --check claude` reports not-installed on a fresh temp
// HOME, and installed after a real `install claude`.
func TestInstallCheckClaudeReportsBeforeAndAfterInstall(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()

	stdout, stderr, exitCode := runBackstory(t, bin, home, "install", "--check", "claude")
	if exitCode == 0 {
		t.Fatalf("install --check claude on fresh home: exit code = 0, want non-zero (stdout: %s, stderr: %s)", stdout, stderr)
	}
	if strings.Contains(stdout, ": present") {
		t.Errorf("install --check claude on fresh home: stdout = %q, want no item reported present", stdout)
	}
	if !strings.Contains(stdout, "claude: ") {
		t.Errorf("install --check claude: stdout = %q, want lines prefixed with the harness name", stdout)
	}

	_, stderr, exitCode = runBackstory(t, bin, home, "install", "claude", "--no-verify")
	if exitCode != 0 {
		t.Fatalf("install claude --no-verify: exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}

	stdout, stderr, exitCode = runBackstory(t, bin, home, "install", "--check", "claude")
	if exitCode != 0 {
		t.Fatalf("install --check claude after install: exit code = %d, want 0 (stdout: %s, stderr: %s)", exitCode, stdout, stderr)
	}
	if strings.Contains(stdout, ": absent") || strings.Contains(stdout, ": foreign-conflict") {
		t.Errorf("install --check claude after install: stdout = %q, want every item present", stdout)
	}
}
