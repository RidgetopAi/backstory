package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestBackfillClaudeCLIAndDaemonIntegration is the punch's clause 5:
// `backstory backfill claude` as a subprocess, then the daemon running the
// same importer once on start.
func TestBackfillClaudeCLIAndDaemonIntegration(t *testing.T) {
	bin := buildBackstory(t)
	fixtures, err := filepath.Abs(filepath.Join("testdata", "claude", "projects"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("CLI prints summary and exits 0", func(t *testing.T) {
		dataDir := t.TempDir()
		cmd := exec.Command(bin, "backfill", "claude", "--root", fixtures) //nolint:gosec // bin is the binary this test just built; fixtures is this test's own testdata
		cmd.Env = testXDGEnv("XDG_DATA_HOME=" + dataDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("backstory backfill claude: %v\n%s", err, out)
		}
		want := "backfill claude: 1 file(s), 1 session(s), 2 event(s), 0 line(s) skipped\n"
		if string(out) != want {
			t.Errorf("output = %q, want %q", out, want)
		}
	})

	t.Run("daemon backfills on start without any client call", func(t *testing.T) {
		runtimeDir := t.TempDir()
		dataDir := t.TempDir()

		cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built
		cmd.Env = testXDGEnv(
			"XDG_RUNTIME_DIR="+runtimeDir,
			"XDG_DATA_HOME="+dataDir,
			"BACKSTORY_CLAUDE_ROOT="+fixtures,
		)
		var out safeBuffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Start(); err != nil {
			t.Fatalf("start daemon: %v", err)
		}
		defer stopDaemon(t, cmd)

		sockPath := filepath.Join(runtimeDir, "backstory", "sock")
		waitForFile(t, sockPath, 2*time.Second)

		// No socket dial anywhere in this subtest: the backfilled session
		// must appear from the daemon's own on-start run alone.
		dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
		waitForSession(t, dbPath, "sess-p-uuid", 5*time.Second)
	})

	t.Run("missing backfill root leaves the daemon running and serving status", func(t *testing.T) {
		runtimeDir := t.TempDir()
		dataDir := t.TempDir()

		cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built
		cmd.Env = testXDGEnv(
			"XDG_RUNTIME_DIR="+runtimeDir,
			"XDG_DATA_HOME="+dataDir,
			"BACKSTORY_CLAUDE_ROOT="+filepath.Join(t.TempDir(), "does-not-exist"),
		)
		var out safeBuffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Start(); err != nil {
			t.Fatalf("start daemon: %v", err)
		}
		defer stopDaemon(t, cmd)

		sockPath := filepath.Join(runtimeDir, "backstory", "sock")
		waitForFile(t, sockPath, 3*time.Second)

		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("dial daemon socket: %v\ndaemon output:\n%s", err, out.String())
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.Write([]byte(`{"method":"status"}` + "\n")); err != nil {
			t.Fatalf("write status request: %v", err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}

		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			t.Fatalf("decode status response: %v\ndaemon output:\n%s", err, out.String())
		}
		if resp.Error != nil {
			t.Fatalf("status error = %+v, want none (daemon must still serve status with a missing backfill root)", resp.Error)
		}
		if len(resp.Result) == 0 {
			t.Fatal("status result is empty, want a StatusResult object")
		}
	})
}

// TestBackfillCodexCLIAndDaemonIntegration is task e9cb97dd's clause 6
// wiring proof: `backstory backfill codex` as a subprocess, then the daemon
// running the same importer once on start (mirrors
// TestBackfillClaudeCLIAndDaemonIntegration above).
func TestBackfillCodexCLIAndDaemonIntegration(t *testing.T) {
	bin := buildBackstory(t)
	fixtures, err := filepath.Abs(filepath.Join("testdata", "codex", "sessions"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("CLI prints summary and exits 0", func(t *testing.T) {
		dataDir := t.TempDir()
		cmd := exec.Command(bin, "backfill", "codex", "--root", fixtures) //nolint:gosec // bin is the binary this test just built; fixtures is this test's own testdata
		cmd.Env = testXDGEnv("XDG_DATA_HOME=" + dataDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("backstory backfill codex: %v\n%s", err, out)
		}
		want := "backfill codex: 1 file(s), 1 session(s), 2 event(s), 2 line(s) skipped, 0 file(s) partial\n"
		if string(out) != want {
			t.Errorf("output = %q, want %q", out, want)
		}
	})

	t.Run("daemon backfills on start without any client call", func(t *testing.T) {
		runtimeDir := t.TempDir()
		dataDir := t.TempDir()

		cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built
		cmd.Env = testXDGEnv(
			"XDG_RUNTIME_DIR="+runtimeDir,
			"XDG_DATA_HOME="+dataDir,
			"BACKSTORY_CODEX_ROOT="+fixtures,
		)
		var out safeBuffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Start(); err != nil {
			t.Fatalf("start daemon: %v", err)
		}
		defer stopDaemon(t, cmd)

		sockPath := filepath.Join(runtimeDir, "backstory", "sock")
		waitForFile(t, sockPath, 2*time.Second)

		// No socket dial anywhere in this subtest: the backfilled session
		// must appear from the daemon's own on-start run alone.
		dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
		waitForSession(t, dbPath, "sess-codex-uuid", 5*time.Second)
	})
}

func stopDaemon(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
	}
}

// waitForSession polls dbPath directly (a second, read-only-in-spirit
// connection alongside the daemon's own — SQLite's WAL mode allows this)
// until a session with harnessSessionID appears or timeout elapses.
func waitForSession(t *testing.T, dbPath, harnessSessionID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(dbPath); statErr == nil {
			if n, err := countSessionsWithHarnessID(dbPath, harnessSessionID); err != nil {
				lastErr = err
			} else if n > 0 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for session harness_session_id=%s in %s: %v", harnessSessionID, dbPath, lastErr)
}

func countSessionsWithHarnessID(dbPath, harnessSessionID string) (int, error) {
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = st.Close() }()
	var n int
	err = st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = ?`, harnessSessionID).Scan(&n)
	return n, err
}
