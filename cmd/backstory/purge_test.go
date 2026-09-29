package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

const purgeProject = "acme-purge"

type purgeFixture struct {
	dbPath          string
	a, b, c         string // sessions: a (t0), b (t0+2h), c (t0+4h) in purgeProject
	aEvents         int
	recordID        string
	t0              time.Time
	lastEventID     int64
	aFirstEventID   int64
	sessionLessID   int64
	otherSessionEvs int
}

func execPurge(t *testing.T, bin string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"purge"}, args...)...) //nolint:gosec // bin is the binary this test just built
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(nil) // never a terminal
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec returns *ExitError directly
		return out.String(), errb.String(), ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run purge: %v", err)
	}
	return out.String(), errb.String(), 0
}

// seedPurgeStore creates three sessions in purgeProject (started 2h apart),
// events for each, one session-less OS event inside the window, and a record
// whose evidence points into session a's events.
func seedPurgeStore(t *testing.T, dbPath string) purgeFixture {
	t.Helper()
	return seedPurgeStoreFor(t, dbPath, purgeProject)
}

func seedPurgeStoreFor(t *testing.T, dbPath, key string) purgeFixture {
	t.Helper()
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	f := purgeFixture{dbPath: dbPath, t0: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)}
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: key, FirstSeen: f.t0}); err != nil {
		t.Fatal(err)
	}
	mk := func(i int, harness string) string {
		id, err := st.StartSession(store.StartSessionParams{
			Agent: "claude", HarnessSessionID: harness, CWD: key, ProjectKey: key,
			StartedAt: f.t0.Add(time.Duration(i) * 2 * time.Hour), Origin: store.OriginBackfilled,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.a, f.b, f.c = mk(0, "h-a"), mk(1, "h-b"), mk(2, "h-c")
	ev := func(sid string, at time.Time, n int) []int64 {
		var ids []int64
		for i := 0; i < n; i++ {
			id, err := st.AppendEvent(store.Event{TS: at.Add(time.Duration(i) * time.Second), Kind: "tool.use", SessionID: sid, Source: "backfill", Payload: `{"name":"Bash"}`})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return ids
	}
	aIDs := ev(f.a, f.t0, 3)
	f.aEvents, f.aFirstEventID = 3, aIDs[0]
	ev(f.b, f.t0.Add(2*time.Hour), 2)
	ev(f.c, f.t0.Add(4*time.Hour), 4)
	f.otherSessionEvs = 6
	id, err := st.AppendEvent(store.Event{TS: f.t0.Add(3 * time.Hour), Kind: "shell.command", Source: "shell", Payload: `{"cmd":"ls"}`})
	if err != nil {
		t.Fatal(err)
	}
	f.sessionLessID = id
	f.lastEventID = id
	f.recordID, err = st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent, Actor: "seed"}, Kind: store.KindNote,
		Text: "note citing purged evidence", SessionID: f.a, ProjectKey: key, Evidence: aIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func countRows(t *testing.T, dbPath, query string, args ...any) int {
	t.Helper()
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var n int
	if err := st.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func tableCounts(t *testing.T, dbPath string) [3]int {
	return [3]int{
		countRows(t, dbPath, `SELECT COUNT(*) FROM timeline_events`),
		countRows(t, dbPath, `SELECT COUNT(*) FROM sessions`),
		countRows(t, dbPath, `SELECT COUNT(*) FROM purge_log`),
	}
}

// TestPurgeSessionYesErasesOnlyThatSession is DONE WHEN clause 1.
func TestPurgeSessionYesErasesOnlyThatSession(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	f := seedPurgeStore(t, dbPath)
	recordsBefore := countRows(t, dbPath, `SELECT COUNT(*) FROM records`)
	totalBefore := countRows(t, dbPath, `SELECT COUNT(*) FROM timeline_events`)

	stdout, stderr, code := execPurge(t, bin, env, "--session", f.a, "--yes")
	if code != 0 {
		t.Fatalf("purge exit %d stderr=%s", code, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "purged 1 sessions, 3 events"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM timeline_events WHERE session_id = ?`, f.a); n != 0 {
		t.Errorf("session a still has %d events", n)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM timeline_events`); n != totalBefore-3 {
		t.Errorf("total events = %d, want %d (other sessions untouched)", n, totalBefore-3)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM sessions WHERE id = ? AND purged_at IS NOT NULL`, f.a); n != 1 {
		t.Errorf("session a purged_at not set")
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM sessions WHERE purged_at IS NOT NULL`); n != 1 {
		t.Errorf("purged_at set on %d sessions, want 1", n)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM purge_log WHERE sessions = 1 AND events = 3`); n != 1 {
		t.Errorf("purge_log rows matching counts = %d, want 1", n)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM purge_log`); n != 1 {
		t.Errorf("purge_log rows = %d, want 1", n)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM records`); n != recordsBefore {
		t.Errorf("records = %d, want %d unchanged", n, recordsBefore)
	}
}

// TestPurgeDryRunPrintsExactCountsAndChangesNothing is DONE WHEN clause 2.
func TestPurgeDryRunPrintsExactCountsAndChangesNothing(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	f := seedPurgeStore(t, dbPath)
	_ = f

	// [t0+1h, t0+5h) selects sessions b and c (start t0+2h, t0+4h) plus the
	// session-less event at t0+3h: 2 sessions, 2+4+1 = 7 events.
	since := f.t0.Add(time.Hour).Format(time.RFC3339)
	until := f.t0.Add(5 * time.Hour).Format(time.RFC3339)
	args := []string{"--project", purgeProject, "--since", since, "--until", until}

	before := tableCounts(t, dbPath)
	stdout, stderr, code := execPurge(t, bin, env, append(args, "--dry-run")...)
	if code != 0 {
		t.Fatalf("dry-run exit %d stderr=%s", code, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "would purge 2 sessions, 7 events"; got != want {
		t.Errorf("dry-run stdout = %q, want %q", got, want)
	}
	if after := tableCounts(t, dbPath); after != before {
		t.Errorf("dry run changed rows: before %v after %v", before, after)
	}

	stdout, stderr, code = execPurge(t, bin, env, append(args, "--yes")...)
	if code != 0 {
		t.Fatalf("real run exit %d stderr=%s", code, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "purged 2 sessions, 7 events"; got != want {
		t.Errorf("real stdout = %q, want %q", got, want)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM timeline_events`); n != 3 {
		t.Errorf("remaining events = %d, want 3 (session a)", n)
	}
	// Without a window, session-less events are never selected.
	stdout, _, _ = execPurge(t, bin, env, "--project", purgeProject, "--dry-run")
	if got, want := strings.TrimSpace(stdout), "would purge 3 sessions, 3 events"; got != want {
		t.Errorf("windowless dry-run = %q, want %q", got, want)
	}
}

// TestPurgeWithoutYesOnNonTTYRefuses is DONE WHEN clause 6.
func TestPurgeWithoutYesOnNonTTYRefuses(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	f := seedPurgeStore(t, dbPath)
	before := tableCounts(t, dbPath)

	_, stderr, code := execPurge(t, bin, env, "--session", f.a)
	if code == 0 {
		t.Fatal("purge without --yes on a non-TTY exited 0")
	}
	if stderr == "" {
		t.Error("stderr empty, want a refusal message")
	}
	if after := tableCounts(t, dbPath); after != before {
		t.Errorf("refused purge changed rows: %v -> %v", before, after)
	}
	if n := countRows(t, dbPath, `SELECT COUNT(*) FROM sessions WHERE purged_at IS NOT NULL`); n != 0 {
		t.Errorf("purged_at set despite refusal")
	}
}

// TestReadersSurvivePurgedEvidenceAndCursor is DONE WHEN clause 5: after
// purging the session a record's evidence and event_cursor point into, the
// SessionStart hook (against a live daemon), this-week, timeline and recall
// all exit 0.
func TestReadersSurvivePurgedEvidenceAndCursor(t *testing.T) {
	bin := buildBackstory(t)
	dbPath, _, env := startTestDaemon(t, bin, noHarnessAncestry...)
	cwd := t.TempDir()
	key := project.Key(cwd, project.RealGit{}, nil)
	f := seedPurgeStoreFor(t, dbPath, key)

	if _, stderr, code := execPurge(t, bin, env, "--session", f.a, "--yes"); code != 0 {
		t.Fatalf("purge exit %d: %s", code, stderr)
	}

	stdout, stderr, code := runHookSubprocess(t, bin, env, map[string]any{
		"session_id": "post-purge", "cwd": cwd, "source": "startup", "hook_event_name": "SessionStart",
	})
	if code != 0 || strings.TrimSpace(stdout) == "" {
		t.Errorf("hook session-start exit %d stdout %q stderr %q", code, stdout, stderr)
	}

	for _, args := range [][]string{
		{"this-week", "--json"},
		{"timeline", "--json", "--project", key},
		{"recall", "--project", key},
		{"recall", "--project", key, "--altitude", "full"},
	} {
		cmd := exec.Command(bin, args...) //nolint:gosec // bin is the binary this test just built
		cmd.Env = env
		cmd.Dir = cwd
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Errorf("backstory %v: %v (stderr: %s)", args, err, errb.String())
		}
	}
}
