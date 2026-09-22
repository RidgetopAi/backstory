package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
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
	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
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

// seedOldShapeStore opens dbPath through store.Open once (so the schema is
// current, migration 6 included) then reaches under it with a raw
// connection to: insert a project + backfilled session + two pre-8ba5487a
// tool.use events (the file path under `detail`, no `path` key) for
// projectKey, and delete schema_version's row for version 6 so the daemon's
// own Open call — the normal read path, not a direct migration call — is
// the thing that migrates them. Migration 6 changes no table shape (a pure
// data rewrite, like 0003), so seeding through the fully-current schema and
// only rewinding the one schema_version bookkeeping row is safe: every
// column these inserts touch is identical before and after version 6.
func seedOldShapeStore(t *testing.T, dbPath, projectKey, sessionID string) {
	t.Helper()

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("seed: store.Open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("seed: close store: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("seed: open raw db: %v", err)
	}
	defer func() { _ = db.Close() }()

	nowNanos := time.Now().UTC().UnixNano()
	if _, err := db.Exec(`INSERT INTO projects (key, toplevel, first_seen) VALUES (?, ?, ?)`,
		projectKey, "/tmp/"+projectKey, nowNanos); err != nil {
		t.Fatalf("seed: insert project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, agent, cwd, project_key, started_at, origin) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, "claude", "/tmp/"+projectKey, projectKey, nowNanos, "backfilled"); err != nil {
		t.Fatalf("seed: insert session: %v", err)
	}

	type oldShape struct {
		ToolUseID string `json:"tool_use_id,omitempty"`
		Name      string `json:"name"`
		Detail    string `json:"detail,omitempty"`
	}
	for i, ev := range []struct{ id, name, detail string }{
		{"tu1", "Edit", "/tmp/a.go"},
		{"tu2", "Write", "/tmp/b.go"},
	} {
		b, err := json.Marshal(oldShape{ToolUseID: ev.id, Name: ev.name, Detail: ev.detail})
		if err != nil {
			t.Fatalf("seed: marshal old-shape payload: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO timeline_events (ts, kind, session_id, source, payload) VALUES (?, ?, ?, ?, ?)`,
			nowNanos+int64(i), payload.KindToolUse, sessionID, "backfill", string(b)); err != nil {
			t.Fatalf("seed: insert old-shape tool.use event: %v", err)
		}
	}

	if _, err := db.Exec(`DELETE FROM schema_version WHERE version = 6`); err != nil {
		t.Fatalf("seed: rewind schema_version past migration 6: %v", err)
	}
}

// requestBlockDirect dials sockPath and issues one DaemonRequest for
// DaemonMethodBlock, exactly like requestBlock in hook.go but returning the
// raw response for a test to inspect (hook.go's requestBlock reads
// $XDG_RUNTIME_DIR itself; this test dials the path it already knows).
func requestBlockDirect(t *testing.T, sockPath, sessionID string) string {
	t.Helper()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial daemon socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req := mcp.DaemonRequest{Session: sessionID, Method: mcp.DaemonMethodBlock}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal block request: %v", err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		t.Fatalf("write block request: %v", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read block response: %v", err)
	}
	var resp mcp.DaemonResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode block response %q: %v", line, err)
	}
	if resp.Error != nil {
		t.Fatalf("daemon returned error for block request: %s", resp.Error.Message)
	}
	var result mcp.BlockResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode block result: %v", err)
	}
	return result.Block
}

// TestDaemonStartMigratesOldShapeStoreThroughNormalReadPath is proof (3)
// for task e96d1a21: starting `backstory daemon` against a store whose
// tool.use events predate the payload contract (task 8ba5487a) ends with
// the SessionStart block's delta reporting the correct file count, observed
// through the daemon's normal block-request read path — not by calling the
// migration directly. There is no subcommand and no flag: store.Open inside
// runDaemon is the only thing that runs it.
func TestDaemonStartMigratesOldShapeStoreThroughNormalReadPath(t *testing.T) {
	bin := buildBackstory(t)

	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	projectKey := project.Key(cwd, project.RealGit{})

	seedOldShapeStore(t, dbPath, projectKey, "sess-daemon-migration")

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

	block := requestBlockDirect(t, sockPath, "hook-session-daemon-migration")

	if !strings.Contains(block, "1 sessions, 2 files touched") {
		t.Errorf("block = %q, want it to contain %q (the corrected delta count, through the normal read path)", block, "1 sessions, 2 files touched")
	}
	if strings.Contains(block, "0 files touched") {
		t.Errorf("block = %q, still reads 0 files touched — the old-shape store was not migrated on daemon start", block)
	}

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
