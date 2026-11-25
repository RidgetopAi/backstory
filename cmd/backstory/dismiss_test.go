package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// fixtureUncommittedEvent is the fixture's one session.git_state event
// (acme-week-main, evidence_ids ["4"] in all.json.golden); its cwd does not
// exist on the test machine, so it is could-not-observe now and the item
// stays — the case before this release, and still the case for a gone folder.
const fixtureUncommittedEvent = "4"

func uncommittedEvidence(t *testing.T, dataDir string) []string {
	t.Helper()
	stdout, stderr, code := runThisWeekCLI(t, dataDir, "--json")
	if code != 0 {
		t.Fatalf("this-week --json exit %d: %s", code, stderr)
	}
	var parsed thisWeekOutputJSON
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range parsed.Attention {
		if it.Kind == "uncommitted-at-session-end" {
			ids = append(ids, strings.Join(it.EvidenceIDs, ","))
		}
	}
	return ids
}

func runDismissCLI(t *testing.T, dataDir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", fixtureWorkspaceDir)
	t.Setenv("HOME", "/home/brian")
	orig := thisWeekNow
	thisWeekNow = func() time.Time { return thisWeekFixtureNow }
	t.Cleanup(func() { thisWeekNow = orig })
	var out, errOut bytes.Buffer
	code = run(append([]string{"dismiss"}, args...), bytes.NewReader(nil), &out, &errOut)
	return out.String(), errOut.String(), code
}

func appendFixtureGitState(t *testing.T, dataDir, sessionID, payloadJSON string, at time.Time) int64 {
	t.Helper()
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	return mustAppendEvent(t, st, store.Event{TS: at, Kind: "session.git_state", SessionID: sessionID, Source: "daemon", Payload: payloadJSON})
}

// `backstory dismiss <event-id>` hides the item; a newer dirty session end in
// the same tree shows it again; a clean session end clears it.
func TestDismissCLIHidesUntilTheNextDirtyEnd(t *testing.T) {
	dataDir := t.TempDir()
	buildThisWeekFixtureStore(t, dataDir)
	if ids := uncommittedEvidence(t, dataDir); len(ids) != 1 || ids[0] != fixtureUncommittedEvent {
		t.Fatalf("before dismiss: evidence %v, want [%s]", ids, fixtureUncommittedEvent)
	}
	stdout, stderr, code := runDismissCLI(t, dataDir, fixtureUncommittedEvent)
	if code != 0 || !strings.Contains(stdout, `dismissed "acme-week-main: session ended with 2 uncommitted change(s) on branch main"`) {
		t.Fatalf("dismiss exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if ids := uncommittedEvidence(t, dataDir); len(ids) != 0 {
		t.Fatalf("after dismiss: evidence %v, want none", ids)
	}
	if _, stderr, code := runDismissCLI(t, dataDir, fixtureUncommittedEvent); code == 0 || !strings.Contains(stderr, "already dismissed") {
		t.Fatalf("second dismiss exit %d stderr %q, want already dismissed", code, stderr)
	}

	newer := appendFixtureGitState(t, dataDir, "sess-main-1", `{"branch":"main","uncommitted_count":5}`, date(3, 11, 0))
	if ids := uncommittedEvidence(t, dataDir); len(ids) != 1 || ids[0] != strconv.FormatInt(newer, 10) {
		t.Fatalf("after a newer dirty end: evidence %v, want [%d]", ids, newer)
	}
	// The old id is no longer any item's evidence.
	if _, stderr, code := runDismissCLI(t, dataDir, fixtureUncommittedEvent); code == 0 || !strings.Contains(stderr, "already dismissed") {
		t.Fatalf("dismiss of the superseded, dismissed id: exit %d stderr %q", code, stderr)
	}

	appendFixtureGitState(t, dataDir, "sess-main-1", `{"branch":"main","uncommitted_count":0}`, date(3, 12, 0))
	if ids := uncommittedEvidence(t, dataDir); len(ids) != 0 {
		t.Fatalf("after a clean end: evidence %v, want none", ids)
	}
}

// dismiss refuses anything that is not an uncommitted item's current
// evidence, and writes nothing when it refuses.
func TestDismissCLIRejectsBadTargetsAndWritesNothing(t *testing.T) {
	dataDir := t.TempDir()
	buildThisWeekFixtureStore(t, dataDir)
	// A later dirty end makes the fixture's event 4 superseded.
	newer := appendFixtureGitState(t, dataDir, "sess-main-1", `{"branch":"main","uncommitted_count":5}`, date(3, 11, 0))
	cases := []struct {
		args []string
		code int
		msg  string
	}{
		{nil, 2, "usage"},
		{[]string{"4", "5"}, 2, "usage"},
		{[]string{"--yes"}, 2, "usage"},
		{[]string{"abc"}, 2, "not an event id"},
		{[]string{"0"}, 2, "not an event id"},
		{[]string{"999999"}, 1, "no event with id 999999"},
		{[]string{"1"}, 1, "not a session.git_state"},
		{[]string{fixtureUncommittedEvent}, 1, "is not what an uncommitted-changes item shows now"},
	}
	for _, c := range cases {
		_, stderr, code := runDismissCLI(t, dataDir, c.args...)
		if code != c.code || !strings.Contains(stderr, c.msg) {
			t.Errorf("dismiss %v: exit %d stderr %q, want exit %d and %q", c.args, code, stderr, c.code, c.msg)
		}
	}
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM settings WHERE key LIKE 'attention_dismissed_%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if n != 0 {
		t.Fatalf("rejected dismissals wrote %d setting(s)", n)
	}
	if ids := uncommittedEvidence(t, dataDir); len(ids) != 1 || ids[0] != strconv.FormatInt(newer, 10) {
		t.Fatalf("evidence %v, want the current item [%d] untouched", ids, newer)
	}
}

// Auto-clear through the real CLI and real git: a session ended with
// uncommitted changes, the human committed them, and the item is gone while
// that tree is clean; dirty again, it is back. The folder deleted, the item
// stays (could-not-observe is never clean).
func TestThisWeekUncommittedClearsWhenTheRealTreeIsCleanNow(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "projects", "vidflow")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	gitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...) //nolint:gosec // fixed args
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	key := filepath.Join(repo, ".git")
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: repo, FirstSeen: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	sid, err := st.StartSession(store.StartSessionParams{ID: "sess-vid", Agent: "claude", CWD: repo, ProjectKey: key, StartedAt: now.Add(-time.Hour), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	mustAppendEvent(t, st, store.Event{TS: now.Add(-40 * time.Minute), Kind: "tool.use", SessionID: sid, Source: "posttooluse", Payload: `{"name":"Edit","path":"a.txt"}`})
	evID := mustAppendEvent(t, st, store.Event{TS: now.Add(-30 * time.Minute), Kind: "session.git_state", SessionID: sid, Source: "daemon", Payload: `{"branch":"master","uncommitted_count":1}`})
	_ = st.Close()

	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", filepath.Join(home, "projects"))
	t.Setenv("TMPDIR", "/nonexistent-backstory-tmp")
	t.Setenv("XDG_RUNTIME_DIR", "")
	items := func() []string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := run([]string{"this-week", "--json"}, bytes.NewReader(nil), &out, &errOut); code != 0 {
			t.Fatalf("this-week exit %d: %s", code, errOut.String())
		}
		var parsed thisWeekOutputJSON
		if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, it := range parsed.Attention {
			if it.Kind == "uncommitted-at-session-end" {
				got = append(got, it.Reason+" @"+it.CWD+" #"+strings.Join(it.EvidenceIDs, ","))
			}
		}
		return got
	}
	want := "projects/vidflow: session ended with 1 uncommitted change(s) on branch master @" + repo + " #" + strconv.FormatInt(evID, 10)
	if got := items(); len(got) != 1 || got[0] != want {
		t.Fatalf("tree dirty now: %q, want [%q]", got, want)
	}
	gitIn("add", "-A")
	gitIn("commit", "-q", "-m", "commit everything")
	if got := items(); len(got) != 0 {
		t.Fatalf("tree clean now: %q, want no item", got)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := items(); len(got) != 1 || got[0] != want {
		t.Fatalf("tree dirty again: %q, want [%q]", got, want)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if got := items(); len(got) != 1 || got[0] != want {
		t.Fatalf("folder gone: %q, want the item kept [%q]", got, want)
	}
}
