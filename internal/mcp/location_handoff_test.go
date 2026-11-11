package mcp

import (
	"encoding/json"
	"os"
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
// are one record. Since decision dc0a4387 repo sessions' handoffs are homed in
// their repos, and the root resumes the newest handoff overall: the newest
// repo-homed one here. `records --location W` stays the root's own writes.
func TestWorkspaceRootHandoffAgreesAcrossSurfaces(t *testing.T) {
	f := newLocFixture(t)
	rootH := f.handoff(f.root, filepath.Join(f.root, "notes.md"), "ROOT-HANDOFF state")
	f.handoff(f.demo, filepath.Join(f.demo, "a.go"), "DEMO-HANDOFF state")
	otherH := f.handoff(f.other, filepath.Join(f.other, "b.go"), "OTHER-HANDOFF state") // newest in W

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
	if week != otherH || resume != otherH {
		t.Errorf("root handoff: this-week=%q block=%q, want both the newest overall %s", week, resume, otherH)
	}
	if newest != rootH {
		t.Errorf("records --location W newest handoff = %q, want the root's own %s", newest, rootH)
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
// while editing a repo (labelled demo). Since decision 1e53165a a session
// always counts for the folder it started in, so it is ALSO the root's
// newest handoff: the block, This Week's row and `records --location` must
// all agree on it (they used to pick the older root-only one).
func TestWorkspaceRootHandoffIgnoresRootSessionEditingARepo(t *testing.T) {
	f := newLocFixture(t)
	f.handoff(f.root, filepath.Join(f.root, "notes.md"), "ROOT-HANDOFF state")
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
	if strayH == "" || week != strayH || resume != strayH || newest != strayH {
		t.Errorf("root handoff: this-week=%q block=%q records --location=%q, want all %s", week, resume, newest, strayH)
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

// Task 01c20ad2 clause 3: a workspace-homed handoff's note result names the
// folder its next session will see it in. Since decision dc0a4387 only a
// session in a non-git folder under the workspace is workspace-homed.
func TestNoteHandoffResultNamesVisibleAt(t *testing.T) {
	f := newLocFixture(t)
	f.demo = filepath.Join(f.root, "notes")
	if err := os.MkdirAll(f.demo, 0o750); err != nil {
		t.Fatal(err)
	}
	key := f.key(f.demo)
	if err := f.st.UpsertProject(store.Project{Key: key, Toplevel: f.demo, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sid, err := f.st.StartSession(store.StartSessionParams{Agent: "claude", CWD: f.demo, ProjectKey: key, StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(NoteParams{Kind: "handoff", Text: "h"})
	resp := handleNote(f.st, f.git, store.Identity{Kind: store.IdentityAgent}, sid, key, f.demo, f.ws, raw, func() (bool, error) { return false, nil })
	if resp.Error != nil {
		t.Fatalf("handleNote: %v", resp.Error)
	}
	var res NoteResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatal(err)
	}
	if res.VisibleAt != f.demo {
		t.Fatalf("visible_at = %q, want %q", res.VisibleAt, f.demo)
	}
	if want := "the next session in " + f.demo + " will see it"; !strings.Contains(res.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", res.Message, want)
	}
}

// Decision 1e53165a clause 1 (Brian's camera case): a session that started in
// the non-git folder W/personal but only edited files under HOME/.local/bin
// and HOME/.config/systemd/user still counts for W/personal, so recall from
// there returns its handoff for a one-word query and a long multi-word one.
func TestRecallFindsHandoffOfSessionThatEditedOnlyOutsideItsFolder(t *testing.T) {
	f := newLocFixture(t)
	personal := filepath.Join(f.root, "personal")
	if err := os.MkdirAll(personal, 0o750); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	key := f.key(personal)
	if err := f.st.UpsertProject(store.Project{Key: key, Toplevel: personal, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sid, err := f.st.StartSession(store.StartSessionParams{Agent: "claude", CWD: personal, ProjectKey: key, StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "cam-watch"),
		filepath.Join(home, ".config", "systemd", "user", "cam.service"),
	} {
		pl, _ := json.Marshal(payload.ToolUse{Name: "Write", Path: p})
		if _, err := f.st.AppendEvent(store.Event{TS: time.Now(), Kind: payload.KindToolUse, SessionID: sid, Source: "shell", Payload: string(pl)}); err != nil {
			t.Fatal(err)
		}
	}
	const marker = "Zqxmarkercam"
	raw, _ := json.Marshal(NoteParams{Kind: "handoff", Text: "camera setup state " + marker, Next: "finish " + marker + " wiring"})
	resp := handleNote(f.st, f.git, store.Identity{Kind: store.IdentityAgent}, sid, key, personal, f.ws, raw, func() (bool, error) { return false, nil })
	if resp.Error != nil {
		t.Fatalf("handleNote: %v", resp.Error)
	}
	var note NoteResult
	if err := json.Unmarshal(resp.Result, &note); err != nil {
		t.Fatal(err)
	}

	for name, q := range map[string]string{
		"one word":   marker,
		"long query": "cameras RLC-810WA setup network wifi PoE " + marker + " install",
	} {
		rp, _ := json.Marshal(RecallParams{Query: q})
		r := handleRecall(f.st, f.git, ident.Identity{CWD: personal, ProjectKey: key}, rp, f.ws)
		if r.Error != nil {
			t.Fatalf("%s: handleRecall: %v", name, r.Error)
		}
		var res RecallResult
		if err := json.Unmarshal(r.Result, &res); err != nil {
			t.Fatal(err)
		}
		if !containsID(res.Items, note.ID) {
			t.Errorf("recall (%s) from W/personal = %v, want handoff %s", name, recallItemIDs(res.Items), note.ID)
		}
	}
}
