package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

// envWithout returns a copy of env with every entry for any of keys
// removed — used to strip inherited XDG_RUNTIME_DIR / XDG_DATA_HOME /
// XDG_STATE_HOME before appending fresh test values, so the result never
// carries a variable twice. A duplicate entry is a real hazard here, not
// just noise: the exec'd child resolves it to the LAST occurrence, but
// anything in the test process itself that scans env by hand (as this
// file's tests used to, via a now-deleted first-match envValue helper)
// would silently see the FIRST occurrence instead — the invoking user's
// real dir, not the test's temp one.
func envWithout(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		drop := false
		for _, k := range keys {
			if strings.HasPrefix(e, k+"=") {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, e)
		}
	}
	return out
}

// testXDGEnv returns os.Environ() with any inherited XDG_RUNTIME_DIR,
// XDG_DATA_HOME, and XDG_STATE_HOME entries removed, then overrides (each
// an already-formatted "KEY=value" string) appended. It is the one helper
// every test in this package that spawns a backstory process must build
// its child env through — hand-rolling append(os.Environ(), "XDG_RUNTIME_DIR=…")
// lets an inherited value survive alongside the test's own, which is
// exactly what let task 7c121b4b's first-match env reader pick the
// invoking user's real runtime dir over the test's temp one.
func testXDGEnv(overrides ...string) []string {
	return append(envWithout(os.Environ(), "XDG_RUNTIME_DIR", "XDG_DATA_HOME", "XDG_STATE_HOME"), overrides...)
}

// startTestDaemon starts `backstory daemon` as a subprocess against fresh
// runtime/data dirs and waits for its socket to appear. It returns the
// store db path, the runtime dir it chose (so callers never need to dig it
// back out of env), and the env every client subprocess (hook, mcp) must
// share to reach this daemon. Any inherited XDG_RUNTIME_DIR / XDG_DATA_HOME
// is stripped before the fresh ones are appended, so env carries each
// exactly once.
func startTestDaemon(t *testing.T, bin string) (dbPath, runtimeDir string, env []string) {
	t.Helper()
	runtimeDir = t.TempDir()
	dataDir := t.TempDir()
	env = testXDGEnv(
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

	sockPath := filepath.Join(runtimeDir, "backstory", "sock")
	waitForFile(t, sockPath, 2*time.Second)
	dbPath = filepath.Join(dataDir, "backstory", "backstory.db")
	return dbPath, runtimeDir, env
}

// runHookSubprocess runs `backstory hook session-start` as a subprocess
// against env (the daemon's env, or an env with no daemon running), feeding
// payload as its stdin, and returns its stdout, stderr and exit code.
func runHookSubprocess(t *testing.T, bin string, env []string, payload map[string]any) (stdout, stderr string, exitCode int) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	cmd := exec.Command(bin, "hook", "session-start") //nolint:gosec // bin is the binary this test just built
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(b)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run backstory hook session-start: %v (stderr: %s)", err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func mustOpenTestStore(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

type sessionRow struct {
	Agent            string
	HarnessSessionID string
	CWD              string
}

func querySessionByHarnessSessionID(t *testing.T, s *store.Store, harnessSessionID string) sessionRow {
	t.Helper()
	var row sessionRow
	err := s.DB().QueryRow(
		`SELECT agent, harness_session_id, cwd FROM sessions WHERE harness_session_id = ?`,
		harnessSessionID).Scan(&row.Agent, &row.HarnessSessionID, &row.CWD)
	if err != nil {
		t.Fatalf("query session with harness_session_id=%q: %v", harnessSessionID, err)
	}
	return row
}

// TestHookSessionStartPrintsBlockAndRecordsHarnessSessionID is the punch's
// DONE WHEN clause 1's first half: `backstory hook session-start`, fed a
// Claude SessionStart payload on stdin against a daemon started in this
// test, prints the block and exits 0; the resulting session row carries
// harness_session_id = the payload's session_id.
func TestHookSessionStartPrintsBlockAndRecordsHarnessSessionID(t *testing.T) {
	bin := buildBackstory(t)
	dbPath, _, env := startTestDaemon(t, bin)

	stdout, stderr, exitCode := runHookSubprocess(t, bin, env, map[string]any{
		"session_id":      "claude-session-abc123",
		"cwd":             "/home/brian/proj",
		"transcript_path": "/home/brian/.claude/projects/proj/abc123.jsonl",
		"source":          "startup",
		"hook_event_name": "SessionStart",
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatalf("stdout is empty, want the rendered block (stderr: %s)", stderr)
	}
	// A fresh project with no history renders the named empty-state body,
	// preceded by the Backstory header — that IS the block, so this also
	// proves the hook printed the daemon's actual rendered output, not some
	// placeholder.
	wantEmpty := block.HeaderLine + "\n\n" + block.EmptyProjectLine
	if got := strings.TrimSpace(stdout); got != wantEmpty {
		t.Errorf("stdout = %q, want exactly %q for a fresh project", got, wantEmpty)
	}

	s := mustOpenTestStore(t, dbPath)
	row := querySessionByHarnessSessionID(t, s, "claude-session-abc123")
	if row.HarnessSessionID != "claude-session-abc123" {
		t.Errorf("sessions.harness_session_id = %q, want %q", row.HarnessSessionID, "claude-session-abc123")
	}
}

// TestHookSessionStartIdentityComesFromResolverNotPayload is the punch's
// DONE WHEN clause 1's identity requirement: "the session's identity/tier
// still comes from the daemon's resolver (a payload naming a different
// session_id or a tier changes nothing about identity)". Two hook
// invocations against the same daemon, from the same test process (so the
// real SO_PEERCRED + /proc ancestry the resolver observes is identical both
// times), differ only in their payload's session_id and one extra field
// (tier) no daemon request schema even has a slot for. Both resulting
// sessions must show the SAME daemon-observed agent and cwd; only
// harness_session_id — a join key — may differ.
//
// Mutation probe (mcp/daemon.go's startSession made the declared "session"
// join key override CWD: `cwd := id.CWD; if v := id.Declared["session"];
// v != "" { cwd = v }`): "hook_test.go:186: session A cwd = \"session-A\",
// session B cwd = \"session-B\"; want identical (identity comes from the
// resolver, not the payload)" -- restoring `CWD: id.CWD` turns it back
// GREEN.
func TestHookSessionStartIdentityComesFromResolverNotPayload(t *testing.T) {
	bin := buildBackstory(t)
	dbPath, _, env := startTestDaemon(t, bin)

	_, stderrA, exitA := runHookSubprocess(t, bin, env, map[string]any{
		"session_id": "session-A",
		"cwd":        "/home/brian/proj",
	})
	if exitA != 0 {
		t.Fatalf("first hook invocation exit code = %d, want 0 (stderr: %s)", exitA, stderrA)
	}

	// tier is not a field sessionStartPayload even declares; a real harness
	// would never send it, but a hostile or buggy one might, and it must be
	// silently dropped exactly like NoteParams drops it (AGENT-CONTRACT.md
	// §The never-list, item 2).
	_, stderrB, exitB := runHookSubprocess(t, bin, env, map[string]any{
		"session_id": "session-B",
		"cwd":        "/home/brian/proj",
		"tier":       "human-declared",
	})
	if exitB != 0 {
		t.Fatalf("second hook invocation exit code = %d, want 0 (stderr: %s)", exitB, stderrB)
	}

	s := mustOpenTestStore(t, dbPath)
	rowA := querySessionByHarnessSessionID(t, s, "session-A")
	rowB := querySessionByHarnessSessionID(t, s, "session-B")

	if rowA.Agent != rowB.Agent {
		t.Errorf("session A agent = %q, session B agent = %q; want identical (identity comes from the resolver, not the payload)",
			rowA.Agent, rowB.Agent)
	}
	if rowA.CWD != rowB.CWD {
		t.Errorf("session A cwd = %q, session B cwd = %q; want identical (identity comes from the resolver, not the payload)",
			rowA.CWD, rowB.CWD)
	}
	if rowA.HarnessSessionID == rowB.HarnessSessionID {
		t.Errorf("harness_session_id did not differ between the two payloads; the fixture is not exercising the join key")
	}
}

// TestHookSessionStartNoSocketPrintsNothingExactlyOneStderrLineExit0 is the
// punch's DONE WHEN clause 1's failure posture: with no socket present, the
// hook must never break a harness boot — nothing on stdout, exactly one
// line on stderr, exit 0.
func TestHookSessionStartNoSocketPrintsNothingExactlyOneStderrLineExit0(t *testing.T) {
	bin := buildBackstory(t)
	runtimeDir := t.TempDir() // no daemon ever started here: no socket file
	env := testXDGEnv("XDG_RUNTIME_DIR=" + runtimeDir)

	stdout, stderr, exitCode := runHookSubprocess(t, bin, env, map[string]any{
		"session_id": "no-daemon-session",
		"cwd":        "/home/brian/proj",
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Errorf("stderr = %q, want exactly one non-empty line", stderr)
	}
}

// TestStartTestDaemonEnvHasNoDuplicateXDGVars guards against the class of
// bug task 7c121b4b fixed: startTestDaemon used to append its fresh
// XDG_RUNTIME_DIR / XDG_DATA_HOME onto os.Environ() without stripping any
// inherited value, so on a runner (or a real desktop) that already set
// XDG_RUNTIME_DIR, env carried it twice. The exec'd child resolves a
// duplicate to its LAST entry, but anything scanning env by hand — as this
// package's tests used to, via a first-match envValue helper — would
// silently read the FIRST entry instead: the invoking user's real runtime
// dir, not the test's temp one. Asserting exactly one entry of each here
// makes that duplication impossible to reintroduce unnoticed.
func TestStartTestDaemonEnvHasNoDuplicateXDGVars(t *testing.T) {
	bin := buildBackstory(t)
	_, _, env := startTestDaemon(t, bin)

	for _, key := range []string{"XDG_RUNTIME_DIR", "XDG_DATA_HOME"} {
		count := 0
		for _, e := range env {
			if strings.HasPrefix(e, key+"=") {
				count++
			}
		}
		if count != 1 {
			t.Errorf("env has %d entries for %s, want exactly 1: %v", count, key, env)
		}
	}
}

// TestTestXDGEnvOverridesInheritedRuntimeDir guards the shared testXDGEnv
// helper directly (task 5e87946c): if testXDGEnv ever stopped stripping an
// inherited XDG_RUNTIME_DIR before appending the test's own value — say,
// by keeping the inherited entry ahead of the override instead of dropping
// it — the result would carry two entries, and a first-match reader would
// silently resolve to the inherited one instead of the test's, reproducing
// the class of bug task 7c121b4b fixed.
func TestTestXDGEnvOverridesInheritedRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/should-not-be-read/by-any-test")
	want := t.TempDir()

	env := testXDGEnv("XDG_RUNTIME_DIR=" + want)

	count := 0
	var got string
	for _, e := range env {
		if strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
			count++
			if count == 1 {
				got = strings.TrimPrefix(e, "XDG_RUNTIME_DIR=")
			}
		}
	}
	if count != 1 {
		t.Fatalf("env has %d XDG_RUNTIME_DIR entries, want exactly 1: %v", count, env)
	}
	if got != want {
		t.Errorf("XDG_RUNTIME_DIR = %q, want %q", got, want)
	}
}
