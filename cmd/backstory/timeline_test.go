package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// timelineFixtureProject is the project key every timeline golden test
// seeds and queries against.
const timelineFixtureProject = "acme-timeline"

// timelineFixtureBase is the fixture's reference instant: every event's ts
// is expressed relative to it, and --since bounds are RFC3339 timestamps
// derived from it, so nothing here depends on time.Now().
var timelineFixtureBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// buildTimelineFixtureStore opens a fresh store at $XDG_DATA_HOME/backstory/
// backstory.db under dataDir and seeds it with five events for
// timelineFixtureProject, inserted in this exact order (fixing their
// sequence/id): session.start, tool.use, tool.use, tool.result,
// session.end. The two tool.use events are inserted with their ts
// SWAPPED relative to insertion order — the second one inserted (sequence
// position 3) carries an EARLIER ts than the first (sequence position 2) —
// the fixture this punch's DONE WHEN clause 2 and its critic mutation
// ("order timeline by ts") must fail against: a ts-sorted read reorders
// these two, a sequence-ordered read does not.
func buildTimelineFixtureStore(t *testing.T, dataDir string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.UpsertProject(store.Project{
		Key: timelineFixtureProject, Toplevel: timelineFixtureProject, FirstSeen: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	sessionID, err := st.StartSession(store.StartSessionParams{
		ID: "sess-timeline-fixture", Agent: "claude", CWD: "/repo", ProjectKey: timelineFixtureProject,
		StartedAt: timelineFixtureBase, Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	at := func(seconds int) time.Time { return timelineFixtureBase.Add(time.Duration(seconds) * time.Second) }
	events := []store.Event{
		{TS: at(0), Kind: "session.start", SessionID: sessionID, Source: "shell", Payload: `{}`},
		{TS: at(120), Kind: "tool.use", SessionID: sessionID, Source: "shell", Payload: `{"path":"live.go"}`},
		{TS: at(60), Kind: "tool.use", SessionID: sessionID, Source: "backfill", Payload: `{"path":"backfilled.go"}`},
		{TS: at(180), Kind: "tool.result", SessionID: sessionID, Source: "shell", Payload: `{"exit_code":0}`},
		{TS: at(240), Kind: "session.end", SessionID: sessionID, Source: "shell", Payload: `{}`},
	}
	for i, e := range events {
		if _, err := st.AppendEvent(e); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}
}

func runTimelineCLI(t *testing.T, dataDir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dataDir)
	var outBuf, errBuf bytes.Buffer
	code = run(append([]string{"timeline"}, args...), bytes.NewReader(nil), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

func timelineGoldenPath(name string) string {
	return filepath.Join("testdata", "timeline", name)
}

// compareTimelineGolden mirrors recall_test.go's compareGolden (same
// RA_UPDATE_GOLDEN=1 regeneration mechanism), against testdata/timeline
// instead of testdata/recall.
func compareTimelineGolden(t *testing.T, name, got string) {
	t.Helper()
	path := timelineGoldenPath(name)
	if os.Getenv("RA_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // test fixture output, not sensitive
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // path is this test's own fixed testdata path
	if err != nil {
		t.Fatalf("read golden %s: %v (run with RA_UPDATE_GOLDEN=1 to create it)", path, err)
	}
	if got != string(want) {
		t.Fatalf("output does not match golden %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// TestTimelineGoldenAll is the golden test for a plain `backstory
// timeline`: every event, in sequence order.
func TestTimelineGoldenAll(t *testing.T) {
	dataDir := t.TempDir()
	buildTimelineFixtureStore(t, dataDir)
	stdout, stderr, code := runTimelineCLI(t, dataDir, "--project", timelineFixtureProject)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	compareTimelineGolden(t, "all.golden", stdout)
}

// TestTimelineGoldenJSON is the golden test for `backstory timeline --json`.
func TestTimelineGoldenJSON(t *testing.T) {
	dataDir := t.TempDir()
	buildTimelineFixtureStore(t, dataDir)
	stdout, stderr, code := runTimelineCLI(t, dataDir, "--project", timelineFixtureProject, "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	compareTimelineGolden(t, "all.json.golden", stdout)
}

// TestTimelineSinceReturnsOnlyEventsAfterBoundInSequenceOrder is the
// punch's DONE WHEN clause 2, proved end to end through the CLI: --since a
// bound that both out-of-order tool.use events pass must return them in
// SEQUENCE order (the order buildTimelineFixtureStore inserted them:
// live.go at sequence position 2, backfilled.go at position 3), never
// resorted by ts (which would put backfilled.go, ts=+60s, before live.go,
// ts=+120s), and must exclude session.start (ts=+0s, before the bound).
func TestTimelineSinceReturnsOnlyEventsAfterBoundInSequenceOrder(t *testing.T) {
	dataDir := t.TempDir()
	buildTimelineFixtureStore(t, dataDir)

	since := timelineFixtureBase.Add(50 * time.Second).Format(time.RFC3339)
	stdout, stderr, code := runTimelineCLI(t, dataDir, "--project", timelineFixtureProject, "--since", since, "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}

	var parsed struct {
		Events []struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if len(parsed.Events) != 4 {
		t.Fatalf("events = %d, want 4 (session.start excluded): %+v", len(parsed.Events), parsed.Events)
	}
	wantPayloadFragments := []string{"live.go", "backfilled.go", "exit_code", "{}"}
	for i, want := range wantPayloadFragments {
		if !bytes.Contains(parsed.Events[i].Payload, []byte(want)) {
			t.Fatalf("event[%d] payload = %s, want it to contain %q (sequence order: live.go before backfilled.go, ts order would reverse them)",
				i, parsed.Events[i].Payload, want)
		}
	}
}

// TestTimelineSinceAcceptsDuration covers --since's other input shape: a
// duration ago from now, not just an absolute RFC3339 timestamp. It seeds
// its own fixture with real, current timestamps (unlike the fixed-base
// fixture the other tests share) since a relative bound needs a real "now"
// to be relative to.
func TestTimelineSinceAcceptsDuration(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.UpsertProject(store.Project{Key: "duration-project", Toplevel: "duration-project", FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	sessionID, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: "/repo", ProjectKey: "duration-project", StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := st.AppendEvent(store.Event{TS: time.Now().Add(-time.Hour), Kind: "a", SessionID: sessionID, Source: "shell", Payload: "{}"}); err != nil {
		t.Fatalf("AppendEvent old: %v", err)
	}
	if _, err := st.AppendEvent(store.Event{TS: time.Now(), Kind: "b", SessionID: sessionID, Source: "shell", Payload: "{}"}); err != nil {
		t.Fatalf("AppendEvent recent: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stdout, stderr, code := runTimelineCLI(t, dataDir, "--project", "duration-project", "--since", "5m", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	var parsed struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if len(parsed.Events) != 1 || parsed.Events[0].Kind != "b" {
		t.Fatalf("events = %+v, want exactly the recent event", parsed.Events)
	}
}

// TestTimelineKindFilter covers --kind.
func TestTimelineKindFilter(t *testing.T) {
	dataDir := t.TempDir()
	buildTimelineFixtureStore(t, dataDir)
	stdout, stderr, code := runTimelineCLI(t, dataDir, "--project", timelineFixtureProject, "--kind", "tool.result", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	var parsed struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if len(parsed.Events) != 1 || parsed.Events[0].Kind != "tool.result" {
		t.Fatalf("events = %+v, want exactly one tool.result event", parsed.Events)
	}
}

// TestTimelineLimitFilter covers --limit: the most recent N events, still
// oldest to newest.
func TestTimelineLimitFilter(t *testing.T) {
	dataDir := t.TempDir()
	buildTimelineFixtureStore(t, dataDir)
	stdout, stderr, code := runTimelineCLI(t, dataDir, "--project", timelineFixtureProject, "--limit", "2", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	var parsed struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if len(parsed.Events) != 2 || parsed.Events[0].Kind != "tool.result" || parsed.Events[1].Kind != "session.end" {
		t.Fatalf("events = %+v, want exactly the last two (tool.result, session.end)", parsed.Events)
	}
}

// TestTimelineInvalidSinceRejected guards --since's own parse error path.
func TestTimelineInvalidSinceRejected(t *testing.T) {
	dataDir := t.TempDir()
	_, stderr, code := runTimelineCLI(t, dataDir, "--project", timelineFixtureProject, "--since", "not-a-time")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stderr == "" {
		t.Fatalf("stderr is empty, want an error naming the rejected --since value")
	}
}

// TestTimelineEmptyProjectPrintsEmptyStateNamingProject is the punch's DONE
// WHEN clause 3 for timeline.
func TestTimelineEmptyProjectPrintsEmptyStateNamingProject(t *testing.T) {
	dataDir := t.TempDir()
	stdout, stderr, code := runTimelineCLI(t, dataDir, "--project", "empty-project")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	want := "no timeline events for project empty-project\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

// TestTimelineEmptyProjectJSONNamesProjectWithNoEvents covers the same
// empty state through --json.
func TestTimelineEmptyProjectJSONNamesProjectWithNoEvents(t *testing.T) {
	dataDir := t.TempDir()
	stdout, stderr, code := runTimelineCLI(t, dataDir, "--project", "empty-project", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	want := `{"project_key":"empty-project","events":[]}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

// TestTimelineShowsNonPositiveTSAsUnknown (task 456410f0): an event stored
// with Go's zero time (or any ts <= 0) is shown as unknown — text and JSON —
// and no output contains a year before 1970.
func TestTimelineShowsNonPositiveTSAsUnknown(t *testing.T) {
	dataDir := t.TempDir()
	buildTimelineFixtureStore(t, dataDir)
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ts := range []time.Time{{}, time.Unix(0, 0), time.Unix(-5, 0)} {
		if _, err := st.AppendEvent(store.Event{TS: ts, Kind: "session.end", SessionID: "sess-timeline-fixture", Source: "backfill", Payload: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	for _, args := range [][]string{{"--project", timelineFixtureProject}, {"--project", timelineFixtureProject, "--json"}} {
		stdout, stderr, code := runTimelineCLI(t, dataDir, args...)
		if code != 0 {
			t.Fatalf("timeline %v exit %d: %s", args, code, stderr)
		}
		if got := strings.Count(stdout, "unknown"); got != 3 {
			t.Errorf("timeline %v: %d unknown timestamps, want 3:\n%s", args, got, stdout)
		}
		for _, y := range []string{"1754", "1969", "0001", "1901"} {
			if strings.Contains(stdout, y+"-") {
				t.Errorf("timeline %v shows pre-1970 year %s:\n%s", args, y, stdout)
			}
		}
	}
}
