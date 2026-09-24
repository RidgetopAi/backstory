package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// runCaptureSubprocess runs `backstory capture <subcommand>` against env and
// returns its combined stdout+stderr and exit code.
func runCaptureSubprocess(t *testing.T, bin string, env []string, subcommand string) (out string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, "capture", subcommand) //nolint:gosec // bin is the binary this test just built, subcommand is a fixed literal per call site
	cmd.Env = env
	outBytes, err := cmd.CombinedOutput()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run backstory capture %s: %v (output: %s)", subcommand, err, outBytes)
	}
	return string(outBytes), exitCode
}

// runStatusSubprocess runs `backstory status` against env and returns its
// combined stdout+stderr and exit code.
func runStatusSubprocess(t *testing.T, bin string, env []string) (out string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, "status") //nolint:gosec // bin is the binary this test just built
	cmd.Env = env
	outBytes, err := cmd.CombinedOutput()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run backstory status: %v (output: %s)", err, outBytes)
	}
	return string(outBytes), exitCode
}

// requestNoteAsHarness is requestBlockAsHarness's counterpart for a "note"
// daemon request that may legitimately fail (a capture-off rejection): it
// spawns harnessBin with its cwd set to cwd, exactly like
// requestBlockAsHarness, but returns the run error instead of calling
// t.Fatalf on it, so a caller can assert on either outcome.
func requestNoteAsHarness(t *testing.T, harnessBin, sockPath, cwd, sessionID string, params json.RawMessage) (out string, err error) {
	t.Helper()
	cmd := exec.Command(harnessBin, sockPath, sessionID, "note", string(params)) //nolint:gosec // harnessBin is the binary this test just built, not external input
	cmd.Dir = cwd
	outBytes, runErr := cmd.CombinedOutput()
	return string(outBytes), runErr
}

// countRecordsForProject returns how many rows records holds for
// projectKey — the same table SCHEMA.md invariant 8 says a write must never
// reach while capture is off.
func countRecordsForProject(t *testing.T, s *store.Store, projectKey string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM records WHERE project_key = ?`, projectKey).Scan(&n); err != nil {
		t.Fatalf("count records for project %s: %v", projectKey, err)
	}
	return n
}

// TestCaptureOffBlocksNoteInsertsStatusReportsOffCaptureOnRestores is the
// punch's DONE WHEN clause 2: `backstory capture off`, then the
// session-start hook and a note write path, insert nothing; `backstory
// status` and the daemon's own MCP status both report capture off;
// `backstory capture on` restores writes.
//
// RA-MUTATION-PROBE candidate: point runCaptureOff/runCaptureOn at a path
// other than captureOffPath() (e.g. a sibling file) -> RED (the note call
// below succeeds instead of being rejected, and the record count becomes
// 1 instead of staying 0); restored to the shared captureOffPath() -> GREEN.
func TestCaptureOffBlocksNoteInsertsStatusReportsOffCaptureOnRestores(t *testing.T) {
	bin := buildBackstory(t)
	harnessBin := buildHarnessClient(t, harnessName)
	dbPath, runtimeDir, env := startTestDaemon(t, bin)
	sockPath := filepath.Join(runtimeDir, "backstory", "sock")
	projectDir := t.TempDir()
	projectKey := project.Key(projectDir, project.RealGit{})

	// Sanity: capture starts on, both by the CLI's own read and by a note
	// call actually succeeding.
	if out, exitCode := runCaptureSubprocess(t, bin, env, "status"); exitCode != 0 || !strings.Contains(out, "capture: on") {
		t.Fatalf("capture status before any toggle = %q (exit %d), want it to contain %q and exit 0", out, exitCode, "capture: on")
	}

	if out, exitCode := runCaptureSubprocess(t, bin, env, "off"); exitCode != 0 || !strings.Contains(out, "capture: off") {
		t.Fatalf("capture off = %q (exit %d), want it to contain %q and exit 0", out, exitCode, "capture: off")
	}

	// The session-start hook still renders (it is a read), but must insert
	// no record or event for this project.
	stdout, stderr, exitCode := runHookSubprocess(t, bin, env, map[string]any{
		"session_id": "capture-off-session-start",
		"cwd":        projectDir,
	})
	if exitCode != 0 {
		t.Fatalf("hook session-start with capture off: exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Errorf("hook session-start with capture off printed nothing, want the rendered block (a read, not a write)")
	}

	s := mustOpenTestStore(t, dbPath)
	if n := countRecordsForProject(t, s, projectKey); n != 0 {
		t.Fatalf("records for project after session-start with capture off = %d, want 0", n)
	}

	// A note write path (the daemon's "note" method, reachable from any
	// socket peer independent of the hook) must be refused, and insert
	// nothing.
	noteOut, noteErr := requestNoteAsHarness(t, harnessBin, sockPath, projectDir, "capture-off-note-session",
		json.RawMessage(`{"kind":"note","text":"must not be written while capture is off"}`))
	if noteErr == nil {
		t.Fatalf("note call with capture off succeeded, want it rejected: %s", noteOut)
	}
	if n := countRecordsForProject(t, s, projectKey); n != 0 {
		t.Fatalf("records for project after a rejected note with capture off = %d, want 0", n)
	}

	// backstory status (which calls the daemon's own MCP status) reports
	// capture off.
	statusOut, statusExit := runStatusSubprocess(t, bin, env)
	if statusExit != 0 {
		t.Fatalf("backstory status with capture off: exit code = %d, want 0 (output: %s)", statusExit, statusOut)
	}
	if !strings.Contains(statusOut, "capture: off") {
		t.Errorf("backstory status output = %q, want it to contain %q", statusOut, "capture: off")
	}

	// capture on restores writes.
	if out, exitCode := runCaptureSubprocess(t, bin, env, "on"); exitCode != 0 || !strings.Contains(out, "capture: on") {
		t.Fatalf("capture on = %q (exit %d), want it to contain %q and exit 0", out, exitCode, "capture: on")
	}

	statusOut, statusExit = runStatusSubprocess(t, bin, env)
	if statusExit != 0 {
		t.Fatalf("backstory status with capture on: exit code = %d, want 0 (output: %s)", statusExit, statusOut)
	}
	if !strings.Contains(statusOut, "capture: on") {
		t.Errorf("backstory status output after capture on = %q, want it to contain %q", statusOut, "capture: on")
	}

	noteOut, noteErr = requestNoteAsHarness(t, harnessBin, sockPath, projectDir, "capture-on-note-session",
		json.RawMessage(`{"kind":"note","text":"written now that capture is back on"}`))
	if noteErr != nil {
		t.Fatalf("note call with capture on failed: %v (output: %s)", noteErr, noteOut)
	}
	if n := countRecordsForProject(t, s, projectKey); n != 1 {
		t.Fatalf("records for project after a note with capture on = %d, want 1", n)
	}
}

// TestCaptureOnAndOffAreIdempotent guards a corner the critic's mutation
// (skip the refusal / diverge the flag path) would not otherwise reach:
// calling capture off twice, or capture on with nothing to remove, must
// still exit 0 and leave the flag file in the expected state.
func TestCaptureOnAndOffAreIdempotent(t *testing.T) {
	bin := buildBackstory(t)
	runtimeDir := t.TempDir()
	env := testXDGEnv("XDG_RUNTIME_DIR=" + runtimeDir)

	if out, exitCode := runCaptureSubprocess(t, bin, env, "on"); exitCode != 0 || !strings.Contains(out, "capture: on") {
		t.Fatalf("capture on with no flag file present = %q (exit %d), want it to contain %q and exit 0", out, exitCode, "capture: on")
	}
	for i := 0; i < 2; i++ {
		if out, exitCode := runCaptureSubprocess(t, bin, env, "off"); exitCode != 0 || !strings.Contains(out, "capture: off") {
			t.Fatalf("capture off (iteration %d) = %q (exit %d), want it to contain %q and exit 0", i, out, exitCode, "capture: off")
		}
	}
	if out, exitCode := runCaptureSubprocess(t, bin, env, "status"); exitCode != 0 || !strings.Contains(out, "capture: off") {
		t.Fatalf("capture status = %q (exit %d), want it to contain %q and exit 0", out, exitCode, "capture: off")
	}
}
