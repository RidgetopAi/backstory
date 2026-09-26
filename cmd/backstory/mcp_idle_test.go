package main

import (
	"bufio"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/store"
)

// buildMCPHarness compiles cmd/backstory/testdata/mcpharness to a binary
// whose path's final component is exactly name (mirrors buildHarnessClient
// in daemon_test.go, for the same reason: the daemon's /proc ancestry walk
// keys on a process's real comm, set by the kernel from the basename passed
// to execve, so the test needs a real process on disk with that name,
// exec'd directly and never through a shell or PATH lookup).
func buildMCPHarness(t *testing.T, name string) string {
	t.Helper()
	found := false
	for _, h := range ident.KnownHarnesses {
		if h == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("harness name %q is not in ident.KnownHarnesses %v", name, ident.KnownHarnesses)
	}

	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/mcpharness") //nolint:gosec // fixed source path, fixed tmp-dir output path
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build mcpharness: %v\n%s", err, out)
	}
	return bin
}

// startDaemonForTest starts `backstory daemon` as a subprocess against
// runtimeDir/dataDir plus any extraEnv, waits for its socket to appear, and
// returns a stop func the test can call explicitly (e.g. before reopening
// the store file directly) — stop is also registered as a t.Cleanup so a
// test that never calls it still tears the daemon down.
func startDaemonForTest(t *testing.T, bin, runtimeDir, dataDir string, extraEnv ...string) (stop func()) {
	t.Helper()
	env := testXDGEnv(
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XDG_DATA_HOME="+dataDir,
		// Isolate from this machine's real ~/.claude/projects (backfill_test.go's
		// own pattern): without this, the daemon's background backfill importer
		// competes for the sqlite store's write lock against whatever real
		// transcript history happens to exist on the box running the test,
		// which is neither hermetic nor this test's concern.
		"BACKSTORY_CLAUDE_ROOT="+filepath.Join(t.TempDir(), "does-not-exist"),
	)
	env = append(env, extraEnv...)

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test built
	cmd.Env = env
	var out safeBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	}
	t.Cleanup(stop)

	sockPath := filepath.Join(runtimeDir, "backstory", "sock")
	waitForFile(t, sockPath, 2*time.Second)
	return stop
}

// TestMCPShimSurvivesIdlePastFirstLineDeadlineThenSucceeds is the punch's
// hero test (DONE WHEN clause 1): `backstory mcp`, launched from a
// test-spawned harness-named helper the way a real harness launches it,
// completes initialize, then stays idle for LONGER than the daemon's
// first-line deadline (lowered here via BACKSTORY_FIRST_LINE_DEADLINE so the
// test runs fast — the production default stays 5s, untouched), then calls
// status and note: both must succeed, and the note must land in the store.
//
// Before task 40008eea's fix this test is RED: the shim dialed the daemon at
// process start, that connection sat idle through the sleep below, the
// daemon's first-line deadline closed it, and the first tool call afterward
// failed with a broken pipe.
func TestMCPShimSurvivesIdlePastFirstLineDeadlineThenSucceeds(t *testing.T) {
	bin := buildBackstory(t)
	harnessBin := buildMCPHarness(t, harnessName)

	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	const fastDeadline = 200 * time.Millisecond
	stopDaemon := startDaemonForTest(t, bin, runtimeDir, dataDir,
		firstLineDeadlineEnvVar+"="+fastDeadline.String())

	env := testXDGEnv(
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XDG_DATA_HOME="+dataDir,
	)

	mcpCmd := exec.Command(harnessBin, bin, "mcp") //nolint:gosec // both binaries were just built by this test
	mcpCmd.Env = env
	stdin, err := mcpCmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := mcpCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var mcpErr safeBuffer
	mcpCmd.Stderr = &mcpErr
	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start mcpharness: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = mcpCmd.Process.Kill()
		_ = mcpCmd.Wait()
	})

	enc := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	sendAndRead := func(req rpcRequest) rpcResponse {
		t.Helper()
		if err := enc.Encode(req); err != nil {
			t.Fatalf("encode %s request: %v", req.Method, err)
		}
		if !scanner.Scan() {
			t.Fatalf("no response to %s: %v\nstderr:\n%s", req.Method, scanner.Err(), mcpErr.String())
		}
		var resp rpcResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s response %q: %v", req.Method, scanner.Text(), err)
		}
		return resp
	}

	initResp := sendAndRead(rpcRequest{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}

	// The punch's whole scenario: a harness launches the shim at session
	// start and the human doesn't call a tool again for a while — long
	// enough here to clear the (test-fast) first-line deadline several
	// times over.
	time.Sleep(6 * fastDeadline)

	statusResp := sendAndRead(rpcRequest{
		JSONRPC: "2.0", ID: 2, Method: "tools/call",
		Params: map[string]any{"name": mcp.ToolStatus, "arguments": map[string]any{}},
	})
	if statusResp.Error != nil {
		t.Fatalf("tools/call(status) after idling past the deadline: %+v", statusResp.Error)
	}

	noteResp := sendAndRead(rpcRequest{
		JSONRPC: "2.0", ID: 3, Method: "tools/call",
		Params: map[string]any{
			"name":      mcp.ToolNote,
			"arguments": map[string]any{"kind": "note", "text": "note after idling past the deadline"},
		},
	})
	if noteResp.Error != nil {
		t.Fatalf("tools/call(note) after idling past the deadline: %+v", noteResp.Error)
	}
	noteCall := requireCallToolResult(t, noteResp.Result)
	var noteResult mcp.NoteResult
	if err := json.Unmarshal(noteCall.StructuredContent, &noteResult); err != nil {
		t.Fatalf("unmarshal note structuredContent: %v", err)
	}
	if noteResult.ID == "" {
		t.Fatal("note result has no id")
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("close mcp stdin: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- mcpCmd.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("mcpharness exited with error after stdin EOF (want exit 0): %v\nstderr:\n%s", err, mcpErr.String())
		}
	case <-time.After(2 * time.Second):
		_ = mcpCmd.Process.Kill()
		t.Fatalf("mcpharness did not exit within 2s of stdin EOF\nstderr:\n%s", mcpErr.String())
	}

	// Stop the daemon before reopening its store file directly, so there is
	// exactly one writer of the sqlite file at a time.
	stopDaemon()

	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = st.Close() }()
	rec, err := st.GetRecord(noteResult.ID)
	if err != nil {
		t.Fatalf("GetRecord(%s): %v", noteResult.ID, err)
	}
	if rec.Text != "note after idling past the deadline" {
		t.Errorf("stored record text = %q, want the note written after idling", rec.Text)
	}
}

// TestMCPInitializeAndToolsListSucceedWithNoDaemonListening is DONE WHEN
// clause 2: `backstory mcp` answers initialize and tools/list even when no
// daemon is listening at all — before the fix, runMCP dialed the daemon
// socket at startup and exited 1 immediately when that dial failed, so the
// subprocess never got as far as reading a JSON-RPC request from stdin.
func TestMCPInitializeAndToolsListSucceedWithNoDaemonListening(t *testing.T) {
	bin := buildBackstory(t)

	runtimeDir := t.TempDir() // no daemon ever started: no socket file here
	dataDir := t.TempDir()
	env := testXDGEnv(
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XDG_DATA_HOME="+dataDir,
	)

	mcpCmd := exec.Command(bin, "mcp") //nolint:gosec // bin is the binary this test just built
	mcpCmd.Env = env
	stdin, err := mcpCmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := mcpCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var mcpErr safeBuffer
	mcpCmd.Stderr = &mcpErr
	if err := mcpCmd.Start(); err != nil {
		t.Fatalf("start backstory mcp: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = mcpCmd.Process.Kill()
		_ = mcpCmd.Wait()
	})

	enc := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	sendAndRead := func(req rpcRequest) rpcResponse {
		t.Helper()
		if err := enc.Encode(req); err != nil {
			t.Fatalf("encode %s request: %v", req.Method, err)
		}
		if !scanner.Scan() {
			t.Fatalf("no response to %s: %v\nstderr:\n%s", req.Method, scanner.Err(), mcpErr.String())
		}
		var resp rpcResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s response %q: %v", req.Method, scanner.Text(), err)
		}
		return resp
	}

	initResp := sendAndRead(rpcRequest{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if initResp.Error != nil {
		t.Fatalf("initialize error with no daemon listening: %+v", initResp.Error)
	}

	listResp := sendAndRead(rpcRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	if listResp.Error != nil {
		t.Fatalf("tools/list error with no daemon listening: %+v", listResp.Error)
	}
	var listResult struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	if len(listResult.Tools) != 5 {
		t.Errorf("tools/list returned %d tools, want 5", len(listResult.Tools))
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("close mcp stdin: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- mcpCmd.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("backstory mcp exited with error after stdin EOF (no daemon ever listened, want exit 0): %v\nstderr:\n%s", err, mcpErr.String())
		}
	case <-time.After(2 * time.Second):
		_ = mcpCmd.Process.Kill()
		t.Fatalf("backstory mcp did not exit within 2s of stdin EOF\nstderr:\n%s", mcpErr.String())
	}
}

// TestStatusSubcommandAgainstRunningDaemon is DONE WHEN clause 6's success
// path: `backstory status` against a running daemon prints project, capture
// state and budget, and exits 0.
func TestStatusSubcommandAgainstRunningDaemon(t *testing.T) {
	bin := buildBackstory(t)
	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	startDaemonForTest(t, bin, runtimeDir, dataDir)

	env := testXDGEnv(
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XDG_DATA_HOME="+dataDir,
	)
	statusCmd := exec.Command(bin, "status") //nolint:gosec // bin is the binary this test just built
	statusCmd.Env = env
	var out, errOut safeBuffer
	statusCmd.Stdout = &out
	statusCmd.Stderr = &errOut
	if err := statusCmd.Run(); err != nil {
		t.Fatalf("backstory status against a running daemon exited with error: %v\nstdout:\n%sstderr:\n%s",
			err, out.String(), errOut.String())
	}

	got := out.String()
	for _, want := range []string{"project:", "capture:", "budget remaining:"} {
		if !strings.Contains(got, want) {
			t.Errorf("status output = %q, want it to contain %q", got, want)
		}
	}
}

// TestStatusSubcommandWithNoDaemonExitsNonZeroNamingSocketPath is DONE WHEN
// clause 6's failure path: `backstory status` with no daemon listening
// exits non-zero and names the socket path it tried, rather than a generic
// unsupported-health-check failure.
func TestStatusSubcommandWithNoDaemonExitsNonZeroNamingSocketPath(t *testing.T) {
	bin := buildBackstory(t)
	runtimeDir := t.TempDir() // no daemon ever started
	dataDir := t.TempDir()
	env := testXDGEnv(
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XDG_DATA_HOME="+dataDir,
	)

	statusCmd := exec.Command(bin, "status") //nolint:gosec // bin is the binary this test just built
	statusCmd.Env = env
	var out, errOut safeBuffer
	statusCmd.Stdout = &out
	statusCmd.Stderr = &errOut
	err := statusCmd.Run()
	if err == nil {
		t.Fatalf("backstory status with no daemon listening exited 0, want non-zero\nstdout:\n%s", out.String())
	}

	wantSock := filepath.Join(runtimeDir, "backstory", "sock")
	if !strings.Contains(errOut.String(), wantSock) {
		t.Errorf("stderr = %q, want it to name the socket path %q it tried", errOut.String(), wantSock)
	}
}
