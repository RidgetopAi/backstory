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

	"github.com/RidgetopAi/backstory/internal/backfill/claude"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// buildBackstoryHarness compiles cmd/backstory itself (unlike
// buildHarnessClient, which compiles the separate testdata/harnessclient
// program) to a binary whose path's final component is exactly harnessName
// ("claude") — see buildHarnessClient's doc comment for why exec'ing a
// binary at a harness-named path, never through a shell or PATH lookup,
// gives the daemon's /proc ancestry walk that comm regardless of what ran
// the test suite itself. Used so `backstory hook post-tool-use` is, for these
// tests, literally the process the daemon's ancestry walk finds Harness
// "claude" at distance zero for — the same guarantee buildHarnessClient
// gives the raw block/recall requests in daemon_test.go, applied to the
// real hook subcommand this punch (task 04b1cb40) adds.
func buildBackstoryHarness(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), harnessName)
	cmd := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec // bin is a t.TempDir() path this test built, not external input
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build backstory as %s: %v\n%s", harnessName, err, out)
	}
	return bin
}

// runHookInDir runs `backstory hook <hookArgs...>` as a subprocess with its
// cwd set to dir — the daemon's /proc ancestry walk falls back to the
// direct peer's own cwd when no known harness is found above it in
// ancestry (internal/ident.Resolver.Resolve), and lands on the harness's
// cwd when one is (as buildBackstoryHarness's "claude"-named bin gives it
// here) — either way dir is what the daemon resolves this call's project
// from, never anything the JSON payload itself claims.
func runHookInDir(t *testing.T, bin, dir string, env []string, hookArgs []string, payload map[string]any) (stdout, stderr string, exitCode int, elapsed time.Duration) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	args := append([]string{"hook"}, hookArgs...)
	cmd := exec.Command(bin, args...) //nolint:gosec // bin is the binary this test just built, hookArgs are fixed literals
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(b)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	start := time.Now()
	err = cmd.Run()
	elapsed = time.Since(start)
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run backstory hook %v: %v (stderr: %s)", hookArgs, err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exitCode, elapsed
}

type toolEventRow struct {
	Kind    string
	Source  string
	Payload string
}

// queryEventsForProject returns every timeline event belonging to a session
// in projectKey, ordered by id — the same membership EventsSinceID uses,
// read directly here so a test can assert on Source (EventsSinceID's own
// TimelineEvent already exposes it, but going through the raw table keeps
// this test independent of block/mcp's own read paths).
func queryEventsForProject(t *testing.T, s *store.Store, projectKey string) []toolEventRow {
	t.Helper()
	rows, err := s.DB().Query(`SELECT e.kind, e.source, e.payload FROM timeline_events e
		JOIN sessions sess ON sess.id = e.session_id
		WHERE sess.project_key = ? ORDER BY e.id`, projectKey)
	if err != nil {
		t.Fatalf("query events for project %s: %v", projectKey, err)
	}
	defer func() { _ = rows.Close() }()
	var out []toolEventRow
	for rows.Next() {
		var r toolEventRow
		if err := rows.Scan(&r.Kind, &r.Source, &r.Payload); err != nil {
			t.Fatalf("scan event row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate event rows: %v", err)
	}
	return out
}

// TestHookPostToolUseEditRecordsOneLiveEventAndBlockCountsFile is task
// 04b1cb40's DONE WHEN clause 1: feeding a recorded Claude Code PostToolUse
// JSON for an Edit of a file into `backstory hook post-tool-use`, run under
// a test-spawned harness-named helper (buildBackstoryHarness) whose cwd is
// a seeded project, results in exactly one new timeline event in that
// project whose source is not backfill, and the SessionStart block for that
// project then counts that file as touched.
func TestHookPostToolUseEditRecordsOneLiveEventAndBlockCountsFile(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()
	filePath := filepath.Join(projectDir, "main.go")

	editPayload := map[string]any{
		"session_id":      "claude-live-edit-session",
		"cwd":             projectDir,
		"transcript_path": filepath.Join(projectDir, "transcript.jsonl"),
		"hook_event_name": "PostToolUse",
		"tool_name":       "Edit",
		"tool_use_id":     "toolu_live_edit_1",
		"tool_input": map[string]any{
			"file_path":  filePath,
			"old_string": "foo",
			"new_string": "bar",
		},
		"tool_response": map[string]any{
			"filePath": filePath,
			"success":  true,
		},
	}

	stdout, stderr, exitCode, _ := runHookInDir(t, bin, projectDir, env, []string{"post-tool-use"}, editPayload)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty (a PostToolUse hook's stdout is not injected as context)", stdout)
	}

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := queryEventsForProject(t, s, projectKey)
	if len(events) != 1 {
		t.Fatalf("project has %d timeline events after one live Edit capture, want exactly 1: %#v", len(events), events)
	}
	ev := events[0]
	if ev.Kind != payload.KindToolUse {
		t.Errorf("event kind = %q, want %q", ev.Kind, payload.KindToolUse)
	}
	if ev.Source != "posttooluse" {
		t.Errorf("event source = %q, want %q (not backfill)", ev.Source, "posttooluse")
	}
	var tu payload.ToolUse
	if err := json.Unmarshal([]byte(ev.Payload), &tu); err != nil {
		t.Fatalf("unmarshal tool.use payload %q: %v", ev.Payload, err)
	}
	if tu.Path != filePath {
		t.Errorf("tool.use path = %q, want %q", tu.Path, filePath)
	}
	if tu.Name != "Edit" {
		t.Errorf("tool.use name = %q, want %q", tu.Name, "Edit")
	}

	block, _, blockExit, _ := runHookInDir(t, bin, projectDir, env, []string{"session-start"}, map[string]any{
		"session_id": "claude-live-edit-block-reader",
		"cwd":        projectDir,
	})
	if blockExit != 0 {
		t.Fatalf("session-start exit code = %d, want 0", blockExit)
	}
	if !strings.Contains(block, "1 files touched") {
		t.Errorf("block = %q, want it to contain %q", block, "1 files touched")
	}
}

// TestHookPostToolUseProjectComesFromResolverNotDeclaredSession is task
// 04b1cb40's DONE WHEN clause 6's "take the project from the payload's cwd
// instead of the observed identity" guard, applied to post_tool_use: two
// invocations against the same daemon, from the same real harness process
// ancestry and cwd (buildBackstoryHarness's "claude"-named bin, dir
// projectDir), differ only in their payload's declared session_id — an
// arbitrary caller-chosen join key (socket.DeclaredFields), never identity.
// Both events must land in the one real project the daemon observed; if the
// handler instead attributed an event to whatever the connection declared
// as its "session" (rather than the connection's own daemon-minted
// sessionID, itself bound to id.ProjectKey at connect time), the event
// would fail to insert at all (sessions.id is a real foreign key, and
// "session-A"/"session-B" name no row), so both calls would silently record
// nothing instead of the two events this test expects — exactly the kind of
// bug AGENT-CONTRACT.md §Observed identity exists to catch, and the RA-
// MUTATION-PROBE receipt in this task's commit body demonstrates by mutating
// dispatchDaemonRequest's post_tool_use case to pass req.Session in place of
// sessionID.
func TestHookPostToolUseProjectComesFromResolverNotDeclaredSession(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()

	_, stderrA, exitA, _ := runHookInDir(t, bin, projectDir, env, []string{"post-tool-use"}, map[string]any{
		"session_id":  "session-A",
		"cwd":         projectDir,
		"tool_name":   "Edit",
		"tool_use_id": "toolu_identity_A",
		"tool_input":  map[string]any{"file_path": filepath.Join(projectDir, "a.go")},
	})
	if exitA != 0 {
		t.Fatalf("first invocation exit code = %d, want 0 (stderr: %s)", exitA, stderrA)
	}

	_, stderrB, exitB, _ := runHookInDir(t, bin, projectDir, env, []string{"post-tool-use"}, map[string]any{
		"session_id":  "session-B",
		"cwd":         projectDir,
		"tool_name":   "Edit",
		"tool_use_id": "toolu_identity_B",
		"tool_input":  map[string]any{"file_path": filepath.Join(projectDir, "b.go")},
	})
	if exitB != 0 {
		t.Fatalf("second invocation exit code = %d, want 0 (stderr: %s)", exitB, stderrB)
	}

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	// Only tool.use events: a session ending also records session.git_state
	// (task c2573b35), which says nothing about where captures land.
	var events []toolEventRow
	for _, e := range queryEventsForProject(t, s, projectKey) {
		if e.Kind == "tool.use" {
			events = append(events, e)
		}
	}
	if len(events) != 2 {
		t.Fatalf("project has %d tool.use events after two live captures from the same real cwd, want 2 — "+
			"a declared session_id must never change which project (or whether) an event is recorded: %#v",
			len(events), events)
	}
}

// TestHookPostToolUseProjectComesFromRealCwdNotPayloadCWD is DONE WHEN
// clause 6's "take the project from the payload's cwd instead of the
// observed identity" guard applied literally to the payload's own "cwd"
// field: the hook subprocess's real OS cwd is realProjectDir (what the
// daemon's SO_PEERCRED + /proc walk actually observes), but the PostToolUse
// JSON it sends declares a "cwd" field pointing at a different directory,
// fakeProjectDir, the way a compromised or buggy harness could claim any
// cwd it likes. The event must land in the project the daemon *observed*
// (realProjectDir) and never in the project the payload merely *declared*
// (fakeProjectDir) — mcp.PostToolUseParams intentionally carries no CWD
// field at all (internal/mcp/hook.go) so there is nothing for a handler to
// misuse here even by accident.
func TestHookPostToolUseProjectComesFromRealCwdNotPayloadCWD(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, env := startTestDaemon(t, bin)
	realProjectDir := t.TempDir()
	fakeProjectDir := t.TempDir()

	_, stderr, exitCode, _ := runHookInDir(t, bin, realProjectDir, env, []string{"post-tool-use"}, map[string]any{
		"session_id":  "claude-cwd-spoof-session",
		"cwd":         fakeProjectDir,
		"tool_name":   "Edit",
		"tool_use_id": "toolu_cwd_spoof_1",
		"tool_input":  map[string]any{"file_path": filepath.Join(realProjectDir, "spoofed.go")},
	})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}

	s := mustOpenTestStore(t, dbPath)
	realKey := project.Key(realProjectDir, project.RealGit{}, nil)
	fakeKey := project.Key(fakeProjectDir, project.RealGit{}, nil)

	realEvents := queryEventsForProject(t, s, realKey)
	if len(realEvents) != 1 {
		t.Fatalf("project at the real observed cwd has %d events, want exactly 1: %#v", len(realEvents), realEvents)
	}
	fakeEvents := queryEventsForProject(t, s, fakeKey)
	if len(fakeEvents) != 0 {
		t.Fatalf("project at the payload's declared (fake) cwd has %d events, want 0 — "+
			"a payload's own \"cwd\" field must never decide which project an event is recorded in: %#v",
			len(fakeEvents), fakeEvents)
	}
}

// TestHookPostToolUseBashRecordsCommandAndExitCode is half of task
// 04b1cb40's DONE WHEN clause 2: a Bash PostToolUse payload is recorded
// with its command, and with its exit code when the payload carries one.
func TestHookPostToolUseBashRecordsCommandAndExitCode(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()

	bashPayload := map[string]any{
		"session_id":  "claude-bash-session",
		"cwd":         projectDir,
		"tool_name":   "Bash",
		"tool_use_id": "toolu_bash_exit_3",
		"tool_input": map[string]any{
			"command":     "exit 3",
			"description": "deliberately fail",
		},
		"tool_response": map[string]any{
			"stdout":      "",
			"stderr":      "",
			"interrupted": false,
			"exit_code":   3,
		},
	}

	_, stderr, exitCode, _ := runHookInDir(t, bin, projectDir, env, []string{"post-tool-use"}, bashPayload)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := queryEventsForProject(t, s, projectKey)
	if len(events) != 2 {
		t.Fatalf("project has %d timeline events after one live Bash capture, want 2 (tool.use + tool.result): %#v", len(events), events)
	}

	var gotUse, gotResult bool
	for _, ev := range events {
		switch ev.Kind {
		case payload.KindToolUse:
			gotUse = true
			var tu payload.ToolUse
			if err := json.Unmarshal([]byte(ev.Payload), &tu); err != nil {
				t.Fatalf("unmarshal tool.use payload %q: %v", ev.Payload, err)
			}
			if tu.Command != "exit 3" {
				t.Errorf("tool.use command = %q, want %q", tu.Command, "exit 3")
			}
			if ev.Source != "posttooluse" {
				t.Errorf("tool.use source = %q, want %q", ev.Source, "posttooluse")
			}
		case payload.KindToolResult:
			gotResult = true
			var tr payload.ToolResult
			if err := json.Unmarshal([]byte(ev.Payload), &tr); err != nil {
				t.Fatalf("unmarshal tool.result payload %q: %v", ev.Payload, err)
			}
			if tr.Exit == nil {
				t.Errorf("tool.result exit = nil, want 3")
			} else if *tr.Exit != 3 {
				t.Errorf("tool.result exit = %d, want 3", *tr.Exit)
			}
		}
	}
	if !gotUse {
		t.Errorf("no tool.use event recorded for the Bash call")
	}
	if !gotResult {
		t.Errorf("no tool.result event recorded for the Bash call")
	}
}

// TestHookPostToolUseBashNoExitCodeRecordsNilNeverZero is the other half of
// task 04b1cb40's DONE WHEN clause 2: a Bash PostToolUse payload with no
// exit code produces an event with no exit code, never 0 (SCHEMA.md: a
// writer must never invent 0).
func TestHookPostToolUseBashNoExitCodeRecordsNilNeverZero(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, _, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()

	bashPayload := map[string]any{
		"session_id":  "claude-bash-no-exit-session",
		"cwd":         projectDir,
		"tool_name":   "Bash",
		"tool_use_id": "toolu_bash_no_exit",
		"tool_input": map[string]any{
			"command": "sleep 0.01",
		},
		"tool_response": map[string]any{
			"stdout":      "",
			"stderr":      "",
			"interrupted": false,
			// deliberately no exit_code field at all
		},
	}

	_, stderr, exitCode, _ := runHookInDir(t, bin, projectDir, env, []string{"post-tool-use"}, bashPayload)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := queryEventsForProject(t, s, projectKey)

	var sawResult bool
	for _, ev := range events {
		if ev.Kind != payload.KindToolResult {
			continue
		}
		sawResult = true
		if strings.Contains(ev.Payload, `"exit"`) {
			t.Errorf("tool.result payload = %q, must not carry an \"exit\" key at all when the source payload had none", ev.Payload)
		}
		var tr payload.ToolResult
		if err := json.Unmarshal([]byte(ev.Payload), &tr); err != nil {
			t.Fatalf("unmarshal tool.result payload %q: %v", ev.Payload, err)
		}
		if tr.Exit != nil {
			t.Errorf("tool.result exit = %d, want nil (never invent 0)", *tr.Exit)
		}
	}
	if !sawResult {
		t.Fatalf("no tool.result event recorded for the Bash call: %#v", events)
	}
}

// TestHookPostToolUseNoSocketExitsQuicklyWritesNothing is half of task
// 04b1cb40's DONE WHEN clause 4: with no daemon listening, `backstory hook
// post-tool-use` exits 0 in under 1 second and writes nothing.
func TestHookPostToolUseNoSocketExitsQuicklyWritesNothing(t *testing.T) {
	bin := buildBackstoryHarness(t)
	runtimeDir := t.TempDir() // no daemon ever started here: no socket file
	env := testXDGEnv("XDG_RUNTIME_DIR=" + runtimeDir)
	projectDir := t.TempDir()

	stdout, stderr, exitCode, elapsed := runHookInDir(t, bin, projectDir, env, []string{"post-tool-use"}, map[string]any{
		"session_id":  "no-daemon-session",
		"cwd":         projectDir,
		"tool_name":   "Edit",
		"tool_use_id": "toolu_no_daemon",
		"tool_input":  map[string]any{"file_path": filepath.Join(projectDir, "f.go")},
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if elapsed > time.Second {
		t.Errorf("took %s to exit with no daemon listening, want under 1s", elapsed)
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Errorf("stderr = %q, want exactly one non-empty line", stderr)
	}
}

// TestHookPostToolUseCaptureOffRecordsNothing is the other half of task
// 04b1cb40's DONE WHEN clause 4: with the capture-off flag file present, it
// records nothing.
func TestHookPostToolUseCaptureOffRecordsNothing(t *testing.T) {
	bin := buildBackstoryHarness(t)
	dbPath, runtimeDir, env := startTestDaemon(t, bin)
	projectDir := t.TempDir()

	flagPath := filepath.Join(runtimeDir, "backstory", "capture-off")
	if err := os.WriteFile(flagPath, []byte{}, 0o600); err != nil {
		t.Fatalf("write capture-off flag file %s: %v", flagPath, err)
	}

	stdout, stderr, exitCode, _ := runHookInDir(t, bin, projectDir, env, []string{"post-tool-use"}, map[string]any{
		"session_id":  "capture-off-session",
		"cwd":         projectDir,
		"tool_name":   "Edit",
		"tool_use_id": "toolu_capture_off",
		"tool_input":  map[string]any{"file_path": filepath.Join(projectDir, "f.go")},
	})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}

	// Scoped to this test's own seeded project, not a whole-table count: the
	// daemon's on-start Claude backfill (runClaudeBackfillOnce) may import
	// real transcripts from this host's own $HOME/.claude/projects into
	// unrelated projects, independent of anything this test does.
	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := queryEventsForProject(t, s, projectKey)
	if len(events) != 0 {
		t.Errorf("project has %d timeline events with capture-off set, want 0: %#v", len(events), events)
	}
}

// TestHookPostToolUseLiveCaptureDedupsAgainstLaterClaudeBackfill is task
// 04b1cb40's DONE WHEN clause 3: after a tool use is captured live, running
// the Claude backfill over a transcript containing that same tool use adds
// no second event for it.
//
// The daemon is stopped with SIGTERM before claude.Import runs directly
// against dbPath — store.Open holds the same sqlite file the live daemon
// used, so importing while the daemon is still up would race two processes
// over one file, exactly what TestDaemonStartMigratesOldShapeStoreThroughNormalReadPath's
// stop-then-reopen pattern avoids.
func TestHookPostToolUseLiveCaptureDedupsAgainstLaterClaudeBackfill(t *testing.T) {
	bin := buildBackstoryHarness(t)
	toolUseID := "toolu_dedup_shared_1"
	sessionID := "claude-dedup-session"

	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	env := testXDGEnv(
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XDG_DATA_HOME="+dataDir,
	)
	projectDir := t.TempDir()
	filePath := filepath.Join(projectDir, "main.go")

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built
	cmd.Env = env
	var out safeBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	sockPath := filepath.Join(runtimeDir, "backstory", "sock")
	waitForFile(t, sockPath, 2*time.Second)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")

	_, stderr, exitCode, _ := runHookInDir(t, bin, projectDir, env, []string{"post-tool-use"}, map[string]any{
		"session_id":  sessionID,
		"cwd":         projectDir,
		"tool_name":   "Edit",
		"tool_use_id": toolUseID,
		"tool_input":  map[string]any{"file_path": filePath},
	})
	if exitCode != 0 {
		t.Fatalf("live capture: exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
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

	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()

	before := countToolUseEventsWithID(t, st, toolUseID)
	if before != 1 {
		t.Fatalf("tool.use events with tool_use_id=%s after live capture = %d, want 1", toolUseID, before)
	}

	transcriptRoot := t.TempDir()
	slugDir := filepath.Join(transcriptRoot, "some-project-slug")
	if err := os.MkdirAll(slugDir, 0o750); err != nil {
		t.Fatalf("mkdir transcript slug dir: %v", err)
	}
	transcriptPath := filepath.Join(slugDir, sessionID+".jsonl")
	writeDedupTranscript(t, transcriptPath, sessionID, projectDir, filePath, toolUseID)

	if _, err := claude.Import(st, claude.Options{Root: transcriptRoot, Git: project.RealGit{}}); err != nil {
		t.Fatalf("claude.Import: %v", err)
	}

	after := countToolUseEventsWithID(t, st, toolUseID)
	if after != 1 {
		t.Errorf("tool.use events with tool_use_id=%s after backfilling a transcript containing the same tool use = %d, want 1 (unchanged — no second event)", toolUseID, after)
	}
}

func countToolUseEventsWithID(t *testing.T, st *store.Store, toolUseID string) int {
	t.Helper()
	var count int
	err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events WHERE kind = ? AND json_extract(payload, '$.tool_use_id') = ?`,
		payload.KindToolUse, toolUseID).Scan(&count)
	if err != nil {
		t.Fatalf("count tool.use events with tool_use_id=%s: %v", toolUseID, err)
	}
	return count
}

// writeDedupTranscript writes a minimal two-line Claude transcript (a user
// prompt line, then an assistant line whose one tool_use block reuses
// toolUseID) — the shape internal/backfill/claude/line.go's transcriptLine
// and contentBlock unmarshal.
func writeDedupTranscript(t *testing.T, path, sessionID, cwd, filePath, toolUseID string) {
	t.Helper()
	now := time.Now().UTC()

	userLine := map[string]any{
		"type":      "user",
		"uuid":      "uuid-dedup-user-1",
		"sessionId": sessionID,
		"cwd":       cwd,
		"timestamp": now.Format(time.RFC3339Nano),
		"message": map[string]any{
			"role":    "user",
			"content": "please edit main.go",
		},
	}
	assistantLine := map[string]any{
		"type":      "assistant",
		"uuid":      "uuid-dedup-asst-1",
		"sessionId": sessionID,
		"cwd":       cwd,
		"timestamp": now.Add(time.Second).Format(time.RFC3339Nano),
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{
					"type":  "tool_use",
					"id":    toolUseID,
					"name":  "Edit",
					"input": map[string]any{"file_path": filePath},
				},
			},
		},
	}

	var buf bytes.Buffer
	for _, line := range []map[string]any{userLine, assistantLine} {
		b, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal transcript line: %v", err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write transcript %s: %v", path, err)
	}
}
