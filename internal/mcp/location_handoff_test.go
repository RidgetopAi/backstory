package mcp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/export"
	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
	"github.com/RidgetopAi/backstory/internal/week"
)

// locFixture is a workspace W holding git repos W/demo and W/other, plus a
// session at W itself: every handoff is noted through handleNote, so each is
// re-homed to the workspace key exactly as in production (decision f3fa04c7)
// — the situation every reader of the repo's own key could not see (task
// ed31b744).
type locFixture struct {
	t                 *testing.T
	st                *store.Store
	git               project.RealGit
	ws                []string
	root, demo, other string
}

func newLocFixture(t *testing.T) *locFixture {
	t.Helper()
	tmp := t.TempDir()
	f := &locFixture{t: t, st: mustOpenStore(t), root: filepath.Join(tmp, "projects")}
	f.demo, f.other = filepath.Join(f.root, "demo"), filepath.Join(f.root, "other")
	initGitRepo(t, f.demo)
	initGitRepo(t, f.other)
	f.ws = []string{f.root}
	return f
}

func (f *locFixture) key(dir string) string { return project.Key(dir, f.git, f.ws) }

// handoff starts a session in dir, has it edit file (so its labels are
// observed, not just its cwd), and notes a handoff from it.
func (f *locFixture) handoff(dir, file, text string) string {
	f.t.Helper()
	key := f.key(dir)
	if err := f.st.UpsertProject(store.Project{Key: key, Toplevel: dir, FirstSeen: time.Now()}); err != nil {
		f.t.Fatalf("UpsertProject: %v", err)
	}
	sid, err := f.st.StartSession(store.StartSessionParams{Agent: "claude", CWD: dir, ProjectKey: key, StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		f.t.Fatalf("StartSession: %v", err)
	}
	pl, _ := json.Marshal(payload.ToolUse{Name: "Edit", Path: file})
	if _, err := f.st.AppendEvent(store.Event{TS: time.Now(), Kind: payload.KindToolUse, SessionID: sid, Source: "shell", Payload: string(pl)}); err != nil {
		f.t.Fatalf("AppendEvent: %v", err)
	}
	raw, _ := json.Marshal(NoteParams{Kind: "handoff", Text: text})
	resp := handleNote(f.st, f.git, store.Identity{Kind: store.IdentityAgent}, sid, key, dir, f.ws, raw, func() (bool, error) { return false, nil })
	if resp.Error != nil {
		f.t.Fatalf("handleNote: %v", resp.Error)
	}
	var res NoteResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		f.t.Fatalf("NoteResult: %v", err)
	}
	return res.ID
}

func (f *locFixture) recall(dir, projectRef string) RecallResult {
	f.t.Helper()
	raw, _ := json.Marshal(RecallParams{Project: projectRef})
	resp := handleRecall(f.st, f.git, ident.Identity{CWD: dir, ProjectKey: f.key(dir)}, raw, f.ws)
	if resp.Error != nil {
		f.t.Fatalf("handleRecall: %v", resp.Error)
	}
	var res RecallResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		f.t.Fatalf("RecallResult: %v", err)
	}
	return res
}

func (f *locFixture) resume(dir string) string {
	f.t.Helper()
	out, err := block.Render(block.Params{Store: f.st, ProjectKey: f.key(dir), SessionID: "caller", CWD: dir, Git: f.git, WorkspaceDirs: f.ws, Now: time.Now()})
	if err != nil {
		f.t.Fatalf("block.Render: %v", err)
	}
	for _, l := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(l, "Resume: (id "); ok {
			id, _, _ := strings.Cut(rest, ")")
			return id
		}
	}
	return ""
}

func (f *locFixture) weekRowHandoff(dir string) string {
	f.t.Helper()
	res, err := week.Build(week.Params{Store: f.st, Git: f.git, WorkspaceDirs: f.ws, Now: time.Now().Add(time.Minute)})
	if err != nil {
		f.t.Fatalf("week.Build: %v", err)
	}
	for _, r := range res.WhereLeftOff {
		if r.Project.CWD == dir {
			return r.Project.HandoffID
		}
	}
	f.t.Fatalf("no This Week row for %s in %+v", dir, res.WhereLeftOff)
	return ""
}

// Task ed31b744 DONE WHEN 1: a handoff noted from W/demo is what MCP recall,
// `export` and the SessionStart Resume show in W/demo; W/other's is in none.
func TestRepoHandoffVisibleToEveryReaderInThatRepo(t *testing.T) {
	f := newLocFixture(t)
	demoH := f.handoff(f.demo, filepath.Join(f.demo, "a.go"), "DEMO-HANDOFF state")
	otherH := f.handoff(f.other, filepath.Join(f.other, "b.go"), "OTHER-HANDOFF state")

	res := f.recall(f.demo, "")
	if !containsID(res.Items, demoH) || containsID(res.Items, otherH) {
		t.Errorf("recall in W/demo = %v, want %s and not %s", recallItemIDs(res.Items), demoH, otherH)
	}
	if got := f.resume(f.demo); got != demoH {
		t.Errorf("block Resume in W/demo = %q, want %s", got, demoH)
	}

	ls, err := f.st.LocationScope(f.demo, f.git, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	md, err := export.Build(export.Params{Store: f.st, ProjectKey: f.key(f.demo), Scope: &ls, Altitude: export.AltitudeSummary, WorkspaceDirs: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "DEMO-HANDOFF") || strings.Contains(md, "OTHER-HANDOFF") || strings.Contains(md, "no handoff yet") {
		t.Errorf("export in W/demo = %q, want demo's handoff only", md)
	}
}

// Task ed31b744 DONE WHEN 2: for the workspace root itself, This Week's row
// handoff, the block's Resume and `records --location W`'s newest handoff
// are one record — the one labelled with the root, not the newest in W.
func TestWorkspaceRootHandoffAgreesAcrossSurfaces(t *testing.T) {
	f := newLocFixture(t)
	rootH := f.handoff(f.root, filepath.Join(f.root, "notes.md"), "ROOT-HANDOFF state")
	f.handoff(f.demo, filepath.Join(f.demo, "a.go"), "DEMO-HANDOFF state")
	f.handoff(f.other, filepath.Join(f.other, "b.go"), "OTHER-HANDOFF state") // newest in W

	ls, err := f.st.LocationScope(f.root, f.git, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := f.st.RecordsForLocation(ls, 100)
	if err != nil {
		t.Fatal(err)
	}
	var newest string
	for _, r := range recs {
		if r.Kind == store.KindHandoff {
			newest = r.ID
			break
		}
	}
	week, resume := f.weekRowHandoff(f.root), f.resume(f.root)
	if week != rootH || resume != rootH || newest != rootH {
		t.Errorf("root handoff: this-week=%q block=%q records --location=%q, want all %s", week, resume, newest, rootH)
	}
}

// Task ed31b744 DONE WHEN 4: recall's `project` (a key or a path) reads that
// project's records, not the caller's.
func TestRecallHonoursProjectKeyAndPath(t *testing.T) {
	f := newLocFixture(t)
	demoH := f.handoff(f.demo, filepath.Join(f.demo, "a.go"), "DEMO-HANDOFF state")
	otherH := f.handoff(f.other, filepath.Join(f.other, "b.go"), "OTHER-HANDOFF state")

	for name, ref := range map[string]string{"path": f.other, "key": f.key(f.other)} {
		res := f.recall(f.demo, ref)
		if !containsID(res.Items, otherH) || containsID(res.Items, demoH) {
			t.Errorf("recall(project=%s %q) from W/demo = %v, want %s and not the caller's %s", name, ref, recallItemIDs(res.Items), otherH, demoH)
		}
		if res.ProjectKey != f.key(f.other) {
			t.Errorf("recall(project=%s) project_key = %q, want %q", name, res.ProjectKey, f.key(f.other))
		}
	}
}

// Task ed31b744 DONE WHEN 2, the fixture the original three-way split needs:
// the newest handoff filed from a session AT the workspace root was written
// while editing a repo (labelled demo, not the root). The block used to
// resume it (writer's key == home), while This Week's row and `records
// --location` picked the older one labelled with the root. All three must be
// the root-labelled one.
func TestWorkspaceRootHandoffIgnoresRootSessionEditingARepo(t *testing.T) {
	f := newLocFixture(t)
	rootH := f.handoff(f.root, filepath.Join(f.root, "notes.md"), "ROOT-HANDOFF state")
	strayH := f.handoff(f.root, filepath.Join(f.demo, "a.go"), "STRAY-HANDOFF state") // newest, labelled demo

	ls, err := f.st.LocationScope(f.root, f.git, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := f.st.RecordsForLocation(ls, 100)
	if err != nil {
		t.Fatal(err)
	}
	var newest string
	for _, r := range recs {
		if r.Kind == store.KindHandoff {
			newest = r.ID
			break
		}
	}
	week, resume := f.weekRowHandoff(f.root), f.resume(f.root)
	if rootH == "" || week != rootH || resume != rootH || newest != rootH {
		t.Errorf("root handoff: this-week=%q block=%q records --location=%q, want all %s (not stray %s)", week, resume, newest, rootH, strayH)
	}
}

// Task ed31b744 DONE WHEN 2 / clause 5 mutation 2: a repo row's handoff is
// the shared resolver's. A session in W/demo that only edited W/other still
// wrote a demo-location handoff (its repo key matches), so This Week's demo
// row must show it — the same record the block resumes in W/demo — which a
// private label-only lookup of the workspace's records cannot.
func TestRepoRowHandoffMatchesBlockResume(t *testing.T) {
	f := newLocFixture(t)
	h := f.handoff(f.demo, filepath.Join(f.other, "b.go"), "DEMO-SESSION-HANDOFF state")
	if got := f.resume(f.demo); got != h {
		t.Fatalf("block Resume in W/demo = %q, want %s", got, h)
	}
	if got := f.weekRowHandoff(f.demo); got != h {
		t.Errorf("This Week W/demo row handoff = %q, want the block's Resume %s", got, h)
	}
}
