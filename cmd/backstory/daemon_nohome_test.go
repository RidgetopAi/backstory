package main

import (
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestDaemonStartsWithoutHome: an unresolvable home dir means no default
// workspace (decision bcc9fa54), never a daemon that refuses to start. With
// HOME and BACKSTORY_WORKSPACE_DIRS both absent the daemon must still open
// its socket.
func TestDaemonStartsWithoutHome(t *testing.T) {
	bin := buildBackstory(t)
	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	env := append(envWithout(testXDGEnv(), "HOME", "BACKSTORY_WORKSPACE_DIRS"),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XDG_DATA_HOME="+dataDir,
	)

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built
	cmd.Env = env
	var out safeBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})

	waitForFile(t, filepath.Join(runtimeDir, "backstory", "sock"), 5*time.Second)
	if cmd.ProcessState != nil {
		t.Fatalf("daemon exited without HOME: %s", out.String())
	}
}
