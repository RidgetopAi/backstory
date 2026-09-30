package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// locFixture is the desk's bug shape (task a9a784ec): a workspace
// HOME/projects holding a git repo app-a and a NON-git folder feedback-x —
// whose sessions share the workspace key — plus a workspace-root session
// that only used WebSearch and wrote 2 notes.
type locFixture struct {
	home, ws, dataDir, dbPath string
	appA, feedbackX           string // directories
	wsKey                     string
	rootSess, appSess, fxSess string
	rootNotes                 [2]string
	appHandoff, fxHandoff     string
	fxNote, appRepoNote       string
	base                      time.Time
}

func newLocFixture(t *testing.T) *locFixture {
	t.Helper()
	f := &locFixture{}
	f.home = t.TempDir()
	f.ws = filepath.Join(f.home, "projects")
	f.appA = filepath.Join(f.ws, "app-a")
	f.feedbackX = filepath.Join(f.ws, "feedback-x")
	for _, d := range []string{f.appA, f.feedbackX} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", f.appA, "init", "-q").CombinedOutput(); err != nil { //nolint:gosec // fixed args
		t.Fatalf("git init: %v\n%s", err, out)
	}
	f.dataDir = t.TempDir()
	f.dbPath = filepath.Join(f.dataDir, "backstory", "backstory.db")
	t.Setenv("HOME", f.home)
	t.Setenv("XDG_DATA_HOME", f.dataDir)
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", f.ws)
	// Keep the fixture's own (temp-dir) HOME out of This Week's excluded roots.
	t.Setenv("TMPDIR", "/nonexistent-backstory-tmp")
	t.Setenv("XDG_RUNTIME_DIR", "")
	f.wsKey = "workspace:" + f.ws
	f.base = time.Now().Add(-3 * time.Hour)

	st, err := store.Open(f.dbPath, []string{f.ws}, project.RealGit{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	appKey := project.Key(f.appA, project.RealGit{}, []string{f.ws})
	for _, k := range []string{f.wsKey, appKey} {
		if err := st.UpsertProject(store.Project{Key: k, Toplevel: k, FirstSeen: f.base}); err != nil {
			t.Fatal(err)
		}
	}
	sess := func(id, cwd, key string, at time.Time) string {
		sid, err := st.StartSession(store.StartSessionParams{
			ID: id, Agent: "claude", CWD: cwd, ProjectKey: key, StartedAt: at, Origin: store.OriginLive,
		})
		if err != nil {
			t.Fatal(err)
		}
		return sid
	}
	tool := func(sid string, at time.Time, name, path string) {
		p, _ := json.Marshal(map[string]string{"name": name, "path": path})
		mustAppendEvent(t, st, store.Event{TS: at, Kind: "tool.use", SessionID: sid, Source: "hook", Payload: string(p)})
	}
	rec := func(id, key, sid string, at time.Time, kind store.RecordKind, text string) string {
		mustInsertThisWeekRecord(t, st, fixtureRecord{ID: id, ProjectKey: key, SessionID: sid, TS: at, Kind: kind, Tier: store.TierAgentDeclared, Text: text})
		return id
	}
	at := func(m int) time.Time { return f.base.Add(time.Duration(m) * time.Minute) }

	f.rootSess = sess("sess-root", f.ws, f.wsKey, at(0))
	tool(f.rootSess, at(1), "WebSearch", "")
	f.rootNotes[0] = rec("root-note-1", f.wsKey, f.rootSess, at(2), store.KindNote, "root note one")
	f.rootNotes[1] = rec("root-note-2", f.wsKey, f.rootSess, at(3), store.KindNote, "root note two")

	f.appSess = sess("sess-app", f.appA, appKey, at(10))
	tool(f.appSess, at(11), "Write", filepath.Join(f.appA, "main.go"))
	f.appHandoff = rec("app-handoff", f.wsKey, f.appSess, at(12), store.KindHandoff, "app-a handoff")
	f.appRepoNote = rec("app-repo-note", appKey, f.appSess, at(13), store.KindNote, "app-a repo note")

	f.fxSess = sess("sess-fx", f.feedbackX, f.wsKey, at(20))
	tool(f.fxSess, at(21), "Write", filepath.Join(f.feedbackX, "notes.md"))
	f.fxHandoff = rec("fx-handoff", f.wsKey, f.fxSess, at(22), store.KindHandoff, "feedback-x handoff")
	f.fxNote = rec("fx-note", f.wsKey, f.fxSess, at(23), store.KindNote, "tiktok notes saved to feedback-x")
	return f
}

func (f *locFixture) recordIDs(t *testing.T, args ...string) []string {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code := run(append([]string{"records", "--json"}, args...), bytes.NewReader(nil), &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("records %v exit %d: %s", args, code, errBuf.String())
	}
	var o recordsOutputJSON
	if err := json.Unmarshal(outBuf.Bytes(), &o); err != nil {
		t.Fatalf("bad records JSON %q: %v", outBuf.String(), err)
	}
	var ids []string
	for _, r := range o.Records {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return ids
}

func sortedIDs(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRecordsLocationScopesToTheRow is DONE WHEN clause 1: each --location
// result is exactly what This Week attributes to that row.
func TestRecordsLocationScopesToTheRow(t *testing.T) {
	f := newLocFixture(t)

	cases := []struct {
		name string
		dir  string
		want []string
	}{
		{"label row", f.feedbackX, sortedIDs(f.fxHandoff, f.fxNote)},
		{"workspace root row", f.ws, sortedIDs(f.rootNotes[0], f.rootNotes[1])},
		{"repo row", f.appA, sortedIDs(f.appHandoff, f.appRepoNote)},
	}
	for _, c := range cases {
		for _, projArgs := range [][]string{nil, {"--project", f.wsKey}} {
			got := f.recordIDs(t, append([]string{"--location", c.dir}, projArgs...)...)
			if !equalIDs(got, c.want) {
				t.Errorf("%s (%v): records = %v, want %v", c.name, projArgs, got, c.want)
			}
		}
	}

	// Without --location the workspace key lists everything filed under it
	// (the desk bug: what the panel used to show for every child row).
	if got := f.recordIDs(t, "--project", f.wsKey); len(got) != 5 {
		t.Errorf("--project workspace key = %v, want the 5 workspace-keyed records", got)
	}

	// Cross-check against This Week's own attribution: the handoff each row
	// shows is in that row's scope, and a label row's records_written equals
	// its scope's record count.
	var out, errb bytes.Buffer
	if code := run([]string{"this-week", "--json"}, bytes.NewReader(nil), &out, &errb); code != 0 {
		t.Fatalf("this-week exit %d: %s", code, errb.String())
	}
	var tw thisWeekOutputJSON
	if err := json.Unmarshal(out.Bytes(), &tw); err != nil {
		t.Fatal(err)
	}
	handoffs := map[string]string{}
	cwds := map[string]string{}
	for _, row := range tw.WhereLeftOff {
		if row.Project != nil {
			handoffs[row.Project.DisplayName] = row.Project.HandoffID
			cwds[row.Project.DisplayName] = row.Project.CWD
		}
	}
	for name, dir := range map[string]string{"projects/feedback-x": f.feedbackX, "projects/app-a": f.appA} {
		if cwds[name] == "" {
			t.Fatalf("this-week has no %s row: %s", name, out.String())
		}
		ids := f.recordIDs(t, "--location", cwds[name])
		found := false
		for _, id := range ids {
			found = found || id == handoffs[name]
		}
		if !found || handoffs[name] == "" {
			t.Errorf("row %s: this-week handoff %q not in --location %s records %v", name, handoffs[name], dir, ids)
		}
	}
	if cwds["projects"] == "" {
		t.Fatalf("this-week has no workspace-root row: %s", out.String())
	}
	written := map[string]int{}
	for _, d := range tw.Week {
		written[d.DisplayName] += d.RecordsWritten
	}
	for name, dir := range map[string]string{"projects": f.ws, "projects/feedback-x": f.feedbackX} {
		if n := len(f.recordIDs(t, "--location", dir)); n != written[name] {
			t.Errorf("row %s: --location gives %d records, this-week week grid attributes %d", name, n, written[name])
		}
	}
}

// TestRecordsLocationListsRepoFiledDecisionOnce is task fd14c6cc's clause 5:
// a decision written from a workspace-cwd session that edited app-a is filed
// under app-a's key (what handleNote now does); it is ALSO attributed to
// app-a by session label, yet `records --location app-a` lists it once.
func TestRecordsLocationListsRepoFiledDecisionOnce(t *testing.T) {
	f := newLocFixture(t)
	st, err := store.Open(f.dbPath, []string{f.ws}, project.RealGit{})
	if err != nil {
		t.Fatal(err)
	}
	appKey := project.Key(f.appA, project.RealGit{}, []string{f.ws})
	if _, err := st.StartSession(store.StartSessionParams{
		ID: "sess-ws-edit", Agent: "claude", CWD: f.ws, ProjectKey: f.wsKey, StartedAt: f.base.Add(30 * time.Minute), Origin: store.OriginLive,
	}); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]string{"name": "Edit", "path": filepath.Join(f.appA, "x.go")})
	mustAppendEvent(t, st, store.Event{TS: f.base.Add(31 * time.Minute), Kind: "tool.use", SessionID: "sess-ws-edit", Source: "hook", Payload: string(p)})
	mustInsertThisWeekRecord(t, st, fixtureRecord{ID: "ws-decision", ProjectKey: appKey, SessionID: "sess-ws-edit", TS: f.base.Add(32 * time.Minute), Kind: store.KindDecision, Tier: store.TierAgentDeclared, Text: "chose X"})
	_ = st.Close()

	n := 0
	for _, id := range f.recordIDs(t, "--location", f.appA) {
		if id == "ws-decision" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("records --location app-a lists the decision %d times, want exactly 1", n)
	}
}
