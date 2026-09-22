package main

import (
	"bytes"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
)

// buildBackstory compiles the backstory binary once for daemon_test.go's
// subprocess tests.
func buildBackstory(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "backstory")
	cmd := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec // bin is a t.TempDir() path this test built, not external input
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build backstory: %v\n%s", err, out)
	}
	return bin
}

// safeBuffer is an io.Writer safe for concurrent use by the subprocess's
// stdout pump and the test's own reads of its contents.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to appear", path)
}

func waitForSubstring(t *testing.T, get func() string, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(get(), substr) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for output to contain %q; got:\n%s", substr, get())
}

// TestLogIdentityIncludesReasonWhenSet is the punch's acceptance clause 2
// (task f2718b5b): the daemon's identity log line carries the reason a
// harness's cwd could not be read, so it stops looking identical to "no
// harness found" in the logs.
func TestLogIdentityIncludesReasonWhenSet(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	logIdentity(logger, ident.Identity{
		Kind:    ident.KindAgent,
		UID:     1000,
		PID:     900,
		Harness: "claude",
		Reason:  "cwd unreadable: permission denied",
	})

	got := buf.String()
	if !strings.Contains(got, `reason="cwd unreadable: permission denied"`) {
		t.Errorf("log line = %q, want it to contain the reason", got)
	}
}

// TestLogIdentityOmitsReasonWhenCwdReadable is
// TestLogIdentityIncludesReasonWhenSet's control: a caller with a readable
// cwd (Reason unset) gets no reason field in the log line at all.
func TestLogIdentityOmitsReasonWhenCwdReadable(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	logIdentity(logger, ident.Identity{
		Kind:    ident.KindAgent,
		UID:     1000,
		PID:     900,
		Harness: "claude",
		CWD:     "/home/brian/proj",
	})

	if got := buf.String(); strings.Contains(got, "reason=") {
		t.Errorf("log line = %q, want no reason field for a readable cwd", got)
	}
}

// TestDaemonStartsAcceptsConnectionExitsOnSIGTERM is the punch's acceptance
// clause 4: `backstory daemon` starts, creates the socket, accepts one
// connection, logs the identity, and exits 0 on SIGTERM within 2s.
func TestDaemonStartsAcceptsConnectionExitsOnSIGTERM(t *testing.T) {
	bin := buildBackstory(t)

	runtimeDir := t.TempDir()
	dataDir := t.TempDir()

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built, not external input
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XDG_DATA_HOME="+dataDir,
	)
	var out safeBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	sockPath := filepath.Join(runtimeDir, "backstory", "sock")
	waitForFile(t, sockPath, 2*time.Second)

	sockInfo, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := sockInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("socket mode = %04o, want 0600", got)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial daemon socket: %v", err)
	}
	if _, err := conn.Write([]byte(`{"session":"s1","actor":"agent"}` + "\n")); err != nil {
		t.Fatalf("write to daemon: %v", err)
	}
	_ = conn.Close()

	waitForSubstring(t, out.String, "identity: kind=agent", 2*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal SIGTERM: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("daemon exited with error after SIGTERM (want exit 0): %v\noutput:\n%s", err, out.String())
		}
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("daemon did not exit within 2s of SIGTERM\noutput:\n%s", out.String())
	}
}
