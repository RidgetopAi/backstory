package main

import (
	"bufio"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDaemonSIGTERMWithIdleClientExitsCleanly is the daemon-stop regression:
// a client that stays connected and idle (as `backstory mcp` does for a whole
// agent session) must not keep the daemon alive past SIGTERM until systemd
// SIGKILLs it, and a request landing as the signal arrives still gets its
// response.
func TestDaemonSIGTERMWithIdleClientExitsCleanly(t *testing.T) {
	bin := buildBackstory(t)
	runtimeDir := t.TempDir()

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built, not external input
	cmd.Env = testXDGEnv("XDG_RUNTIME_DIR="+runtimeDir, "XDG_DATA_HOME="+t.TempDir())
	var out safeBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	sockPath := filepath.Join(runtimeDir, "backstory", "sock")
	waitForFile(t, sockPath, 5*time.Second)

	const statusReq = `{"method":"status","no_session":true}` + "\n"

	// Idle client: completes one request, then just stays connected.
	idle, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial idle client: %v", err)
	}
	defer func() { _ = idle.Close() }()
	idleR := bufio.NewReader(idle)
	if _, err := idle.Write([]byte(statusReq)); err != nil {
		t.Fatalf("idle write: %v", err)
	}
	if _, err := idleR.ReadString('\n'); err != nil {
		t.Fatalf("idle first response: %v", err)
	}

	// Request in flight as the signal lands.
	busy, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial busy client: %v", err)
	}
	defer func() { _ = busy.Close() }()
	if _, err := busy.Write([]byte(statusReq)); err != nil {
		t.Fatalf("busy write: %v", err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal SIGTERM: %v", err)
	}
	_ = busy.SetReadDeadline(time.Now().Add(10 * time.Second))
	if resp, err := bufio.NewReader(busy).ReadString('\n'); err != nil || !strings.Contains(resp, "result") {
		t.Errorf("in-flight request got (%q, %v), want its response", resp, err)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("daemon exited non-zero after SIGTERM: %v\noutput:\n%s", err, out.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("daemon did not exit within 10s of SIGTERM with an idle client connected\noutput:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "signal received, shutting down") {
		t.Errorf("log lacks clean-shutdown line:\n%s", out.String())
	}
	_ = idle.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := idleR.ReadString('\n'); err == nil {
		t.Error("idle client read succeeded after daemon exit, want EOF")
	}
}
