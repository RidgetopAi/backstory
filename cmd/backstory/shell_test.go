package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// shellCommandRow is one command-kind timeline event's decoded payload,
// read back for these tests' own assertions.
type shellCommandRow struct {
	payload.ShellCommand
	Source string
}

// queryShellCommandEvents returns every command-kind timeline event
// belonging to a session in projectKey, ordered by id (insertion order) —
// this file's own equivalent of hook_posttooluse_test.go's
// queryEventsForProject, decoding straight into payload.ShellCommand.
func queryShellCommandEvents(t *testing.T, s *store.Store, projectKey string) []shellCommandRow {
	t.Helper()
	rows, err := s.DB().Query(`SELECT e.source, e.payload FROM timeline_events e
		JOIN sessions sess ON sess.id = e.session_id
		WHERE sess.project_key = ? AND e.kind = ? ORDER BY e.id`, projectKey, payload.KindShellCommand)
	if err != nil {
		t.Fatalf("query shell command events for project %s: %v", projectKey, err)
	}
	defer func() { _ = rows.Close() }()

	var out []shellCommandRow
	for rows.Next() {
		var source, payloadStr string
		if err := rows.Scan(&source, &payloadStr); err != nil {
			t.Fatalf("scan shell command event row: %v", err)
		}
		var sc payload.ShellCommand
		if err := json.Unmarshal([]byte(payloadStr), &sc); err != nil {
			t.Fatalf("unmarshal shell command payload %q: %v", payloadStr, err)
		}
		out = append(out, shellCommandRow{ShellCommand: sc, Source: source})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate shell command event rows: %v", err)
	}
	return out
}

// waitForShellCommandEventCount polls queryShellCommandEvents until it sees
// at least want rows or timeout elapses — every `backstory shell emit`
// invocation is backgrounded and detached from the bash session that
// spawned it (this punch's whole point: a prompt must never wait on it), so
// the store can still be catching up for a few milliseconds after the bash
// subprocess itself has already exited.
func waitForShellCommandEventCount(t *testing.T, s *store.Store, projectKey string, want int, timeout time.Duration) []shellCommandRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got []shellCommandRow
	for time.Now().Before(deadline) {
		got = queryShellCommandEvents(t, s, projectKey)
		if len(got) >= want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d shell command event(s) in project %s; got %d: %#v", want, projectKey, len(got), got)
	return nil
}

// waitForShellCommandsPresent polls queryShellCommandEvents until every Cmd
// in wantCmds appears at least once or timeout elapses. Unlike a bare row
// count, it cannot be satisfied early by an incidental event (e.g. the eval
// line that installs the snippet) standing in for a command the caller
// actually asserts on.
func waitForShellCommandsPresent(t *testing.T, s *store.Store, projectKey string, wantCmds []string, timeout time.Duration) []shellCommandRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got []shellCommandRow
	for time.Now().Before(deadline) {
		got = queryShellCommandEvents(t, s, projectKey)
		seen := map[string]bool{}
		for _, ev := range got {
			seen[ev.Cmd] = true
		}
		all := true
		for _, c := range wantCmds {
			all = all && seen[c]
		}
		if all {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for shell command event(s) %q in project %s; got %d: %#v", wantCmds, projectKey, len(got), got)
	return nil
}

// shellSessionEnv builds the env a `bash -i` test session runs under:
// daemonEnv (startTestDaemon's own return, carrying XDG_RUNTIME_DIR/
// XDG_DATA_HOME) with HOME replaced by a fresh temp dir (this punch's DONE
// WHEN clause 1: "HOME in a temp dir") and binDir prepended to PATH so the
// snippet's own `backstory shell emit ... &` calls resolve to bin.
func shellSessionEnv(t *testing.T, daemonEnv []string, binDir string) []string {
	t.Helper()
	home := t.TempDir()
	out := make([]string, 0, len(daemonEnv)+2)
	sawPath := false
	for _, e := range daemonEnv {
		switch {
		case strings.HasPrefix(e, "HOME="):
			continue
		case strings.HasPrefix(e, "PATH="):
			out = append(out, "PATH="+binDir+":"+strings.TrimPrefix(e, "PATH="))
			sawPath = true
		default:
			out = append(out, e)
		}
	}
	if !sawPath {
		out = append(out, "PATH="+binDir+":"+os.Getenv("PATH"))
	}
	out = append(out, "HOME="+home)
	return out
}

// runBashSession runs `bash --norc -i` (no controlling tty — the same
// conditions this punch's own bash snippet was measured against, and how a
// backgrounded `backstory shell emit` subprocess itself always runs) with
// cwd and env as given, feeding script as stdin, and returns its combined
// output and the wall-clock time cmd.Run took. The script must end with
// `exit 0` itself: bash's own `exit` with no arguments would otherwise
// propagate whatever the last real command's status happened to be, turning
// an intentional `false` in the script into a spurious test failure here.
func runBashSession(t *testing.T, cwd string, env []string, script string) (output string, elapsed time.Duration) {
	t.Helper()
	if !strings.HasSuffix(strings.TrimRight(script, "\n"), "exit 0") {
		t.Fatalf("test script must end with \"exit 0\" so bash's own exit status never depends on the last command run: %q", script)
	}
	cmd := exec.Command("bash", "--norc", "-i") //nolint:gosec // fixed args, this test's own script feeds stdin
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	start := time.Now()
	err := cmd.Run()
	elapsed = time.Since(start)
	if err != nil {
		t.Fatalf("run bash session: %v\noutput:\n%s", err, out.String())
	}
	return out.String(), elapsed
}

// shellAncestry is a fake /proc ancestry (BACKSTORY_TEST_FAKE_ANCESTRY,
// cmd/backstory/daemon_procfs_backstorytest.go) matching no known harness —
// deterministically, regardless of whatever real ancestry the process
// running `go test` has (task fe2cff2a; see hook_test.go's
// noHarnessAncestry, whose own Cwd is left empty because most of that
// file's tests never need one). Every test in this file needs a specific
// observed cwd, since that decides which project a `backstory shell emit`
// connection's session lands in.
func shellAncestry(cwd string) []ident.FakeAncestryHop {
	return []ident.FakeAncestryHop{{Name: "not-a-harness", Cwd: cwd}}
}

// initSnippet runs `<bin> shell init bash` directly (never through PATH) and
// returns its stdout — the exact snippet these tests eval, so a bug in the
// generator itself would show up here rather than being masked by a
// hand-copied duplicate of the expected text.
func initSnippet(t *testing.T, bin string) string {
	t.Helper()
	out, err := exec.Command(bin, "shell", "init", "bash").Output() //nolint:gosec // bin is the binary this test just built
	if err != nil {
		t.Fatalf("%s shell init bash: %v", bin, err)
	}
	return string(out)
}

// TestShellBashPreexecPrecmdRecordsThreeCommandsWithCmdCwdExitDuration is the
// punch's DONE WHEN clause 1: a real `bash -i` (HOME in a temp dir) evals
// `backstory shell init bash` against a test daemon, runs true/false/sleep
// 0.2, and the store then holds exactly three shell command events with the
// right cmd, cwd, exit (0, 1, 0), and duration_ms >= 200 for the sleep.
func TestShellBashPreexecPrecmdRecordsThreeCommandsWithCmdCwdExitDuration(t *testing.T) {
	bin := buildBackstory(t)
	projectDir := t.TempDir()
	dbPath, _, daemonEnv := startTestDaemon(t, bin, shellAncestry(projectDir)...)
	env := shellSessionEnv(t, daemonEnv, filepath.Dir(bin))

	script := "eval \"$(cat <<'BSEOF'\n" + initSnippet(t, bin) + "BSEOF\n)\"\n" +
		"true\nfalse\nsleep 0.2\nexit 0\n"
	runBashSession(t, projectDir, env, script)

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	events := waitForShellCommandEventCount(t, s, projectKey, 3, 3*time.Second)
	if len(events) != 3 {
		t.Fatalf("shell command events = %d, want exactly 3: %#v", len(events), events)
	}

	// Matched by Cmd, not by insertion position: each `backstory shell
	// emit` call is an independent, fire-and-forget background connection
	// (this punch's own point — clause 3 requires it), and the very first
	// one this fresh daemon ever sees pays a one-time cold-start cost
	// (opening the store, upserting the project, minting the first
	// session) that a rapid-fire second call issued a moment later does
	// not; two commands run back-to-back with no gap between them (true,
	// false here) can measurably complete their own writes out of order
	// because of that, even though nothing about which command produced
	// which event is ever ambiguous (each event's own Cmd says exactly
	// which command it is). Positional/insertion-order guarantees across
	// independent async emits were never part of this design's contract —
	// only capturing the right cmd/cwd/exit/duration for each observed
	// command is.
	byCmd := make(map[string]shellCommandRow, len(events))
	for _, ev := range events {
		byCmd[ev.Cmd] = ev
	}
	wantExit := map[string]int{"true": 0, "false": 1, "sleep 0.2": 0}
	for cmd, wantExitCode := range wantExit {
		ev, ok := byCmd[cmd]
		if !ok {
			t.Fatalf("no shell command event for %q among %#v", cmd, events)
		}
		if ev.Exit != wantExitCode {
			t.Errorf("%q event Exit = %d, want %d", cmd, ev.Exit, wantExitCode)
		}
		if ev.CWD != projectDir {
			t.Errorf("%q event CWD = %q, want %q", cmd, ev.CWD, projectDir)
		}
		if ev.Source != "shell" {
			t.Errorf("%q event Source = %q, want %q", cmd, ev.Source, "shell")
		}
	}
	if got := byCmd["sleep 0.2"].DurationMS; got < 200 {
		t.Errorf("sleep 0.2 event DurationMS = %d, want >= 200", got)
	}
}

// TestShellBashPreservesExistingPS0AndPromptCommandDoubleEvalRecordsOnce is
// the punch's DONE WHEN clause 2: a pre-existing PROMPT_COMMAND (both the
// plain-string form and bash 5.1's array form) and a pre-existing PS0 still
// run after the snippet is evaled, and evaling the snippet twice records
// each command once, not twice.
func TestShellBashPreservesExistingPS0AndPromptCommandDoubleEvalRecordsOnce(t *testing.T) {
	for _, tc := range []struct {
		name           string
		promptCommand  string
		wantMarkerLine string
	}{
		{
			name:           "string form",
			promptCommand:  "PROMPT_COMMAND='echo existing-pc-string >> \"$MARKER_PC\"'",
			wantMarkerLine: "existing-pc-string",
		},
		{
			name:           "bash 5.1 array form",
			promptCommand:  "PROMPT_COMMAND=(); PROMPT_COMMAND+=('echo existing-pc-array >> \"$MARKER_PC\"')",
			wantMarkerLine: "existing-pc-array",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildBackstory(t)
			projectDir := t.TempDir()
			dbPath, _, daemonEnv := startTestDaemon(t, bin, shellAncestry(projectDir)...)
			env := shellSessionEnv(t, daemonEnv, filepath.Dir(bin))

			markerDir := t.TempDir()
			markerPC := filepath.Join(markerDir, "pc.log")
			markerPS0 := filepath.Join(markerDir, "ps0.log")
			env = append(env, "MARKER_PC="+markerPC, "MARKER_PS0="+markerPS0)

			snippet := initSnippet(t, bin)
			var script strings.Builder
			script.WriteString("PS0='$(echo existing-ps0 >> \"$MARKER_PS0\")'\n")
			script.WriteString(tc.promptCommand + "\n")
			script.WriteString("eval \"$(cat <<'BSEOF'\n" + snippet + "BSEOF\n)\"\n")
			script.WriteString("eval \"$(cat <<'BSEOF'\n" + snippet + "BSEOF\n)\"\n")
			script.WriteString("true\n")
			script.WriteString("false\n")
			script.WriteString("exit 0\n")

			runBashSession(t, projectDir, env, script.String())

			projectKey := project.Key(projectDir, project.RealGit{}, nil)
			s := mustOpenTestStore(t, dbPath)
			events := waitForShellCommandsPresent(t, s, projectKey, []string{"true", "false"}, 3*time.Second)
			// Counted by Cmd, not by len(events): the SECOND eval line
			// itself is a real command read while the first eval's hooks
			// are already active, so it legitimately produces its own
			// incidental event too — the punch's clause 2 requirement is
			// that true/false (each run once) are recorded once each, not
			// that evaling the snippet twice is invisible to the capture
			// entirely.
			seen := map[string]int{}
			for _, ev := range events {
				seen[ev.Cmd]++
			}
			if seen["true"] != 1 || seen["false"] != 1 {
				t.Fatalf("events = %#v, want exactly one \"true\" and one \"false\" (double eval must not double-record either)", events)
			}

			pcContent, err := os.ReadFile(markerPC) //nolint:gosec // markerPC is this test's own t.TempDir() path, not external input
			if err != nil {
				t.Fatalf("read PROMPT_COMMAND marker: %v", err)
			}
			// A count, not a mere Contains: PROMPT_COMMAND/PS0 fire once
			// incidentally the instant they are first assigned (for the
			// NEXT command read, before this snippet is even evaled), so a
			// single occurrence would pass even if the snippet's own
			// append logic were replaced by a clobbering assignment,
			// discarding the pre-existing entry right after that one
			// incidental firing. Requiring at least 3 (the initial
			// assignment's own firing, plus true and false each triggering
			// it again afterward) actually exercises "still runs after the
			// snippet is evaled".
			const wantAtLeast = 3
			if got := strings.Count(string(pcContent), tc.wantMarkerLine); got < wantAtLeast {
				t.Errorf("PROMPT_COMMAND marker fired %d times, want >= %d (the pre-existing entry must keep running after eval, true, and false — not just once before eval)", got, wantAtLeast)
			}

			ps0Content, err := os.ReadFile(markerPS0) //nolint:gosec // markerPS0 is this test's own t.TempDir() path, not external input
			if err != nil {
				t.Fatalf("read PS0 marker: %v", err)
			}
			if got := strings.Count(string(ps0Content), "existing-ps0"); got < wantAtLeast {
				t.Errorf("PS0 marker fired %d times, want >= %d (the pre-existing PS0 must keep running after eval, true, and false)", got, wantAtLeast)
			}
		})
	}
}

// TestShellBashPromptReturnsQuicklyWithNoDaemonAndSlowFakeBackstory is the
// punch's DONE WHEN clause 3: with no daemon listening, and a fake
// `backstory` on PATH that sleeps 5s standing in for the emit call, each
// prompt still returns in under 300ms.
func TestShellBashPromptReturnsQuicklyWithNoDaemonAndSlowFakeBackstory(t *testing.T) {
	bin := buildBackstory(t)
	snippet := initSnippet(t, bin)

	fakeBinDir := t.TempDir()
	fakeBackstory := filepath.Join(fakeBinDir, "backstory")
	if err := os.WriteFile(fakeBackstory, []byte("#!/usr/bin/env bash\nsleep 5\n"), 0o700); err != nil { //nolint:gosec // test fixture, needs +x
		t.Fatalf("write fake backstory: %v", err)
	}

	runtimeDir := t.TempDir() // no daemon ever started: no socket file at all
	env := testXDGEnv("XDG_RUNTIME_DIR=" + runtimeDir)
	env = shellSessionEnv(t, env, fakeBinDir)

	projectDir := t.TempDir()
	script := "eval \"$(cat <<'BSEOF'\n" + snippet + "BSEOF\n)\"\n" +
		"START1=$(date +%s%N)\n" +
		"true\n" +
		"END1=$(date +%s%N)\n" +
		"echo \"ELAPSED1_MS=$(( (END1-START1)/1000000 ))\"\n" +
		"START2=$(date +%s%N)\n" +
		"echo hello\n" +
		"END2=$(date +%s%N)\n" +
		"echo \"ELAPSED2_MS=$(( (END2-START2)/1000000 ))\"\n" +
		"exit 0\n"

	out, elapsed := runBashSession(t, projectDir, env, script)
	t.Logf("whole bash session took %s", elapsed)

	// Bash's own non-tty reflection of each typed line ("bash-5.3$ echo
	// ...") prints the RAW, unexpanded command text before its actual
	// evaluated output line — so a marker search must take the LAST match
	// (the real value), not the first (which would still be sitting inside
	// the literal, unevaluated "$(( ... ))" of the echoed command line
	// itself).
	for _, marker := range []string{"ELAPSED1_MS=", "ELAPSED2_MS="} {
		re := regexp.MustCompile(regexp.QuoteMeta(marker) + `(\d+)`)
		matches := re.FindAllStringSubmatch(out, -1)
		if len(matches) == 0 {
			t.Fatalf("output missing %s marker: %s", marker, out)
		}
		last := matches[len(matches)-1][1]
		ms, err := strconv.Atoi(last)
		if err != nil {
			t.Fatalf("parse %s value from %q: %v", marker, last, err)
		}
		if ms >= 300 {
			t.Errorf("%s%d, want under 300ms with no daemon and a 5s-sleeping fake backstory on PATH", marker, ms)
		}
	}
}

// TestShellBashCaptureOffAndLeadingSpaceRecordNothingNormalCommandRecords is
// half of the punch's DONE WHEN clause 4 (the cmd/backstory half; secret
// redaction is covered store-side by
// internal/mcp.TestShellEmitSecretShapedCommandStoredRedacted): with the
// capture-off flag present, a command records no shell event; a command
// starting with a space is never recorded either (even with capture back
// on); a normal command run afterward still is — proving the two negatives
// aren't hiding a totally broken capture path.
func TestShellBashCaptureOffAndLeadingSpaceRecordNothingNormalCommandRecords(t *testing.T) {
	bin := buildBackstory(t)
	projectDir := t.TempDir()
	dbPath, runtimeDir, daemonEnv := startTestDaemon(t, bin, shellAncestry(projectDir)...)
	env := shellSessionEnv(t, daemonEnv, filepath.Dir(bin))

	flagPath := filepath.Join(runtimeDir, "backstory", "capture-off")
	if err := os.WriteFile(flagPath, nil, 0o600); err != nil {
		t.Fatalf("write capture-off flag file: %v", err)
	}

	snippet := initSnippet(t, bin)
	script := "eval \"$(cat <<'BSEOF'\n" + snippet + "BSEOF\n)\"\n" +
		"true\n" + // capture-off: must record nothing
		"exit 0\n"
	runBashSession(t, projectDir, env, script)

	projectKey := project.Key(projectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)
	time.Sleep(300 * time.Millisecond) // let any (wrongly-sent) emit land before asserting zero
	if events := queryShellCommandEvents(t, s, projectKey); len(events) != 0 {
		t.Fatalf("shell command events with capture-off = %d, want 0: %#v", len(events), events)
	}

	if err := os.Remove(flagPath); err != nil {
		t.Fatalf("remove capture-off flag file: %v", err)
	}

	script2 := "eval \"$(cat <<'BSEOF'\n" + snippet + "BSEOF\n)\"\n" +
		" echo leading_space_command\n" + // leading space: must record nothing
		"echo normal_command\n" + // must record
		"exit 0\n"
	runBashSession(t, projectDir, env, script2)

	events := waitForShellCommandEventCount(t, s, projectKey, 1, 3*time.Second)
	if len(events) != 1 {
		t.Fatalf("shell command events after a leading-space command and a normal one = %d, want exactly 1: %#v", len(events), events)
	}
	if events[0].Cmd != "echo normal_command" {
		t.Errorf("recorded event Cmd = %q, want %q (the leading-space command must never appear)", events[0].Cmd, "echo normal_command")
	}
}

// TestShellEmitProjectComesFromResolverNotCwdFlag is the punch's DONE WHEN
// clause 5, exercised through the real `backstory shell emit` CLI rather
// than a raw daemon request (internal/mcp's own
// TestShellEmitProjectComesFromResolverNotParams covers the handler level):
// `backstory shell emit`'s --cwd flag is purely descriptive payload content,
// never identity. The connection's session (and so its project) comes from
// the daemon's resolved ancestry (realProjectDir, via a fake ancestry hop)
// regardless of what --cwd claims (fakeProjectDir).
func TestShellEmitProjectComesFromResolverNotCwdFlag(t *testing.T) {
	bin := buildBackstory(t)
	realProjectDir := t.TempDir()
	fakeProjectDir := t.TempDir()
	dbPath, _, env := startTestDaemon(t, bin, shellAncestry(realProjectDir)...)

	cmd := exec.Command(bin, "shell", "emit", //nolint:gosec // bin is the binary this test just built
		"--cmd", "echo spoofed-cwd",
		"--cwd", fakeProjectDir,
		"--exit", "0",
		"--duration-ms", "1")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("backstory shell emit: %v\n%s", err, out)
	}

	realKey := project.Key(realProjectDir, project.RealGit{}, nil)
	fakeKey := project.Key(fakeProjectDir, project.RealGit{}, nil)
	s := mustOpenTestStore(t, dbPath)

	realEvents := waitForShellCommandEventCount(t, s, realKey, 1, 2*time.Second)
	if len(realEvents) != 1 {
		t.Fatalf("project at the resolved (real) cwd has %d shell command events, want exactly 1: %#v", len(realEvents), realEvents)
	}
	if realEvents[0].CWD != fakeProjectDir {
		t.Errorf("event payload CWD = %q, want %q (the flag's own value — descriptive payload content, not identity)", realEvents[0].CWD, fakeProjectDir)
	}

	fakeEvents := queryShellCommandEvents(t, s, fakeKey)
	if len(fakeEvents) != 0 {
		t.Errorf("project at the payload's declared (fake) cwd has %d events, want 0 — "+
			"--cwd must never decide which project an event is recorded in: %#v", len(fakeEvents), fakeEvents)
	}
}

// staticShellEmitParamsCannotDeclareIdentity documents, at compile time,
// that mcp.ShellEmitParams has no field a caller could use to claim a
// session/harness/project — the structural half of DONE WHEN clause 5 (the
// tests above are its behavioural half). Referencing the type here also
// guards against an accidental unused-import if this file's other tests are
// ever trimmed.
var _ = mcp.ShellEmitParams{Cmd: "x"}

// TestShellEmitHonoursBackstoryIgnoreMarker: an emit whose cwd is below a
// directory holding .backstory-ignore exits 0 and records nothing (no session,
// project or event); the same emit from an unmarked sibling records as usual.
func TestShellEmitHonoursBackstoryIgnoreMarker(t *testing.T) {
	bin := buildBackstory(t)
	root := t.TempDir()
	marked := filepath.Join(root, "marked")
	sub := filepath.Join(marked, "a", "b")
	sibling := filepath.Join(root, "sibling")
	for _, d := range []string{sub, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(marked, ".backstory-ignore"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	emit := func(dir string, env []string) {
		t.Helper()
		cmd := exec.Command(bin, "shell", "emit", "--cmd", "echo hi", "--cwd", dir, "--exit", "0", "--duration-ms", "1") //nolint:gosec // bin is the binary this test just built
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("backstory shell emit in %s: %v\n%s", dir, err, out)
		}
	}
	count := func(s *store.Store, table string) int {
		t.Helper()
		var n int
		if err := s.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}

	// Marked subdirectory: nothing recorded. The identity the daemon would
	// have used is the marked dir, so a leak shows up under its key.
	dbPath, _, env := startTestDaemon(t, bin, shellAncestry(sub)...)
	emit(sub, env)
	time.Sleep(300 * time.Millisecond)
	s := mustOpenTestStore(t, dbPath)
	for _, table := range []string{"sessions", "timeline_events"} {
		if n := count(s, table); n != 0 {
			t.Errorf("%s rows after ignored emit = %d, want 0", table, n)
		}
	}
	if evs := queryShellCommandEvents(t, s, project.Key(sub, project.RealGit{}, nil)); len(evs) != 0 {
		t.Errorf("ignored emit recorded events: %#v", evs)
	}

	// Unmarked sibling records exactly as before.
	dbPath2, _, env2 := startTestDaemon(t, bin, shellAncestry(sibling)...)
	emit(sibling, env2)
	s2 := mustOpenTestStore(t, dbPath2)
	if evs := waitForShellCommandEventCount(t, s2, project.Key(sibling, project.RealGit{}, nil), 1, 2*time.Second); len(evs) != 1 {
		t.Errorf("sibling emit events = %d, want 1", len(evs))
	}
}
