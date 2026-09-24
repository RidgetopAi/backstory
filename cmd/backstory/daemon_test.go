package main

import (
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
	cmd.Env = testXDGEnv(
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

// harnessName is the recognised harness (internal/ident.KnownHarnesses)
// TestDaemonStartMigratesOldShapeStoreThroughNormalReadPath's self-spawned
// helper process is built and exec'd as. It is asserted against
// ident.KnownHarnesses below rather than assumed, so this test breaks
// loudly instead of silently if that table ever drops "claude".
const harnessName = "claude"

// buildHarnessClient compiles cmd/backstory/testdata/harnessclient to a
// binary whose path's final component is exactly name. Linux sets a
// process's /proc/<pid>/status "Name:" (comm) from the basename of the path
// passed to execve, not argv[0], so exec'ing this binary directly (never
// through a shell or PATH lookup) gives the daemon's ancestry walk a comm
// of name regardless of what built or launched the test binary itself.
func buildHarnessClient(t *testing.T, name string) string {
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
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/harnessclient") //nolint:gosec // fixed source path, fixed tmp-dir output path
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build harnessclient: %v\n%s", err, out)
	}
	return bin
}

// requestBlockAsHarness is punch e96d1a21's clause 4: instead of the go
// test process itself dialing the daemon socket (which would make the
// resolved identity depend on whatever process tree happens to be running
// `go test` — a Claude Code session, a plain shell, a cron job), this
// spawns harnessBin as its own child process with its cwd set to cwd, and
// that child is the one that dials the socket and issues the block
// request. The daemon's /proc ancestry walk starts at that child's pid,
// matches harnessName at distance zero, and stops there — so the ancestry
// above the child, i.e. whatever ran this test suite, is never consulted
// and cannot change the result.
func requestBlockAsHarness(t *testing.T, harnessBin, sockPath, cwd, sessionID string) string {
	t.Helper()

	cmd := exec.Command(harnessBin, sockPath, sessionID) //nolint:gosec // harnessBin is the binary this test just built, not external input
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("harness client block request: %v\n%s", err, out)
	}
	return string(out)
}

// TestDaemonStartMigratesOldShapeStoreThroughNormalReadPath is proof (3)
// and (4) for task e96d1a21: starting `backstory daemon` against a store
// whose tool.use events predate the payload contract (task 8ba5487a) ends
// with the SessionStart block's delta reporting the correct file count,
// observed through the daemon's normal block-request read path — not by
// calling the migration directly. There is no subcommand and no flag:
// store.Open inside runDaemon is the only thing that runs it.
//
// The block request itself is issued from a harness process this test
// spawns and controls (requestBlockAsHarness), not from the go test
// process — see that function's doc comment for why: the daemon resolves a
// caller's project by walking /proc for the nearest recognised harness, so
// a test that dialed the socket directly from `go test` would have its
// result depend on whatever ancestry happened to be running the suite.
//
// projectDir is a fresh t.TempDir(), not this package's own directory
// (which sits inside the backstory git checkout, and so would resolve to
// the same project key whether it came from the spawned harness's cwd or
// from whatever real ancestor happened to be running the suite — masking
// exactly the bug clause 4 exists to catch). Because projectDir is outside
// any git working tree, project.Key falls back to the raw path itself
// (internal/project/key.go), so it is guaranteed to differ from the
// project key any real invoking process's cwd would produce. Only a client
// whose OWN cwd was set to projectDir — i.e. only the harness this test
// spawned and pointed at projectDir — can make the block resolve this
// project at all; a client that inherited its ancestry's cwd instead would
// resolve a different (or no) project and read back "0 sessions, 0 files
// touched", regardless of what environment ran the test.
func TestDaemonStartMigratesOldShapeStoreThroughNormalReadPath(t *testing.T) {
	bin := buildBackstory(t)
	harnessBin := buildHarnessClient(t, harnessName)

	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	projectDir := t.TempDir()

	projectKey := project.Key(projectDir, project.RealGit{})
	if cwd, err := os.Getwd(); err == nil && projectKey == project.Key(cwd, project.RealGit{}) {
		t.Fatalf("projectDir %q resolved to the same project key as this test's own cwd %q — the isolation this test depends on is broken", projectDir, cwd)
	}

	seedOldShapeStore(t, dbPath, projectKey, "sess-daemon-migration")

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built, not external input
	cmd.Env = testXDGEnv(
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

	block := requestBlockAsHarness(t, harnessBin, sockPath, projectDir, "hook-session-daemon-migration")

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

// seedRecallStore opens dbPath through store.Open (creating it), then
// inserts one backfilled session in projectKey and, through it, two
// decision records (oldDecisionID first, newDecisionID second — so
// newDecisionID is the newer one) and one handoff record, plus a single
// timeline event appended after the handoff so recall's recent-timeline
// summary has something to report. It closes the store before returning so
// the daemon this test starts next owns the only open connection.
func seedRecallStore(t *testing.T, dbPath, projectKey, cwd string) (handoffID, oldDecisionID, newDecisionID string) {
	t.Helper()

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("seed: store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.UpsertProject(store.Project{Key: projectKey, Toplevel: cwd, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("seed: upsert project %s: %v", projectKey, err)
	}
	sessionID, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: cwd, ProjectKey: projectKey, StartedAt: time.Now(), Origin: store.OriginBackfilled,
	})
	if err != nil {
		t.Fatalf("seed: start session: %v", err)
	}

	oldDecisionID, err = st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindDecision,
		Text: "seeded: chose approach A", SessionID: sessionID, ProjectKey: projectKey,
	})
	if err != nil {
		t.Fatalf("seed: insert old decision: %v", err)
	}
	newDecisionID, err = st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindDecision,
		Text: "seeded: chose approach B", SessionID: sessionID, ProjectKey: projectKey,
	})
	if err != nil {
		t.Fatalf("seed: insert new decision: %v", err)
	}
	handoffID, err = st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
		Text: "seeded: resume here", SessionID: sessionID, ProjectKey: projectKey,
	})
	if err != nil {
		t.Fatalf("seed: insert handoff: %v", err)
	}

	if _, err := st.AppendEvent(store.Event{
		TS: time.Now(), Kind: "test.event", SessionID: sessionID, Source: "test", Payload: "{}",
	}); err != nil {
		t.Fatalf("seed: append event: %v", err)
	}

	return handoffID, oldDecisionID, newDecisionID
}

// seedOtherProjectDecision opens dbPath through store.Open and inserts a
// single decision record in a different project (otherProjectKey), the
// project-isolation half of
// TestDaemonRecallIsProjectAnchoredThroughHarnessSpawnedHelper: this record
// must never appear in a recall for projectKey.
func seedOtherProjectDecision(t *testing.T, dbPath, otherProjectKey, otherCwd string) string {
	t.Helper()

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("seed other project: store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.UpsertProject(store.Project{Key: otherProjectKey, Toplevel: otherCwd, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("seed other project: upsert project %s: %v", otherProjectKey, err)
	}
	sessionID, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: otherCwd, ProjectKey: otherProjectKey, StartedAt: time.Now(), Origin: store.OriginBackfilled,
	})
	if err != nil {
		t.Fatalf("seed other project: start session: %v", err)
	}
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindDecision,
		Text: "seeded: a different project's private decision", SessionID: sessionID, ProjectKey: otherProjectKey,
	})
	if err != nil {
		t.Fatalf("seed other project: insert decision: %v", err)
	}
	return id
}

// requestRecallAsHarness is requestBlockAsHarness's recall counterpart
// (DONE WHEN clause 6: same pattern as
// TestDaemonStartMigratesOldShapeStoreThroughNormalReadPath): it spawns
// harnessBin as its own child with cwd set to cwd, so the daemon's /proc
// ancestry walk resolves the project from THAT child's cwd, never from
// whatever ran this test suite. harnessBin is told to speak the "recall"
// tool through an actual mcp.Server (clause 1: "through the MCP shim"), not
// a hand-rolled DaemonRequest line.
func requestRecallAsHarness(t *testing.T, harnessBin, sockPath, cwd, sessionID string, params json.RawMessage) string {
	t.Helper()

	args := []string{sockPath, sessionID, mcp.ToolRecall}
	if params != nil {
		args = append(args, string(params))
	}
	cmd := exec.Command(harnessBin, args...) //nolint:gosec // harnessBin is the binary this test just built, not external input
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("harness client recall request: %v\n%s", err, out)
	}
	return string(out)
}

// TestDaemonRecallIsProjectAnchoredThroughHarnessSpawnedHelper is the
// punch's DONE WHEN clause 1, proof kind "cmd/backstory tests": through the
// MCP shim over a real daemon socket, recall with no anchor, called from a
// test-spawned harness-named helper whose cwd is a seeded project, returns
// that project's whole ledger anchored on the project itself (task
// cdf2f9eb's internal/recall engine call — no more v0-stub handoff/decision
// special-casing), newest record first by sequence; every item carries its
// id and provenance tier. A record from a different project seeded in the
// same store does not appear. Clause 6: this test constructs its own
// harness-named helper (requestRecallAsHarness -> buildHarnessClient)
// rather than inheriting the suite's ancestry, the same pattern as
// TestDaemonStartMigratesOldShapeStoreThroughNormalReadPath.
//
// RA-MUTATION-PROBE: handleRecall's `projectKey := id.ProjectKey`
// (mcp/recall.go) replaced with "" -> RED (this test: ProjectKey no longer
// equals the seeded project, and the other project's decision id leaks into
// Items since a project-anchored recall with an empty project key matches
// no project's records — either failure fires); restored -> GREEN.
func TestDaemonRecallIsProjectAnchoredThroughHarnessSpawnedHelper(t *testing.T) {
	bin := buildBackstory(t)
	harnessBin := buildHarnessClient(t, harnessName)

	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	projectDir := t.TempDir()
	otherProjectDir := t.TempDir()

	projectKey := project.Key(projectDir, project.RealGit{})
	otherProjectKey := project.Key(otherProjectDir, project.RealGit{})
	if projectKey == otherProjectKey {
		t.Fatalf("projectDir %q and otherProjectDir %q resolved to the same project key %q — the isolation this test depends on is broken",
			projectDir, otherProjectDir, projectKey)
	}

	handoffID, oldDecisionID, newDecisionID := seedRecallStore(t, dbPath, projectKey, projectDir)
	otherDecisionID := seedOtherProjectDecision(t, dbPath, otherProjectKey, otherProjectDir)

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built, not external input
	cmd.Env = testXDGEnv(
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

	raw := requestRecallAsHarness(t, harnessBin, sockPath, projectDir, "hook-session-daemon-recall", nil)

	var result mcp.RecallResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal recall result %q: %v", raw, err)
	}

	if result.ProjectKey != projectKey {
		t.Errorf("ProjectKey = %q, want %q", result.ProjectKey, projectKey)
	}
	if result.Anchor != "project" {
		t.Errorf("Anchor = %q, want project", result.Anchor)
	}
	if len(result.Items) != 3 {
		t.Fatalf("len(Items) = %d, want 3 (handoff + 2 decisions): %s", len(result.Items), raw)
	}
	wantOrder := []string{handoffID, newDecisionID, oldDecisionID}
	for i, id := range wantOrder {
		if result.Items[i].ID != id {
			t.Errorf("Items[%d].ID = %q, want %q (newest first by sequence)", i, result.Items[i].ID, id)
		}
	}
	for _, item := range result.Items {
		if item.Tier == "" {
			t.Errorf("item %s has no Tier, want a provenance tier", item.ID)
		}
	}
	if strings.Contains(raw, otherDecisionID) {
		t.Errorf("recall for %s leaked a different project's decision id %s: %s", projectKey, otherDecisionID, raw)
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

// TestDaemonRecallOnEmptyProjectReturnsHonestEmptyResultThroughHarnessSpawnedHelper
// is the punch's DONE WHEN clause 3, exercised through the same
// harness-spawned pattern as the clause 1 test above: a project the daemon
// has never seen a record or event for still returns a project_key, not an
// error and not not-implemented.
func TestDaemonRecallOnEmptyProjectReturnsHonestEmptyResultThroughHarnessSpawnedHelper(t *testing.T) {
	bin := buildBackstory(t)
	harnessBin := buildHarnessClient(t, harnessName)

	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	projectDir := t.TempDir()
	projectKey := project.Key(projectDir, project.RealGit{})

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built, not external input
	cmd.Env = testXDGEnv(
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

	raw := requestRecallAsHarness(t, harnessBin, sockPath, projectDir, "hook-session-daemon-recall-empty", nil)

	var result mcp.RecallResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal recall result %q: %v", raw, err)
	}
	if result.ProjectKey != projectKey {
		t.Errorf("ProjectKey = %q, want %q", result.ProjectKey, projectKey)
	}
	if len(result.Items) != 0 {
		t.Errorf("Items = %+v, want empty for an empty project", result.Items)
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
